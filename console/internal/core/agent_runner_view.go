package core

import (
	"context"
	"sort"
	"strings"

	"omixor-core/config"
)

// ---- 視圖與動作 ----

// AgentRunnerView 視圖 agent_runner
type AgentRunnerView struct {
	Agents      []RunnerAgentRow `json:"agents"`
	ClaudeBin   string           `json:"claudeBin"`
	ClaudeFound bool             `json:"claudeFound"`
	// ClaudeLoggedIn claude 已登入(找到且 `claude auth status` 回報已登入);ClaudeLoginChecking 還沒確認(檢查中、逾時、不支援);
	// ClaudeLoginHint 找到但未登入時給畫面的說明
	ClaudeLoggedIn      bool           `json:"claudeLoggedIn"`
	ClaudeLoginChecking bool           `json:"claudeLoginChecking"`
	ClaudeLoginHint     string         `json:"claudeLoginHint,omitempty"`
	Current             *RunnerCurrent `json:"current"`
	Efforts             []string       `json:"efforts"`
	Error               string         `json:"error,omitempty"`
	History             []RunnerRecord `json:"history"`
	LastError           string         `json:"lastError,omitempty"`
	Loop                bool           `json:"loop"`
	Org                 string         `json:"org"`
	// Disabled 系統設定關掉了「自動執行」:不領新任務(進行中的做完);DisabledReason 給畫面的說明
	Disabled       bool   `json:"disabled"`
	DisabledReason string `json:"disabledReason,omitempty"`
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
	if !a.featureEnabled(org, runnerFeature) {
		v.Disabled = true
		v.DisabledReason = "「自動執行」在系統設定裡關閉了:不會再領新的任務(進行中的會做完),角色的設定都保留,打開後照原本的設定繼續。"
	}
	bin := findClaude()
	v.ClaudeBin, v.ClaudeFound = bin, bin != ""
	if bin != "" {
		switch a.loginForView(bin) {
		case loginIn:
			v.ClaudeLoggedIn = true
		case loginOut:
			v.ClaudeLoginHint = loginOutHint
		default:
			v.ClaudeLoginChecking = true
		}
	}
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
	st.seedHistory(org)
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

// seedHistory 第一次看某組織時,把日誌檔裡最近的執行補進 history(重開程式後執行紀錄不會是空的);
// 已在記憶體裡的(同 RunID)不重複。讀不到日誌檔下次再試
func (s *runnerState) seedHistory(org string) {
	s.mu.Lock()
	done := s.seeded[org]
	s.mu.Unlock()
	if done || org == "" {
		return
	}
	runLogMu.Lock()
	f, err := loadRunLogs(org)
	runLogMu.Unlock()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seeded == nil {
		s.seeded = map[string]bool{}
	}
	s.seeded[org] = true
	have := map[string]bool{}
	for _, r := range s.history {
		have[r.RunID] = true
	}
	for _, l := range f.Runs {
		if !have[l.RunID] {
			s.history = append(s.history, l.record())
		}
	}
	sort.SliceStable(s.history, func(i, j int) bool { return s.history[i].StartedAt.After(s.history[j].StartedAt.Time) })
	if len(s.history) > runnerHistoryMax {
		s.history = s.history[:runnerHistoryMax]
	}
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
		if config.Current().Bool(config.NoTimers) {
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
	// agent_runner_start 人工啟動一筆待領取的任務(task = 任務 id);規則見 startTask
	RegisterAction("agent_runner_start", func(a *App, args map[string]any) (any, error) {
		id := strings.TrimSpace(agentArg(args, "task"))
		if id == "" {
			return nil, UserError{"要給任務 id(task)"}
		}
		if _, err := a.startTask(context.Background(), a.currentOrgNow(), id); err != nil {
			return nil, err
		}
		a.Changed("agent_runner")
		return map[string]any{"started": true, "task": id}, nil
	})
	// agent_runner_log 取一筆執行的完整日誌:task = 任務 id(取最近一筆),run = 日誌 id(RunnerRecord.runId,指定哪一筆);沒有回 log: null
	RegisterAction("agent_runner_log", func(a *App, args map[string]any) (any, error) {
		task, run := strings.TrimSpace(agentArg(args, "task")), strings.TrimSpace(agentArg(args, "run"))
		if task == "" && run == "" {
			return nil, UserError{"要給任務 id(task)或執行 id(run)"}
		}
		l, err := RunLogOf(a.currentOrgNow(), task, run)
		if err != nil {
			return nil, UserError{"讀不到執行日誌:" + err.Error()}
		}
		return map[string]any{"log": l}, nil
	})
	RegisterAction("agent_runner_stop", func(a *App, args map[string]any) (any, error) {
		if !a.StopRunner() {
			return nil, UserError{"目前沒有在自動執行的任務"}
		}
		return map[string]any{"stopped": true}, nil
	})
}
