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

// 範例資料:欄位盡量填滿(含選填欄位、時間、巢狀);列表另外放一筆只有必填欄位的,確認省略的欄位各邊也讀得出來

func sampleIssue() model.Issue {
	return model.Issue{
		ID: "ISS-12", Title: "登入頁在 Safari 白畫面", Body: "## 重現步驟\n\n1. 開 Safari\n2. 進 /login\n\n![截圖](/api/attachments/att-1)",
		Status: "closed", Priority: "high", Labels: []string{"bug", "前端"},
		Source: "網站訪客 7d", Link: "https://example.com/login", Repo: "github:acme/web", Assignee: "eng",
		CreatedBy: "tim@example.com", CreatedAt: ct("2026-11-01T08:30:00Z"), UpdatedAt: ct("2026-11-03T10:00:05Z"),
		ClosedAt: ctp("2026-11-03T10:00:05Z"),
		Comments: []model.Comment{
			{ID: "5f0c6c1e-7a52-4d0b-9a3e-2a1c2b3d4e5f", Author: "tim@example.com", Text: "我這邊也遇到", At: ct("2026-11-01T09:00:00Z")},
			{ID: "jira-10023", Author: "agent:eng", Text: "已修正,見 PR #42", At: ct("2026-11-03T09:59:00Z")},
		},
	}
}

func minimalIssue() model.Issue {
	return model.Issue{
		ID: "ISS-3", Title: "最少欄位的 Issue", Status: "open", Priority: "normal", Labels: []string{}, Comments: []model.Comment{},
		CreatedBy: "mcp", CreatedAt: ct("2026-10-20T00:00:00Z"), UpdatedAt: ct("2026-10-20T00:00:00Z"),
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
		CreatedBy: "tim@example.com", CreatedAt: ct("2026-11-02T00:00:00Z"), UpdatedAt: ct("2026-11-02T01:00:00Z"),
		ClaimedAt: ctp("2026-11-02T00:50:00Z"), ClaimedBy: "tim@example.com", LockedUntil: ctp("2026-11-02T01:50:00Z"),
		CompletedAt: ctp("2026-11-02T02:00:00Z"), RoutineID: sp("r-daily"),
		Result: "已修正", Links: []string{"ISS-12", "https://github.com/acme/web/pull/42"},
		Notes: []model.AgentTaskNote{
			{ID: "n-1", Author: "agent:eng", Text: "開始處理", At: ct("2026-11-02T00:51:00Z")},
			{ID: "8d6a4e0b-1c2d-4e5f-8a9b-0c1d2e3f4a5b", Author: "omixor", Text: "領取逾時,退回待領取", At: ct("2026-11-02T00:55:00Z")},
		},
	}
}

func minimalTask() model.AgentTask {
	return model.AgentTask{
		ID: "T-8", AgentID: "pm", Title: "寫規格", Priority: "normal", Status: "queued", CreatedBy: "routine",
		CreatedAt: ct("2026-11-03T00:00:00Z"), UpdatedAt: ct("2026-11-03T00:00:00Z"), Links: []string{}, Notes: []model.AgentTaskNote{},
	}
}

func sampleChannel() model.ChannelSummary {
	return model.ChannelSummary{
		Channel: model.Channel{
			ID: "CH-2", Name: "登入頁改版", Topic: "討論 Safari 白畫面", IssueID: "ISS-12",
			Members:   []string{"user:tim@example.com", "agent:eng"},
			CreatedBy: "user:tim@example.com", CreatedAt: ct("2026-11-01T10:00:00Z"), UpdatedAt: ct("2026-11-02T10:00:00Z"),
		},
		Unread: 3, LastMessageAt: ctp("2026-11-03T08:00:00Z"),
	}
}

func minimalChannel() model.ChannelSummary {
	return model.ChannelSummary{Channel: model.Channel{
		ID: "CH-1", Name: "閒聊", Members: []string{"user:tim@example.com"}, CreatedBy: "user:tim@example.com",
		CreatedAt: ct("2026-10-01T00:00:00Z"), UpdatedAt: ct("2026-10-01T00:00:00Z"),
	}}
}

