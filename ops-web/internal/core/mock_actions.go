package core

import (
	"time"
)

// 範例資料的動作,對應 Swift AppState.swift 的「動作」區(acknowledge、resolve、retry…)。
// 全部都要拿著 Mu 呼叫;寫進動態牆的文字和 Swift 版一字不差。呼叫端改完要自己呼叫 Changed(集合名稱)。

// 列舉的 rawValue(順序同 Swift 的 allCases)與顯示文字
var (
	Severities       = []string{"critical", "warning", "info"}
	IncidentStatuses = []string{"investigating", "identified", "monitoring", "resolved"}
	WorkTypes        = []string{"feature", "bug", "chore", "incident"}
	WorkStatuses     = []string{"backlog", "todo", "inProgress", "review", "done"}
	RiskLevels       = []string{"low", "medium", "high"}
	LogLevels        = []string{"debug", "info", "warn", "error"}
	AppSections      = []string{"overview", "development", "pipelines", "infrastructure", "monitoring", "incidents", "automation", "cabinet", "settings"}
	DeployEnvs       = []string{"production", "staging", "development"}
	TimeRanges       = []string{"1h", "6h", "24h", "7d", "30d"}
	Autonomies       = []string{"suggest", "approve", "auto"}

	SeverityLabel       = map[string]string{"critical": "Critical", "warning": "Warning", "info": "Info"}
	IncidentStatusLabel = map[string]string{"investigating": "調查中", "identified": "已找到原因", "monitoring": "觀察中", "resolved": "已解決"}
	WorkStatusLabel     = map[string]string{"backlog": "Backlog", "todo": "待辦", "inProgress": "進行中", "review": "審查中", "done": "完成"}
	RunStatusLabel      = map[string]string{"queued": "排隊中", "running": "執行中", "succeeded": "成功", "failed": "失敗", "canceled": "已取消", "waitingApproval": "等待核准"}
	DeployEnvLabel      = map[string]string{"production": "Production", "staging": "Staging", "development": "Development"}
	AutonomyLabel       = map[string]string{"suggest": "只建議", "approve": "需核准", "auto": "自動執行"}
	severityRank        = map[string]int{"critical": 0, "warning": 1, "info": 2}
)

// SeverityRank critical < warning < info(Swift Severity 的 Comparable)
func SeverityRank(s string) int {
	if r, ok := severityRank[s]; ok {
		return r
	}
	return 3
}

func nowISO() string { return ISO(time.Now()) }

// MARK: 告警

func (a *App) AcknowledgeAlert(id string, who Actor) bool {
	_, al := a.Find("alerts", id)
	if al == nil {
		return false
	}
	al["state"] = "acknowledged"
	al["acknowledgedBy"] = Object(who)
	a.Log(who, "bell.badge", "確認告警「"+Str(al, "title")+"」", "monitoring")
	return true
}

func (a *App) ResolveAlert(id string, who Actor) bool {
	_, al := a.Find("alerts", id)
	if al == nil {
		return false
	}
	al["state"] = "resolved"
	a.Log(who, "checkmark.circle", "解除告警「"+Str(al, "title")+"」", "monitoring")
	return true
}

func (a *App) SilenceAlert(id string, who Actor) bool {
	_, al := a.Find("alerts", id)
	if al == nil {
		return false
	}
	al["state"] = "silenced"
	a.Log(who, "bell.slash", "靜音告警「"+Str(al, "title")+"」", "monitoring")
	return true
}

// MARK: AI 自動化(只有人能做)

func (a *App) DecideApproval(id string, approve bool) bool {
	_, ap := a.Find("approvals", id)
	if ap == nil {
		return false
	}
	icon, verb := "xmark.seal", "拒絕"
	ap["state"] = "rejected"
	if approve {
		icon, verb = "checkmark.seal", "核准"
		ap["state"] = "approved"
	}
	ap["decidedBy"] = a.Actor().ID
	a.Log(a.Actor(), icon, verb+" AI 請求「"+Str(ap, "title")+"」", "automation")
	return true
}

