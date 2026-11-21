// 助理回答:建議問題、功能開關判斷、Reply 分派與狀態/診斷/公告回答。
package core

import (
	"fmt"
	"strings"

	"omixor-core/model"
)

// MARK: - 建議問題

// Suggestions 建議問題:系統設定關掉的功能不建議(同 App 的 suggestions())。要拿著 Mu
func (r AssistResponder) Suggestions() []string {
	out := []string{}
	for _, q := range r.allSuggestions() {
		if k := assistTopicFeature(q); k == "" || r.a.FeatureOn(k) {
			out = append(out, q)
		}
	}
	return out
}

// assistTopicFeature 問題講的是哪個功能("" = 不屬於可以關的功能)。
// 照 App 的 topicFeature(AssistResponder.swift),順序對應回答的分派順序
func assistTopicFeature(raw string) string {
	q := strings.ToLower(raw)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(q, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("延遲", "慢", "latency", "p95", "錯誤率"):
		return "infrastructure.services"
	case has("公告", "草擬", "status page", "對外"):
		return ""
	case has("部署", "deploy", "上線", "release"):
		return "pipelines.deployments"
	case has("核准", "審核", "approval", "決定", "待辦請求"):
		return "automation.inbox"
	case has("失敗", "ci", "pipeline", "建置", "build", "流程"):
		if has("自動化") {
			return "automation.runs"
		}
		return "pipelines.runs"
	case has("告警", "alert", "警報"):
		return "monitoring.alerts"
	case has("事件", "incident", "inc-"):
		return "incidents.list"
	case has("伺服器", "server", "cpu", "記憶體", "主機", "容器", "重啟", "資源"):
		return "infrastructure.servers"
	case has("pr", "審查", "review", "合併", "卡住"):
		return "development.pulls"
	case has("sprint", "進度", "衝刺", "工作項目"):
		return "development.board"
	case has("值班", "on-call", "oncall", "on call"):
		return "incidents.oncall"
	case has("自動化", "automation", "省", "ai 任務"):
		return "automation.flows"
	}
	return ""
}

// assistClosedReply 問到系統設定關掉的功能:說明已關閉並給「前往系統」,不回範例資料(同 App 的 reply(to:))。
// 沒關掉回傳 false。要拿著 Mu
func assistClosedReply(a *App, raw string) (AssistReply, bool) {
	key := assistTopicFeature(raw)
	if key == "" || a.FeatureOn(key) {
		return AssistReply{}, false
	}
	name, why := key, ""
	if f, ok := model.FeatureByKey(key); ok {
		name = f.Name
		if f.Experimental {
			why = "(實驗功能,目前是範例資料)"
		}
	}
	return assistReplyOf("這個功能已關閉:「"+name+"」在系統設定裡關閉了"+why+"。需要的話請組織管理員在「系統」頁打開。",
		assistGo("system", nil, "前往系統")), true
}

func (r AssistResponder) allSuggestions() []string {
	svc := "api-server"
	if w := r.worstLatency(); w != nil {
		svc = asStr(w, "name")
	}
	switch r.section {
	case "development":
		return []string{"哪些 PR 卡住了?", "這個 Sprint 進度如何?", "AI 審查標出哪些風險?"}
	case "pipelines":
		return []string{"最近哪些執行失敗?為什麼?", "總結今天的部署", "有部署在等核准嗎?"}
	case "infrastructure":
		return []string{"哪台伺服器資源最吃緊?", svc + " 狀況如何?", "有容器一直重啟嗎?"}
	case "monitoring":
		return []string{"為什麼 " + svc + " 延遲變高?", "列出觸發中的告警", "哪些告警還沒開事件?"}
	case "incidents":
		return []string{"總結進行中的事件", "草擬事件公告", "這次事件和部署有關嗎?"}
	case "automation":
		return []string{"有什麼需要我核准?", "AI 自動化省了多少時間?", "哪個自動化最常失敗?"}
	case "cabinet":
		return []string{"總結目前狀況"}
	case "settings":
		return []string{"哪些整合還沒連接?", "總結目前狀況"}
	case "channels":
		return []string{"總結目前狀況"}
	}
	return []string{"總結目前狀況", "今天有哪些部署?", "有什麼需要我決定?", "誰在值班?"}
}

// MARK: - 回答

