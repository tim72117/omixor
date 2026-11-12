package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"omixor-core/claude"
	"omixor-core/model"
	"omixor-core/service"
)

// 角色自動執行:派發(任務排進待領取)時,角色開了「自動執行」,web 就在背景替它領取,
// 啟動這台 Mac 上的 Claude Code(core/claude,claude -p,用登入 CLI 的訂閱額度)以角色身分處理,做完回報。
//
//	設定:每個角色一份,存在組織資料夾的 agent-runner.json(這台機器的設定,不進核心)
//	迴圈:OnStart 啟動,每 15 秒(任務有變動時提早)看目前組織的待領取任務;一次只跑一筆
//	領取:AgentOps().ClaimTask(本機模式身分是 ops-web,遠端模式是雲端帳號),拿到角色憑證
//	執行:角色憑證只寫進權限 600 的暫存 MCP 設定檔(--mcp-config),跑完刪掉;不放命令列、log、prompt 或回報
//	回報:Claude 自己回報了就不重複;仍是進行中時,成功回報 done(結果用最後的回覆),失敗回報 blocked
//
// App 版(app/Sources/Omixor/AgentRunner)照同一套設定格式與流程。

// AgentRunConfig 一個角色的自動執行設定(欄位依字母順序)
type AgentRunConfig struct {
	AllowBash      bool   `json:"allowBash"`
	AutoRun        bool   `json:"autoRun"`
	Effort         string `json:"effort"`
	Model          string `json:"model"`
	TimeoutMinutes int    `json:"timeoutMinutes"`
	Workdir        string `json:"workdir"`
}

// AgentRunnerFile agent-runner.json 的內容
type AgentRunnerFile struct {
	Agents map[string]AgentRunConfig `json:"agents"`
}

// RunnerCurrent 目前在跑的任務
type RunnerCurrent struct {
	AgentID    string `json:"agentId"`
	AgentName  string `json:"agentName"`
	LastOutput string `json:"lastOutput"`
	Org        string `json:"org"`
	StartedAt  Time   `json:"startedAt"`
	TaskID     string `json:"taskId"`
	Title      string `json:"title"`
}

// RunnerRecord 一筆執行紀錄;Outcome 是 done、blocked(含 Claude 自己回報的 blocked 與人工停止)、error(Claude 執行失敗,任務已回報 blocked)
type RunnerRecord struct {
	AgentID         string  `json:"agentId"`
	AgentName       string  `json:"agentName"`
	CostUSD         float64 `json:"costUsd"`
	DurationSeconds int     `json:"durationSeconds"`
	Error           string  `json:"error,omitempty"`
	FinishedAt      Time    `json:"finishedAt"`
	Outcome         string  `json:"outcome"`
	StartedAt       Time    `json:"startedAt"`
	TaskID          string  `json:"taskId"`
	Title           string  `json:"title"`
}

const (
	agentRunnerFileName   = "agent-runner.json"
	runnerHistoryMax      = 20
	runnerDefaultTimeout  = 30
	runnerMaxTimeout      = 8 * 60 // 不超過核心的租約總長(MaxLeaseTotal)
	runnerResultMaxRunes  = 4000
	runnerOutputMaxRunes  = 600
	runnerErrorMaxRunes   = 500
	runnerClaimRetryAfter = 5 * time.Minute
)

// 可替換的部分(測試換成假的;不要真的呼叫 claude)
var (
	// runClaude 執行一次 Claude
	runClaude = claude.Run
	// findClaude 找 claude 執行檔;找不到回 ""
	findClaude = lookClaudeBin
	// runnerLogf 執行紀錄的 log(不能帶憑證)
	runnerLogf = func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "角色自動執行:"+format+"\n", args...)
	}
	// runnerInterval 迴圈間隔;runnerRenewEvery 執行中續約的間隔(核心的憑證 2 小時到期)
	runnerInterval   = 15 * time.Second
	runnerRenewEvery = 40 * time.Minute
)

