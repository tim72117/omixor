package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/claude"
	"omixor-core/service"
)

// fakeRun 假的 Claude:記下每次的參數與 MCP 設定檔,照 do 決定結果(不會真的呼叫 claude)
type fakeRun struct {
	mu      sync.Mutex
	opts    []claude.Options
	prompts []string
	cfgPath string
	cfgMode os.FileMode
	cfg     map[string]any
	secret  string
	dirOK   bool // 執行當下 Dir 存在
	do      func(ctx context.Context, secret string) (*claude.Result, error)
}

func (f *fakeRun) run(ctx context.Context, prompt string, o claude.Options, onEvent func(claude.Event)) (*claude.Result, error) {
	f.mu.Lock()
	f.opts = append(f.opts, o)
	f.prompts = append(f.prompts, prompt)
	if i := slices.Index(o.ExtraArgs, "--mcp-config"); i >= 0 && i+1 < len(o.ExtraArgs) {
		f.cfgPath = o.ExtraArgs[i+1]
		if st, err := os.Stat(f.cfgPath); err == nil {
			f.cfgMode = st.Mode().Perm()
		}
		b, _ := os.ReadFile(f.cfgPath)
		_ = json.Unmarshal(b, &f.cfg)
		srv, _ := f.cfg["mcpServers"].(map[string]any)["omixor"].(map[string]any)
		auth, _ := srv["headers"].(map[string]any)["Authorization"].(string)
		if _, cred, ok := strings.Cut(strings.TrimPrefix(auth, "Bearer "), ":"); ok {
			f.secret = cred
		}
	}
	if st, err := os.Stat(o.Dir); err == nil && st.IsDir() {
		f.dirOK = true
	}
	secret := f.secret
	f.mu.Unlock()
	if onEvent != nil {
		ev := &claude.Assistant{}
		ev.Message.Content = []claude.Block{{Type: "text", Text: "處理中…"}}
		onEvent(ev)
	}
	if f.do != nil {
		return f.do(ctx, secret)
	}
	return &claude.Result{Subtype: "success", Text: "已完成:整理好了", TotalCostUSD: 0.12}, nil
}

// setupRunner 測試用 App、假的 claude 與 log;回傳 log 內容的讀取函式
func setupRunner(t *testing.T, f *fakeRun) (*App, string, func() string) {
	t.Helper()
	a := newTestApp(t)
	org := a.currentOrgNow()
	oldRun, oldFind, oldLog := runClaude, findClaude, runnerLogf
	var logMu sync.Mutex
	var logs strings.Builder
	runClaude = f.run
	findClaude = func() string { return "/fake/bin/claude" }
	runnerLogf = func(format string, args ...any) {
		logMu.Lock()
		fmt.Fprintf(&logs, format+"\n", args...)
		logMu.Unlock()
	}
	t.Cleanup(func() { runClaude, findClaude, runnerLogf = oldRun, oldFind, oldLog })
	return a, org, func() string { logMu.Lock(); defer logMu.Unlock(); return logs.String() }
}

func assignTask(t *testing.T, a *App, org, agent, title string) AgentTask {
	t.Helper()
	tk, err := a.AgentOps().AssignTask(org, service.TaskInput{AgentID: agent, Title: title, Instructions: "說明:" + title, Author: MCPAuthor, Links: []string{"ISS-1"}})
	if err != nil {
		t.Fatal(err)
	}
	return *tk
}

func taskNow(t *testing.T, a *App, org, id string) AgentTask {
	t.Helper()
	tk, err := a.AgentOps().GetAgentTask(org, id)
	if err != nil {
		t.Fatal(err)
	}
	return *tk
}

func saveCfg(t *testing.T, org, agent string, c AgentRunConfig) {
	t.Helper()
	if _, err := SaveRunnerConfig(org, agent, c); err != nil {
		t.Fatal(err)
	}
}

// 只處理開了 autoRun 的角色;一次一筆
func TestRunnerOnlyAutoRunAgents(t *testing.T) {
	f := &fakeRun{}
	a, org, _ := setupRunner(t, f)
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	saveCfg(t, org, "triager", AgentRunConfig{AutoRun: false, Model: "haiku"})
	off := assignTask(t, a, org, "triager", "沒開自動執行")
	on := assignTask(t, a, org, "narrator", "開了自動執行")
	if !a.runnerTick(context.Background(), org) {
		t.Fatal("要跑開了自動執行的那筆")
	}
	if a.runnerTick(context.Background(), org) {
		t.Fatal("沒有其他可跑的了")
	}
	if len(f.opts) != 1 {
		t.Fatalf("只該跑一次:%d", len(f.opts))
	}
	if s := taskNow(t, a, org, off.ID).Status; s != "queued" {
		t.Fatalf("沒開自動執行的角色不能領:%s", s)
	}
	if s := taskNow(t, a, org, on.ID).Status; s != "done" {
		t.Fatalf("要回報 done:%s", s)
	}
}

