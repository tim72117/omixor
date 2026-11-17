package core

import (
	"strings"
	"time"
)

// 畫面上的其他範例資料動作(Swift 版寫在各頁面的 View 裡,只改本機資料):
//   PR 核准/要求修改/合併/關閉/重新開啟、加入審查(DevPullRequestsTab.swift DevPRActions)
//   看板指派(DevBoardTab.swift DevWorkActions.assign)
//   核准部署關卡、交給 AI 修復(PipeShared.swift cicdApproveGate、PipeRunDetail.swift PipeAIFixSheet.start)
//   推進環境(PipeEnvironmentsTab.swift start)、伺服器重新開機(InfraServerDetail.swift reboot)
//   告警規則開關(MonRulesTab.swift setEnabled)
// 都要拿著 Mu、在 a.As(…) 裡呼叫(署名用 a.Actor())。

// PR 狀態 → (icon, 動詞),同 DevPRActions
var prStatusAction = map[string][2]string{
	"approved":         {"checkmark.seal", "核准"},
	"changesRequested": {"exclamationmark.bubble", "要求修改"},
	"merged":           {"arrow.triangle.merge", "合併"},
	"closed":           {"xmark.circle", "關閉"},
	"open":             {"arrow.uturn.backward.circle", "重新開啟"},
}

// SetPullRequest 改 PR 狀態並寫動態;Swift 版沒有改成草稿的按鈕,不支援的狀態回傳錯誤
func (a *App) SetPullRequest(id, status, note string) error {
	act, ok := prStatusAction[status]
	if !ok {
		return UserError{"不支援的 PR 狀態「" + status + "」"}
	}
	_, pr := a.Find("pull_requests", id)
	if pr == nil {
		return UserError{"找不到 PR #" + id}
	}
	pr["status"] = status
	pr["updatedAt"] = nowISO()
	suffix := ""
	if t := strings.TrimSpace(note); t != "" {
		suffix = ":" + t
	}
	a.Log(a.Actor(), act[0], act[1]+" PR #"+id+"「"+Str(pr, "title")+"」"+suffix, "development")
	return nil
}

// JoinReview 把 person 加到審查者(已經在就不動,不寫動態,同 Swift)
func (a *App) JoinReview(id, person string) bool {
	_, pr := a.Find("pull_requests", id)
	if pr == nil {
		return false
	}
	rev, _ := pr["reviewers"].([]any)
	for _, r := range rev {
		if r == person {
			return true
		}
	}
	pr["reviewers"] = append(rev, person)
	return true
}

// ActorByID 畫面只送 id:是人就是 person,是自動化或其他 AI 名稱就是 ai,空字串是 nil(未指派)
func (a *App) ActorByID(id string) (*Actor, bool) {
	if id == "" {
		return nil, true
	}
	if _, p := a.Find("people", id); p != nil {
		x := Person(id)
		return &x, true
	}
	if _, au := a.Find("automations", id); au != nil || id == MCPActorID {
		x := AI(id)
		return &x, true
	}
	for _, f := range aiNameResolvers {
		if _, ok := f(a, id); ok {
			x := AI(id)
			return &x, true
		}
	}
	return nil, false
}

// AssignWorkItem 指派(who 為 nil 是取消指派),同 DevWorkActions.assign
func (a *App) AssignWorkItem(id string, who *Actor) bool {
	_, it := a.Find("work_items", id)
	if it == nil {
		return false
	}
	name := "未指派"
	if who == nil {
		delete(it, "assignee")
	} else {
		it["assignee"] = Object(*who)
		name = a.Name(*who)
	}
	a.Log(a.Actor(), "person.crop.circle.badge.checkmark", "把 "+id+" 指派給 "+name, "development")
	return true
}

func (a *App) findRun(id int) map[string]any {
	_, r := a.Find("pipeline_runs", formatInt(int64(id)))
	return r
}

func runTargetLabel(r map[string]any) string {
	if l, ok := DeployEnvLabel[Str(r, "targetEnv")]; ok {
		return l
	}
	return "Production"
}

