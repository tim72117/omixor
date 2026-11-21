package model_test

// 跨語言契約範例檔:把 core API 實際回傳的 JSON 形狀(model 與 service 的回應型別)用代表性樣本序列化成
// testdata/contract/*.json。Swift(macOS App、iOS)與 TS(console/ui)的測試讀同一批檔案,用各自實際的解碼路徑解碼,
// 所以 Go 這邊改了欄位,這個測試會先要求重生範例檔,接著另外三邊的測試就會失敗,提醒同步改解碼端。
//
//	平常:比對產生的 JSON 和檔案內容一致,不一致就失敗
//	改了 model 之後:UPDATE_CONTRACT=1 go test ./model/ -run TestContract 重寫範例檔,再跑 app、ios、console/ui 的測試
//
// 範例只放 {"result": …} 裡面的 result 內容(說明見 testdata/contract/README.md)。

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

const contractDir = "testdata/contract"

func ct(s string) model.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return model.Time{Time: t.UTC()}
}

func ctp(s string) *model.Time { t := ct(s); return &t }
func sp(s string) *string      { return &s }

// 範例用的 actor 物件(ISS-36 C-3;和相容字串並存)
var (
	avTim = &model.ActorView{ID: "usr_019a3f2c-0000-7000-8000-000000000001", Kind: model.ActorKindUser, Handle: "tim@example.com", Name: "Tim", Email: "tim@example.com", Status: model.ActorStatusActive}
	avEng = &model.ActorView{ID: "agt_019a3f2c-0000-7000-8000-000000000002", Kind: model.ActorKindAgent, Handle: "eng", Name: "工程師", Status: model.ActorStatusActive}
	avPM  = &model.ActorView{ID: "agt_019a3f2c-0000-7000-8000-000000000003", Kind: model.ActorKindAgent, Handle: "pm", Name: "產品經理", Status: model.ActorStatusSuspended}
	avMCP = &model.ActorView{ID: "sys_legacy_mcp", Kind: model.ActorKindSystem, Handle: "legacy_mcp", Name: "mcp", Status: model.ActorStatusActive}
	avSys = &model.ActorView{ID: "sys_omixor", Kind: model.ActorKindSystem, Handle: "omixor", Name: "omixor 系統", Status: model.ActorStatusActive}
	avRtn = &model.ActorView{ID: "sys_routine", Kind: model.ActorKindSystem, Handle: "routine", Name: "例行排程", Status: model.ActorStatusActive}
	avOld = &model.ActorView{ID: "usr_019a3f2c-0000-7000-8000-000000000004", Kind: model.ActorKindUser, Handle: "old@example.com", Name: "Old", Email: "old@example.com", Status: model.ActorStatusDisabled}
)

func views(l ...*model.ActorView) []model.ActorView {
	out := make([]model.ActorView, 0, len(l))
	for _, v := range l {
		out = append(out, *v)
	}
	return out
}

// 範例資料:欄位盡量填滿(含選填欄位、時間、巢狀);列表另外放一筆只有必填欄位的,確認省略的欄位各邊也讀得出來

func sampleIssue() model.Issue {
	return model.Issue{
		ID: "019a3f2c-8e40-7b1a-9c2d-3e4f5a6b7c8d", Key: "APP-12", Project: "PRJ-1", Num: 12, Version: 3,
		Title: "登入頁在 Safari 白畫面", Body: "## 重現步驟\n\n1. 開 Safari\n2. 進 /login\n\n![截圖](/api/attachments/att-1)",
		Status: "closed", Priority: "high", Labels: []string{"bug", "前端"},
		Source: "網站訪客 7d", Link: "https://example.com/login", Repo: "github:acme/web", Assignee: "eng", AssigneeActor: avEng,
		CreatedBy: "tim@example.com", CreatedByActor: avTim, DeletedBy: "old@example.com", DeletedByActor: avOld, CreatedAt: ct("2026-11-01T08:30:00Z"), UpdatedAt: ct("2026-11-03T10:00:05Z"),
		ClosedAt: ctp("2026-11-03T10:00:05Z"),
		Comments: []model.Comment{
			{ID: "5f0c6c1e-7a52-4d0b-9a3e-2a1c2b3d4e5f", Author: "tim@example.com", AuthorActor: avTim, Text: "我這邊也遇到", At: ct("2026-11-01T09:00:00Z")},
			{ID: "jira-10023", Author: "agent:eng", AuthorActor: avEng, Text: "已修正,見 PR #42", At: ct("2026-11-03T09:59:00Z")},
		},
	}
}