// 有工作資料夾:檔案工具 + Bash、Dir、MCP 設定檔(600、執行完刪除)、成功回報 done、secret 不外流
func TestRunnerOptionsWithWorkdirAndSuccess(t *testing.T) {
	f := &fakeRun{}
	a, org, logs := setupRunner(t, f)
	work := t.TempDir()
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true, Model: "sonnet", Effort: "high", Workdir: work, AllowBash: true, TimeoutMinutes: 10})
	tk := assignTask(t, a, org, "narrator", "寫旁白")
	if !a.runnerTick(context.Background(), org) {
		t.Fatal("要跑")
	}
	o := f.opts[0]
	if o.Model != "sonnet" || o.Effort != "high" || !o.Lean || !o.NoPersist || o.NoTools || o.Dir != work || o.Bin != "/fake/bin/claude" {
		t.Fatalf("Options 不對:%+v", o)
	}
	if !slices.Equal(o.Tools, []string{"Read", "Glob", "Grep", "Edit", "Write", "Bash"}) {
		t.Fatalf("工具不對:%v", o.Tools)
	}
	if !slices.Equal(o.AllowedTools, []string{"mcp__omixor", "Read", "Glob", "Grep", "Edit", "Write", "Bash"}) {
		t.Fatalf("AllowedTools 不對:%v", o.AllowedTools)
	}
	if o.PermissionMode == "bypassPermissions" || slices.Contains(o.Args("x"), "bypassPermissions") {
		t.Fatal("不能用 bypassPermissions")
	}
	if !slices.Contains(o.Args("x"), "--strict-mcp-config") {
		t.Fatal("要帶 --strict-mcp-config")
	}
	if f.cfgMode != 0o600 {
		t.Fatalf("MCP 設定檔權限要 600:%o", f.cfgMode)
	}
	srv := f.cfg["mcpServers"].(map[string]any)["omixor"].(map[string]any)
	if srv["type"] != "http" || !strings.HasSuffix(srv["url"].(string), "/mcp/agent") {
		t.Fatalf("MCP 設定不對:%v", srv)
	}
	auth := srv["headers"].(map[string]any)["Authorization"].(string)
	if !strings.HasPrefix(auth, "Bearer narrator:") || f.secret == "" {
		t.Fatalf("Authorization 不對:%q", auth)
	}
	if _, err := os.Stat(f.cfgPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("執行完要刪掉 MCP 設定檔")
	}
	if _, err := os.Stat(filepath.Dir(f.cfgPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("執行完要刪掉暫存資料夾")
	}
	if !strings.Contains(o.AppendSystemPrompt, "旁白製作") || !strings.Contains(o.AppendSystemPrompt, "agent_context") || !strings.Contains(o.AppendSystemPrompt, "status=blocked") {
		t.Fatalf("系統提示不對:%s", o.AppendSystemPrompt)
	}
	if p := f.prompts[0]; !strings.Contains(p, tk.ID) || !strings.Contains(p, "寫旁白") || !strings.Contains(p, "說明:寫旁白") || !strings.Contains(p, "ISS-1") {
		t.Fatalf("prompt 不對:%s", p)
	}
	got := taskNow(t, a, org, tk.ID)
	if got.Status != "done" || got.Result != "已完成:整理好了" {
		t.Fatalf("要回報 done 與結果:%+v", got)
	}
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	if len(v.History) != 1 || v.History[0].Outcome != "done" || v.History[0].CostUSD != 0.12 || v.Current != nil || !v.ClaudeFound {
		t.Fatalf("執行紀錄不對:%+v", v)
	}
	// secret 不出現在參數、提示、log、狀態、任務裡
	for name, s := range map[string]string{
		"參數": strings.Join(o.Args(f.prompts[0]), " "), "log": logs(), "狀態": string(Marshal(v)),
		"任務": string(Marshal(got)), "動態": string(Marshal(a.C["activity"])),
	} {
		if strings.Contains(s, f.secret) {
			t.Fatalf("%s 裡有 secret", name)
		}
	}
	a.Mu.Lock()
	act := Marshal(a.C["activity"])
	a.Mu.Unlock()
	if !strings.Contains(string(act), "開始處理 "+tk.ID) || !strings.Contains(string(act), "完成 "+tk.ID) {
		t.Fatal("動態牆要有開始與完成")
	}
}