// ApproveGate 核准 run 裡等待核准的關卡,同 cicdApproveGate
func (a *App) ApproveGate(runID int) bool {
	r := a.findRun(runID)
	if r == nil {
		return false
	}
	r["status"] = "running"
	stages, _ := r["stages"].([]any)
	for _, st := range stages {
		s, _ := st.(map[string]any)
		if s == nil || Str(s, "status") != "waitingApproval" {
			continue
		}
		s["status"] = "running"
		jobs, _ := s["jobs"].([]any)
		for _, jb := range jobs {
			if j, ok := jb.(map[string]any); ok && Str(j, "status") == "waitingApproval" {
				j["status"] = "queued"
			}
		}
	}
	a.Log(a.Actor(), "checkmark.seal", "核准 "+Str(r, "pipeline")+" #"+formatInt(int64(runID))+" 部署到 "+runTargetLabel(r), "pipelines")
	return true
}

// CICDFixerActor 負責修 CI 的 AI 自動化,同 cicdFixerActor
func (a *App) CICDFixerActor() Actor {
	for _, au := range a.C["automations"] {
		s := strings.ToLower(Str(au, "id") + Str(au, "name") + Str(au, "trigger"))
		if strings.Contains(s, "ci") || strings.Contains(s, "build") || strings.Contains(s, "修復") {
			return AI(Str(au, "id"))
		}
	}
	if len(a.C["automations"]) > 0 {
		return AI(Str(a.C["automations"][0], "id"))
	}
	return AI("ci-fixer")
}

// StartRunFrom 以既有 run 為範本開新的 run,同 cicdStartRun(第一個 job 執行中、其他排隊,清掉日誌與時間)
func (a *App) StartRunFrom(run map[string]any, trigger string, who Actor, branch, message string) int {
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
		stage["status"] = "queued"
		if s == 0 {
			stage["status"] = "running"
		}
		jobs, _ := stage["jobs"].([]any)
		for j, jb := range jobs {
			if job, ok := jb.(map[string]any); ok {
				job["status"] = "queued"
				if s == 0 && j == 0 {
					job["status"] = "running"
				}
				delete(job, "duration")
				job["log"] = []any{}
			}
		}
	}
	if branch == "" {
		branch = Str(run, "branch")
	}
	if message == "" {
		message = Str(run, "commitMessage")
	}
	nr := map[string]any{
		"id": float64(newID), "pipeline": run["pipeline"], "repo": run["repo"], "branch": branch,
		"commitSHA": run["commitSHA"], "commitMessage": message, "trigger": trigger,
		"triggeredBy": Object(who), "status": "running", "stages": stages, "startedAt": nowISO(),
	}
	if env, ok := run["targetEnv"]; ok && env != nil {
		nr["targetEnv"] = env
	}
	a.C["pipeline_runs"] = append([]map[string]any{nr}, a.C["pipeline_runs"]...)
	return newID
}

// AIFix 交給 AI 修復,同 PipeAIFixSheet.start;rerun 時回傳新 run 的編號(否則 0)
func (a *App) AIFix(runID int, openPR, rerun bool) (int, bool) {
	r := a.findRun(runID)
	if r == nil {
		return 0, false
	}
	actor := a.CICDFixerActor()
	id := formatInt(int64(runID))
	branch := "ai/fix-run-" + id
	a.Log(actor, "wand.and.stars", "開始修復 "+Str(r, "pipeline")+" #"+id+",建立分支 "+branch, "pipelines")
	newID := 0
	if rerun {
		newID = a.StartRunFrom(r, "AI 修復", actor, branch, "fix: 修正 #"+id+" 的失敗(AI)")
	}
	if openPR {
		a.Log(actor, "arrow.triangle.pull", "為 #"+id+" 的修復開 PR,等待審查", "development")
	}
	return newID, true
}

// MARK: 推進環境

type trackedDeps map[string]bool

// trackDeployment 記住畫面發起的部署,完成後回寫服務版本(同 Swift 的 CICDSession.trackedDeployments)
func (a *App) trackDeployment(id string) {
	t, _ := a.Ext["mock.trackedDeployments"].(trackedDeps)
	if t == nil {
		t = trackedDeps{}
		a.Ext["mock.trackedDeployments"] = t
	}
	t[id] = true
}