func minimalIssue() model.Issue {
	return model.Issue{
		ID: "0199f8a0-0000-7000-8000-000000000003", Key: "ISS-3", Num: 3, Version: 1, Title: "最少欄位的 Issue", Status: "open", Priority: "normal", Labels: []string{}, Comments: []model.Comment{},
		CreatedBy: "mcp", CreatedByActor: avMCP, CreatedAt: ct("2026-10-20T00:00:00Z"), UpdatedAt: ct("2026-10-20T00:00:00Z"),
	}
}

func sampleAgent() model.Agent {
	return model.Agent{
		ID: "eng", Name: "工程師", Role: "寫程式、修 bug", Symbol: "hammer", Color: "teal", Active: true,
		Instructions: "你是工程師。**先寫測試**再改程式。", CreatedAt: ct("2026-10-01T00:00:00Z"),
		DocAccess: map[string]string{"docs": "write", "/studio/tools": "read", "overview": "none"},
		Routines: []model.AgentRoutine{
			{ID: "r-daily", Title: "每日站會摘要", Instructions: "整理昨天的進度", Kind: "daily", Hour: 9, Minute: 30, Weekday: 2,
				EveryHours: 6, Priority: "normal", Enabled: true, CreatedAt: ct("2026-10-01T00:00:00Z"), LastRunAt: ctp("2026-11-14T01:30:00Z")},
			{ID: "r-hourly", Title: "檢查 CI", Kind: "hourly", Hour: 0, Minute: 0, Weekday: 1, EveryHours: 4,
				Priority: "low", Enabled: false, CreatedAt: ct("2026-10-02T00:00:00Z")},
		},
	}
}

func sampleTask() model.AgentTask {
	return model.AgentTask{
		ID: "T-7", AgentID: "eng", Title: "修 ISS-12", Instructions: "看 **ISS-12**,修好後回報", Priority: "high", Status: "inProgress",
		CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-02T00:00:00Z"), UpdatedAt: ct("2026-11-02T01:00:00Z"),
		ClaimedAt: ctp("2026-11-02T00:50:00Z"), ClaimedBy: "tim@example.com", ClaimedByActor: avTim, LockedUntil: ctp("2026-11-02T01:50:00Z"),
		CompletedAt: ctp("2026-11-02T02:00:00Z"), RoutineID: sp("r-daily"),
		Result: "已修正", Links: []string{"ISS-12", "https://github.com/acme/web/pull/42"},
		Notes: []model.AgentTaskNote{
			{ID: "n-1", Author: "agent:eng", AuthorActor: avEng, Text: "開始處理", At: ct("2026-11-02T00:51:00Z")},
			{ID: "8d6a4e0b-1c2d-4e5f-8a9b-0c1d2e3f4a5b", Author: "omixor", AuthorActor: avSys, Text: "領取逾時,退回待領取", At: ct("2026-11-02T00:55:00Z")},
		},
	}
}

func minimalTask() model.AgentTask {
	return model.AgentTask{
		ID: "T-8", AgentID: "pm", Title: "寫規格", Priority: "normal", Status: "queued", CreatedBy: "routine", CreatedByActor: avRtn,
		CreatedAt: ct("2026-11-03T00:00:00Z"), UpdatedAt: ct("2026-11-03T00:00:00Z"), Links: []string{}, Notes: []model.AgentTaskNote{},
	}
}

func sampleChannel() model.ChannelSummary {
	return model.ChannelSummary{
		Channel: model.Channel{
			ID: "CH-2", Name: "登入頁改版", Topic: "討論 Safari 白畫面", IssueID: "019a3f2c-8e40-7b1a-9c2d-3e4f5a6b7c8d",
			Members: []string{"user:tim@example.com", "agent:eng"}, MemberActors: views(avTim, avEng),
			CreatedBy: "user:tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-01T10:00:00Z"), UpdatedAt: ct("2026-11-02T10:00:00Z"),
		},
		Unread: 3, LastMessageAt: ctp("2026-11-03T08:00:00Z"),
	}
}

func minimalChannel() model.ChannelSummary {
	return model.ChannelSummary{Channel: model.Channel{
		ID: "CH-1", Name: "閒聊", Members: []string{"user:tim@example.com"}, MemberActors: views(avTim), CreatedBy: "user:tim@example.com", CreatedByActor: avTim,
		CreatedAt: ct("2026-10-01T00:00:00Z"), UpdatedAt: ct("2026-10-01T00:00:00Z"),
	}}
}

func sampleMessage() model.ChannelMessage {
	return model.ChannelMessage{
		ID: "M-5", ChannelID: "CH-2", Author: "user:tim@example.com", AuthorActor: avTim, Text: "@工程師 可以看一下 **ISS-12** 嗎?",
		Mentions: []string{"agent:eng"}, MentionActors: views(avEng), At: ct("2026-11-03T08:00:00Z"),
	}
}