// 沒有工作資料夾:不給任何檔案工具,只有 omixor 工具,Dir 是暫存資料夾
func TestRunnerOptionsWithoutWorkdir(t *testing.T) {
	f := &fakeRun{}
	a, org, _ := setupRunner(t, f)
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true, AllowBash: true})
	assignTask(t, a, org, "narrator", "純對話")
	a.runnerTick(context.Background(), org)
	o := f.opts[0]
	if !o.NoTools || len(o.Tools) != 0 || !slices.Equal(o.AllowedTools, []string{"mcp__omixor"}) {
		t.Fatalf("沒有工作資料夾不能有檔案工具:%+v", o)
	}
	if o.Dir == "" || !f.dirOK || strings.HasPrefix(o.Dir, DataRoot()) {
		t.Fatalf("Dir 要是暫存資料夾:%q", o.Dir)
	}
	if _, err := os.Stat(o.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("暫存資料夾要刪掉")
	}
	if a := o.Args("x"); slices.Contains(a, "Bash") {
		t.Fatal("沒有工作資料夾不能開 Bash")
	}
}

// Claude 失敗 → blocked(note 寫原因、不含 secret),不再重試
func TestRunnerFailureReportsBlocked(t *testing.T) {
	f := &fakeRun{}
	f.do = func(ctx context.Context, secret string) (*claude.Result, error) {
		return nil, errors.New("claude 結束但沒有結果:未登入 " + secret)
	}
	a, org, logs := setupRunner(t, f)
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	tk := assignTask(t, a, org, "narrator", "會失敗")
	a.runnerTick(context.Background(), org)
	got := taskNow(t, a, org, tk.ID)
	if got.Status != "blocked" || len(got.Notes) == 0 || !strings.Contains(got.Notes[len(got.Notes)-1].Text, "自動執行失敗") {
		t.Fatalf("失敗要回報 blocked 並寫原因:%+v", got)
	}
	if strings.Contains(string(Marshal(got)), f.secret) || strings.Contains(logs(), f.secret) {
		t.Fatal("回報與 log 不能有 secret")
	}
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	if v.History[0].Outcome != "error" || v.LastError == "" || strings.Contains(string(Marshal(v)), f.secret) {
		t.Fatalf("執行狀態要記錯誤(不含 secret):%+v", v)
	}
	if a.runnerTick(context.Background(), org) || len(f.opts) != 1 {
		t.Fatal("失敗的任務不能一直重試")
	}

	// is_error 也算失敗
	f.do = func(ctx context.Context, secret string) (*claude.Result, error) {
		return &claude.Result{Subtype: "error_max_turns", IsError: true, Text: "超過回合數"}, nil
	}
	tk2 := assignTask(t, a, org, "narrator", "is_error")
	a.runnerTick(context.Background(), org)
	if s := taskNow(t, a, org, tk2.ID).Status; s != "blocked" {
		t.Fatalf("is_error 要回報 blocked:%s", s)
	}
}

// Claude 自己回報 done:不重複回報(結果不被蓋掉)
func TestRunnerNoDoubleReport(t *testing.T) {
	f := &fakeRun{}
	a, org, _ := setupRunner(t, f)
	f.do = func(ctx context.Context, secret string) (*claude.Result, error) {
		id := a.TasksOf(org)[0].ID
		st, res := "done", "Claude 自己寫的結果"
		if _, err := a.AgentOps().ReportTask(org, id, service.TaskReport{Status: &st, Result: &res}); err != nil {
			return nil, err
		}
		return &claude.Result{Subtype: "success", Text: "最後的回覆", TotalCostUSD: 0.5}, nil
	}
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	tk := assignTask(t, a, org, "narrator", "自己回報")
	a.runnerTick(context.Background(), org)
	got := taskNow(t, a, org, tk.ID)
	if got.Status != "done" || got.Result != "Claude 自己寫的結果" {
		t.Fatalf("已回報 done 的不能再回報:%+v", got)
	}
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	if v.History[0].Outcome != "done" || v.History[0].CostUSD != 0.5 {
		t.Fatalf("紀錄不對:%+v", v.History[0])
	}
}