func (a *App) SetAutonomy(autonomy, automationID string) bool {
	_, au := a.Find("automations", automationID)
	if au == nil {
		return false
	}
	au["autonomy"] = autonomy
	a.Log(a.Actor(), "slider.horizontal.3", "把「"+Str(au, "name")+"」改為"+AutonomyLabel[autonomy], "automation")
	return true
}

func (a *App) ToggleAutomation(id string) bool {
	_, au := a.Find("automations", id)
	if au == nil {
		return false
	}
	enabled, _ := au["enabled"].(bool)
	enabled = !enabled
	au["enabled"] = enabled
	verb := "停用"
	if enabled {
		verb = "啟用"
	}
	a.Log(a.Actor(), "power", verb+"「"+Str(au, "name")+"」", "automation")
	return true
}

// MARK: CI/CD

// RetryRun 複製一筆 run 重新執行,新 run 放在最前面;回傳新 run(找不到回傳 nil)
func (a *App) RetryRun(runID int, who Actor) map[string]any {
	_, run := a.Find("pipeline_runs", formatInt(int64(runID)))
	if run == nil {
		return nil
	}
	newID := 0
	for _, r := range a.C["pipeline_runs"] {
		if n := int(Num(r, "id")); n > newID {
			newID = n
		}
	}
	newID++
	stages, _ := Clone(run["stages"]).([]any)
	if stages == nil {
		stages = []any{}
	}
	for s, st := range stages {
		stage, _ := st.(map[string]any)
		if stage == nil {
			continue
		}
		status := "queued"
		if s == 0 {
			status = "running"
		}
		stage["status"] = status
		jobs, _ := stage["jobs"].([]any)
		for _, jb := range jobs {
			if job, ok := jb.(map[string]any); ok {
				job["status"] = status
				delete(job, "duration")
			}
		}
	}
	nr := map[string]any{
		"id": float64(newID), "pipeline": run["pipeline"], "repo": run["repo"], "branch": run["branch"],
		"commitSHA": run["commitSHA"], "commitMessage": run["commitMessage"], "trigger": "手動重試",
		"triggeredBy": Object(who), "status": "running", "stages": stages, "startedAt": nowISO(),
	}
	if env, ok := run["targetEnv"]; ok && env != nil {
		nr["targetEnv"] = env
	}
	a.C["pipeline_runs"] = append([]map[string]any{nr}, a.C["pipeline_runs"]...)
	a.Log(who, "arrow.clockwise", "重新執行 "+Str(run, "pipeline")+" #"+formatInt(int64(newID)), "pipelines")
	return nr
}

func (a *App) CancelRun(runID int, who Actor) bool {
	id := formatInt(int64(runID))
	_, run := a.Find("pipeline_runs", id)
	if run == nil {
		return false
	}
	run["status"] = "canceled"
	a.Log(who, "stop.circle", "取消 "+Str(run, "pipeline")+" #"+id, "pipelines")
	return true
}

func (a *App) ApproveDeployment(id string) bool {
	_, d := a.Find("deployments", id)
	if d == nil {
		return false
	}
	d["status"] = "running"
	a.Log(a.Actor(), "checkmark.seal", "核准部署 "+Str(d, "service")+" "+Str(d, "version")+" 到 "+DeployEnvLabel[Str(d, "env")], "pipelines")
	return true
}

// Rollback 建一筆回滾到上一版的部署(放在最前面),回傳新部署
func (a *App) Rollback(deploymentID string) map[string]any {
	_, d := a.Find("deployments", deploymentID)
	if d == nil {
		return nil
	}
	prev := Str(d, "previousVersion")
	nd := map[string]any{
		"id": UUID(), "service": d["service"], "version": prev, "previousVersion": d["version"], "env": d["env"],
		"status": "running", "strategy": "Rolling", "progress": 0.05, "startedAt": nowISO(),
		"by": Object(a.Actor()), "changes": []any{"回滾到 " + prev}, "healthGate": "pending",
	}
	a.C["deployments"] = append([]map[string]any{nd}, a.C["deployments"]...)
	a.Log(a.Actor(), "arrow.uturn.backward", "回滾 "+Str(d, "service")+" 到 "+prev, "pipelines")
	return nd
}

