package core

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// AI 助理面板的回答,同 Swift 版 Features/Assistant/AssistResponder.swift(與 AssistModels.swift)。
// 規則式回應器:只從範例資料組出固定格式的文字,不連網、不呼叫模型。
//
//	Action assistant_briefing {section}           → AssistBriefingResult(目前狀況卡片 + 建議問題)
//	Action assistant_ask      {section, question} → AssistReplyResult(回答文字 + 動作按鈕)
//
// section 沒給時用目前導覽的頁面(a.Nav.Section)。格式寫在 docs/PORTING.md 的 Action 表。

// AssistBriefLine 「目前狀況」卡片的一行;color 是 Swift 的顏色名稱(green、orange、red、blue、purple、indigo、teal、gray、secondary)
type AssistBriefLine struct {
	Color string `json:"color"`
	Icon  string `json:"icon"`
	Text  string `json:"text"`
}

type AssistBriefing struct {
	Headline string            `json:"headline"`
	Lines    []AssistBriefLine `json:"lines"`
	Tone     string            `json:"tone"`
}

// AssistAction 回答裡可以按的動作按鈕(同 Swift 的 AssistAction)。
//
//	kind = acknowledge {alertId, title}  確認告警
//	       approve     {approvalId}      核准
//	       reject      {approvalId}      拒絕
//	       retry       {runId}           重新執行 CI
//	       go          {section, focus}  前往頁面(focus 可能是 null)
//	       copy        {text}            複製文字
//
// key 同 Swift 的 id(按過就停用用的);prominent = 會改變狀態的動作用醒目樣式。
type AssistAction map[string]any

type AssistReply struct {
	Actions []AssistAction `json:"actions"`
	Text    string         `json:"text"`
}

var assistSections = []string{"overview", "development", "pipelines", "infrastructure", "monitoring", "incidents", "automation", "cabinet", "settings", "channels", "system"}

var assistSectionTitle = map[string]string{
	"overview": "總覽", "development": "開發進度", "pipelines": "CI/CD", "infrastructure": "伺服器與服務",
	"monitoring": "監控與告警", "incidents": "事件", "automation": "AI 自動化", "cabinet": "文件櫃", "settings": "設定",
	"channels": "對話", "system": "系統",
}

var assistSectionIcon = map[string]string{
	"overview": "square.grid.2x2", "development": "hammer", "pipelines": "arrow.triangle.branch", "infrastructure": "server.rack",
	"monitoring": "waveform.path.ecg", "incidents": "flame", "automation": "sparkles", "cabinet": "archivebox", "settings": "gearshape",
	"channels": "bubble.left.and.bubble.right", "system": "switch.2",
}

// MARK: - 動作

func assistAck(alertID, title string) AssistAction {
	return AssistAction{"kind": "acknowledge", "alertId": alertID, "title": title, "key": "ack-" + alertID,
		"label": "確認告警:" + title, "icon": "bell.badge", "prominent": true}
}

func assistApprove(id string) AssistAction {
	return AssistAction{"kind": "approve", "approvalId": id, "key": "approve-" + id, "label": "核准", "icon": "checkmark.seal", "prominent": true}
}

func assistReject(id string) AssistAction {
	return AssistAction{"kind": "reject", "approvalId": id, "key": "reject-" + id, "label": "拒絕", "icon": "xmark.seal", "prominent": false}
}

func assistRetry(runID int) AssistAction {
	return AssistAction{"kind": "retry", "runId": runID, "key": fmt.Sprintf("retry-%d", runID),
		"label": fmt.Sprintf("重新執行 #%d", runID), "icon": "arrow.clockwise", "prominent": true}
}

// assistGo focus 是 nil 時輸出 null
func assistGo(section string, focus *string, label string) AssistAction {
	f := ""
	var fv any
	if focus != nil {
		f, fv = *focus, *focus
	}
	return AssistAction{"kind": "go", "section": section, "focus": fv, "key": "go-" + section + "-" + f,
		"label": label, "icon": assistSectionIcon[section], "prominent": false}
}

