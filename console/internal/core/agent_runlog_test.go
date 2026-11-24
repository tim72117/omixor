package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"omixor-core/claude"
	"omixor-core/service"
)

func mustRunLog(t *testing.T, org, task string) *RunLog {
	t.Helper()
	l, err := RunLogOf(org, task, "")
	if err != nil || l == nil {
		t.Fatalf("找不到 %s 的執行日誌:%v", task, err)
	}
	return l
}

func toolUse(id, name, input string) claude.Event {
	ev := &claude.Assistant{}
	ev.Message.Content = []claude.Block{{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(input)}}
	return ev
}

func toolResult(id, text string, isErr bool) claude.Event {
	b, _ := json.Marshal([]claude.Block{{Type: "tool_result", ToolUseID: id, IsError: isErr, Content: json.RawMessage(Marshal(text))}})
	u := &claude.User{}
	u.Message.Content = b
	return u
}

// 角色呼叫了工具但沒回報:日誌依序記下工具與結果、機密被遮、過長的結果被截斷、reportedBy=runner、伺服器確認的呼叫也在
func TestRunLogStreamMaskingAndRunnerFallback(t *testing.T) {
	f := &fakeRun{preamble: noEvents}
	a, org, _ := setupRunner(t, f)
	f.events = func(secret string) []claude.Event {
		init := &claude.Init{Model: "claude-x", SessionID: "s1", MCPServers: []claude.MCPServer{{Name: "omixor", Status: "connected"}}}
		return []claude.Event{
			init,
			toolUse("t1", "mcp__omixor__agent_context", `{"task":"T-1","token":"abcdef123456","note":"`+secret+`"}`),
			toolResult("t1", "角色設定… Authorization: Bearer "+secret, false),
			toolUse("t2", "mcp__omixor__issue_update", `{"id":"ISS-1"}`),
			toolResult("t2", strings.Repeat("長", 5000), true),
		}
	}
	f.do = func(ctx context.Context, secret string) (*claude.Result, error) {
		a.RoleCallDone(org, a.TasksOf(org)[0].ID, "agent_context", nil)
		a.RoleCallDone(org, "T-other", "agent_context", nil) // 別的任務不記
		return &claude.Result{Subtype: "success", Text: "完成", TotalCostUSD: 0.1}, nil
	}
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	tk := assignTask(t, a, org, "narrator", "日誌")
	a.runnerTick(context.Background(), org)

	l := mustRunLog(t, org, tk.ID)
	var kinds, tools []string
	for _, e := range l.Entries {
		kinds = append(kinds, e.Kind)
		tools = append(tools, e.Tool)
	}
	if got := strings.Join(kinds, ","); got != "start,text,tool_call,tool_result,tool_call,tool_result,server_call,end" {
		t.Fatalf("項目順序不對:%s", got)
	}
	if tools[2] != "mcp__omixor__agent_context" || tools[3] != "mcp__omixor__agent_context" || tools[5] != "mcp__omixor__issue_update" {
		t.Fatalf("工具名稱不對:%v", tools)
	}
	if l.ReportedBy != ReportedByRunner || l.ToolCalls != 2 || l.ServerCalls != 1 || l.Model != "claude-x" || l.MCPStatus != "omixor:connected" || l.Outcome != "done" {
		t.Fatalf("摘要不對:%+v", l)
	}
	if l.Entries[3].Status != "ok" || l.Entries[5].Status != "error" || l.Entries[6].Status != "ok" {
		t.Fatalf("狀態不對:%+v", l.Entries)
	}
	if n := utf8.RuneCountInString(l.Entries[5].Text); n > runLogResultMax+1 {
		t.Fatalf("結果沒截斷:%d", n)
	}
	if n := utf8.RuneCountInString(l.Entries[2].Args); n > runLogArgsMax+1 {
		t.Fatalf("參數摘要沒截斷:%d", n)
	}
	raw := string(Marshal(l))
	for _, bad := range []string{f.secret, "abcdef123456"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("日誌不能有機密 %q:%s", bad, raw)
		}
	}
	// 視圖與動作
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	h := v.History[0]
	if h.RunID != l.RunID || h.ReportedBy != ReportedByRunner || h.EntryCount != len(l.Entries) || h.ToolCalls != 2 || h.ServerCalls != 1 {
		t.Fatalf("執行紀錄的日誌摘要不對:%+v", h)
	}
	out, err := actions["agent_runner_log"](a, map[string]any{"task": tk.ID})
	if err != nil || out.(map[string]any)["log"].(*RunLog).RunID != l.RunID {
		t.Fatalf("agent_runner_log:%v %v", out, err)
	}
	if _, err := actions["agent_runner_log"](a, map[string]any{}); err == nil {
		t.Fatal("沒給 task / run 要報錯")
	}
}

