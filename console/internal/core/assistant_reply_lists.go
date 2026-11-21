// 助理回答(清單類):部署、核准、CI、告警、事件、伺服器、PR、Sprint、值班等回答與 fallback。
package core

import (
	"fmt"
	"strings"
	"time"
)

func (r AssistResponder) incidentDeployCorrelation() AssistReply {
	inc := r.topIncident()
	if inc == nil {
		return assistReplyOf("目前沒有進行中的事件。")
	}
	start := asTime(inc, "startedAt")
	lo, hi := start.Add(-2*time.Hour), start.Add(600*time.Second)
	svcs := asStrs(inc, "services")
	ds := asFilter(r.c("deployments"), func(x asstObj) bool {
		t := asTime(x, "startedAt")
		in := false
		for _, s := range svcs {
			if s == asStr(x, "service") {
				in = true
			}
		}
		return in && !t.Before(lo) && !t.After(hi)
	})
	d := asMax(ds, func(a, b asstObj) bool { return asTime(a, "startedAt").Before(asTime(b, "startedAt")) })
	if d == nil {
		return assistReplyOf(asID(inc)+" 開始前 2 小時內,受影響的服務沒有部署紀錄,看起來和部署無關。",
			assistGo("incidents", assistSP(asID(inc)), "前往事件"))
	}
	gap := int(start.Sub(asTime(d, "startedAt")).Seconds() / 60)
	changes := asStrs(d, "changes")
	if len(changes) > 3 {
		changes = changes[:3]
	}
	return assistReplyOf(fmt.Sprintf("很可能有關:%s 在事件開始前 %d 分鐘部署了 %s(前一版 %s)。\n變更:%s\n建議先確認這次變更,必要時回滾到 %s。",
		r.svcName(asStr(d, "service")), max(gap, 0), asStr(d, "version"), asStr(d, "previousVersion"),
		strings.Join(changes, "、"), asStr(d, "previousVersion")),
		assistGo("pipelines", assistSP(asID(d)), "查看部署"), assistGo("incidents", assistSP(asID(inc)), "前往事件"))
}

func (r AssistResponder) deploymentsSummary() AssistReply {
	today := asFilter(r.c("deployments"), func(x asstObj) bool { return assistIsToday(asTime(x, "startedAt"), r.now) })
	if len(today) == 0 {
		return assistReplyOf("今天還沒有部署紀錄。")
	}
	st := func(s string) func(asstObj) bool { return func(x asstObj) bool { return asStr(x, "status") == s } }
	waiting := asFilter(today, st("waitingApproval"))
	lines := []string{fmt.Sprintf("今天共 %d 次部署:成功 %d、失敗 %d、進行中 %d、等待核准 %d。",
		len(today), asCount(today, st("succeeded")), asCount(today, st("failed")), asCount(today, st("running")), len(waiting))}
	sorted := asSorted(today, func(a, b asstObj) bool { return asTime(a, "startedAt").After(asTime(b, "startedAt")) })
	for _, d := range asPrefix(sorted, 5) {
		lines = append(lines, fmt.Sprintf("• %s %s %s → %s(%s,%s)", AssistFmtTime(asTime(d, "startedAt")), r.svcName(asStr(d, "service")),
			asStr(d, "version"), assistEnvShort(asStr(d, "env")), assistRunStatusLabel(asStr(d, "status")), r.a.Name(ActorFrom(d["by"]))))
	}
	actions := []AssistAction{assistGo("pipelines", nil, "前往 CI/CD")}
	if len(waiting) > 0 {
		actions = append([]AssistAction{assistGo("pipelines", assistSP(asID(waiting[0])), "查看待核准部署")}, actions...)
	}
	return assistReplyOf(strings.Join(lines, "\n"), actions...)
}