// syncTrackedDeployments 同 PipelinesView.syncServiceVersions;回傳有沒有改到服務
func (a *App) syncTrackedDeployments() bool {
	t, _ := a.Ext["mock.trackedDeployments"].(trackedDeps)
	changed := false
	for id := range t {
		_, d := a.Find("deployments", id)
		if d == nil {
			delete(t, id)
			continue
		}
		if Str(d, "status") != "succeeded" {
			continue
		}
		if _, s := a.Find("services", Str(d, "service")); s != nil {
			v, _ := s["versions"].(map[string]any)
			if v == nil {
				v = map[string]any{}
				s["versions"] = v
			}
			if v[Str(d, "env")] != d["version"] {
				v[Str(d, "env")] = d["version"]
				changed = true
			}
		}
		delete(t, id)
	}
	return changed
}

// Promote 把服務的上一個環境版本推進到 to(staging 或 production),同 PipeEnvironmentsTab.start。
// production 是 Canary 10%、等待核准;staging 是 Rolling、直接開始。
func (a *App) Promote(serviceID, to string) (map[string]any, error) {
	_, s := a.Find("services", serviceID)
	if s == nil {
		return nil, UserError{"找不到服務 " + serviceID}
	}
	from := ""
	switch to {
	case "production":
		from = "staging"
	case "staging":
		from = "development"
	default:
		return nil, UserError{"to 必須是 staging 或 production,收到「" + to + "」"}
	}
	versions, _ := s["versions"].(map[string]any)
	version, ok := versions[from].(string)
	if !ok {
		return nil, UserError{Str(s, "name") + " 在 " + DeployEnvLabel[from] + " 沒有版本"}
	}
	prev, ok := versions[to].(string)
	if !ok {
		prev = "—"
	}
	prod := to == "production"
	d := map[string]any{
		"id": "dep-" + strings.ToLower(UUID()[:8]), "service": serviceID, "version": version, "previousVersion": prev,
		"env": to, "status": "running", "strategy": "Rolling", "progress": 0.05, "startedAt": nowISO(),
		"by": Object(a.Actor()), "changes": []any{"從 " + DeployEnvLabel[from] + " 推進 " + version}, "healthGate": "pending",
	}
	if prod {
		d["status"], d["strategy"], d["progress"], d["canaryPercent"] = "waitingApproval", "Canary", 0.0, 10
	}
	a.C["deployments"] = append([]map[string]any{d}, a.C["deployments"]...)
	a.trackDeployment(Str(d, "id"))
	a.Log(a.Actor(), "arrow.up.forward.circle", "推進 "+Str(s, "name")+" "+version+" 到 "+DeployEnvLabel[to], "pipelines")
	return d, nil
}

// MARK: 伺服器、告警規則

// RebootDelay 重新開機多久後恢復(Swift 版 6 秒;測試可以改)
var RebootDelay = 6 * time.Second

// RebootServer 同 InfraServerDetail.reboot:先進維護中,RebootDelay 後還在維護中就恢復正常並以系統署名寫動態
func (a *App) RebootServer(id string) bool {
	_, s := a.Find("servers", id)
	if s == nil {
		return false
	}
	name := Str(s, "name")
	s["health"] = "maintenance"
	s["uptimeDays"] = 0
	a.Log(a.Actor(), "restart", "重新開機 "+name, "infrastructure")
	time.AfterFunc(RebootDelay, func() {
		a.Mu.Lock()
		done := false
		if _, s := a.Find("servers", id); s != nil && Str(s, "health") == "maintenance" {
			s["health"] = "healthy"
			a.Log(SystemActor, "checkmark.circle", name+" 重新開機完成", "infrastructure")
			done = true
		}
		a.Mu.Unlock()
		if done {
			a.Changed("servers")
		}
	})
	return true
}

