package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"omixor-core/claude"
	"omixor-core/redact"
)

// 角色執行日誌:自動執行(runTask)每跑一筆,就把 Claude 的 stream-json 事件(文字、工具呼叫、工具結果、錯誤)
// 與角色端點(/mcp/agent)伺服器端確認的呼叫,依序記成一份日誌,事後能查「角色到底有沒有呼叫 omixor 工具、是誰回報的」。
//
//	存放:組織資料夾的 agent-run-logs.json(經過存放處 ReadData/WriteData,和 agent-runner.json 一樣是這台機器的資料,不進核心、不新增資料表)
//	保留:每個組織最近 runLogKeep 筆;一份日誌最多 runLogMaxEntries 筆項目、約 runLogMaxBytes 位元組,超過的項目丟掉並標 truncated
//	機密:每一個存進去的字串都先過角色憑證(secret)、核心的 redact.Secrets 遮蔽,再截斷;參數只存「鍵=截短的值」
//	只在執行結束時寫檔(執行中的項目在記憶體);程式在執行中途結束就沒有這一筆

const (
	runLogFileName   = "agent-run-logs.json"
	runLogKeep       = 50      // 每個組織保留最近幾筆執行
	runLogMaxEntries = 300     // 一筆執行最多幾個項目
	runLogMaxBytes   = 150_000 // 一筆執行項目內容的總長上限(位元組,估計)
	runLogArgsMax    = 200     // 參數摘要
	runLogArgValMax  = 60      // 摘要裡單一參數值
	runLogResultMax  = 300     // 工具結果摘錄
	runLogTextMax    = 300     // 文字、錯誤摘錄
)

// 誰回報了任務
const (
	ReportedByRole   = "role"   // 角色自己回報(執行結束時任務已不是進行中)
	ReportedByRunner = "runner" // 角色沒回報,由 omixor 補回報
)

// 項目種類
const (
	LogStart      = "start"
	LogText       = "text"
	LogToolCall   = "tool_call"
	LogToolResult = "tool_result"
	LogServerCall = "server_call" // 角色端點伺服器端確認的呼叫(不靠解析 Claude 的輸出)
	LogError      = "error"
	LogEnd        = "end"
)

// RunLogEntry 日誌的一個項目;Status 是 ok 或 error(只有工具結果、伺服器呼叫、結束有)
type RunLogEntry struct {
	Args   string `json:"args,omitempty"`
	At     Time   `json:"at"`
	Kind   string `json:"kind"`
	Status string `json:"status,omitempty"`
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
}

// RunLog 一筆執行的日誌。ReportedBy:role = 角色自己回報了任務(執行結束時任務已不是進行中)、
// runner = 角色沒回報、由 omixor 補回報、空 = 不知道(讀不到任務狀態)
type RunLog struct {
	AgentID         string        `json:"agentId"`
	AgentName       string        `json:"agentName"`
	CostUSD         float64       `json:"costUsd"`
	DurationSeconds int           `json:"durationSeconds"`
	Entries         []RunLogEntry `json:"entries"`
	Error           string        `json:"error,omitempty"`
	FinishedAt      Time          `json:"finishedAt"`
	MCPStatus       string        `json:"mcpStatus,omitempty"` // Claude 回報的 MCP 伺服器狀態,例如 omixor:connected
	Model           string        `json:"model,omitempty"`
	Outcome         string        `json:"outcome"`
	ReportedBy      string        `json:"reportedBy"`
	RunID           string        `json:"runId"`
	ServerCalls     int           `json:"serverCalls"` // 伺服器端確認的工具呼叫數
	SessionID       string        `json:"sessionId,omitempty"`
	StartedAt       Time          `json:"startedAt"`
	TaskID          string        `json:"taskId"`
	Title           string        `json:"title"`
	ToolCalls       int           `json:"toolCalls"` // 從 Claude 輸出解析到的工具呼叫數(含超過項目上限而沒記下的)
	// Truncated 項目超過上限,後面的被丟掉(結束項目一定會留)
	Truncated bool `json:"truncated,omitempty"`
}

// RunLogFile agent-run-logs.json 的內容(最新的在前)
type RunLogFile struct {
	Runs []RunLog `json:"runs"`
}

func runLogPath(org string) string { return filepath.Join(OrgDir(org), runLogFileName) }

var runLogMu sync.Mutex

