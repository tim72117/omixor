package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"omixor-core/claude"
	"omixor-core/config"
	"omixor-core/model"
	"omixor-core/service"
)

// 角色自動執行:派發(任務排進待領取)時,角色開了「自動執行」,web 就在背景替它領取,
// 啟動這台 Mac 上的 Claude Code(core/claude,claude -p,用登入 CLI 的訂閱額度)以角色身分處理,做完回報。
//
//	設定:每個角色一份,存在組織資料夾的 agent-runner.json(這台機器的設定,不進核心)
//	迴圈:OnStart 啟動,每 15 秒(任務有變動時提早)看目前組織的待領取任務;一次只跑一筆
//	領取:AgentOps().ClaimTask(本機模式身分是 omixor-console,遠端模式是雲端帳號),拿到角色憑證
//	執行:角色憑證只寫進權限 600 的暫存 MCP 設定檔(--mcp-config),跑完刪掉;不放命令列、log、prompt 或回報
//	回報:Claude 自己回報了就不重複;仍是進行中時,成功回報 done(結果用最後的回覆),失敗回報 blocked
//
// App 版(mac/Sources/Omixor/AgentRunner)照同一套設定格式與流程。

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
	// 執行日誌的摘要(完整日誌用 agent_runner_log 取,見 agent_runlog.go)
	EntryCount  int    `json:"entryCount"`
	ReportedBy  string `json:"reportedBy"` // role、runner 或空(不知道)
	RunID       string `json:"runId"`
	ServerCalls int    `json:"serverCalls"`
	ToolCalls   int    `json:"toolCalls"`
}

const (
	agentRunnerFileName    = "agent-runner.json"
	runnerHistoryMax       = 20
	runnerDefaultTimeout   = 30
	runnerMaxTimeout       = 8 * 60 // 不超過核心的租約總長(MaxLeaseTotal)
	runnerResultMaxRunes   = 4000
	runnerOutputMaxRunes   = 600
	runnerErrorMaxRunes    = 500
	runnerClaimRetryAfter  = 5 * time.Minute
	runnerMCPConnectWaitMs = 15000 // 第一輪前最多等 omixor 的 MCP 連線這麼久(毫秒)
)