func assistReplyOf(text string, actions ...AssistAction) AssistReply {
	if actions == nil {
		actions = []AssistAction{}
	}
	return AssistReply{Text: text, Actions: actions}
}

func (r AssistResponder) Reply(raw string) AssistReply {
	q := strings.ToLower(raw)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(q, w) {
				return true
			}
		}
		return false
	}
	svc := r.mentionedService(q)
	if has("延遲", "慢", "latency", "p95", "錯誤率", "為什麼", "error") {
		s := svc
		if s == nil && has("延遲", "慢", "latency", "p95") {
			s = r.worstLatency()
		}
		if s != nil {
			return r.diagnose(s)
		}
	}
	switch {
	case has("公告", "草擬", "status page", "對外"):
		return r.announcement()
	case has("和部署有關", "跟部署有關", "部署有關"):
		return r.incidentDeployCorrelation()
	case has("部署", "deploy", "上線", "release"):
		return r.deploymentsSummary()
	case has("核准", "審核", "approval", "決定", "待辦請求"):
		return r.approvalsReply()
	case has("失敗", "ci", "pipeline", "建置", "build", "流程"):
		if has("自動化") {
			return r.automationsReply()
		}
		return r.failedRunsReply()
	case has("還沒開事件", "沒開事件"):
		return r.alertsReply(true)
	case has("告警", "alert", "警報"):
		return r.alertsReply(false)
	case has("事件", "incident", "inc-"):
		return r.incidentsReply()
	case has("伺服器", "server", "cpu", "記憶體", "主機", "容器", "重啟", "資源"):
		return r.serversReply()
	case has("pr", "審查", "review", "合併", "卡住"):
		return r.prsReply()
	case has("sprint", "進度", "衝刺", "工作項目"):
		return r.sprintReply()
	case has("值班", "on-call", "oncall", "on call"):
		return r.onCallReply()
	case has("自動化", "automation", "省", "ai 任務"):
		return r.automationsReply()
	case has("整合", "integration"):
		return r.integrationsReply()
	case svc != nil:
		return r.diagnose(svc)
	case has("狀況", "總結", "摘要", "概況", "今天", "現在", "怎麼樣", "如何"):
		return r.statusReply()
	}
	return r.fallback()
}

func (r AssistResponder) statusReply() AssistReply {
	b := r.Briefing()
	body := []string{}
	for _, l := range b.Lines {
		body = append(body, "• "+l.Text)
	}
	actions := []AssistAction{}
	if len(r.openIncidents()) > 0 {
		var f *string
		if t := r.topIncident(); t != nil {
			f = assistSP(asID(t))
		}
		actions = append(actions, assistGo("incidents", f, "前往事件"))
	}
	if firing := r.firingAlerts(); len(firing) > 0 {
		actions = append(actions, assistGo("monitoring", assistSP(asID(firing[0])), "查看告警"))
	}
	if len(r.pendingApprovals()) > 0 {
		actions = append(actions, assistGo("automation", nil, "前往核准"))
	}
	text := strings.Join(body, "\n")
	if text == "" {
		text = "目前沒有資料可以總結。"
	}
	return assistReplyOf(fmt.Sprintf("**%s:%s**\n%s", assistSectionTitle[r.section], b.Headline, text), actions...)
}