func loadRunLogs(org string) (RunLogFile, error) {
	var f RunLogFile
	if org == "" {
		return f, nil
	}
	if err := ReadJSON(runLogPath(org), &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RunLogFile{}, err
	}
	return f, nil
}

// persistRunLog 把一筆日誌放到最前面,只留最近 runLogKeep 筆(同 RunID 的取代)
func persistRunLog(org string, l RunLog) error {
	runLogMu.Lock()
	defer runLogMu.Unlock()
	f, err := loadRunLogs(org)
	if err != nil {
		// 檔案壞了:不阻擋這一筆,從空的開始
		f = RunLogFile{}
	}
	runs := []RunLog{l}
	for _, r := range f.Runs {
		if r.RunID != l.RunID {
			runs = append(runs, r)
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt.Time) })
	if len(runs) > runLogKeep {
		runs = runs[:runLogKeep]
	}
	f.Runs = runs
	return WriteJSON(runLogPath(org), f)
}

// RunLogOf 查日誌:runID 非空以它為準,否則取該任務最近一筆
func RunLogOf(org, taskID, runID string) (*RunLog, error) {
	runLogMu.Lock()
	defer runLogMu.Unlock()
	f, err := loadRunLogs(org)
	if err != nil {
		return nil, err
	}
	for i := range f.Runs {
		r := f.Runs[i]
		if (runID != "" && r.RunID == runID) || (runID == "" && r.TaskID == taskID) {
			return &r, nil
		}
	}
	return nil, nil
}

// ---- 記錄器 ----

// runRecorder 一筆執行進行中的日誌(執行緒安全:stream 回呼與角色端點的稽核回呼會同時進來)
type runRecorder struct {
	mu      sync.Mutex
	log     RunLog
	secret  string
	pending map[string]string // tool_use id → 工具名稱
	bytes   int
	org     string
	// omixorMCP 、omixorCalls 給「角色有沒有實際用到 omixor 工具」的判斷:init 裡 omixor 伺服器的狀態(沒出現是空字串)、
	// 解析到的 mcp__omixor__ 工具呼叫數
	// omixorOK 是其中成功回來的結果數(tool_result 沒有 is_error);omixorLastErr 是最後一筆失敗結果的內容(已遮蔽、截斷)
	omixorMCP     string
	omixorCalls   int
	omixorOK      int
	omixorLastErr string
}

// omixorErrExcerptMax 「全部失敗」說明裡附的最後一筆錯誤的字數上限
const omixorErrExcerptMax = 160

// omixorMCPName 執行時 MCP 設定檔裡 omixor 伺服器的名稱;工具名稱是 mcp__<名稱>__<工具>
const omixorMCPName = "omixor"

// omixorToolPrefix omixor 工具在 Claude 輸出裡的名稱前綴
const omixorToolPrefix = "mcp__" + omixorMCPName + "__"

// omixorMCPStatus init 事件裡 omixor 伺服器的連線狀態;沒列出來回空字串
func omixorMCPStatus(servers []claude.MCPServer) string {
	for _, s := range servers {
		if s.Name == omixorMCPName {
			return s.Status
		}
	}
	return ""
}

// 角色看得到 omixor 工具與否的判斷:connected 照常跑;pending 還在連,先跑著看(結束時再依有沒有呼叫判斷);其餘(failed、needs-auth、沒列出來)馬上中止
type mcpDecision int

const (
	mcpProceed mcpDecision = iota
	mcpWatch
	mcpAbort
)

// mcpGate 依 init 事件裡 omixor 伺服器的狀態決定要不要繼續這次執行
func mcpGate(status string) mcpDecision {
	switch status {
	case "connected":
		return mcpProceed
	case "pending":
		return mcpWatch
	}
	return mcpAbort
}

// mcpStatusLabel 給人看的 MCP 狀態(沒列出來顯示「未知」)
func mcpStatusLabel(status string) string {
	if status == "" {
		return "未知"
	}
	return status
}

// mcpUnusableMessage 角色沒有拿到 omixor 工具時的說明(寫進任務備註與執行錯誤)
func mcpUnusableMessage(status string) string {
	return "角色沒有連上 omixor 的工具(MCP 狀態:" + mcpStatusLabel(status) + "),沒有實際執行任務"
}

// mcpNoToolCallMessage omixor 連上了、角色卻一個工具都沒呼叫時的說明
func mcpNoToolCallMessage(status string) string {
	return "角色沒有呼叫任何 omixor 工具(MCP 狀態:" + mcpStatusLabel(status) + "),沒有實際執行任務"
}