// 角色自己回報:reportedBy=role;沒有任何工具呼叫的日誌 toolCalls=0
func TestRunLogReportedByRole(t *testing.T) {
	f := &fakeRun{preamble: noEvents}
	a, org, _ := setupRunner(t, f)
	f.events = func(string) []claude.Event {
		return []claude.Event{toolUse("t1", "mcp__omixor__agent_task_report", `{"status":"done"}`)}
	}
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	tk := assignTask(t, a, org, "narrator", "自己回報")
	f.do = func(ctx context.Context, secret string) (*claude.Result, error) {
		st, res := "done", "好了"
		_, err := a.AgentOps().ReportTask(org, tk.ID, service.TaskReport{Status: &st, Result: &res})
		return &claude.Result{Subtype: "success", Text: "最後"}, err
	}
	a.runnerTick(context.Background(), org)
	if l := mustRunLog(t, org, tk.ID); l.ReportedBy != ReportedByRole || l.ToolCalls != 1 {
		t.Fatalf("要是 role:%+v", l)
	}
}

// 項目數有上限:多的丟掉並標 truncated,結束項目一定留,工具呼叫數照算
func TestRunLogEntryCap(t *testing.T) {
	r := newRunRecorder("o", Agent{ID: "a"}, AgentTask{ID: "T-1"}, time.Now(), "")
	for i := 0; i < runLogMaxEntries+100; i++ {
		r.onEvent(toolUse(fmt.Sprint(i), "mcp__omixor__x", `{}`))
	}
	l := r.finish(RunnerRecord{Outcome: "done"}, ReportedByRunner)
	if len(l.Entries) != runLogMaxEntries || !l.Truncated || l.ToolCalls != runLogMaxEntries+100 || l.Entries[len(l.Entries)-1].Kind != LogEnd {
		t.Fatalf("上限不對:%d %v %d", len(l.Entries), l.Truncated, l.ToolCalls)
	}
}

// 每個組織只留最近 runLogKeep 筆,最新的在前;同 RunID 取代
func TestRunLogRetention(t *testing.T) {
	a := newTestApp(t)
	org := a.currentOrgNow()
	for i := 0; i < runLogKeep+5; i++ {
		l := RunLog{RunID: fmt.Sprintf("r%d", i), TaskID: "T-1", StartedAt: Time{time.Now().Add(time.Duration(i) * time.Minute)}}
		if err := persistRunLog(org, l); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := loadRunLogs(org)
	if len(f.Runs) != runLogKeep || f.Runs[0].RunID != fmt.Sprintf("r%d", runLogKeep+4) || f.Runs[runLogKeep-1].RunID != "r5" {
		t.Fatalf("保留不對:%d %s..%s", len(f.Runs), f.Runs[0].RunID, f.Runs[len(f.Runs)-1].RunID)
	}
	_ = persistRunLog(org, RunLog{RunID: "r5", TaskID: "T-9", StartedAt: f.Runs[runLogKeep-1].StartedAt})
	f, _ = loadRunLogs(org)
	if len(f.Runs) != runLogKeep || f.Runs[runLogKeep-1].TaskID != "T-9" {
		t.Fatal("同 RunID 要取代")
	}
}