func sampleMessage() model.ChannelMessage {
	return model.ChannelMessage{
		ID: "M-5", ChannelID: "CH-2", Author: "user:tim@example.com", Text: "@工程師 可以看一下 **ISS-12** 嗎?",
		Mentions: []string{"agent:eng"}, At: ct("2026-11-03T08:00:00Z"),
	}
}

func sampleIncident() model.Incident {
	return model.Incident{
		ID: "INC-2041", Title: "API 延遲飆高", Sev: 2, Status: "resolved", Services: []string{"api", "db"},
		Commander: "tim@example.com", StartedAt: ct("2026-11-05T03:00:00Z"), ResolvedAt: ctp("2026-11-05T04:30:00Z"),
		Impact: "約 20% 請求逾時", AISummary: sp("資料庫連線池耗盡,調大後恢復"), FollowUps: []string{"ISS-13", "WI-7"},
		AlertIDs: []string{"alert-cpu-1"}, CreatedBy: "mcp",
		Timeline: []model.IncidentEvent{
			{ID: "0b8f2a8e-3c4d-4e5f-9a0b-1c2d3e4f5a6b", Date: ct("2026-11-05T03:00:00Z"), Actor: model.SystemActor(), Kind: "alert", Text: "p95 延遲 > 2s"},
			{ID: "1c9f3b9f-4d5e-4f60-8b1c-2d3e4f5a6b7c", Date: ct("2026-11-05T03:10:00Z"), Actor: model.PersonActor("tim@example.com"), Kind: "status", Text: "狀態改為「已找到原因」"},
			{ID: "evt-legacy-1", Date: ct("2026-11-05T03:20:00Z"), Actor: model.AIActor("mcp"), Kind: "aiFinding", Text: "連線池用滿"},
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
		Tags: []string{"課程", "L0"}, Author: "agent:pm", CreatedAt: ct("2026-10-10T00:00:00Z"), UpdatedAt: ct("2026-10-11T00:00:00Z"),
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
			{ID: "DOC-1", Title: "放在區塊本身的文件", Body: "", Section: "overview", Tags: []string{}, Author: "tim@example.com",
				CreatedAt: ct("2026-10-01T00:00:00Z"), UpdatedAt: ct("2026-10-01T00:00:00Z")},
		},
		LayoutSaved: true,
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
				{ID: "M-4", ChannelID: "CH-2", Author: "agent:eng", Text: "收到", Mentions: []string{}, At: ct("2026-11-03T07:00:00Z")},
				sampleMessage(),
			},
			HasMore: true,
		},
		// POST /api/orgs/{org}/channels/{id}/read
		"channel_read.json": model.ChannelRead{ChannelID: "CH-2", Member: "user:tim@example.com", LastRead: 5, At: ct("2026-11-03T08:01:00Z")},
		// GET /api/orgs/{org}/channels/inbox?for=
		"channel_inbox.json": service.ChannelInbox{
			Node: "agent:eng",
			Mentions: []service.InboxMention{{ChannelID: "CH-2", ChannelName: "登入頁改版", ID: "M-5", Author: "user:tim@example.com",
				Text: "@工程師 可以看一下 **ISS-12** 嗎?", At: ct("2026-11-03T08:00:00Z")}},
			Channels: []service.InboxChannel{{ID: "CH-2", Name: "登入頁改版", Unread: 3}},
		},
		// GET /api/orgs/{org}/incidents/{id}、POST 宣告/更新
		"incident.json": sampleIncident(),
		// GET /api/orgs/{org}/incidents
		"incident_list.json": []model.Incident{sampleIncident(), minimalIncident()},
		// GET /api/orgs/{org}/cabinet
		"cabinet.json": sampleCabinet(),
		// GET /api/orgs/{org}/docs/{id}、POST 新增/修改
		"cabinet_doc.json": sampleDoc(),
		// GET /api/orgs/{org}/chain、POST(整份取代)
		"org_chain.json": model.OrgChain{
			Edges: []model.ChainEdge{
				{From: "user:tim@example.com", To: "agent:pm", Label: "目標"},
				{From: "agent:pm", To: "agent:eng", Label: ""},
			},
			UpdatedAt: ctp("2026-11-01T00:00:00Z"),
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