// mcpAllCallsFailedMessage 角色呼叫了 omixor 工具、卻沒有任何一次成功時的說明;lastErr 是最後一筆錯誤(可空)
func mcpAllCallsFailedMessage(calls int, lastErr string) string {
	msg := fmt.Sprintf("角色呼叫了 omixor 的工具,但全部失敗,沒有實際執行任務(呼叫 %d 次)", calls)
	if lastErr != "" {
		msg += ":" + truncRunes(lastErr, omixorErrExcerptMax)
	}
	return msg
}

// noWork 回報「角色沒有實際做事」的原因:沒呼叫過任何 omixor 工具、或呼叫了卻全部失敗才算;至少一次成功回空字串
func (r *runRecorder) noWork() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.omixorOK > 0:
		return ""
	case r.omixorCalls > 0:
		return mcpAllCallsFailedMessage(r.omixorCalls, r.omixorLastErr)
	case r.omixorMCP == "connected":
		return mcpNoToolCallMessage(r.omixorMCP)
	}
	return mcpUnusableMessage(r.omixorMCP)
}

func newRunRecorder(org string, g Agent, t AgentTask, started time.Time, secret string) *runRecorder {
	r := &runRecorder{
		org: org, secret: secret, pending: map[string]string{},
		log: RunLog{
			AgentID: g.ID, AgentName: g.Name, Entries: []RunLogEntry{}, StartedAt: Time{started.UTC().Truncate(time.Second)},
			TaskID: t.ID, Title: truncRunes(redact.Secrets(t.Title), 200),
			RunID: fmt.Sprintf("%s-%d", t.ID, started.UnixMilli()),
		},
	}
	r.add(RunLogEntry{Kind: LogStart, Text: "開始執行"})
	return r
}

// scrub 去控制字元 → 角色憑證 → 核心的機密遮蔽。先遮蔽再截斷,才不會把機密從中間切開而漏出前半段
func (r *runRecorder) scrub(s string) string {
	s = redact.StripControl(s, false)
	if r.secret != "" {
		s = strings.ReplaceAll(s, r.secret, "***")
	}
	return redact.Secrets(s)
}

// clean 存進日誌的任何字串都要過這裡:scrub 後截斷
func (r *runRecorder) clean(s string, max int) string { return truncRunes(r.scrub(s), max) }

// add 加一個項目(超過上限就丟掉並標 truncated;呼叫端要持有 mu 或在建立階段)
func (r *runRecorder) add(e RunLogEntry) {
	if e.At.IsZero() {
		e.At = Time{time.Now().UTC()}
	}
	size := len(e.Args) + len(e.Text) + len(e.Tool) + 40
	if e.Kind != LogEnd && (len(r.log.Entries) >= runLogMaxEntries-1 || r.bytes+size > runLogMaxBytes) {
		r.log.Truncated = true // 留一格給結束項目
		return
	}
	r.bytes += size
	r.log.Entries = append(r.log.Entries, e)
}

// summarizeArgs 工具參數 → 「鍵=值」摘要(值各自截短、整串截斷、機密遮蔽)
func (r *runRecorder) summarizeArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return r.clean(string(raw), runLogArgsMax)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var v string
		switch x := m[k].(type) {
		case string:
			v = x
		default:
			b, _ := json.Marshal(x)
			v = string(b)
		}
		// 「鍵=值」整段一起遮蔽,才認得出 token=…、password=… 這類以鍵名判斷的機密
		parts = append(parts, truncRunes(r.scrub(k+"="+v), runLogArgValMax+len(k)+1))
	}
	return truncRunes(strings.Join(parts, ", "), runLogArgsMax)
}