// 人工停止:任務回報 blocked「人工停止」
func TestRunnerStop(t *testing.T) {
	f := &fakeRun{}
	started := make(chan struct{})
	f.do = func(ctx context.Context, secret string) (*claude.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	a, org, _ := setupRunner(t, f)
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	tk := assignTask(t, a, org, "narrator", "跑很久")
	done := make(chan struct{})
	go func() { a.runnerTick(context.Background(), org); close(done) }()
	<-started
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	if v.Current == nil || v.Current.TaskID != tk.ID || v.Current.LastOutput != "處理中…" {
		t.Fatalf("要看得到在跑的任務:%+v", v.Current)
	}
	if _, err := Action(a, "agent_runner_stop", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("停止後要結束")
	}
	got := taskNow(t, a, org, tk.ID)
	if got.Status != "blocked" || !strings.Contains(got.Notes[len(got.Notes)-1].Text, "人工停止") {
		t.Fatalf("要回報 blocked 人工停止:%+v", got)
	}
	if _, err := Action(a, "agent_runner_stop", map[string]any{}); err == nil {
		t.Fatal("沒有在跑時要回錯誤")
	}
}

// 找不到 claude:不領取,記在狀態
func TestRunnerNoClaude(t *testing.T) {
	f := &fakeRun{}
	a, org, _ := setupRunner(t, f)
	findClaude = func() string { return "" }
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	tk := assignTask(t, a, org, "narrator", "沒有 claude")
	if a.runnerTick(context.Background(), org) || len(f.opts) != 0 {
		t.Fatal("找不到 claude 不能跑")
	}
	if s := taskNow(t, a, org, tk.ID).Status; s != "queued" {
		t.Fatalf("任務要留在待領取:%s", s)
	}
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	if v.ClaudeFound || !strings.Contains(v.LastError, "找不到 claude") {
		t.Fatalf("要記在狀態:%+v", v)
	}
}

// 已被別人領走(409)就跳過
func TestRunnerSkipsLocked(t *testing.T) {
	f := &fakeRun{}
	a, org, _ := setupRunner(t, f)
	tk := assignTask(t, a, org, "narrator", "被領走")
	if _, err := a.AgentOps().ClaimTask(org, service.ClaimInput{AgentID: "narrator", TaskID: tk.ID}); err != nil {
		t.Fatal(err)
	}
	ran, err := a.runTask(context.Background(), org, tk, AgentRunConfig{AutoRun: true}, "/fake/bin/claude")
	if ran || err != nil || len(f.opts) != 0 {
		t.Fatalf("409 要跳過:%v %v", ran, err)
	}
}

// 設定檢查與存檔動作
func TestRunnerConfigValidate(t *testing.T) {
	f := &fakeRun{}
	a, org, _ := setupRunner(t, f)
	bad := []AgentRunConfig{
		{Workdir: "relative/dir"},
		{Workdir: "/no/such/dir/omixor-test"},
		{Workdir: filepath.Dir(DataRoot())}, // 包住資料資料夾
		{Effort: "turbo"},
		{Model: "--dangerously"},
		{TimeoutMinutes: 100000},
	}
	for _, c := range bad {
		if _, err := normalizeRunConfig(c); err == nil {
			t.Fatalf("要拒絕:%+v", c)
		}
	}
	c, err := normalizeRunConfig(AgentRunConfig{AllowBash: true})
	if err != nil || c.AllowBash || c.TimeoutMinutes != 30 {
		t.Fatalf("沒有工作資料夾不開 Bash、逾時預設 30:%+v %v", c, err)
	}
	work := t.TempDir()
	if _, err := Action(a, "agent_runner_save", map[string]any{"agent": "narrator", "autoRun": true, "model": "opus", "effort": "max", "workdir": work, "allowBash": false, "timeoutMinutes": float64(45)}); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "agent_runner_save", map[string]any{"agent": "nobody", "autoRun": true}); err == nil {
		t.Fatal("找不到角色要回錯誤")
	}
	file, err := LoadRunnerConfig(org)
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Agents["narrator"]; !got.AutoRun || got.Model != "opus" || got.Effort != "max" || got.Workdir != work || got.TimeoutMinutes != 45 {
		t.Fatalf("存檔不對:%+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(OrgDir(org), "agent-runner.json"))
	if !strings.Contains(string(b), `"agents"`) || !strings.Contains(string(b), `"narrator"`) {
		t.Fatalf("檔案格式不對:%s", b)
	}
}