func sampleDecision() model.Decision {
	return model.Decision{
		ID: "DEC-3", Kind: "decision", Title: "待決定的結構要不要和需要協助合成一張表?", Body: "## 背景\n\n兩種都是「要人」的事。",
		Options: []model.DecisionOption{
			{ID: "o1", Label: "合成一張表", Description: "用 kind 欄分開", Recommended: true},
			{ID: "o2", Label: "分兩張表"},
		},
		Status: "decided", Answer: "o1", AnswerNote: "同一個狀態機就夠了", SourceIssue: "ISS-37", Blocks: []string{"ISS-38", "等憑證"},
		CreatedBy: "agent:eng", CreatedByActor: avEng, DecidedBy: "tim@example.com", DecidedByActor: avTim, CreatedAt: ct("2026-11-21T08:00:00Z"),
		DecidedAt: ctp("2026-11-21T09:00:00Z"), UpdatedAt: ct("2026-11-21T09:00:00Z"),
	}
}

func sampleHelp() model.Decision {
	return model.Decision{
		ID: "DEC-4", Kind: "help", Title: "請在 Mac 上編譯 iOS 版", Body: "需要 Xcode 16。", Options: []model.DecisionOption{},
		Status: "decided", Answer: "done", AnswerNote: "已編譯", Evidence: "https://example.com/build/42", SourceIssue: "", Blocks: []string{},
		CreatedBy: "mcp", CreatedByActor: avMCP, DecidedBy: "tim@example.com", DecidedByActor: avTim, CreatedAt: ct("2026-11-21T08:10:00Z"),
		DecidedAt: ctp("2026-11-21T09:30:00Z"), UpdatedAt: ct("2026-11-21T09:30:00Z"),
	}
}

// 只有必填欄位:開放中(answer、answerNote 是空字串,沒有 decidedAt、decidedByActor),沒有來源 Issue
func minimalDecision() model.Decision {
	return model.Decision{
		ID: "DEC-5", Kind: "decision", Title: "最少欄位", Options: []model.DecisionOption{{ID: "o1", Label: "A"}, {ID: "o2", Label: "B"}},
		Status: "open", Blocks: []string{}, CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-21T10:00:00Z"), UpdatedAt: ct("2026-11-21T10:00:00Z"),
	}
}

// 子項(issue_item):完成的一筆(帶證據、前置、領取者已清空)
func sampleItem() model.IssueItem {
	return model.IssueItem{
		ID: "0197b3a4-1111-7e8f-9a0b-1c2d3e4f5a6b", IssueID: "0197b3a4-0000-7e8f-9a0b-1c2d3e4f5a6b", Seq: 2, Key: "ISS-37.2", Title: "service 授權與 API", Status: "done",
		Assignee: "agent:eng", AssigneeActor: avEng, DependsOn: "ISS-37.1", Files: []string{"core/service/issue_items.go", "core/api/issue_items.go"},
		Acceptance: "守門測試通過", Evidence: []byte(`{"commit":"abc1234","note":"沒驗證 Swift","tests":"go test ./... 通過"}`), TaskID: "T-12",
		CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-21T08:00:00Z"), UpdatedAt: ct("2026-11-21T09:00:00Z"), CompletedAt: ctp("2026-11-21T09:00:00Z"),
	}
}

// 卡住的一筆:在等使用者決定,有領取者以外的所有欄位(領取已清空)
func sampleItemBlocked() model.IssueItem {
	return model.IssueItem{
		ID: "0197b3a4-2222-7e8f-9a0b-1c2d3e4f5a6b", IssueID: "0197b3a4-0000-7e8f-9a0b-1c2d3e4f5a6b", Seq: 3, Key: "ISS-37.3", Title: "決定欄位型別", Status: "blocked",
		BlockReason: "text[] 還是 jsonb 要人決定", WaitForKind: "user_decision", WaitFor: "DEC-6", CreatedBy: "agent:eng", CreatedByActor: avEng,
		CreatedAt: ct("2026-11-21T08:30:00Z"), UpdatedAt: ct("2026-11-21T09:10:00Z"),
	}
}

// 進行中的一筆:輕量領取(領取者與租約,沒有任務)
func sampleItemDoing() model.IssueItem {
	return model.IssueItem{
		ID: "0197b3a4-3333-7e8f-9a0b-1c2d3e4f5a6b", IssueID: "0197b3a4-0000-7e8f-9a0b-1c2d3e4f5a6b", Seq: 4, Key: "ISS-37.4", Title: "CLI 與 MCP", Status: "doing",
		Assignee: "tim@example.com", AssigneeActor: avTim, ClaimedBy: "tim@example.com", ClaimedByActor: avTim, LockedUntil: ctp("2026-11-21T17:00:00Z"),
		CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-21T08:40:00Z"), UpdatedAt: ct("2026-11-21T09:20:00Z"),
	}
}