// onEvent 接 claude.Run 的事件
func (r *runRecorder) onEvent(ev claude.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e := ev.(type) {
	case *claude.Init:
		r.log.Model, r.log.SessionID = r.clean(e.Model, 80), r.clean(e.SessionID, 80)
		var ms []string
		for _, s := range e.MCPServers {
			ms = append(ms, r.clean(s.Name, 40)+":"+r.clean(s.Status, 40))
		}
		r.log.MCPStatus = strings.Join(ms, ",")
		r.omixorMCP = omixorMCPStatus(e.MCPServers)
	case *claude.Assistant:
		if r.log.Model == "" {
			r.log.Model = r.clean(e.Message.Model, 80)
		}
		if r.log.SessionID == "" {
			r.log.SessionID = r.clean(e.SessionID, 80)
		}
		for _, b := range e.Message.Content {
			switch b.Type {
			case "text":
				if s := r.clean(b.Text, runLogTextMax); s != "" {
					r.add(RunLogEntry{Kind: LogText, Text: s})
				}
			case "tool_use":
				r.log.ToolCalls++
				if strings.HasPrefix(b.Name, omixorToolPrefix) {
					r.omixorCalls++
				}
				name := r.clean(b.Name, 120)
				if b.ID != "" {
					r.pending[b.ID] = name
				}
				r.add(RunLogEntry{Kind: LogToolCall, Tool: name, Args: r.summarizeArgs(b.Input)})
			}
		}
	case *claude.User:
		for _, b := range e.Blocks() {
			if b.Type != "tool_result" {
				continue
			}
			st := "ok"
			if b.IsError {
				st = "error"
			}
			tool, text := r.pending[b.ToolUseID], r.clean(b.ResultText(), runLogResultMax)
			if strings.HasPrefix(tool, omixorToolPrefix) {
				if b.IsError {
					r.omixorLastErr = text
				} else {
					r.omixorOK++
				}
			}
			r.add(RunLogEntry{Kind: LogToolResult, Tool: tool, Status: st, Text: text})
		}
	}
}

// server 角色端點確認了一次呼叫(任務綁定的憑證發出的)
func (r *runRecorder) server(tool string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log.ServerCalls++
	e := RunLogEntry{Kind: LogServerCall, Tool: r.clean(tool, 120), Status: "ok"}
	if err != nil {
		e.Status, e.Text = "error", r.clean(err.Error(), runLogTextMax)
	}
	r.add(e)
}

// finish 補上結果寫成完整日誌(結束項目一定留);回傳要存的日誌
func (r *runRecorder) finish(rec RunnerRecord, reportedBy string) RunLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.Error != "" {
		r.add(RunLogEntry{Kind: LogError, Text: r.clean(rec.Error, runLogTextMax)})
	}
	st := "ok"
	if rec.Outcome != "done" {
		st = "error"
	}
	r.add(RunLogEntry{Kind: LogEnd, Status: st, Text: r.clean(rec.Outcome, 40), At: rec.FinishedAt})
	r.log.Outcome, r.log.Error = rec.Outcome, r.clean(rec.Error, runnerErrorMaxRunes)
	r.log.CostUSD, r.log.DurationSeconds, r.log.FinishedAt = rec.CostUSD, rec.DurationSeconds, rec.FinishedAt
	r.log.ReportedBy = reportedBy
	return r.log
}

// finishRunLog 結束一筆執行:寫入日誌檔,回傳補上日誌摘要的執行紀錄。
// 日誌寫不進去只記 log,不影響任務結果(日誌是輔助資料)
func (a *App) finishRunLog(org string, r *runRecorder, rec RunnerRecord) RunnerRecord {
	l := r.finish(rec, rec.ReportedBy)
	if err := persistRunLog(org, l); err != nil {
		runnerLogf("%s 執行日誌寫入失敗:%v", rec.TaskID, err)
	}
	return l.record()
}

// record 日誌 → 執行紀錄(重開程式後從日誌檔補回執行紀錄用,也是 finishRunLog 的回傳)
func (l RunLog) record() RunnerRecord {
	return RunnerRecord{
		AgentID: l.AgentID, AgentName: l.AgentName, CostUSD: l.CostUSD, DurationSeconds: l.DurationSeconds, Error: l.Error,
		FinishedAt: l.FinishedAt, Outcome: l.Outcome, StartedAt: l.StartedAt, TaskID: l.TaskID, Title: l.Title,
		EntryCount: len(l.Entries), ReportedBy: l.ReportedBy, RunID: l.RunID, ServerCalls: l.ServerCalls, ToolCalls: l.ToolCalls,
	}
}

// RoleCallDone 角色端點(/mcp/agent)處理完一次工具呼叫時由 web 的 MCP 層呼叫:
// 憑證綁定的任務正好是目前在跑的那筆時,記成日誌裡「伺服器確認的呼叫」
func (a *App) RoleCallDone(org, taskID, tool string, err error) {
	st := a.runSt()
	st.mu.Lock()
	r := st.rec
	st.mu.Unlock()
	if r != nil && r.org == org && r.log.TaskID == taskID { // TaskID 建立後不變,不用鎖
		r.server(tool, err)
	}
}
