// 助理共用的小工具:讀範例資料的 asXxx 函式、標籤與時間/數字格式化。
package core

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

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