// 可替換的部分(測試換成假的;不要真的呼叫 claude)
var (
	// runClaude 執行一次 Claude
	runClaude = claude.Run
	// findClaude 找 claude 執行檔;找不到回 ""
	findClaude = lookClaudeBin
	// claudeAuthStatus 執行 `claude auth status --json`(只查登入狀態,不呼叫模型、不碰憑證);測試換成假的
	claudeAuthStatus = func(ctx context.Context, bin string) ([]byte, error) {
		return exec.CommandContext(ctx, bin, "auth", "status", "--json").Output()
	}
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
	if b := config.Current().Str(config.ClaudeBin); b != "" {
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

// ---- claude 登入檢查 ----

type loginState int

const (
	loginUnknown loginState = iota // 還沒檢查或檢查失敗(逾時、指令不支援):不當成已登入
	loginIn
	loginOut
)

const (
	loginCacheTTL     = 30 * time.Second
	loginCheckTimeout = 5 * time.Second
	loginOutHint      = "找到 claude,但還沒登入:在終端機執行 claude,再輸入 /login"
	loginOutError     = "claude 尚未登入:在終端機執行 claude,再輸入 /login;登入後會自動繼續,任務先留在待領取"
	loginUnknownError = "無法確認 claude 是否已登入(`claude auth status` 沒有回應或不支援),任務先留在待領取"
)

// parseClaudeAuth 解析 `claude auth status --json` 的輸出。
// 已驗證(claude 2.1.205):未登入時輸出 {"loggedIn":false,...} 且結束碼 0;已登入時假設為 {"loggedIn":true,...}。
// 也容許純文字輸出(含 "Not logged in" 視為未登入)。逾時、指令失敗、看不懂的輸出都回 loginUnknown。
func parseClaudeAuth(out []byte, runErr error) loginState {
	var j struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &j) == nil && j.LoggedIn != nil {
		if *j.LoggedIn {
			return loginIn
		}
		return loginOut
	}
	if strings.Contains(strings.ToLower(string(out)), "not logged in") {
		return loginOut
	}
	return loginUnknown
}

// setLogin 記下登入狀態;狀態變了回 true。登入後清掉先前「尚未登入」的 lastError
func (s *runnerState) setLogin(bin string, ls loginState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.loginBin != bin || s.login != ls
	s.loginBin, s.login, s.loginAt = bin, ls, time.Now()
	if ls == loginIn && (s.lastError == loginOutError || s.lastError == loginUnknownError) {
		s.lastError = ""
		changed = true
	}
	return changed
}

// refreshLogin 同步檢查一次(最多 loginCheckTimeout)並更新快取
func (a *App) refreshLogin(ctx context.Context, bin string) loginState {
	cctx, cancel := context.WithTimeout(ctx, loginCheckTimeout)
	defer cancel()
	out, err := claudeAuthStatus(cctx, bin)
	ls := parseClaudeAuth(out, err)
	if cctx.Err() != nil {
		ls = loginUnknown
	}
	if a.runSt().setLogin(bin, ls) {
		a.Changed("agent_runner")
	}
	return ls
}

// ensureLoggedIn 給領任務前用:快取還新鮮就直接用,否則同步檢查
func (a *App) ensureLoggedIn(ctx context.Context, bin string) loginState {
	st := a.runSt()
	st.mu.Lock()
	if st.loginBin == bin && time.Since(st.loginAt) < loginCacheTTL {
		ls := st.login
		st.mu.Unlock()
		return ls
	}
	st.mu.Unlock()
	return a.refreshLogin(ctx, bin)
}

// loginForView 給畫面用,不阻塞:回快取(可能過期、可能 unknown),需要時在背景更新後通知畫面
func (a *App) loginForView(bin string) loginState {
	st := a.runSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	ls := loginUnknown
	if st.loginBin == bin {
		ls = st.login
	}
	if (st.loginBin != bin || time.Since(st.loginAt) >= loginCacheTTL) && !st.loginChecking {
		st.loginChecking = true
		go func() {
			a.refreshLogin(context.Background(), bin)
			st.mu.Lock()
			st.loginChecking = false
			st.mu.Unlock()
		}()
	}
	return ls
}

// notLoggedInBlock 不能啟動的原因(空白 = 已登入)
func notLoggedInBlock(ls loginState) string {
	switch ls {
	case loginIn:
		return ""
	case loginOut:
		return loginOutError
	}
	return loginUnknownError
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

// runnerCfgMu 設定檔是整份讀出、改一個角色、整份寫回;不同角色同時存(快速連按兩個開關)時要序列化,否則後寫的會蓋掉先寫的
var runnerCfgMu sync.Mutex

// SaveRunnerConfig 存某角色的設定
func SaveRunnerConfig(org, agentID string, c AgentRunConfig) (AgentRunConfig, error) {
	c, err := normalizeRunConfig(c)
	if err != nil {
		return c, err
	}
	runnerCfgMu.Lock()
	defer runnerCfgMu.Unlock()
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
	loop      bool // 迴圈有在跑(OMIXOR_NO_TIMERS=1 時不跑)
	current   *RunnerCurrent
	cancel    context.CancelFunc
	stopped   bool // 人工停止目前這筆
	busy      bool // 有一筆正在領取或執行(迴圈挑的、或人工啟動的);同一時間只跑一筆
	history   []RunnerRecord
	seeded    map[string]bool // 已從日誌檔補進 history 的組織(重開後執行紀錄不會是空的)
	rec       *runRecorder    // 進行中那筆的執行日誌
	lastError string
	skip      map[string]time.Time // org/任務 → 這個時間前不再試(領取失敗、不是 409 的錯誤)
	wake      chan struct{}
	// claude 登入狀態快取(只在 mu 下讀寫)
	loginBin      string
	login         loginState
	loginAt       time.Time
	loginChecking bool
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

// runnerFeature 系統設定裡「自動執行」的功能 key(見 core/model/features.go)
const runnerFeature = "automation.runner"

// runnerTick 挑一筆開了自動執行的待領取任務來跑;有跑回 true(呼叫端會馬上再挑下一筆)。
// 系統設定關掉「自動執行」時不領新任務(角色各自的 AutoRun 照舊保留;進行中的那筆照常做完,做完後下一輪就停)
func (a *App) runnerTick(ctx context.Context, org string) bool {
	if !a.featureEnabled(org, runnerFeature) {
		return false
	}
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
	// 一次一筆:人工啟動的那筆還在跑時,迴圈不另外領取(那筆結束後迴圈會被喚醒)
	if !a.runSt().acquire() {
		return false
	}
	defer a.runSt().release()
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
	if msg := notLoggedInBlock(a.ensureLoggedIn(ctx, bin)); msg != "" {
		// 不領取、任務留在待領取;同一則訊息只記一次,不洗版
		st.mu.Lock()
		same := st.lastError == msg
		st.lastError = msg
		st.mu.Unlock()
		if !same {
			a.Changed("agent_runner")
		}
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

// acquire 搶「正在執行」的名額;已有一筆在領取或執行就回 false
func (s *runnerState) acquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy || s.current != nil {
		return false
	}
	s.busy = true
	return true
}

func (s *runnerState) release() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// startTask 人工啟動一筆待領取的任務(「自動執行」關著的角色用):用角色存好的設定(沒存過 = 預設設定,
// 只用 omixor 工具),即使角色的 autoRun 是關的也跑,領取、執行、回報都走 runTask。
// 先檢查:系統設定的「自動執行」開著、角色啟用、任務還是待領取、找得到 claude。
// 一次一筆:迴圈或另一筆人工啟動的正在跑時直接回錯誤(不排隊),請使用者等它做完或先停止。
// 通過檢查就在背景執行並回傳一個執行結束時關閉的 channel;領取失敗(被別人領走等)記在 lastError,不回給呼叫端。
func (a *App) startTask(ctx context.Context, org, taskID string) (<-chan struct{}, error) {
	if org == "" {
		return nil, UserError{"還沒有組織"}
	}
	if !a.featureEnabled(org, runnerFeature) {
		return nil, UserError{"「自動執行」在系統設定裡關閉了,不能啟動任務"}
	}
	var task *AgentTask
	for _, t := range a.TasksOf(org) {
		if t.ID == taskID {
			t := t
			task = &t
			break
		}
	}
	if task == nil {
		return nil, UserError{"找不到任務 " + taskID}
	}
	if task.Status != "queued" {
		return nil, UserError{"任務 " + taskID + " 不是待領取,不能啟動"}
	}
	if g := a.FindAgentIn(org, task.AgentID); g == nil || !g.Active {
		return nil, UserError{"角色 " + task.AgentID + " 已停用,不能啟動它的任務"}
	}
	bin := findClaude()
	if bin == "" {
		return nil, UserError{"找不到 claude(Claude Code CLI):請安裝並用訂閱帳號登入,或設定 OMIXOR_CLAUDE_BIN"}
	}
	if msg := notLoggedInBlock(a.ensureLoggedIn(ctx, bin)); msg != "" {
		return nil, UserError{msg}
	}
	cfg, err := LoadRunnerConfig(org)
	if err != nil {
		return nil, UserError{"讀不到自動執行設定:" + err.Error()}
	}
	c, ok := cfg.Agents[task.AgentID]
	if !ok {
		c = AgentRunConfig{TimeoutMinutes: runnerDefaultTimeout}
	}
	st := a.runSt()
	if !st.acquire() {
		return nil, UserError{"目前已有任務在自動執行,請等它做完(或先停止)再啟動"}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer st.release()
		ran, err := a.runTask(ctx, org, *task, c, bin)
		switch {
		case err != nil:
			st.setError(err.Error())
			a.Changed("agent_runner")
		case !ran:
			st.setError(task.ID + " 沒有啟動:已被別人領取")
			a.Changed("agent_runner")
		}
		a.wakeRunner() // 做完後迴圈接著看自動執行的角色
	}()
	return done, nil
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
	runRec := newRunRecorder(org, g, task, started, secret)
	st.mu.Lock()
	st.rec = runRec
	st.mu.Unlock()
	result, costUSD, runErr := a.execClaude(runCtx, org, g, task, c, bin, cred, runRec.onEvent, func(text string) {
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
	st.current, st.cancel, st.stopped, st.rec = nil, nil, false, nil
	st.mu.Unlock()

	// 回報:Claude 已經自己回報(不是進行中)就不重複
	rec.Outcome = "error"
	cur, gerr := ops.GetAgentTask(org, task.ID)
	switch {
	case gerr != nil:
		rec.Error = scrub("讀不到任務狀態:" + gerr.Error())
	case cur.Status != "inProgress":
		rec.ReportedBy = ReportedByRole
		rec.Outcome = cur.Status
		if cur.Status == "done" || cur.Status == "blocked" {
			a.logRunnerFinal(g, *cur)
		}
		if runErr != nil && cur.Status != "done" {
			rec.Error = truncRunes(scrub(runErr.Error()), runnerErrorMaxRunes)
		}
	default:
		rec.ReportedBy = ReportedByRunner
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
		} else if why := runRec.noWork(); why != "" {
			// 角色沒呼叫過任何 omixor 工具:不是「做完沒回報」,不能報 done
			note := why
			rep, rec.Error = service.TaskReport{Status: &status, Note: &note}, why
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
	rec = a.finishRunLog(org, runRec, rec)
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
		Env:                claude.WaitForMCPEnv(runnerMCPConnectWaitMs),
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
	b.WriteString("1. 先用 agent_context 讀角色設定與任務。\n")
	b.WriteString("2. 照任務說明完成。\n")
	b.WriteString("3. 需要在 Issue 說明時,用 issue_update 留言。\n")
	b.WriteString("4. 做完用 agent_task_report 回報 status=done,在 result 寫結果;需要人決定時回報 status=blocked,在 note 寫要問的問題,然後結束。\n")
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
	b.WriteString("\n完成後用 agent_task_report 回報。")
	return b.String()
}

// execClaude 寫 MCP 設定檔、啟動 Claude、執行中續約;回傳最後的回覆、估計花費與錯誤(錯誤訊息可能要再過濾憑證)
func (a *App) execClaude(ctx context.Context, org string, g Agent, t AgentTask, c AgentRunConfig, bin string, cred model.AgentGrant, onEvent func(claude.Event), onText func(string)) (string, float64, error) {
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
	var gateErr error // init 時 omixor 的 MCP 不能用而中止
	res, err := runClaude(runCtx, runnerPrompt(t), o, func(ev claude.Event) {
		onEvent(ev) // 執行日誌
		if in, ok := ev.(*claude.Init); ok && gateErr == nil {
			if st := omixorMCPStatus(in.MCPServers); mcpGate(st) == mcpAbort {
				gateErr = errors.New(mcpUnusableMessage(st))
				cancel()
			}
		}
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
	// 實際執行的結果也更新登入狀態:成功 = 已登入;失敗訊息有 "Not logged in" = 未登入
	if err == nil {
		a.runSt().setLogin(bin, loginIn)
	} else if strings.Contains(strings.ToLower(err.Error()+" "+text), "not logged in") {
		a.runSt().setLogin(bin, loginOut)
	}
	if gateErr != nil {
		err = gateErr
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