// 只有必填欄位:待領取、沒有負責人與前置
func minimalItem() model.IssueItem {
	return model.IssueItem{
		ID: "0197b3a4-4444-7e8f-9a0b-1c2d3e4f5a6b", IssueID: "0197b3a4-0000-7e8f-9a0b-1c2d3e4f5a6b", Seq: 5, Key: "ISS-37.5", Title: "最少欄位", Status: "todo",
		CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-21T10:00:00Z"), UpdatedAt: ct("2026-11-21T10:00:00Z"),
	}
}

func sampleIncident() model.Incident {
	return model.Incident{
		ID: "INC-2041", Title: "API 延遲飆高", Sev: 2, Status: "resolved", Services: []string{"api", "db"},
		Commander: "tim@example.com", CommanderActor: avTim, StartedAt: ct("2026-11-05T03:00:00Z"), ResolvedAt: ctp("2026-11-05T04:30:00Z"),
		Impact: "約 20% 請求逾時", AISummary: sp("資料庫連線池耗盡,調大後恢復"), FollowUps: []string{"ISS-13", "WI-7"},
		AlertIDs: []string{"alert-cpu-1"}, CreatedBy: "mcp", CreatedByActor: avMCP,
		Timeline: []model.IncidentEvent{
			{ID: "0b8f2a8e-3c4d-4e5f-9a0b-1c2d3e4f5a6b", Date: ct("2026-11-05T03:00:00Z"), Actor: model.SystemActor(), ActorRef: avSys, Kind: "alert", Text: "p95 延遲 > 2s"},
			{ID: "1c9f3b9f-4d5e-4f60-8b1c-2d3e4f5a6b7c", Date: ct("2026-11-05T03:10:00Z"), Actor: model.PersonActor("tim@example.com"), ActorRef: avTim, Kind: "status", Text: "狀態改為「已找到原因」"},
			{ID: "evt-legacy-1", Date: ct("2026-11-05T03:20:00Z"), Actor: model.AIActor("mcp"), ActorRef: avMCP, Kind: "aiFinding", Text: "連線池用滿"},
		},
	}
}

func minimalIncident() model.Incident {
	return model.Incident{
		ID: "INC-2042", Title: "排程沒跑", Sev: 4, Status: "investigating", Services: []string{}, Commander: "",
		StartedAt: ct("2026-11-06T00:00:00Z"), Timeline: []model.IncidentEvent{}, FollowUps: []string{},
	}
}

func sampleDoc() model.CabinetDoc {
	return model.CabinetDoc{
		ID: "DOC-4", Title: "AI 工具介紹", Body: "# AI 工具\n\n- Claude\n- Codex", Section: "levels", Tab: sp("/studio/tools"),
		Tags: []string{"課程", "L0"}, Author: "agent:pm", AuthorActor: avPM, CreatedAt: ct("2026-10-10T00:00:00Z"), UpdatedAt: ct("2026-10-11T00:00:00Z"),
	}
}

func sampleCabinet() model.Cabinet {
	return model.Cabinet{
		Layout: []model.CabinetSection{
			{ID: "overview", Label: "總覽", Path: "/studio", Tabs: []model.CabinetTab{}},
			{ID: "levels", Label: "課程", Path: "/studio/tools", Tabs: []model.CabinetTab{
				{Path: "/studio/tools", Label: "AI 工具介紹", Code: sp("L0"), Group: sp("做出自己的軟體")},
				{Path: "/studio/ai-tips", Label: "AI 工具技巧"},
			}},
		},
		Docs: []model.CabinetDoc{
			sampleDoc(),
			{ID: "DOC-1", Title: "放在區塊本身的文件", Body: "", Section: "overview", Tags: []string{}, Author: "tim@example.com", AuthorActor: avTim,
				CreatedAt: ct("2026-10-01T00:00:00Z"), UpdatedAt: ct("2026-10-01T00:00:00Z")},
		},
		LayoutSaved:   true,
		LayoutVersion: 3,
	}
}

func sampleProject() model.Project {
	return model.Project{ID: "PRJ-2", Name: "官網改版", Description: "2027 年官網與文件站改版", Key: "WEB", Repos: []string{"acme/website", "gitlab:acme/docs"}, Restricted: true, MyRole: model.ProjectRoleAdmin, CreatedAt: ct("2026-11-01T00:00:00Z"), UpdatedAt: ct("2026-11-05T08:00:00Z")}
}