func (r AssistResponder) approvalsReply() AssistReply {
	pending := r.pendingApprovals()
	if len(pending) == 0 {
		return assistReplyOf("目前沒有等你核准的 AI 請求。")
	}
	lines := []string{fmt.Sprintf("有 %d 個 AI 請求等你決定:", len(pending))}
	for _, p := range asPrefix(pending, 4) {
		lines = append(lines, fmt.Sprintf("• %s(%s,%s到期)", asStr(p, "title"), assistRiskLabel(asStr(p, "risk")), r.relative(asTime(p, "expiresAt"))))
	}
	top := asSorted(pending, func(a, b asstObj) bool { return asTime(a, "expiresAt").Before(asTime(b, "expiresAt")) })[0]
	lines = append(lines, fmt.Sprintf("\n最快到期的是「%s」:%s\n回滾方式:%s", asStr(top, "title"), asStr(top, "rationale"), asStr(top, "rollbackPlan")))
	id := asID(top)
	return assistReplyOf(strings.Join(lines, "\n"), assistApprove(id), assistReject(id), assistGo("automation", assistSP(id), "查看詳情"))
}

func (r AssistResponder) failedRunsReply() AssistReply {
	failed := r.failedRuns()
	if len(failed) == 0 {
		return assistReplyOf(fmt.Sprintf("最近沒有失敗的執行。%d 條流程正在執行或排隊。", len(r.runningPipelines())),
			assistGo("pipelines", nil, "前往 CI/CD"))
	}
	lines := []string{fmt.Sprintf("最近 %d 次執行失敗:", len(failed))}
	for _, run := range asPrefix(failed, 3) {
		l := fmt.Sprintf("• %s #%s", asStr(run, "pipeline"), asID(run))
		for _, st := range asList(run, "stages") {
			if asStr(st, "status") == "failed" {
				l += "(卡在 " + asStr(st, "name") + ")"
				break
			}
		}
		lines = append(lines, l+":"+asStr(run, "commitMessage"))
		if d, ok := asOpt(run, "aiDiagnosis"); ok {
			lines = append(lines, "  AI 分析:"+d)
		}
	}
	top := failed[0]
	return assistReplyOf(strings.Join(lines, "\n"), assistRetry(asInt(top, "id")), assistGo("pipelines", assistSP(asID(top)), "查看執行"))
}

func (r AssistResponder) alertsReply(onlyWithoutIncident bool) AssistReply {
	list := r.firingAlerts()
	if onlyWithoutIncident {
		list = asFilter(list, func(x asstObj) bool { _, has := asOpt(x, "incidentId"); return !has })
	}
	if len(list) == 0 {
		if onlyWithoutIncident {
			return assistReplyOf("所有觸發中的告警都已關聯到事件。")
		}
		return assistReplyOf("目前沒有觸發中的告警。")
	}
	head := fmt.Sprintf("%d 則告警觸發中:", len(list))
	if onlyWithoutIncident {
		head = fmt.Sprintf("%d 則告警還沒有關聯事件:", len(list))
	}
	lines := []string{head}
	for _, al := range asPrefix(list, 5) {
		lines = append(lines, fmt.Sprintf("• [%s] %s — %s,%s,已 %s", assistSeverityLabel(asStr(al, "severity")), asStr(al, "title"),
			r.svcName(asStr(al, "service")), asStr(al, "value"), r.since(asTime(al, "startedAt"))))
	}
	if len(list) > 5 {
		lines = append(lines, fmt.Sprintf("…還有 %d 則", len(list)-5))
	}
	top := list[0]
	return assistReplyOf(strings.Join(lines, "\n"), assistAck(asID(top), asStr(top, "title")), assistGo("monitoring", assistSP(asID(top)), "查看告警"))
}

func (r AssistResponder) incidentsReply() AssistReply {
	open := r.openIncidents()
	if len(open) == 0 {
		return assistReplyOf("目前沒有進行中的事件。")
	}
	lines := []string{fmt.Sprintf("%d 起事件處理中:", len(open))}
	for _, i := range asSorted(open, func(a, b asstObj) bool { return asInt(a, "sev") < asInt(b, "sev") }) {
		lines = append(lines, fmt.Sprintf("• %s SEV%d %s(%s,%s,指揮官 %s)", asID(i), asInt(i, "sev"), asStr(i, "title"),
			assistIncidentStatusLabel(asStr(i, "status")), r.since(asTime(i, "startedAt")), r.personName(asStr(i, "commander"))))
	}
	if top := r.topIncident(); top != nil {
		if s, ok := asOpt(top, "aiSummary"); ok {
			lines = append(lines, "\nAI 摘要:"+s)
		}
		if last := asMax(asList(top, "timeline"), func(a, b asstObj) bool { return asTime(a, "date").Before(asTime(b, "date")) }); last != nil {
			lines = append(lines, fmt.Sprintf("最新進度(%s):%s", r.relative(asTime(last, "date")), asStr(last, "text")))
		}
		return assistReplyOf(strings.Join(lines, "\n"), assistGo("incidents", assistSP(asID(top)), "前往事件"))
	}
	return assistReplyOf(strings.Join(lines, "\n"))
}

