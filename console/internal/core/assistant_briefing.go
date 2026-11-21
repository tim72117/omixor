// 助理「目前狀況」卡片:Briefing 與各頁面的 xxxBrief。
package core

import (
	"fmt"
	"strings"
)

// MARK: - 目前狀況

func assistLineOf(icon, color, text string) AssistBriefLine {
	return AssistBriefLine{Icon: icon, Color: color, Text: text}
}

func (r AssistResponder) Briefing() AssistBriefing {
	switch r.section {
	case "development":
		return r.developmentBrief()
	case "pipelines":
		return r.pipelinesBrief()
	case "infrastructure":
		return r.infraBrief()
	case "monitoring":
		return r.monitoringBrief()
	case "incidents":
		return r.incidentsBrief()
	case "automation":
		return r.automationBrief()
	case "cabinet":
		return AssistBriefing{Headline: "文件櫃", Tone: "secondary",
			Lines: []AssistBriefLine{assistLineOf("archivebox", "secondary", fmt.Sprintf("文件櫃有 %d 份文件", r.cabinetDocCount()))}}
	case "settings":
		return r.settingsBrief()
	case "channels":
		n, unread := r.channelCounts()
		text := fmt.Sprintf("%d 個頻道", n)
		if unread > 0 {
			text += fmt.Sprintf(",%d 則未讀", unread)
		}
		return AssistBriefing{Headline: "對話", Tone: "secondary",
			Lines: []AssistBriefLine{assistLineOf("bubble.left.and.bubble.right", "secondary", text)}}
	}
	return r.overviewBrief()
}

// channelCounts 頻道數與未讀總數:讀訊息頻道功能登記的 View(channels → {"channels":[{unread}]})
func (r AssistResponder) channelCounts() (n, unread int) {
	f, ok := views["channels"]
	if !ok {
		return 0, 0
	}
	m, _ := Object(f(r.a)).(map[string]any)
	list, _ := m["channels"].([]any)
	for _, x := range list {
		if c, ok := x.(map[string]any); ok {
			u, _ := c["unread"].(float64)
			unread += int(u)
		}
	}
	return len(list), unread
}

// cabinetDocCount 文件櫃的文件數:讀文件櫃功能登記的 View(cabinet → {"docs":[…]}),避免直接依賴它的資料結構
func (r AssistResponder) cabinetDocCount() int {
	f, ok := views["cabinet"]
	if !ok {
		return 0
	}
	m, _ := Object(f(r.a)).(map[string]any)
	docs, _ := m["docs"].([]any)
	return len(docs)
}

func (r AssistResponder) overviewBrief() AssistBriefing {
	lines := []AssistBriefLine{}
	incidents, firing := r.openIncidents(), r.firingAlerts()
	services := r.c("services")
	bad := asFilter(services, func(x asstObj) bool { return asStr(x, "health") != "healthy" })
	if len(services) == 0 {
		lines = append(lines, assistLineOf("server.rack", "secondary", "尚未載入任何服務資料"))
	} else if len(bad) == 0 {
		lines = append(lines, assistLineOf("checkmark.circle.fill", "green", fmt.Sprintf("%d 個服務全部正常", len(services))))
	} else {
		lines = append(lines, assistLineOf("exclamationmark.triangle.fill", "orange",
			fmt.Sprintf("%d/%d 個服務異常:%s", len(bad), len(services), asNames(bad, 3))))
	}
	if len(incidents) > 0 {
		lines = append(lines, assistLineOf("flame.fill", "red", fmt.Sprintf("%d 起事件處理中", len(incidents))))
	}
	if len(firing) > 0 {
		crit := asCount(firing, func(x asstObj) bool { return asStr(x, "severity") == "critical" })
		color, extra := "orange", ""
		if crit > 0 {
			color, extra = "red", fmt.Sprintf("(%d 則 Critical)", crit)
		}
		lines = append(lines, assistLineOf("bell.badge.fill", color, fmt.Sprintf("%d 則告警觸發中", len(firing))+extra))
	}
	if n := len(r.runningPipelines()); n > 0 {
		lines = append(lines, assistLineOf("arrow.triangle.2.circlepath", "blue", fmt.Sprintf("%d 條流程執行中", n)))
	}
	if n := len(r.pendingApprovals()); n > 0 {
		lines = append(lines, assistLineOf("hand.raised.fill", "purple", fmt.Sprintf("%d 個 AI 請求等你核准", n)))
	}
	if shift := r.currentOnCall(); shift != nil {
		lines = append(lines, assistLineOf("person.badge.clock", "secondary", "值班:"+r.personName(asStr(shift, "person"))))
	}
	headline, tone := "一切正常", "green"
	if len(incidents) > 0 {
		headline, tone = "有事件正在處理", "red"
	} else if len(firing) > 0 || len(bad) > 0 {
		headline, tone = "有些地方需要留意", "orange"
	}
	return AssistBriefing{Headline: headline, Tone: tone, Lines: lines}
}