// sampleProjectDoc 放在專案裡的文件(project 有值;未分專案的文件不帶 project,見 sampleDoc)
func sampleProjectDoc() model.CabinetDoc {
	return model.CabinetDoc{
		ID: "DOC-9", Project: "PRJ-2", Title: "首頁規格", Body: "# 首頁\n\n- Hero\n- 案例", Section: "spec", Tab: sp("/spec/home"),
		Tags: []string{"規格"}, Author: "agent:pm", AuthorActor: avPM, CreatedAt: ct("2026-11-02T00:00:00Z"), UpdatedAt: ct("2026-11-03T00:00:00Z"),
	}
}

// contractFixtures 檔名 → 內容(都是 API 回應 {"result": …} 裡的 result)
func contractFixtures() map[string]any {
	return map[string]any{
		// GET /api/orgs/{org}/issues/{id}、POST 建立/修改/留言
		"issue.json": sampleIssue(),
		// GET /api/orgs/{org}/issues
		"issue_list.json": []model.Issue{sampleIssue(), minimalIssue()},
		// GET /api/orgs/{org}/agents/{id}、POST /api/orgs/{org}/agents
		"agent.json": sampleAgent(),
		// GET /api/orgs/{org}/agents
		"agent_list.json": []model.Agent{sampleAgent(), {ID: "pm", Name: "產品經理", Role: "", Symbol: "", Color: "blue", Active: false,
			CreatedAt: ct("2026-10-01T00:00:00Z"), DocAccess: map[string]string{}, Routines: []model.AgentRoutine{}}},
		// GET /api/orgs/{org}/agent-tasks/{id}、report、cancel、requeue
		"agent_task.json": sampleTask(),
		// GET /api/orgs/{org}/agent-tasks?status=all
		"agent_task_list.json": []model.AgentTask{sampleTask(), minimalTask()},
		// POST /api/orgs/{org}/agent-tasks/claim(領到任務時)
		"agent_task_claim.json": service.ClaimResult{
			Agent: ptr(sampleAgent()), Task: ptr(sampleTask()),
			Credential: &model.AgentGrant{AgentID: "eng", TaskID: "T-7", Secret: "s3cr3t-example", Code: "123456", ExpiresAt: "2026-11-02T01:50:00Z"},
		},
		// 同上,沒有可領的任務時(task 是 null)
		"agent_task_claim_empty.json": service.ClaimResult{Agent: ptr(sampleAgent())},
		// GET /api/orgs/{org}/channels/{id}、POST 建立/修改/成員
		"channel.json": sampleChannel(),
		// GET /api/orgs/{org}/channels(lastMessageAt 沒訊息時是 null)
		"channel_list.json": []model.ChannelSummary{sampleChannel(), minimalChannel()},
		// POST /api/orgs/{org}/channels/{id}/messages
		"channel_message.json": sampleMessage(),
		// GET /api/orgs/{org}/channels/{id}/messages
		"channel_messages.json": service.MessagePage{
			Messages: []model.ChannelMessage{
				{ID: "M-4", ChannelID: "CH-2", Author: "agent:eng", AuthorActor: avEng, Text: "收到", Mentions: []string{}, At: ct("2026-11-03T07:00:00Z")},
				sampleMessage(),
			},
			HasMore: true,
		},
		// POST /api/orgs/{org}/channels/{id}/read
		"channel_read.json": model.ChannelRead{ChannelID: "CH-2", Member: "user:tim@example.com", MemberActor: avTim, LastRead: 5, At: ct("2026-11-03T08:01:00Z")},
		// GET /api/orgs/{org}/channels/inbox?for=
		"channel_inbox.json": service.ChannelInbox{
			Node: "agent:eng", NodeActor: avEng,
			Mentions: []service.InboxMention{{ChannelID: "CH-2", ChannelName: "登入頁改版", ID: "M-5", Author: "user:tim@example.com", AuthorActor: avTim,
				Text: "@工程師 可以看一下 **ISS-12** 嗎?", At: ct("2026-11-03T08:00:00Z")}},
			Channels: []service.InboxChannel{{ID: "CH-2", Name: "登入頁改版", Unread: 3}},
		},
		// GET /api/orgs/{org}/incidents/{id}、POST 宣告/更新
		"incident.json": sampleIncident(),
		// GET /api/orgs/{org}/incidents
		"incident_list.json": []model.Incident{sampleIncident(), minimalIncident()},
		// GET /api/orgs/{org}/decisions/{id}、POST 提出/answer/withdraw/reopen(decision:有選項,answer 是選項 id 或 other)。
		// 署名一律是呼叫者本人:createdBy、decidedBy 是相容字串(人 = email、角色 = agent:<handle>),createdByActor、decidedByActor 是 actor 物件;sourceIssue 是來源 Issue 的顯示編號 key
		"decision.json": sampleDecision(),
		// help(需要協助):沒有選項,answer 是 done / declined,可附 evidence
		"decision_help.json": sampleHelp(),
		// GET /api/orgs/{org}/decisions(舊的在前;只列呼叫者看得到的:來源 Issue 在受限專案、呼叫者不是成員的不列;開放中的 answer、answerNote 是空字串,沒有 decidedAt)
		"decision_list.json": []model.Decision{sampleDecision(), sampleHelp(), minimalDecision()},
		// POST /api/orgs/{org}/decisions/{id}/to-issue(body {priority?, labels?, repo?, project?};只有人;新 Issue 的 source 是 DEC-n,決定的 sourceIssue 指向新 Issue 的 key)
		"decision_to_issue.json": service.DecisionIssueResult{Issue: &model.Issue{ID: "0197b3a4-5c6d-7e8f-9a0b-1c2d3e4f5a6b", Key: "ISS-40", Num: 40, Version: 1, Title: "待決定的結構", Status: "open", Priority: "normal",
			Labels: []string{"待決定"}, Comments: []model.Comment{}, Source: "DEC-5", CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-21T10:01:00Z"),
			UpdatedAt: ct("2026-11-21T10:01:00Z")}, Decision: func() *model.Decision { d := minimalDecision(); d.SourceIssue = "ISS-40"; return &d }()},
		// GET /api/orgs/{org}/decisions/summary(來源 Issue key → 筆數;只算呼叫者看得到的決定)
		"decision_summary.json": map[string]model.DecisionSummary{"ISS-37": {Open: 1, Decided: 2}},
		// GET /api/orgs/{org}/issues/{id}/items/{itemId}、POST 新增、PATCH 修改、claim(輕量)/release/report/block/unblock/cancel 的回應。
		// key = Issue 顯示編號.序號;assignee、claimedBy、createdBy 是相容字串(人 = email、角色 = agent:<handle>)並存 xxxActor 物件;dependsOn、waitFor(item)輸出成子項 key;
		// 沒有負責人、前置、files、證據的欄位省略;完成的有 evidence(JSON 物件)與 completedAt
		"issue_item.json": sampleItem(),
		// 卡住:blockReason 必填,waitForKind(user_decision / item / external)與 waitFor 成對
		"issue_item_blocked.json": sampleItemBlocked(),
		// 進行中(輕量領取):claimedBy、claimedByActor、lockedUntil(租約到期)
		"issue_item_doing.json": sampleItemDoing(),
		// GET /api/orgs/{org}/issues/{id}/items(seq 順序;預設只回未完成,includeDone=1 連 done、canceled 一起;status= 指定單一狀態)
		"issue_item_list.json": []model.IssueItem{sampleItemBlocked(), sampleItemDoing(), minimalItem(), sampleItem()},
		// POST /api/orgs/{org}/issues/{id}/items/{itemId}/claim:負責人是角色時帶 task(綁定的 agent_task,links = [Issue id, 子項 id])與一次性 credential
		"issue_item_claim.json": service.ItemClaimResult{Item: func() *model.IssueItem {
			i := sampleItemDoing()
			i.Assignee, i.AssigneeActor, i.TaskID = "agent:eng", avEng, "T-12"
			return &i
		}(), Task: &model.AgentTask{ID: "T-12", AgentID: "eng", Status: "inProgress", Title: "處理子項 ISS-37.4:CLI 與 MCP", Instructions: "請處理 Issue ISS-37 的子項 ISS-37.4",
			Links: []string{"0197b3a4-0000-7e8f-9a0b-1c2d3e4f5a6b", "0197b3a4-3333-7e8f-9a0b-1c2d3e4f5a6b"}, Notes: []model.AgentTaskNote{}, Priority: "normal",
			CreatedBy: "tim@example.com", CreatedByActor: avTim, ClaimedBy: "tim@example.com", ClaimedByActor: avTim, LockedUntil: ctp("2026-11-21T17:00:00Z"),
			CreatedAt: ct("2026-11-21T09:20:00Z"), UpdatedAt: ct("2026-11-21T09:20:00Z")},
			Credential: &model.AgentGrant{AgentID: "eng", TaskID: "T-12", Secret: "s3cr3t-only-shown-once", Code: "123456", ExpiresAt: "2026-11-21T17:00:00Z"}},
		// POST /api/orgs/{org}/issues/{id}/items/{itemId}/promote:新 Issue(source 是子項 key、內文帶驗收與檔案)與標為 canceled、promotedIssueId 是新 Issue 顯示編號的子項
		"issue_item_promote.json": service.ItemPromoteResult{Issue: &model.Issue{ID: "0197b3a4-5c6d-7e8f-9a0b-1c2d3e4f5a6b", Key: "ISS-41", Num: 41, Version: 1, Title: "CLI 與 MCP", Status: "open", Priority: "normal",
			Labels: []string{}, Comments: []model.Comment{}, Source: "ISS-37.4", CreatedBy: "tim@example.com", CreatedByActor: avTim, CreatedAt: ct("2026-11-21T10:01:00Z"),
			UpdatedAt: ct("2026-11-21T10:01:00Z")}, Item: func() *model.IssueItem {
			i := sampleItemDoing()
			i.Status, i.ClaimedBy, i.ClaimedByActor, i.LockedUntil, i.PromotedIssueID, i.CompletedAt = "canceled", "", nil, nil, "ISS-41", ctp("2026-11-21T10:01:00Z")
			return &i
		}()},
		// GET /api/orgs/{org}/issues、GET /api/orgs/{org}/issues/{id}:有子項的 Issue 帶 itemsOpen(todo / doing / blocked)與 itemsTotal(不含 canceled);沒有子項就沒有這兩個欄位
		"issue_with_items.json": func() model.Issue { i := sampleIssue(); i.ItemsOpen, i.ItemsTotal = 2, 4; return i }(),
		// GET /api/orgs/{org}/cabinet
		"cabinet.json": sampleCabinet(),
		// GET /api/orgs/{org}/docs/{id}、POST 新增/修改
		"cabinet_doc.json": sampleDoc(),
		// GET /api/orgs/{org}/projects/{id}、POST /api/orgs/{org}/projects、PUT /api/orgs/{org}/projects/{id}
		"project.json": sampleProject(),
		// GET /api/orgs/{org}/projects(依建立順序;description 沒填是空字串)
		// restricted = 受限專案(只有專案成員與組織管理員看得到,看不到的不出現在清單);myRole = 呼叫者在專案的角色(admin、member,不是成員沒有這個欄位)
		"project_list.json": []model.Project{
			{ID: "PRJ-1", Name: "App", Description: "", Repos: []string{}, CreatedAt: ct("2026-10-01T00:00:00Z"), UpdatedAt: ct("2026-10-01T00:00:00Z")},
			sampleProject(),
		},
		// GET /api/orgs/{org}/projects/{id}/members(依加入時間;看不到的專案 404)。POST …/members(body {"actor": "bob@example.com", "role": "member"})回傳這位成員(同其中一筆);
		// POST …/members/remove(body {"actor": "…"})回 true。node 是相容的節點寫法(user:<email>、agent:<角色 id>),actor 是 actor 物件
		"project_member_list.json": []model.ProjectMember{
			{Project: "PRJ-2", Actor: avTim, Node: "user:tim@example.com", Role: model.ProjectRoleAdmin, CreatedAt: ct("2026-11-02T00:00:00Z")},
			{Project: "PRJ-2", Actor: avEng, Node: "agent:eng", Role: model.ProjectRoleMember, CreatedAt: ct("2026-11-03T00:00:00Z")},
		},
		// GET /api/orgs/{org}/projects/repos(整個組織的專案與 repo 連結,依專案號碼、連結時間;GET …/projects/{id}/repos 只有那個專案的)。
		// POST …/projects/{id}/repos、…/repos/delete(body {"repo": "…"})回傳更新後的專案(同 project.json,repos 是連結的 repo)
		"project_repo_list.json": []model.ProjectRepo{
			{Project: "PRJ-1", Repo: "acme/app", CreatedAt: ct("2026-11-02T00:00:00Z")},
			{Project: "PRJ-2", Repo: "acme/website", CreatedAt: ct("2026-11-03T00:00:00Z")},
			{Project: "PRJ-2", Repo: "gitlab:acme/docs", CreatedAt: ct("2026-11-03T00:00:01Z")},
		},
		// GET /api/orgs/{org}/docs/{id}、POST 新增/修改(放在專案裡的文件:多一個 project)
		"cabinet_doc_project.json": sampleProjectDoc(),
		// GET /api/orgs/{org}/cabinet?project=PRJ-2(多一個 project;未分專案的 cabinet.json 不帶)
		"cabinet_project.json": model.Cabinet{
			Project: "PRJ-2",
			Layout: []model.CabinetSection{
				{ID: "spec", Label: "規格", Path: "", Tabs: []model.CabinetTab{{Path: "/spec/home", Label: "首頁", Code: sp("P1")}}},
			},
			Docs:          []model.CabinetDoc{sampleProjectDoc()},
			LayoutSaved:   true,
			LayoutVersion: 1,
		},
		// GET /api/orgs/{org}/chain、POST(整份取代)
		"org_chain.json": model.OrgChain{
			Edges: []model.ChainEdge{
				{From: "user:tim@example.com", To: "agent:pm", Label: "目標", FromActor: avTim, ToActor: avPM},
				{From: "agent:pm", To: "agent:eng", Label: "", FromActor: avPM, ToActor: avEng},
			},
			UpdatedAt: ctp("2026-11-01T00:00:00Z"),
		},
		// GET /api/orgs/{org}/actors(ISS-36 B-4;也是各資料「誰」欄位旁邊 xxxActor 物件的型別)
		"actor_list.json": []service.ActorListItem{*avTim, *avOld, *avEng, *avPM, *avSys},
		// GET /api/orgs/{org}/members
		"member_list.json": []model.Member{{Org: "acme", UserID: "u_tim", Email: "tim@example.com", Name: "Tim", Role: "admin", AddedAt: ct("2026-09-01T00:00:00Z"), ActorRef: avTim}},
		// GET /api/admin/audit?limit=(操作者:userId、email 是相容欄位,actorRef 是 actor 物件;沒有操作者的項目沒有 actorRef)
		"audit_list.json": []model.AuditEntry{
			{ID: 2, At: ct("2026-11-05T03:00:00Z"), UserID: "u_tim", Email: "tim@example.com", Source: "login", ActorRef: avTim, Action: "issue.create", Org: "acme", Detail: map[string]any{"id": "ISS-12"}},
			{ID: 1, At: ct("2026-11-05T02:00:00Z"), UserID: "", Email: "", Action: "login.denied"},
		},
		// PUT /api/orgs/{org}、POST /api/orgs(單一組織)
		"org.json": model.Org{ID: "acme", Name: "Acme 研發", CreatedAt: ct("2026-09-01T00:00:00Z"), Symbol: "building.2", Color: "teal",
			Note: "主要組織", MyRole: "admin"},
		// GET /api/orgs(我能用的組織;myRole 是我的角色)
		"org_list.json": []model.Org{
			{ID: "acme", Name: "Acme 研發", CreatedAt: ct("2026-09-01T00:00:00Z"), Symbol: "building.2", Color: "teal", Note: "主要組織", MyRole: "admin"},
			{ID: "side_2", Name: "副業", CreatedAt: ct("2026-09-02T00:00:00Z"), MyRole: "member"},
		},
	}
}

