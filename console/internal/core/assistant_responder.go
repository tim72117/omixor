// 助理回應器:AssistResponder 型別與從範例資料取各類清單的查詢方法、FocusLabel。
package core

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

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