func (r AssistResponder) serversReply() AssistReply {
	servers := r.c("servers")
	if len(servers) == 0 {
		return assistReplyOf("目前沒有伺服器資料。")
	}
	hot := r.hotServers()
	maxCPU := asMax(servers, func(a, b asstObj) bool { return asNum(a, "cpu") < asNum(b, "cpu") })
	lines := []string{}
	if len(hot) == 0 {
		lines = append(lines, fmt.Sprintf("所有 %d 台伺服器資源都在安全範圍,CPU 最高的是 %s(%d%%)。", len(servers), asStr(maxCPU, "name"), int(asNum(maxCPU, "cpu"))))
	} else {
		lines = append(lines, fmt.Sprintf("%d 台伺服器資源吃緊:", len(hot)))
		for _, s := range asPrefix(hot, 4) {
			lines = append(lines, fmt.Sprintf("• %s(%s):CPU %d%%、記憶體 %d%%、磁碟 %d%%", asStr(s, "name"), assistEnvShort(asStr(s, "env")),
				int(asNum(s, "cpu")), int(asNum(s, "memory")), int(asNum(s, "disk"))))
		}
	}
	restarting := []string{}
	for _, srv := range servers {
		for _, c := range asList(srv, "containers") {
			if asInt(c, "restarts") >= 3 {
				restarting = append(restarting, fmt.Sprintf("%s@%s(%d 次)", asStr(c, "name"), asStr(srv, "name"), asInt(c, "restarts")))
			}
		}
	}
	if len(restarting) > 0 {
		if len(restarting) > 3 {
			restarting = restarting[:3]
		}
		lines = append(lines, "重啟次數偏高的容器:"+strings.Join(restarting, "、"))
	}
	focus := maxCPU
	if len(hot) > 0 {
		focus = hot[0]
	}
	return assistReplyOf(strings.Join(lines, "\n"), assistGo("infrastructure", assistSP(asID(focus)), "查看伺服器"))
}

func (r AssistResponder) prsReply() AssistReply {
	prs := r.openPRs()
	if len(prs) == 0 {
		return assistReplyOf("目前沒有未合併的 PR。")
	}
	old := func(p asstObj) bool { return r.now.Sub(asTime(p, "updatedAt")).Seconds() > 2*86400 }
	stuck := asFilter(prs, func(p asstObj) bool {
		return asStr(p, "checks") == "failed" || asStr(p, "status") == "changesRequested" || old(p)
	})
	lines := []string{fmt.Sprintf("%d 個 PR 未合併,其中 %d 個可能卡住:", len(prs), len(stuck))}
	show := stuck
	if len(stuck) == 0 {
		show = prs
	}
	for _, p := range asPrefix(show, 4) {
		why := []string{}
		if asStr(p, "checks") == "failed" {
			why = append(why, "檢查失敗")
		}
		if asStr(p, "status") == "changesRequested" {
			why = append(why, "需修改")
		}
		if old(p) {
			why = append(why, r.relative(asTime(p, "updatedAt"))+"更新")
		}
		if rv, ok := p["aiReview"].(map[string]any); ok && asStr(rv, "risk") != "low" {
			why = append(why, "AI:"+assistRiskLabel(asStr(rv, "risk")))
		}
		l := fmt.Sprintf("• #%s %s", asID(p), asStr(p, "title"))
		if len(why) > 0 {
			l += "(" + strings.Join(why, "、") + ")"
		}
		lines = append(lines, l)
	}
	return assistReplyOf(strings.Join(lines, "\n"), assistGo("development", nil, "前往開發進度"))
}

