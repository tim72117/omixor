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
	"strings"
	"sync"
	"time"

	"omixor-core/claude"
	"omixor-core/config"
)

// 角色自動執行:派發(任務排進待領取)時,角色開了「自動執行」,web 就在背景替它領取,
// 啟動這台 Mac 上的 Claude Code(core/claude,claude -p,用登入 CLI 的訂閱額度)以角色身分處理,做完回報。
//
//	設定:每個角色一份,存在組織資料夾的 agent-runner.json(這台機器的設定,不進核心)
//	迴圈:OnStart 啟動,每 15 秒(任務有變動時提早)看目前組織的待領取任務;一次只跑一筆
//	領取:AgentOps().ClaimTask(身分是 omixor-console),拿到角色憑證
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
	// Org 這筆屬於哪個組織(紀錄是整台一份,非管理員的畫面只列目前組織的;不輸出)
	Org string `json:"-"`
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