func (r AssistResponder) sprintPoints() (total, done int) {
	for _, w := range r.c("work_items") {
		total += asInt(w, "points")
		if asStr(w, "status") == "done" {
			done += asInt(w, "points")
		}
	}
	return
}

func (r AssistResponder) developmentBrief() AssistBriefing {
	prs := r.openPRs()
	failing := asFilter(prs, func(x asstObj) bool { return asStr(x, "checks") == "failed" })
	highRisk := asFilter(prs, func(x asstObj) bool {
		rv, _ := x["aiReview"].(map[string]any)
		return rv != nil && asStr(rv, "risk") == "high"
	})
	lines := []AssistBriefLine{assistLineOf("arrow.triangle.pull", "blue", fmt.Sprintf("%d 個 PR 尚未合併", len(prs)))}
	if len(failing) > 0 {
		lines = append(lines, assistLineOf("xmark.circle.fill", "red", fmt.Sprintf("%d 個 PR 檢查失敗", len(failing))))
	}
	if len(highRisk) > 0 {
		lines = append(lines, assistLineOf("exclamationmark.shield.fill", "orange", fmt.Sprintf("AI 審查標記 %d 個高風險 PR", len(highRisk))))
	}
	total, done := r.sprintPoints()
	if total > 0 {
		lines = append(lines, assistLineOf("chart.bar.fill", "indigo",
			fmt.Sprintf("%s 完成 %d/%d 點(%d%%)", asStr(r.a.Sprint, "name"), done, total, done*100/total)))
	}
	if ai := asCount(r.c("work_items"), func(x asstObj) bool { return asBool(x, "createdByAI") }); ai > 0 {
		lines = append(lines, assistLineOf("sparkles", "purple", fmt.Sprintf("%d 個工作項目由 AI 建立", ai)))
	}
	tone := "orange"
	if len(failing) == 0 && len(highRisk) == 0 {
		tone = "green"
	}
	headline := "有 PR 卡在檢查"
	if len(failing) == 0 {
		headline = "開發進度正常"
	}
	return AssistBriefing{Headline: headline, Tone: tone, Lines: lines}
}

func (r AssistResponder) pipelinesBrief() AssistBriefing {
	failed := asFilter(r.failedRuns(), func(x asstObj) bool { return r.now.Sub(asTime(x, "startedAt")).Seconds() < 86400 })
	waiting := asFilter(r.c("deployments"), func(x asstObj) bool { return asStr(x, "status") == "waitingApproval" })
	today := asFilter(r.c("deployments"), func(x asstObj) bool { return assistIsToday(asTime(x, "startedAt"), r.now) })
	lines := []AssistBriefLine{assistLineOf("arrow.triangle.2.circlepath", "blue", fmt.Sprintf("%d 條流程執行中或排隊", len(r.runningPipelines())))}
	if len(failed) > 0 {
		lines = append(lines, assistLineOf("xmark.circle.fill", "red", fmt.Sprintf("24 小時內 %d 次執行失敗", len(failed))))
		f := failed[0]
		text := fmt.Sprintf("最近失敗:%s #%s", asStr(f, "pipeline"), asID(f))
		if d, ok := asOpt(f, "aiDiagnosis"); ok {
			text += "— " + d
		}
		lines = append(lines, assistLineOf("sparkles", "purple", text))
	}
	if len(waiting) > 0 {
		lines = append(lines, assistLineOf("hand.raised.fill", "purple", fmt.Sprintf("%d 個部署等待核准", len(waiting))))
	}
	lines = append(lines, assistLineOf("shippingbox.fill", "secondary", fmt.Sprintf("今天已部署 %d 次", len(today))))
	if len(failed) == 0 {
		return AssistBriefing{Headline: "CI/CD 運作順利", Tone: "green", Lines: lines}
	}
	return AssistBriefing{Headline: "有執行失敗待處理", Tone: "red", Lines: lines}
}