func assistCopy(text string) AssistAction {
	return AssistAction{"kind": "copy", "text": text, "key": "copy-" + VoiceSha([]byte(text))[:16],
		"label": "複製文字", "icon": "doc.on.doc", "prominent": false}
}

func assistSP(s string) *string { return &s }

// MARK: - 讀範例資料的小工具(資料是 Swift MCPJSON 的格式)

type asstObj = map[string]any

func asStr(m asstObj, k string) string { s, _ := m[k].(string); return s }

func asOpt(m asstObj, k string) (string, bool) { s, ok := m[k].(string); return s, ok }

func asNum(m asstObj, k string) float64 {
	switch x := m[k].(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

func asInt(m asstObj, k string) int { return int(asNum(m, k)) }

func asBool(m asstObj, k string) bool { b, _ := m[k].(bool); return b }

func asTime(m asstObj, k string) time.Time {
	t, _ := ParseTime(asStr(m, k))
	return t
}

func asList(m asstObj, k string) []asstObj {
	out := []asstObj{}
	l, _ := m[k].([]any)
	for _, x := range l {
		if o, ok := x.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

func asStrs(m asstObj, k string) []string {
	out := []string{}
	l, _ := m[k].([]any)
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asID(m asstObj) string { s, _ := ScalarString(m["id"]); return s }

func asFilter(l []asstObj, f func(asstObj) bool) []asstObj {
	out := []asstObj{}
	for _, x := range l {
		if f(x) {
			out = append(out, x)
		}
	}
	return out
}

func asCount(l []asstObj, f func(asstObj) bool) int { return len(asFilter(l, f)) }

// asMax 同 Swift 的 max(by:):第一個最大的
func asMax(l []asstObj, less func(a, b asstObj) bool) asstObj {
	var r asstObj
	for i, x := range l {
		if i == 0 || less(r, x) {
			r = x
		}
	}
	return r
}

// asMin 同 Swift 的 min(by:):第一個最小的
func asMin(l []asstObj, less func(a, b asstObj) bool) asstObj {
	var r asstObj
	for i, x := range l {
		if i == 0 || less(x, r) {
			r = x
		}
	}
	return r
}

func asSorted(l []asstObj, less func(a, b asstObj) bool) []asstObj {
	out := append([]asstObj{}, l...)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func asPrefix(l []asstObj, n int) []asstObj {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func asNames(l []asstObj, n int) string {
	out := []string{}
	for _, x := range asPrefix(l, n) {
		out = append(out, asStr(x, "name"))
	}
	return strings.Join(out, "、")
}

// MARK: - Swift 的標籤與格式

func assistHealthLabel(s string) string {
	return map[string]string{"healthy": "正常", "degraded": "效能下降", "down": "中斷", "maintenance": "維護中", "unknown": "未知"}[s]
}

func assistEnvLabel(s string) string {
	return map[string]string{"production": "Production", "staging": "Staging", "development": "Development"}[s]
}

func assistEnvShort(s string) string {
	return map[string]string{"production": "prod", "staging": "stg", "development": "dev"}[s]
}

func assistRunStatusLabel(s string) string {
	return map[string]string{"queued": "排隊中", "running": "執行中", "succeeded": "成功", "failed": "失敗", "canceled": "已取消", "waitingApproval": "等待核准"}[s]
}

func assistIncidentStatusLabel(s string) string {
	return map[string]string{"investigating": "調查中", "identified": "已找到原因", "monitoring": "觀察中", "resolved": "已解決"}[s]
}

func assistIncidentStatusColor(s string) string {
	return map[string]string{"investigating": "red", "identified": "orange", "monitoring": "blue", "resolved": "green"}[s]
}

var assistSeverities = []string{"critical", "warning", "info"}

func assistSeverityLabel(s string) string {
	return map[string]string{"critical": "Critical", "warning": "Warning", "info": "Info"}[s]
}

func assistSeverityColor(s string) string {
	return map[string]string{"critical": "red", "warning": "orange", "info": "blue"}[s]
}

func assistSeverityRank(s string) int {
	return map[string]int{"critical": 0, "warning": 1, "info": 2}[s]
}

func assistRiskLabel(s string) string {
	return map[string]string{"low": "低風險", "medium": "中風險", "high": "高風險"}[s]
}

func assistAutonomyLabel(s string) string {
	return map[string]string{"suggest": "只建議", "approve": "需核准", "auto": "自動執行"}[s]
}

func assistWorkStatusLabel(s string) string {
	return map[string]string{"backlog": "Backlog", "todo": "待辦", "inProgress": "進行中", "review": "審查中", "done": "完成"}[s]
}

// AssistFmtTime 同 Swift 的 Fmt.time:當地時間 HH:mm
func AssistFmtTime(t time.Time) string { return t.Local().Format("15:04") }

// AssistFmtPercent 同 Fmt.percent:String(format: "%.Nf%%")
func AssistFmtPercent(v float64, digits int) string { return fmt.Sprintf("%.*f%%", digits, v) }

// AssistFmtNumber 同 Fmt.number
func AssistFmtNumber(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
	if v >= 1000 {
		return fmt.Sprintf("%.1fk", v/1000)
	}
	return fmt.Sprintf("%.0f", v)
}

// AssistFmtRelative 同 Fmt.relative(RelativeDateTimeFormatter,zh_TW,.short):「3小時前」「30分鐘後」「0秒後」
func AssistFmtRelative(t, now time.Time) string {
	d := t.Sub(now)
	secs := int64(math.Abs(d.Seconds()))
	suffix := "前"
	if d >= 0 || secs == 0 {
		suffix = "後"
	}
	from, to := now, t
	if to.Before(from) {
		from, to = to, from
	}
	months := 0
	for months < 1200 && !from.AddDate(0, months+1, 0).After(to) {
		months++
	}
	days := secs / 86400
	switch {
	case months >= 12:
		return fmt.Sprintf("%d年%s", months/12, suffix)
	case months >= 1:
		return fmt.Sprintf("%d個月%s", months, suffix)
	case days >= 7:
		return fmt.Sprintf("%d週%s", days/7, suffix)
	case days >= 1:
		return fmt.Sprintf("%d天%s", days, suffix)
	case secs >= 3600:
		return fmt.Sprintf("%d小時%s", secs/3600, suffix)
	case secs >= 60:
		return fmt.Sprintf("%d分鐘%s", secs/60, suffix)
	}
	return fmt.Sprintf("%d秒%s", secs, suffix)
}

func assistIsToday(t, now time.Time) bool {
	a, b := t.Local(), now.Local()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// MARK: - 回應器

// AssistResponder 同 Swift 的 AssistResponder。用的時候要拿著 Mu。
type AssistResponder struct {
	a       *App
	section string
	now     time.Time
}

func NewAssistResponder(a *App, section string) AssistResponder {
	return AssistResponder{a: a, section: section, now: time.Now()}
}

func (r AssistResponder) c(name string) []asstObj { return r.a.C[name] }

func (r AssistResponder) find(coll, id string) asstObj {
	_, m := r.a.Find(coll, id)
	return m
}

func (r AssistResponder) svcName(id string) string {
	if s := r.find("services", id); s != nil {
		return asStr(s, "name")
	}
	return id
}

func (r AssistResponder) personName(id string) string {
	if p := r.find("people", id); p != nil {
		return asStr(p, "name")
	}
	return id
}

func (r AssistResponder) firingAlerts() []asstObj {
	l := asFilter(r.c("alerts"), func(x asstObj) bool { return asStr(x, "state") == "firing" })
	return asSorted(l, func(a, b asstObj) bool {
		return assistSeverityRank(asStr(a, "severity")) < assistSeverityRank(asStr(b, "severity"))
	})
}

func (r AssistResponder) openIncidents() []asstObj {
	return asFilter(r.c("incidents"), func(x asstObj) bool { return asStr(x, "status") != "resolved" })
}

func (r AssistResponder) pendingApprovals() []asstObj {
	return asFilter(r.c("approvals"), func(x asstObj) bool { return asStr(x, "state") == "pending" })
}

func (r AssistResponder) runningPipelines() []asstObj {
	return asFilter(r.c("pipeline_runs"), func(x asstObj) bool {
		s := asStr(x, "status")
		return s == "running" || s == "queued"
	})
}

func (r AssistResponder) currentOnCall() asstObj {
	for _, x := range r.c("on_call") {
		if asStr(x, "role") == "Primary" && !asTime(x, "start").After(r.now) && asTime(x, "end").After(r.now) {
			return x
		}
	}
	return nil
}

// 告警的 service 欄位可能是 id 也可能是名稱,兩種都比對
func (r AssistResponder) alertsFor(s asstObj) []asstObj {
	return asFilter(r.firingAlerts(), func(x asstObj) bool {
		return asStr(x, "service") == asStr(s, "id") || asStr(x, "service") == asStr(s, "name")
	})
}

func (r AssistResponder) worstLatency() asstObj {
	return asMax(r.c("services"), func(a, b asstObj) bool { return asNum(a, "latencyP95") < asNum(b, "latencyP95") })
}

func (r AssistResponder) failedRuns() []asstObj {
	l := asFilter(r.c("pipeline_runs"), func(x asstObj) bool { return asStr(x, "status") == "failed" })
	return asSorted(l, func(a, b asstObj) bool { return asTime(a, "startedAt").After(asTime(b, "startedAt")) })
}

func (r AssistResponder) openPRs() []asstObj {
	return asFilter(r.c("pull_requests"), func(x asstObj) bool {
		s := asStr(x, "status")
		return s == "open" || s == "approved" || s == "changesRequested"
	})
}

func (r AssistResponder) hotServers() []asstObj {
	l := asFilter(r.c("servers"), func(x asstObj) bool {
		return asNum(x, "cpu") > 80 || asNum(x, "memory") > 85 || asNum(x, "disk") > 85
	})
	return asSorted(l, func(a, b asstObj) bool { return asNum(a, "cpu") > asNum(b, "cpu") })
}

func (r AssistResponder) topIncident() asstObj {
	return asMin(r.openIncidents(), func(a, b asstObj) bool {
		if asInt(a, "sev") != asInt(b, "sev") {
			return asInt(a, "sev") < asInt(b, "sev")
		}
		return asTime(b, "startedAt").Before(asTime(a, "startedAt"))
	})
}

func (r AssistResponder) since(t time.Time) string {
	m := max(0, int(r.now.Sub(t).Seconds()/60))
	if m < 60 {
		return fmt.Sprintf("%d 分鐘", m)
	}
	if m < 1440 {
		return fmt.Sprintf("%d 小時 %d 分", m/60, m%60)
	}
	return fmt.Sprintf("%d 天", m/1440)
}

func (r AssistResponder) relative(t time.Time) string { return AssistFmtRelative(t, r.now) }

// 在使用者的問題裡找到提及的服務(名稱最長者優先,避免 api 吃掉 api-server)
func (r AssistResponder) mentionedService(q string) asstObj {
	l := asSorted(r.c("services"), func(a, b asstObj) bool {
		return utf8.RuneCountInString(asStr(a, "name")) > utf8.RuneCountInString(asStr(b, "name"))
	})
	for _, s := range l {
		if strings.Contains(q, strings.ToLower(asStr(s, "name"))) || strings.Contains(q, strings.ToLower(asStr(s, "id"))) {
			return s
		}
	}
	return nil
}

// FocusLabel 目前畫面正在看的項目名稱(給情境標籤用);沒有回傳 nil
func (r AssistResponder) FocusLabel() *string {
	f := r.a.Nav.FocusID
	if f == "" {
		return nil
	}
	if x := r.find("alerts", f); x != nil {
		return assistSP(asStr(x, "title"))
	}
	if x := r.find("incidents", f); x != nil {
		return assistSP(asID(x) + " " + asStr(x, "title"))
	}
	if x := r.find("services", f); x != nil {
		return assistSP(asStr(x, "name"))
	}
	if x := r.find("servers", f); x != nil {
		return assistSP(asStr(x, "name"))
	}
	if x := r.find("approvals", f); x != nil {
		return assistSP(asStr(x, "title"))
	}
	if x := r.find("pipeline_runs", f); x != nil {
		return assistSP(fmt.Sprintf("%s #%s", asStr(x, "pipeline"), asID(x)))
	}
	if x := r.find("automations", f); x != nil {
		return assistSP(asStr(x, "name"))
	}
	if x := r.find("deployments", f); x != nil {
		return assistSP(r.svcName(asStr(x, "service")) + " " + asStr(x, "version"))
	}
	if x := r.find("pull_requests", f); x != nil {
		return assistSP("PR #" + asID(x))
	}
	return assistSP(f)
}

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

// MARK: - 建議問題

func (r AssistResponder) Suggestions() []string {
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

// MARK: - Action

// AssistBriefingResult assistant_briefing 的回傳
type AssistBriefingResult struct {
	AssistBriefing
	FocusLabel   *string  `json:"focusLabel"`
	Section      string   `json:"section"`
	SectionTitle string   `json:"sectionTitle"`
	Suggestions  []string `json:"suggestions"`
}

// AssistReplyResult assistant_ask 的回傳
type AssistReplyResult struct {
	AssistReply
	Question string `json:"question"`
	Section  string `json:"section"`
}

// assistVisibleOnly 拿掉「前往」系統設定裡關掉的頁面的按鈕(同 App 的 AssistResponder 依開關)。要拿著 Mu
func assistVisibleOnly(a *App, r AssistReply) AssistReply {
	if len(r.Actions) == 0 {
		return r
	}
	out := make([]AssistAction, 0, len(r.Actions))
	for _, x := range r.Actions {
		if k, _ := x["kind"].(string); k == "go" {
			if s, _ := x["section"].(string); s != "" && !a.SectionVisible(s) {
				continue
			}
		}
		out = append(out, x)
	}
	r.Actions = out
	return r
}

func assistSection(a *App, args map[string]any) (string, error) {
	s, _ := args["section"].(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return a.Nav.Section, nil
	}
	for _, x := range assistSections {
		if x == s {
			return s, nil
		}
	}
	return "", UserError{fmt.Sprintf("section 必須是 %s 之一,收到「%s」", strings.Join(assistSections, "、"), s)}
}

func init() {
	RegisterAction("assistant_briefing", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		sec, err := assistSection(a, args)
		if err != nil {
			return nil, err
		}
		r := NewAssistResponder(a, sec)
		return AssistBriefingResult{AssistBriefing: r.Briefing(), FocusLabel: r.FocusLabel(), Section: sec,
			SectionTitle: assistSectionTitle[sec], Suggestions: r.Suggestions()}, nil
	})
	RegisterAction("assistant_ask", func(a *App, args map[string]any) (any, error) {
		q, _ := args["question"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, UserError{"缺少參數 question"}
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		sec, err := assistSection(a, args)
		if err != nil {
			return nil, err
		}
		return AssistReplyResult{AssistReply: assistVisibleOnly(a, NewAssistResponder(a, sec).Reply(q)), Question: q, Section: sec}, nil
	})
}