// MARK: 事件

func incidentEvent(who Actor, kind, text string) map[string]any {
	return map[string]any{"id": UUID(), "date": nowISO(), "actor": Object(who), "kind": kind, "text": text}
}

// AppendIncidentEvent 在事件時間軸加一筆
func (a *App) AppendIncidentEvent(inc map[string]any, who Actor, kind, text string) {
	tl, _ := inc["timeline"].([]any)
	inc["timeline"] = append(tl, incidentEvent(who, kind, text))
}

// UpdateIncident 改狀態並在時間軸加一筆;note 為 nil 時用「狀態改為「…」」
func (a *App) UpdateIncident(id, status string, note *string, who Actor) bool {
	_, inc := a.Find("incidents", id)
	if inc == nil {
		return false
	}
	inc["status"] = status
	if status == "resolved" {
		inc["resolvedAt"] = nowISO()
	}
	text := "狀態改為「" + IncidentStatusLabel[status] + "」"
	if note != nil {
		text = *note
	}
	a.AppendIncidentEvent(inc, who, "status", text)
	a.Log(who, "flame", Str(inc, "id")+" 狀態改為"+IncidentStatusLabel[status], "incidents")
	return true
}

// AddIncidentNote 加註記(空字串不加,同 Swift)
func (a *App) AddIncidentNote(id, text string, who Actor) bool {
	_, inc := a.Find("incidents", id)
	if inc == nil || text == "" {
		return false
	}
	a.AppendIncidentEvent(inc, who, "note", text)
	return true
}

// MARK: 開發

// MoveWorkItem 移動看板欄位;who 為 nil 時不寫動態(同 Swift 的 by: nil)
func (a *App) MoveWorkItem(id, status string, who *Actor) bool {
	_, it := a.Find("work_items", id)
	if it == nil {
		return false
	}
	it["status"] = status
	if who != nil {
		a.Log(*who, "rectangle.stack", "把 "+id+" 移到「"+WorkStatusLabel[status]+"」", "development")
	}
	return true
}

// MARK: 設定

func (a *App) SetIntegration(id string, connected bool) bool {
	_, it := a.Find("integrations", id)
	if it == nil {
		return false
	}
	it["connected"] = connected
	return true
}

// MARK: 查詢(同 Swift 的 firingAlerts、openIncidents…)

func (a *App) FiringAlerts() []map[string]any {
	out := []map[string]any{}
	for _, al := range a.C["alerts"] {
		if Str(al, "state") == "firing" {
			out = append(out, al)
		}
	}
	sortStable(out, func(x, y map[string]any) bool {
		return SeverityRank(Str(x, "severity")) < SeverityRank(Str(y, "severity"))
	})
	return out
}

func (a *App) filterC(collection string, keep func(map[string]any) bool) []map[string]any {
	out := []map[string]any{}
	for _, it := range a.C[collection] {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

func (a *App) OpenIncidents() []map[string]any {
	return a.filterC("incidents", func(m map[string]any) bool { return Str(m, "status") != "resolved" })
}

func (a *App) PendingApprovals() []map[string]any {
	return a.filterC("approvals", func(m map[string]any) bool { return Str(m, "state") == "pending" })
}

func (a *App) RunningPipelines() []map[string]any {
	return a.filterC("pipeline_runs", func(m map[string]any) bool {
		s := Str(m, "status")
		return s == "running" || s == "queued"
	})
}

// CurrentOnCall 目前的 Primary 值班(沒有回傳 nil)
func (a *App) CurrentOnCall() map[string]any {
	now := time.Now()
	for _, s := range a.C["on_call"] {
		if Str(s, "role") != "Primary" {
			continue
		}
		start, err1 := ParseTime(Str(s, "start"))
		end, err2 := ParseTime(Str(s, "end"))
		if err1 == nil && err2 == nil && !start.After(now) && end.After(now) {
			return s
		}
	}
	return nil
}