func (r AssistResponder) infraBrief() AssistBriefing {
	servers := r.c("servers")
	down := asFilter(servers, func(x asstObj) bool { return asStr(x, "health") == "down" })
	degraded := asFilter(servers, func(x asstObj) bool { return asStr(x, "health") == "degraded" })
	lines := []AssistBriefLine{assistLineOf("server.rack", "secondary", fmt.Sprintf("%d 台伺服器、%d 個服務", len(servers), len(r.c("services"))))}
	if len(down) > 0 {
		lines = append(lines, assistLineOf("xmark.octagon.fill", "red", "中斷:"+asNames(down, 3)))
	}
	if len(degraded) > 0 {
		lines = append(lines, assistLineOf("exclamationmark.triangle.fill", "orange", "效能下降:"+asNames(degraded, 3)))
	}
	if hot := r.hotServers(); len(hot) > 0 {
		h := hot[0]
		lines = append(lines, assistLineOf("thermometer.high", "orange",
			fmt.Sprintf("資源最吃緊:%s CPU %d%%、記憶體 %d%%", asStr(h, "name"), int(asNum(h, "cpu")), int(asNum(h, "memory")))))
	}
	restarts := 0
	for _, s := range servers {
		restarts += asCount(asList(s, "containers"), func(c asstObj) bool { return asInt(c, "restarts") >= 3 })
	}
	if restarts > 0 {
		lines = append(lines, assistLineOf("arrow.counterclockwise", "orange", fmt.Sprintf("%d 個容器重啟 3 次以上", restarts)))
	}
	ok := len(down) == 0 && len(degraded) == 0
	headline, tone := "有主機狀況異常", "red"
	if ok {
		headline = "基礎設施穩定"
	}
	if len(down) == 0 {
		tone = "orange"
		if ok {
			tone = "green"
		}
	}
	return AssistBriefing{Headline: headline, Tone: tone, Lines: lines}
}

func (r AssistResponder) monitoringBrief() AssistBriefing {
	firing := r.firingAlerts()
	lines := []AssistBriefLine{}
	if len(firing) == 0 {
		lines = append(lines, assistLineOf("checkmark.circle.fill", "green", "沒有觸發中的告警"))
	} else {
		for _, sev := range assistSeverities {
			if n := asCount(firing, func(x asstObj) bool { return asStr(x, "severity") == sev }); n > 0 {
				lines = append(lines, assistLineOf("bell.fill", assistSeverityColor(sev), fmt.Sprintf("%s %d 則", assistSeverityLabel(sev), n)))
			}
		}
		top := firing[0]
		lines = append(lines, assistLineOf("exclamationmark.triangle.fill", assistSeverityColor(asStr(top, "severity")),
			fmt.Sprintf("最嚴重:%s(%s,%s,已 %s)", asStr(top, "title"), r.svcName(asStr(top, "service")), asStr(top, "value"), r.since(asTime(top, "startedAt")))))
		if note, ok := asOpt(top, "aiNote"); ok {
			lines = append(lines, assistLineOf("sparkles", "purple", "AI 判讀:"+note))
		}
		noIncident := asCount(firing, func(x asstObj) bool {
			_, has := asOpt(x, "incidentId")
			return !has && asStr(x, "severity") == "critical"
		})
		if noIncident > 0 {
			lines = append(lines, assistLineOf("flame", "red", fmt.Sprintf("%d 則 Critical 尚未開事件", noIncident)))
		}
	}
	crit := asCount(firing, func(x asstObj) bool { return asStr(x, "severity") == "critical" }) > 0
	if len(firing) == 0 {
		return AssistBriefing{Headline: "監控一切正常", Tone: "green", Lines: lines}
	}
	tone := "orange"
	if crit {
		tone = "red"
	}
	return AssistBriefing{Headline: fmt.Sprintf("%d 則告警觸發中", len(firing)), Tone: tone, Lines: lines}
}