// RunnerEfforts 推論強度(空白 = CLI 預設)
var RunnerEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// runnerFileTools 有工作資料夾時給的檔案工具(allowBash 再加 Bash)
var runnerFileTools = []string{"Read", "Glob", "Grep", "Edit", "Write"}

var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-\[\]]*$`)

// lookClaudeBin 找 claude:環境變數 OMIXOR_CLAUDE_BIN > 常見安裝位置 > PATH(同 App 版)
func lookClaudeBin() string {
	var cands []string
	if b := os.Getenv("OMIXOR_CLAUDE_BIN"); b != "" {
		cands = append(cands, b)
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, "/opt/homebrew/bin/claude", "/usr/local/bin/claude",
			filepath.Join(home, ".local", "bin", "claude"), filepath.Join(home, ".claude", "local", "claude"))
	}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" {
			cands = append(cands, filepath.Join(d, "claude"))
		}
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}

// ---- 設定檔 ----

func agentRunnerPath(org string) string { return filepath.Join(OrgDir(org), agentRunnerFileName) }

// LoadRunnerConfig 讀組織的自動執行設定(沒有檔案是空的)
func LoadRunnerConfig(org string) (AgentRunnerFile, error) {
	f := AgentRunnerFile{Agents: map[string]AgentRunConfig{}}
	if org == "" {
		return f, nil
	}
	if err := ReadJSON(agentRunnerPath(org), &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return AgentRunnerFile{Agents: map[string]AgentRunConfig{}}, err
	}
	if f.Agents == nil {
		f.Agents = map[string]AgentRunConfig{}
	}
	return f, nil
}

// normalizeRunConfig 檢查並補預設值(存檔前、執行前都要過)
func normalizeRunConfig(c AgentRunConfig) (AgentRunConfig, error) {
	c.Model = strings.TrimSpace(c.Model)
	c.Effort = strings.TrimSpace(c.Effort)
	c.Workdir = strings.TrimSpace(c.Workdir)
	if c.Model != "" && !modelNameRe.MatchString(c.Model) {
		return c, UserError{"模型名稱不對:" + c.Model + "(opus、sonnet、haiku 或完整 id)"}
	}
	if c.Effort != "" && !iaOneOf(c.Effort, RunnerEfforts) {
		return c, UserError{"推論強度必須是 " + strings.Join(RunnerEfforts, "、") + " 之一"}
	}
	if c.TimeoutMinutes <= 0 {
		c.TimeoutMinutes = runnerDefaultTimeout
	}
	if c.TimeoutMinutes > runnerMaxTimeout {
		return c, UserError{fmt.Sprintf("逾時最多 %d 分鐘", runnerMaxTimeout)}
	}
	if c.Workdir != "" {
		if !filepath.IsAbs(c.Workdir) {
			return c, UserError{"工作資料夾要是絕對路徑:" + c.Workdir}
		}
		c.Workdir = filepath.Clean(c.Workdir)
		st, err := os.Stat(c.Workdir)
		if err != nil || !st.IsDir() {
			return c, UserError{"工作資料夾不存在:" + c.Workdir}
		}
		// 不能包住 omixor 的資料與憑證(~/.config/omixor、資料資料夾),否則檔案工具讀得到
		for _, p := range runnerProtectedDirs() {
			if within(p, c.Workdir) {
				return c, UserError{"工作資料夾不能包含 omixor 的資料或憑證資料夾(" + p + "),請選更下層的專案資料夾"}
			}
		}
	}
	if c.AllowBash && c.Workdir == "" {
		c.AllowBash = false // 沒有工作資料夾不開任何工具
	}
	return c, nil
}

func runnerProtectedDirs() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "omixor"))
	}
	return append(out, DataRoot())
}

// within p 在 dir 底下(或就是 dir)
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// SaveRunnerConfig 存某角色的設定
func SaveRunnerConfig(org, agentID string, c AgentRunConfig) (AgentRunConfig, error) {
	c, err := normalizeRunConfig(c)
	if err != nil {
		return c, err
	}
	f, err := LoadRunnerConfig(org)
	if err != nil {
		return c, err
	}
	f.Agents[agentID] = c
	return c, WriteJSON(agentRunnerPath(org), f)
}

// ---- 執行狀態 ----

type runnerState struct {
	mu        sync.Mutex
	loop      bool // 迴圈有在跑(OPS_NO_TIMERS=1 時不跑)
	current   *RunnerCurrent
	cancel    context.CancelFunc
	stopped   bool // 人工停止目前這筆
	history   []RunnerRecord
	lastError string
	skip      map[string]time.Time // org/任務 → 這個時間前不再試(領取失敗、不是 409 的錯誤)
	wake      chan struct{}
}

var runnerStates sync.Map // *App → *runnerState

func (a *App) runSt() *runnerState {
	if s, ok := runnerStates.Load(a); ok {
		return s.(*runnerState)
	}
	s, _ := runnerStates.LoadOrStore(a, &runnerState{skip: map[string]time.Time{}, wake: make(chan struct{}, 1)})
	return s.(*runnerState)
}

func (s *runnerState) setError(msg string) {
	s.mu.Lock()
	s.lastError = msg
	s.mu.Unlock()
}

// wakeRunner 讓迴圈提早檢查
func (a *App) wakeRunner() {
	select {
	case a.runSt().wake <- struct{}{}:
	default:
	}
}

// runnerLoop 每 runnerInterval 檢查一次;「agents」主題有變動時提早
func (a *App) runnerLoop() {
	st := a.runSt()
	st.mu.Lock()
	st.loop = true
	st.mu.Unlock()
	ch := a.Subscribe()
	defer a.Unsubscribe(ch)
	tick := time.NewTicker(runnerInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
		case <-st.wake:
		case ev := <-ch:
			if !hasTopic(ev.Topics, "agents") {
				continue
			}
		}
		for {
			org := a.currentOrgNow()
			if org == "" || !a.runnerTick(context.Background(), org) {
				break
			}
		}
		// 跑的期間累積的變動通知不用一則一則處理
		for drained := false; !drained; {
			select {
			case <-ch:
			default:
				drained = true
			}
		}
	}
}

func hasTopic(topics []string, t string) bool {
	for _, x := range topics {
		if x == t || x == "*" {
			return true
		}
	}
	return false
}

// runnerTick 挑一筆開了自動執行的待領取任務來跑;有跑回 true(呼叫端會馬上再挑下一筆)
func (a *App) runnerTick(ctx context.Context, org string) bool {
	cfg, err := LoadRunnerConfig(org)
	if err != nil {
		a.runSt().setError("讀不到自動執行設定:" + err.Error())
		return false
	}
	auto := false
	for _, c := range cfg.Agents {
		auto = auto || c.AutoRun
	}
	if !auto {
		return false
	}
	active := map[string]bool{}
	for _, g := range a.AgentsOf(org) {
		active[g.ID] = g.Active
	}
	st := a.runSt()
	now := time.Now()
	var cands []AgentTask
	st.mu.Lock()
	for _, t := range a.TasksOf(org) {
		if t.Status != "queued" || !cfg.Agents[t.AgentID].AutoRun || !active[t.AgentID] {
			continue
		}
		if until, ok := st.skip[org+"/"+t.ID]; ok && now.Before(until) {
			continue
		}
		cands = append(cands, t)
	}
	st.mu.Unlock()
	if len(cands) == 0 {
		return false
	}
	bin := findClaude()
	if bin == "" {
		// 不領取:任務留在待領取,裝好 claude 或別的執行者都還能處理
		st.setError("找不到 claude(Claude Code CLI):請安裝並用訂閱帳號登入,或設定 OMIXOR_CLAUDE_BIN")
		a.Changed("agent_runner")
		return false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		pi, pj := runnerPriority(cands[i].Priority), runnerPriority(cands[j].Priority)
		if pi != pj {
			return pi < pj
		}
		return cands[i].CreatedAt.Before(cands[j].CreatedAt.Time)
	})
	for _, t := range cands {
		ran, err := a.runTask(ctx, org, t, cfg.Agents[t.AgentID], bin)
		if err != nil {
			st.mu.Lock()
			st.skip[org+"/"+t.ID] = time.Now().Add(runnerClaimRetryAfter)
			st.lastError = err.Error()
			st.mu.Unlock()
			a.Changed("agent_runner")
			continue
		}
		if ran {
			return true
		}
	}
	return false
}

func runnerPriority(p string) int {
	for i, v := range model.IssuePriorities {
		if v == p {
			return i
		}
	}
	return len(model.IssuePriorities)
}

// runTask 領取並執行一筆。被別人領走(409)回 false、nil;領取時其他錯誤回 error(之後一段時間不再試);
// 領到之後的任何失敗都回報 blocked,不回 error
func (a *App) runTask(ctx context.Context, org string, t AgentTask, c AgentRunConfig, bin string) (bool, error) {
	ops := a.AgentOps()
	res, err := ops.ClaimTask(org, service.ClaimInput{AgentID: t.AgentID, TaskID: t.ID})
	if err != nil {
		if service.Code(err) == 409 {
			runnerLogf("%s 已被別人領取,跳過", t.ID)
			return false, nil
		}
		runnerLogf("領取 %s 失敗:%v", t.ID, err)
		return false, fmt.Errorf("領取 %s 失敗:%v", t.ID, err)
	}
	if res == nil || res.Task == nil || res.Credential == nil {
		return false, nil
	}
	task := *res.Task
	g := Agent{ID: task.AgentID, Name: task.AgentID}
	if res.Agent != nil {
		g = *res.Agent
	}
	cred := *res.Credential
	secret := cred.Secret
	scrub := func(s string) string {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
		return s
	}

	st := a.runSt()
	started := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st.mu.Lock()
	st.current = &RunnerCurrent{AgentID: g.ID, AgentName: g.Name, Org: org, StartedAt: Time{started.UTC().Truncate(time.Second)}, TaskID: task.ID, Title: task.Title}
	st.cancel, st.stopped = cancel, false
	st.mu.Unlock()
	a.Changed("agent_runner")
	runnerLogf("開始 %s「%s」(角色 %s)", task.ID, task.Title, g.ID)

	rec := RunnerRecord{AgentID: g.ID, AgentName: g.Name, StartedAt: Time{started.UTC().Truncate(time.Second)}, TaskID: task.ID, Title: task.Title}
	result, costUSD, runErr := a.execClaude(runCtx, org, g, task, c, bin, cred, func(text string) {
		st.mu.Lock()
		if st.current != nil {
			st.current.LastOutput = tailRunes(scrub(text), runnerOutputMaxRunes)
		}
		st.mu.Unlock()
		a.Changed("agent_runner")
	})
	rec.CostUSD = costUSD

	st.mu.Lock()
	stopped := st.stopped
	st.current, st.cancel, st.stopped = nil, nil, false
	st.mu.Unlock()

	// 回報:Claude 已經自己回報(不是進行中)就不重複
	rec.Outcome = "error"
	cur, gerr := ops.GetAgentTask(org, task.ID)
	switch {
	case gerr != nil:
		rec.Error = scrub("讀不到任務狀態:" + gerr.Error())
	case cur.Status != "inProgress":
		rec.Outcome = cur.Status
		if cur.Status == "done" || cur.Status == "blocked" {
			a.logRunnerFinal(g, *cur)
		}
		if runErr != nil && cur.Status != "done" {
			rec.Error = truncRunes(scrub(runErr.Error()), runnerErrorMaxRunes)
		}
	default:
		var rep service.TaskReport
		status := "blocked"
		if stopped {
			note := "人工停止自動執行"
			rep, rec.Outcome, rec.Error = service.TaskReport{Status: &status, Note: &note}, "blocked", "人工停止"
		} else if runErr != nil {
			msg := truncRunes(scrub(runErr.Error()), runnerErrorMaxRunes)
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				msg = fmt.Sprintf("逾時(%d 分鐘)", c.TimeoutMinutes)
			}
			note := "自動執行失敗:" + msg
			rep, rec.Error = service.TaskReport{Status: &status, Note: &note}, msg
		} else {
			status = "done"
			text := truncRunes(scrub(strings.TrimSpace(result)), runnerResultMaxRunes)
			if text == "" {
				text = "(自動執行完成,Claude 沒有留下文字結果)"
			}
			rep, rec.Outcome = service.TaskReport{Status: &status, Result: &text}, "done"
		}
		if _, err := ops.ReportTask(org, task.ID, rep); err != nil {
			rec.Outcome = "error"
			rec.Error = scrub("回報失敗:" + err.Error())
		}
	}
	fin := time.Now()
	rec.FinishedAt = Time{fin.UTC().Truncate(time.Second)}
	rec.DurationSeconds = int(fin.Sub(started).Seconds())
	st.mu.Lock()
	st.history = append([]RunnerRecord{rec}, st.history...)
	if len(st.history) > runnerHistoryMax {
		st.history = st.history[:runnerHistoryMax]
	}
	if rec.Outcome == "error" {
		st.lastError = task.ID + ":" + rec.Error
	} else {
		st.lastError = ""
	}
	st.mu.Unlock()
	a.Changed("agent_runner")
	runnerLogf("結束 %s:%s %s", task.ID, rec.Outcome, rec.Error)
	return true, nil
}

// logRunnerFinal Claude 自己回報 done/blocked 時,在動態牆補一筆(走 web 的 ReportTask 才會自動記)
func (a *App) logRunnerFinal(g Agent, t AgentTask) {
	o := a.AgentOps()
	switch t.Status {
	case "done":
		o.logf(AI(g.ID), "checkmark.circle", g.Name+"完成 "+t.ID+"「"+t.Title+"」(自動執行)")
	case "blocked":
		o.logf(AI(g.ID), "hand.raised", g.Name+"在 "+t.ID+"「"+t.Title+"」需要協助(自動執行)")
	}
}

// runnerOptions 組出這次執行的 Claude 參數(MCP 設定檔路徑另外帶;憑證不在這裡)
func runnerOptions(g Agent, c AgentRunConfig, bin, mcpConfig, scratch string) claude.Options {
	o := claude.Options{
		Model: c.Model, Effort: c.Effort, Bin: bin,
		Lean: true, NoPersist: true,
		AllowedTools:       []string{"mcp__omixor"},
		AppendSystemPrompt: runnerSystemPrompt(g, c),
		ExtraArgs:          []string{"--mcp-config", mcpConfig},
	}
	if c.Workdir != "" {
		tools := append([]string{}, runnerFileTools...)
		if c.AllowBash {
			tools = append(tools, "Bash")
		}
		o.Tools = tools
		o.AllowedTools = append(o.AllowedTools, tools...)
		o.Dir = c.Workdir
	} else {
		o.NoTools = true
		o.Dir = scratch
	}
	return o
}

func runnerSystemPrompt(g Agent, c AgentRunConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是 omixor 的虛擬角色「%s」(id: %s),由這台 Mac 上的 omixor 自動執行,旁邊沒有人即時回答問題。\n", g.Name, g.ID)
	if g.Role != "" {
		fmt.Fprintf(&b, "職責:%s\n", g.Role)
	}
	if s := strings.TrimSpace(g.Instructions); s != "" {
		b.WriteString("\n## 角色設定\n\n" + s + "\n")
	}
	b.WriteString("\n## 規則\n\n")
	b.WriteString("1. 先用 ops_agent_context 讀角色設定與任務。\n")
	b.WriteString("2. 照任務說明完成。\n")
	b.WriteString("3. 需要在 Issue 說明時,用 ops_issue_update 留言。\n")
	b.WriteString("4. 做完用 ops_agent_task_report 回報 status=done,在 result 寫結果;需要人決定時回報 status=blocked,在 note 寫要問的問題,然後結束。\n")
	b.WriteString("5. 不要把任何憑證、密碼、token 或金鑰寫進 Issue、留言、文件或回報。\n")
	if c.Workdir != "" {
		fmt.Fprintf(&b, "6. 工作資料夾是 %s,檔案只在這個資料夾裡讀寫。\n", c.Workdir)
	} else {
		b.WriteString("6. 這次沒有檔案工具,只能用 omixor 的工具。\n")
	}
	return b.String()
}

func runnerPrompt(t AgentTask) string {
	var b strings.Builder
	fmt.Fprintf(&b, "請處理任務 %s:%s\n", t.ID, t.Title)
	if s := strings.TrimSpace(t.Instructions); s != "" {
		b.WriteString("\n任務說明:\n" + s + "\n")
	}
	if len(t.Links) > 0 {
		b.WriteString("\n相關連結:" + strings.Join(t.Links, "、") + "\n")
	}
	b.WriteString("\n完成後用 ops_agent_task_report 回報。")
	return b.String()
}

// execClaude 寫 MCP 設定檔、啟動 Claude、執行中續約;回傳最後的回覆、估計花費與錯誤(錯誤訊息可能要再過濾憑證)
func (a *App) execClaude(ctx context.Context, org string, g Agent, t AgentTask, c AgentRunConfig, bin string, cred model.AgentGrant, onText func(string)) (string, float64, error) {
	c, err := normalizeRunConfig(c)
	if err != nil {
		return "", 0, fmt.Errorf("自動執行設定不正確:%v", err)
	}
	a.Mu.Lock()
	endpoint := a.AgentEndpointURL()
	a.Mu.Unlock()

	// 暫存:MCP 設定檔(含憑證,權限 600)放在 0700 的資料夾;沒有工作資料夾時 Claude 在空的 work 裡執行
	tmp, err := os.MkdirTemp("", "omixor-agent-run-")
	if err != nil {
		return "", 0, fmt.Errorf("建立暫存資料夾:%v", err)
	}
	defer os.RemoveAll(tmp)
	scratch := filepath.Join(tmp, "work")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		return "", 0, fmt.Errorf("建立暫存資料夾:%v", err)
	}
	cfgPath := filepath.Join(tmp, "mcp.json")
	conf := map[string]any{"mcpServers": map[string]any{"omixor": map[string]any{
		"type": "http", "url": endpoint,
		"headers": map[string]string{"Authorization": "Bearer " + cred.AgentID + ":" + cred.Secret},
	}}}
	if err := os.WriteFile(cfgPath, Marshal(conf), 0o600); err != nil {
		return "", 0, fmt.Errorf("寫 MCP 設定檔:%v", err)
	}
	defer os.Remove(cfgPath)

	o := runnerOptions(g, c, bin, cfgPath, scratch)
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMinutes)*time.Minute)
	defer cancel()

	// 執行中續約(租約與憑證從續約時再延)
	renewDone := make(chan struct{})
	defer close(renewDone)
	go func() {
		tk := time.NewTicker(runnerRenewEvery)
		defer tk.Stop()
		for {
			select {
			case <-renewDone:
				return
			case <-tk.C:
				st, note := "inProgress", "自動執行中"
				if _, err := a.AgentOps().ReportTask(org, t.ID, service.TaskReport{Status: &st, Note: &note}); err != nil {
					runnerLogf("%s 續約失敗:%v", t.ID, err)
				}
			}
		}
	}()

	var last string
	res, err := runClaude(runCtx, runnerPrompt(t), o, func(ev claude.Event) {
		if as, ok := ev.(*claude.Assistant); ok {
			if s := strings.TrimSpace(as.Text()); s != "" {
				last = s
				onText(s)
			}
		}
	})
	cost := 0.0
	text := last
	if res != nil {
		cost = res.TotalCostUSD
		if strings.TrimSpace(res.Text) != "" {
			text = res.Text
		}
	}
	if err == nil && res != nil && res.IsError {
		err = fmt.Errorf("claude 回報錯誤(%s):%s", res.Subtype, res.Text)
	}
	if err != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("逾時(%d 分鐘)", c.TimeoutMinutes)
	}
	return text, cost, err
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func tailRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return "…" + string(r[len(r)-n:])
}

// ---- 視圖與動作 ----

// AgentRunnerView 視圖 agent_runner
type AgentRunnerView struct {
	Agents      []RunnerAgentRow `json:"agents"`
	ClaudeBin   string           `json:"claudeBin"`
	ClaudeFound bool             `json:"claudeFound"`
	Current     *RunnerCurrent   `json:"current"`
	Efforts     []string         `json:"efforts"`
	Error       string           `json:"error,omitempty"`
	History     []RunnerRecord   `json:"history"`
	LastError   string           `json:"lastError,omitempty"`
	Loop        bool             `json:"loop"`
	Org         string           `json:"org"`
}

// RunnerAgentRow 一個角色與它的設定
type RunnerAgentRow struct {
	Active bool           `json:"active"`
	Config AgentRunConfig `json:"config"`
	ID     string         `json:"id"`
	Name   string         `json:"name"`
}

// runnerView 在 Mu 下執行(只讀本機檔案與快取)
func (a *App) runnerView() AgentRunnerView {
	org := a.CurrentOrgID()
	v := AgentRunnerView{Org: org, Agents: []RunnerAgentRow{}, History: []RunnerRecord{}, Efforts: RunnerEfforts}
	bin := findClaude()
	v.ClaudeBin, v.ClaudeFound = bin, bin != ""
	cfg, err := LoadRunnerConfig(org)
	if err != nil {
		v.Error = err.Error()
	}
	for _, g := range a.Agents() {
		c, ok := cfg.Agents[g.ID]
		if !ok || c.TimeoutMinutes <= 0 {
			c.TimeoutMinutes = runnerDefaultTimeout
		}
		v.Agents = append(v.Agents, RunnerAgentRow{Active: g.Active, Config: c, ID: g.ID, Name: g.Name})
	}
	st := a.runSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.current != nil {
		cur := *st.current
		v.Current = &cur
	}
	v.History = append(v.History, st.history...)
	v.LastError, v.Loop = st.lastError, st.loop
	return v
}

// StopRunner 停掉目前在跑的那筆(任務回報 blocked「人工停止」);沒有在跑回 false
func (a *App) StopRunner() bool {
	st := a.runSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.current == nil || st.cancel == nil {
		return false
	}
	st.stopped = true
	st.cancel()
	return true
}

func init() {
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go a.runnerLoop()
	})
	RegisterView("agent_runner", func(a *App) any { return a.runnerView() })
	RegisterAction("agent_runner_save", func(a *App, args map[string]any) (any, error) {
		id := strings.TrimSpace(agentArg(args, "agent"))
		if id == "" {
			return nil, UserError{"要給角色 id(agent)"}
		}
		org := a.currentOrgNow()
		if org == "" {
			return nil, UserError{"還沒有組織"}
		}
		if a.FindAgentIn(org, id) == nil {
			return nil, UserError{"找不到角色 " + id}
		}
		var c AgentRunConfig
		m := map[string]any{}
		for k, v := range args {
			if k != "agent" && k != UserKey {
				m[k] = v
			}
		}
		if err := ReadInto(m, &c); err != nil {
			return nil, UserError{"設定格式不對:" + err.Error()}
		}
		saved, err := SaveRunnerConfig(org, id, c)
		if err != nil {
			return nil, agentUserErr(err)
		}
		name := a.AgentOps().agentName(org, id)
		state := "關閉"
		if saved.AutoRun {
			state = "開啟"
		}
		a.AgentOps().logf(UserActor(a, args), "play.circle", state+name+"的自動執行")
		a.Changed("agent_runner")
		a.wakeRunner()
		return saved, nil
	})
	RegisterAction("agent_runner_stop", func(a *App, args map[string]any) (any, error) {
		if !a.StopRunner() {
			return nil, UserError{"目前沒有在自動執行的任務"}
		}
		return map[string]any{"stopped": true}, nil
	})
}