func (r AssistResponder) sprintReply() AssistReply {
	items := r.c("work_items")
	if len(items) == 0 {
		return assistReplyOf("目前沒有工作項目資料。")
	}
	total, done := r.sprintPoints()
	daysLeft := max(0, int(asTime(r.a.Sprint, "end").Sub(r.now).Hours()/24))
	head := "**" + asStr(r.a.Sprint, "name") + "**"
	if g := asStr(r.a.Sprint, "goal"); g != "" {
		head += ":" + g
	}
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	lines := []string{head, fmt.Sprintf("完成 %d/%d 點(%d%%),剩 %d 天。", done, total, pct, daysLeft)}
	for _, st := range []string{"inProgress", "review", "todo"} {
		if n := asCount(items, func(x asstObj) bool { return asStr(x, "status") == st }); n > 0 {
			lines = append(lines, fmt.Sprintf("• %s %d 項", assistWorkStatusLabel(st), n))
		}
	}
	if bugs := asCount(items, func(x asstObj) bool { return asStr(x, "type") == "bug" && asStr(x, "status") != "done" }); bugs > 0 {
		lines = append(lines, fmt.Sprintf("• 未完成的 Bug %d 個", bugs))
	}
	return assistReplyOf(strings.Join(lines, "\n"), assistGo("development", nil, "前往開發進度"))
}

func (r AssistResponder) onCallReply() AssistReply {
	s := r.currentOnCall()
	if s == nil {
		return assistReplyOf("目前查不到值班資料。")
	}
	pid := asStr(s, "person")
	text := "目前主要值班是 **" + r.personName(pid) + "**"
	if p := r.find("people", pid); p != nil {
		text += "(" + asStr(p, "role") + ")"
	}
	text += ",到 " + AssistFmtTime(asTime(s, "end")) + "。"
	for _, sec := range r.c("on_call") {
		if asStr(sec, "role") != "Primary" && !asTime(sec, "start").After(r.now) && asTime(sec, "end").After(r.now) {
			text += "\n次要值班:" + r.personName(asStr(sec, "person")) + "。"
			break
		}
	}
	return assistReplyOf(text, assistGo("incidents", nil, "前往事件"))
}

func (r AssistResponder) automationsReply() AssistReply {
	list := r.c("automations")
	if len(list) == 0 {
		return assistReplyOf("目前沒有設定任何 AI 自動化。")
	}
	runs, saved := r.automationTotals()
	enabled := asCount(list, func(x asstObj) bool { return asBool(x, "enabled") })
	lines := []string{fmt.Sprintf("%d/%d 個自動化啟用中,30 天共執行 %d 次,省下約 %d 小時。", enabled, len(list), runs, int(saved))}
	if best := asMax(list, func(a, b asstObj) bool { return asNum(a, "timeSavedHours") < asNum(b, "timeSavedHours") }); best != nil {
		lines = append(lines, fmt.Sprintf("• 最省時:%s(%d 小時)", asStr(best, "name"), int(asNum(best, "timeSavedHours"))))
	}
	ran := asFilter(list, func(x asstObj) bool { return asInt(x, "runs30d") > 0 })
	if worst := asMin(ran, func(a, b asstObj) bool { return asNum(a, "successRate") < asNum(b, "successRate") }); worst != nil {
		lines = append(lines, fmt.Sprintf("• 成功率最低:%s(%d%%,%s)", asStr(worst, "name"), int(asNum(worst, "successRate")*100), assistAutonomyLabel(asStr(worst, "autonomy"))))
	}
	return assistReplyOf(strings.Join(lines, "\n"), assistGo("automation", nil, "前往 AI 自動化"))
}

func (r AssistResponder) integrationsReply() AssistReply {
	off := asFilter(r.c("integrations"), func(x asstObj) bool { return !asBool(x, "connected") })
	text := "所有整合都已連接。"
	if len(off) > 0 {
		names := []string{}
		for _, x := range off {
			names = append(names, fmt.Sprintf("%s(%s)", asStr(x, "name"), asStr(x, "category")))
		}
		text = "還沒連接的整合:" + strings.Join(names, "、")
	}
	return assistReplyOf(text, assistGo("settings", nil, "前往設定"))
}

func (r AssistResponder) fallback() AssistReply {
	s := r.Suggestions()
	if len(s) > 3 {
		s = s[:3]
	}
	ideas := []string{}
	for _, x := range s {
		ideas = append(ideas, "「"+x+"」")
	}
	return assistReplyOf("這個問題我還不會回答。我可以從目前的資料回答告警、事件、部署、CI 失敗、伺服器資源、PR、Sprint、值班和 AI 核准相關的問題,例如 " + strings.Join(ideas, "、") + "。")
}