func (r AssistResponder) incidentsBrief() AssistBriefing {
	inc := r.topIncident()
	if inc == nil {
		recent := asCount(r.c("incidents"), func(x asstObj) bool { return asStr(x, "status") == "resolved" })
		return AssistBriefing{Headline: "沒有進行中的事件", Tone: "green",
			Lines: []AssistBriefLine{assistLineOf("checkmark.seal.fill", "green", fmt.Sprintf("已解決事件共 %d 起", recent))}}
	}
	st := asStr(inc, "status")
	lines := []AssistBriefLine{
		assistLineOf("flame.fill", assistIncidentStatusColor(st), fmt.Sprintf("%s SEV%d:%s", asID(inc), asInt(inc, "sev"), asStr(inc, "title"))),
		assistLineOf("clock", "secondary", fmt.Sprintf("%s,已持續 %s", assistIncidentStatusLabel(st), r.since(asTime(inc, "startedAt")))),
		assistLineOf("person.fill", "secondary", "指揮官:"+r.personName(asStr(inc, "commander"))),
	}
	if svcs := asStrs(inc, "services"); len(svcs) > 0 {
		names := []string{}
		for _, s := range svcs {
			names = append(names, r.svcName(s))
		}
		lines = append(lines, assistLineOf("square.stack.3d.up", "orange", "影響:"+strings.Join(names, "、")))
	}
	if s, ok := asOpt(inc, "aiSummary"); ok {
		lines = append(lines, assistLineOf("sparkles", "purple", s))
	}
	open := len(r.openIncidents())
	if others := open - 1; others > 0 {
		lines = append(lines, assistLineOf("ellipsis.circle", "secondary", fmt.Sprintf("另有 %d 起事件處理中", others)))
	}
	return AssistBriefing{Headline: fmt.Sprintf("%d 起事件處理中", open), Tone: "red", Lines: lines}
}

func (r AssistResponder) automationTotals() (runs int, saved float64) {
	for _, x := range r.c("automations") {
		runs += asInt(x, "runs30d")
		saved += asNum(x, "timeSavedHours")
	}
	return
}

func (r AssistResponder) automationBrief() AssistBriefing {
	pending := r.pendingApprovals()
	lines := []AssistBriefLine{}
	if len(pending) == 0 {
		lines = append(lines, assistLineOf("hand.raised.fill", "secondary", "沒有等待核准的請求"))
	} else {
		lines = append(lines, assistLineOf("hand.raised.fill", "purple", fmt.Sprintf("%d 個請求等你核准", len(pending))))
	}
	if high := asCount(pending, func(x asstObj) bool { return asStr(x, "risk") == "high" }); high > 0 {
		lines = append(lines, assistLineOf("exclamationmark.shield.fill", "red", fmt.Sprintf("其中 %d 個高風險", high)))
	}
	autos := r.c("automations")
	enabled := asCount(autos, func(x asstObj) bool { return asBool(x, "enabled") })
	lines = append(lines, assistLineOf("sparkles", "indigo", fmt.Sprintf("%d/%d 個自動化啟用中", enabled, len(autos))))
	runs, saved := r.automationTotals()
	if runs > 0 {
		lines = append(lines, assistLineOf("clock.arrow.circlepath", "green", fmt.Sprintf("30 天執行 %d 次,省下約 %d 小時", runs, int(saved))))
	}
	if n := asCount(r.c("automation_runs"), func(x asstObj) bool { return asStr(x, "status") == "running" }); n > 0 {
		lines = append(lines, assistLineOf("gearshape.2.fill", "blue", fmt.Sprintf("%d 個 AI 任務執行中", n)))
	}
	if len(pending) == 0 {
		return AssistBriefing{Headline: "AI 自動化運作中", Tone: "green", Lines: lines}
	}
	return AssistBriefing{Headline: "有請求等你決定", Tone: "purple", Lines: lines}
}

func (r AssistResponder) settingsBrief() AssistBriefing {
	ints := r.c("integrations")
	connected := asCount(ints, func(x asstObj) bool { return asBool(x, "connected") })
	lines := []AssistBriefLine{assistLineOf("puzzlepiece.extension.fill", "indigo", fmt.Sprintf("%d/%d 個整合已連接", connected, len(ints)))}
	off := asFilter(ints, func(x asstObj) bool { return !asBool(x, "connected") })
	if len(off) > 0 {
		lines = append(lines, assistLineOf("bolt.slash", "secondary", "未連接:"+asNames(off, 3)))
	}
	return AssistBriefing{Headline: "設定與整合", Tone: "secondary", Lines: lines}
}
