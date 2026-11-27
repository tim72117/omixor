package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"omixor-core/claude"
	"omixor-core/model"
	"omixor-core/service"
)

// 自動執行的執行端:挑任務、跑 claude、回報與提示詞(設定、登入檢查與狀態見 agent_runner.go)

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
func (a *App) startTask(ctx context.Context, org, taskID string, who ...*Caller) (<-chan struct{}, error) {
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
		ran, err := a.runTask(ctx, org, *task, c, bin, who...)
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
func (a *App) runTask(ctx context.Context, org string, t AgentTask, c AgentRunConfig, bin string, who ...*Caller) (bool, error) {
	ops := a.AgentOps()
	// 人工啟動:領取以啟動的人署名(核心的領取者);執行中的回報照舊是系統的工作
	res, err := a.AgentOpsFor(firstCaller(who)).ClaimTask(org, service.ClaimInput{AgentID: t.AgentID, TaskID: t.ID})
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
	result, costUSD, runErr := a.execClaude(runCtx, org, g, task, c, bin, cred, res.AutoPostsResult, runRec.onEvent, func(text string) {
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
		if err := reportForRole(ops, org, task.ID, cred.Code, rep); err != nil {
			rec.Outcome = "error"
			rec.Error = scrub("回報失敗:" + err.Error())
		}
	}
	fin := time.Now()
	rec.FinishedAt = Time{fin.UTC().Truncate(time.Second)}
	rec.DurationSeconds = int(fin.Sub(started).Seconds())
	rec = a.finishRunLog(org, runRec, rec)
	rec.Org = org
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

// reportForRole runner 代角色回報(Claude 自己沒回報):用領取驗證碼走 WorkerReport,server 才知道是角色的回報(完成留言署名角色)。
// 沒有驗證碼、或 worker 回報失敗(角色中途停用、租約到期、舊 server 沒有這個路由…)退回一般回報:署名會變成本人,但結果不能掉
func reportForRole(ops AgentOps, org, taskID, code string, in service.TaskReport) error {
	if code != "" {
		_, err := ops.WorkerReport(org, taskID, code, in)
		if err == nil {
			return nil
		}
		runnerLogf("%s 以角色身分回報失敗,改用一般回報:%v", taskID, err)
	}
	_, err := ops.ReportTask(org, taskID, in)
	return err
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
func runnerOptions(g Agent, c AgentRunConfig, bin, mcpConfig, scratch string, autoPostsResult bool) claude.Options {
	o := claude.Options{
		Model: c.Model, Effort: c.Effort, Bin: bin,
		Lean: true, NoPersist: true,
		AllowedTools:       []string{"mcp__omixor"},
		AppendSystemPrompt: runnerSystemPrompt(g, c, autoPostsResult),
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

// runnerReportRules 規則第 3、4 條:server 在領取回應明示會把結果貼到 Issue(service.ClaimResult.AutoPostsResult)時,叫角色不要重複留言;
// 沒明示(舊 server)就不會貼,角色要自己留言
func runnerReportRules(autoPostsResult bool) string {
	if autoPostsResult {
		return "3. 過程中需要在 Issue 說明時,用 issue_update 留言;任務結果不用另外留言(見下一條)。\n" +
			"4. 做完用 agent_task_report 回報 status=done,結果寫在 agent_task_report 的 result。任務有連到 Issue 時(子項任務除外),系統會自動把 result 貼到該 Issue 的留言,不要用 issue_update 重複留言。需要人決定時回報 status=blocked,在 note 寫要問的問題,然後結束。\n"
	}
	return "3. 需要在 Issue 說明時,用 issue_update 留言。\n" +
		"4. 做完用 agent_task_report 回報 status=done,在 result 寫結果;需要人決定時回報 status=blocked,在 note 寫要問的問題,然後結束。\n"
}

func runnerSystemPrompt(g Agent, c AgentRunConfig, autoPostsResult bool) string {
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
	b.WriteString(runnerReportRules(autoPostsResult))
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
func (a *App) execClaude(ctx context.Context, org string, g Agent, t AgentTask, c AgentRunConfig, bin string, cred model.AgentGrant, autoPostsResult bool, onEvent func(claude.Event), onText func(string)) (string, float64, error) {
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

	o := runnerOptions(g, c, bin, cfgPath, scratch, autoPostsResult)
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