func ptr[T any](v T) *T { return &v }

func encodeContract(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func TestContractFixtures(t *testing.T) {
	update := os.Getenv("UPDATE_CONTRACT") == "1"
	fx := contractFixtures()
	if update {
		if err := os.MkdirAll(contractDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := make([]string, 0, len(fx))
	for n := range fx {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		want, err := encodeContract(fx[name])
		if err != nil {
			t.Fatalf("%s:編碼失敗:%v", name, err)
		}
		path := filepath.Join(contractDir, name)
		if update {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s:讀不到範例檔(%v)。請執行 UPDATE_CONTRACT=1 go test ./model/ -run TestContract 產生", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s:core 輸出的 JSON 和範例檔不一致(model 欄位改了?)。\n"+
				"請執行 UPDATE_CONTRACT=1 go test ./model/ -run TestContract 重生,並跑 app(swift test)、ios、console/ui(npm test)的契約測試、同步修解碼端。\n"+
				"--- 範例檔\n%s\n--- 現在的輸出\n%s", name, got, want)
		}
	}
	// 目錄裡沒有登記的範例檔(改名或拿掉的):提醒清掉,免得其他平台還在測舊的形狀
	entries, err := os.ReadDir(contractDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			if _, ok := fx[e.Name()]; !ok {
				t.Errorf("%s:範例檔沒有在 contractFixtures 登記(改名或不用了就刪掉,並同步 Swift/TS 的契約測試)", e.Name())
			}
		}
	}
}

// TestContractFixturesRoundTrip 範例檔讀回 Go 型別再編碼要一模一樣(確認範例沒有 Go 自己讀不懂的欄位)
func TestContractFixturesRoundTrip(t *testing.T) {
	for name, v := range contractFixtures() {
		b, err := os.ReadFile(filepath.Join(contractDir, name))
		if err != nil {
			continue // TestContractFixtures 會報
		}
		// 用同一個型別的新值解碼
		target := newLike(v)
		if err := json.Unmarshal(b, target); err != nil {
			t.Errorf("%s:Go 讀不回來:%v", name, err)
			continue
		}
		again, _ := encodeContract(target)
		if !bytes.Equal(again, b) {
			t.Errorf("%s:讀回再編碼不一致\n%s", name, again)
		}
	}
}

func newLike(v any) any { return reflect.New(reflect.TypeOf(v)).Interface() }