// SetAlertRule 同 MonRulesTab.setEnabled:狀態一樣時什麼都不做
func (a *App) SetAlertRule(id string, on bool) bool {
	_, r := a.Find("alert_rules", id)
	if r == nil {
		return false
	}
	if cur, _ := r["enabled"].(bool); cur == on {
		return true
	}
	r["enabled"] = on
	icon, verb := "bell.slash", "停用"
	if on {
		icon, verb = "bell", "啟用"
	}
	a.Log(a.Actor(), icon, verb+"告警規則「"+Str(r, "name")+"」", "monitoring")
	return true
}

func argInt(args map[string]any, k string) (int, bool) {
	switch x := args[k].(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		if n, ok := DigitsInt(x); ok && strings.TrimSpace(x) != "" {
			return n, true
		}
	}
	return 0, false
}

// withUser 拿鎖、以登入者身分執行 f,成功時通知 topics
func withUser(a *App, args map[string]any, f func() (any, error), topics ...string) (any, error) {
	a.Mu.Lock()
	var out any
	var err error
	a.As(UserActor(a, args), func() { out, err = f() })
	if err == nil && out != nil {
		out = Clone(out)
	}
	a.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	a.Changed(topics...)
	return out, nil
}

func init() {
	RegisterAction("pull_request_set", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return withUser(a, args, func() (any, error) {
			if err := a.SetPullRequest(id, argString(args, "status"), argString(args, "note")); err != nil {
				return nil, err
			}
			_, pr := a.Find("pull_requests", id)
			return pr, nil
		}, "pull_requests")
	})
	RegisterAction("pull_request_join_review", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return withUser(a, args, func() (any, error) {
			person := argString(args, "person")
			if person == "" {
				person = a.Actor().ID
			}
			if _, p := a.Find("people", person); p == nil {
				return nil, UserError{"找不到 person " + person}
			}
			if !a.JoinReview(id, person) {
				return nil, UserError{"找不到 PR #" + id}
			}
			_, pr := a.Find("pull_requests", id)
			return pr, nil
		}, "pull_requests")
	})
	RegisterAction("work_item_assign", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return withUser(a, args, func() (any, error) {
			who, ok := a.ActorByID(argString(args, "assignee"))
			if !ok {
				return nil, UserError{"找不到 " + argString(args, "assignee")}
			}
			if !a.AssignWorkItem(id, who) {
				return nil, UserError{"找不到工作項目 " + id}
			}
			_, it := a.Find("work_items", id)
			return it, nil
		}, "work_items")
	})
	RegisterAction("pipeline_approve_gate", func(a *App, args map[string]any) (any, error) {
		return withUser(a, args, func() (any, error) {
			id, _ := argInt(args, "id")
			if !a.ApproveGate(id) {
				return nil, UserError{"找不到 pipeline run #" + formatInt(int64(id))}
			}
			return a.findRun(id), nil
		}, "pipeline_runs")
	})
	RegisterAction("pipeline_ai_fix", func(a *App, args map[string]any) (any, error) {
		return withUser(a, args, func() (any, error) {
			id, _ := argInt(args, "id")
			newID, ok := a.AIFix(id, argBool(args, "openPR"), argBool(args, "rerun"))
			if !ok {
				return nil, UserError{"找不到 pipeline run #" + formatInt(int64(id))}
			}
			var runID any
			if newID > 0 {
				runID = newID
			}
			return map[string]any{"runId": runID}, nil
		}, "pipeline_runs")
	})
	RegisterAction("deployment_promote", func(a *App, args map[string]any) (any, error) {
		return withUser(a, args, func() (any, error) {
			d, err := a.Promote(argString(args, "service"), argString(args, "to"))
			if err != nil {
				return nil, err
			}
			return d, nil
		}, "deployments")
	})
	RegisterAction("server_reboot", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return mockAction(a, args, "servers", id, func() bool { return a.RebootServer(id) }, "servers")
	})
	RegisterAction("alert_rule_toggle", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return mockAction(a, args, "alert_rules", id, func() bool {
			_, r := a.Find("alert_rules", id)
			if r == nil {
				return false
			}
			on, _ := r["enabled"].(bool)
			on = !on
			if _, given := args["enabled"]; given {
				on = argBool(args, "enabled")
			}
			return a.SetAlertRule(id, on)
		}, "alert_rules")
	})
}