func (r AssistResponder) diagnose(s asstObj) AssistReply {
	parts := []string{}
	actions := []AssistAction{}
	sid := asStr(s, "id")
	parts = append(parts, fmt.Sprintf("**%s** 目前%s:p95 延遲 %d ms、錯誤率 %s、每秒 %s 次請求。",
		asStr(s, "name"), assistHealthLabel(asStr(s, "health")), int(asNum(s, "latencyP95")),
		AssistFmtPercent(asNum(s, "errorRate"), 2), AssistFmtNumber(asNum(s, "rps"))))
	parts = append(parts, fmt.Sprintf("30 天可用率 %s(目標 %s),錯誤預算剩 %d%%。",
		AssistFmtPercent(asNum(s, "uptime30d"), 2), AssistFmtPercent(asNum(s, "sloTarget"), 2), int(asNum(s, "errorBudgetRemaining")*100)))

	causes := []string{}
	if related := r.alertsFor(s); len(related) > 0 {
		al := related[0]
		causes = append(causes, fmt.Sprintf("告警「%s」觸發中(%s,目前 %s,已 %s)", asStr(al, "title"), asStr(al, "rule"), asStr(al, "value"), r.since(asTime(al, "startedAt"))))
		if note, ok := asOpt(al, "aiNote"); ok {
			causes = append(causes, "AI 初步判讀:"+note)
		}
		actions = append(actions, assistAck(asID(al), asStr(al, "title")), assistGo("monitoring", assistSP(asID(al)), "查看告警"))
	}
	recent := asFilter(r.c("deployments"), func(x asstObj) bool {
		return asStr(x, "service") == sid && r.now.Sub(asTime(x, "startedAt")).Seconds() < 6*3600
	})
	if d := asMax(recent, func(a, b asstObj) bool { return asTime(a, "startedAt").Before(asTime(b, "startedAt")) }); d != nil {
		causes = append(causes, fmt.Sprintf("%s前部署了 %s(前一版 %s,%s),時間點接近,可能相關",
			r.since(asTime(d, "startedAt")), asStr(d, "version"), asStr(d, "previousVersion"), assistEnvLabel(asStr(d, "env"))))
		actions = append(actions, assistGo("pipelines", assistSP(asID(d)), "查看部署"))
	}
	for _, hid := range asStrs(s, "hostIds") {
		if h := r.find("servers", hid); h != nil && (asNum(h, "cpu") > 80 || asNum(h, "memory") > 85) {
			causes = append(causes, fmt.Sprintf("主機 %s CPU %d%%、記憶體 %d%%,資源吃緊", asStr(h, "name"), int(asNum(h, "cpu")), int(asNum(h, "memory"))))
			break
		}
	}
	deps := []string{}
	for _, did := range asStrs(s, "dependencies") {
		if d := r.find("services", did); d != nil && asStr(d, "health") != "healthy" {
			deps = append(deps, fmt.Sprintf("%s(%s)", asStr(d, "name"), assistHealthLabel(asStr(d, "health"))))
		}
	}
	if len(deps) > 0 {
		causes = append(causes, "依賴的服務異常:"+strings.Join(deps, "、"))
	}
	for _, inc := range r.openIncidents() {
		if contains := func() bool {
			for _, x := range asStrs(inc, "services") {
				if x == sid {
					return true
				}
			}
			return false
		}(); contains {
			causes = append(causes, fmt.Sprintf("已有事件 %s 在處理(%s)", asID(inc), assistIncidentStatusLabel(asStr(inc, "status"))))
			actions = append(actions, assistGo("incidents", assistSP(asID(inc)), "前往事件"))
			break
		}
	}
	if len(causes) == 0 {
		parts = append(parts, "目前沒有找到相關告警、近期部署或主機壓力,數字看起來在正常範圍內。")
	} else {
		bul := []string{}
		for _, c := range causes {
			bul = append(bul, "• "+c)
		}
		parts = append(parts, "可能原因:\n"+strings.Join(bul, "\n"))
	}
	actions = append(actions, assistGo("infrastructure", assistSP(sid), "查看服務"))
	return assistReplyOf(strings.Join(parts, "\n"), actions...)
}

func (r AssistResponder) announcement() AssistReply {
	inc := r.topIncident()
	if inc == nil {
		return assistReplyOf("目前沒有進行中的事件,不需要發布公告。如果要預告維護,可以告訴我時段和影響的服務。")
	}
	names := []string{}
	for _, s := range asStrs(inc, "services") {
		names = append(names, r.svcName(s))
	}
	svcs := strings.Join(names, "、")
	if svcs == "" {
		svcs = "部分服務"
	}
	next := ""
	switch asStr(inc, "status") {
	case "investigating":
		next = "我們正在調查原因"
	case "identified":
		next = "我們已找到原因,正在修復"
	case "monitoring":
		next = "修復已上線,正在觀察是否恢復穩定"
	case "resolved":
		next = "問題已解決"
	}
	draft := fmt.Sprintf("【服務狀態更新】%s\n自 %s 起,%s出現異常。%s\n目前進度:%s。下一次更新預計在 30 分鐘內。造成不便,敬請見諒。",
		asStr(inc, "title"), AssistFmtTime(asTime(inc, "startedAt")), svcs, asStr(inc, "impact"), next)
	return assistReplyOf(fmt.Sprintf("以下是 %s 的公告草稿,發布前請再確認:\n\n%s", asID(inc), draft),
		assistCopy(draft), assistGo("incidents", assistSP(asID(inc)), "前往事件"))
}
