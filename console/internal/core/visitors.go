package core

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// 網站訪客:evoleap 後台的會員與訪客統計(GET /api/admin/overview?range=…),透過「連線」以管理員身分讀取。
// 同 Swift 的 Visitors/SiteVisitors.swift。缺欄位、null、型別不符都用預設值(Go 的 nil slice 是 null)。

type SiteCount struct {
	Count int    `json:"count"`
	Name  string `json:"name"`
}

type SiteGuestEvent struct {
	E string `json:"e"`
	T int    `json:"t"`
}

type SiteLessonStat struct {
	AvgPercent         float64 `json:"avgPercent"`
	Completed          int     `json:"completed"`
	GuestBots          int     `json:"guestBots"`
	GuestFailed        int     `json:"guestFailed"`
	GuestIdleVisibleMs int64   `json:"guestIdleVisibleMs"`
	GuestPlayed        int     `json:"guestPlayed"`
	GuestReady         int     `json:"guestReady"`
	GuestReadyAvgMs    int64   `json:"guestReadyAvgMs"`
	GuestTracked       int     `json:"guestTracked"`
	GuestViews         int     `json:"guestViews"`
	GuestVisitors      int     `json:"guestVisitors"`
	GuestWatchedMs     int64   `json:"guestWatchedMs"`
	Learners           int     `json:"learners"`
	Lesson             string  `json:"lesson"`
}

type SiteGuestVisit struct {
	Device    string           `json:"device"`
	Events    []SiteGuestEvent `json:"events"`
	Lesson    string           `json:"lesson"`
	LoadError string           `json:"loadError"`
	OpenedAt  *Time            `json:"openedAt,omitempty"`
	ReadyMs   int              `json:"readyMs"`
	Source    string           `json:"source"`
	VisibleMs int              `json:"visibleMs"`
}

type SitePathStep struct {
	Lesson string `json:"lesson"`
	Played bool   `json:"played"`
}

type SitePathCount struct {
	Count  int            `json:"count"`
	Played int            `json:"played"`
	Steps  []SitePathStep `json:"steps"`
}

type SiteHomeStay struct {
	Device     string `json:"device"`
	Guest      string `json:"guest"`
	LastOpened *Time  `json:"lastOpened,omitempty"`
	LongestMs  int    `json:"longestMs"`
	MaxScroll  int    `json:"maxScroll"`
	Source     string `json:"source"`
	TotalMs    int64  `json:"totalMs"`
	Visits     int    `json:"visits"`
}

type SiteLoginStats struct {
	Fails            []SiteCount `json:"fails"`
	GoogleErrors     []SiteCount `json:"googleErrors"`
	GoogleFailed     int         `json:"googleFailed"`
	GoogleReady      int         `json:"googleReady"`
	GoogleReadyAvgMs int64       `json:"googleReadyAvgMs"`
	Next             []SiteCount `json:"next"`
	Outcomes         []SiteCount `json:"outcomes"`
	Visits           int         `json:"visits"`
}

type SiteGuestBreakdown struct {
	Devices     []SiteCount      `json:"devices"`
	Errors      []SiteCount      `json:"errors"`
	HomePaths   []SitePathCount  `json:"homePaths"`
	HomeStays   []SiteHomeStay   `json:"homeStays"`
	HomeVisits  int              `json:"homeVisits"`
	IdleVisible []SiteCount      `json:"idleVisible"`
	IdleVisits  []SiteGuestVisit `json:"idleVisits"`
	Login       SiteLoginStats   `json:"login"`
	Sources     []SiteCount      `json:"sources"`
}

type SiteOverview struct {
	ActiveUsers int                `json:"activeUsers"`
	GuestStats  SiteGuestBreakdown `json:"guestStats"`
	Guests      int                `json:"guests"`
	Lessons     []SiteLessonStat   `json:"lessons"`
	Users       int                `json:"users"`
}

// PlayerLessons 有播放器的課(登入門檻 gate:、一般頁面 page: 不算)
func (o SiteOverview) PlayerLessons() []SiteLessonStat {
	out := []SiteLessonStat{}
	for _, l := range o.Lessons {
		if !strings.HasPrefix(l.Lesson, "gate:") && !strings.HasPrefix(l.Lesson, "page:") {
			out = append(out, l)
		}
	}
	return out
}

// Gates 登入門檻被看到幾次
func (o SiteOverview) Gates() []SiteCount {
	out := []SiteCount{}
	for _, l := range o.Lessons {
		if strings.HasPrefix(l.Lesson, "gate:") && l.GuestTracked > 0 {
			out = append(out, SiteCount{Name: strings.TrimPrefix(l.Lesson, "gate:"), Count: l.GuestTracked})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

type SiteFunnel struct {
	Bots    int `json:"bots"`
	Failed  int `json:"failed"`
	Played  int `json:"played"`
	Ready   int `json:"ready"`
	Tracked int `json:"tracked"`
}

// Funnel 訪客漏斗(有播放器的課加總)
func (o SiteOverview) Funnel() SiteFunnel {
	var f SiteFunnel
	for _, l := range o.PlayerLessons() {
		f.Tracked += l.GuestTracked
		f.Ready += l.GuestReady
		f.Played += l.GuestPlayed
		f.Failed += l.GuestFailed
		f.Bots += l.GuestBots
	}
	return f
}

// MARK: 寬鬆解碼

// svInt 同 Swift 的 Int 解碼:整數才算,其他用預設值
func svInt(v any) int {
	if f, ok := v.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1<<62 {
		return int(f)
	}
	return 0
}

func svInt64(v any) int64 { return int64(svInt(v)) }

func svFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func svDate(v any) *Time {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	t, err := ParseTime(s)
	if err != nil {
		return nil
	}
	return timePtr(t)
}

func svCounts(v any) []SiteCount {
	out := []SiteCount{}
	arr, ok := v.([]any)
	if !ok {
		return out
	}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return []SiteCount{} // Swift:陣列裡有一個解不開就整個用預設值
		}
		out = append(out, SiteCount{Name: jStr(m["name"]), Count: svInt(m["count"])})
	}
	return out
}

func svObjects(v any) ([]map[string]any, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := []map[string]any{}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}

func svLesson(m map[string]any) SiteLessonStat {
	return SiteLessonStat{Lesson: jStr(m["lesson"]), Learners: svInt(m["learners"]), Completed: svInt(m["completed"]),
		AvgPercent: svFloat(m["avgPercent"]), GuestViews: svInt(m["guestViews"]), GuestVisitors: svInt(m["guestVisitors"]),
		GuestWatchedMs: svInt64(m["guestWatchedMs"]), GuestBots: svInt(m["guestBots"]), GuestTracked: svInt(m["guestTracked"]),
		GuestReady: svInt(m["guestReady"]), GuestFailed: svInt(m["guestFailed"]), GuestPlayed: svInt(m["guestPlayed"]),
		GuestIdleVisibleMs: svInt64(m["guestIdleVisibleMs"]), GuestReadyAvgMs: svInt64(m["guestReadyAvgMs"])}
}

func svVisit(m map[string]any) SiteGuestVisit {
	events := []SiteGuestEvent{}
	if arr, ok := svObjects(m["events"]); ok {
		for _, e := range arr {
			events = append(events, SiteGuestEvent{T: svInt(e["t"]), E: jStr(e["e"])})
		}
	}
	return SiteGuestVisit{Lesson: jStr(m["lesson"]), Device: jStr(m["device"]), Source: jStr(m["source"]), ReadyMs: svInt(m["readyMs"]),
		LoadError: jStr(m["loadError"]), VisibleMs: svInt(m["visibleMs"]), Events: events, OpenedAt: svDate(m["openedAt"])}
}

func svPath(m map[string]any) SitePathCount {
	steps := []SitePathStep{}
	if arr, ok := svObjects(m["steps"]); ok {
		for _, s := range arr {
			steps = append(steps, SitePathStep{Lesson: jStr(s["lesson"]), Played: jBool(s["played"])})
		}
	}
	return SitePathCount{Steps: steps, Count: svInt(m["count"]), Played: svInt(m["played"])}
}

func svStay(m map[string]any) SiteHomeStay {
	return SiteHomeStay{Guest: jStr(m["guest"]), Device: jStr(m["device"]), Source: jStr(m["source"]), Visits: svInt(m["visits"]),
		TotalMs: svInt64(m["totalMs"]), LongestMs: svInt(m["longestMs"]), MaxScroll: svInt(m["maxScroll"]), LastOpened: svDate(m["lastOpened"])}
}

func svLogin(v any) SiteLoginStats {
	m, _ := v.(map[string]any)
	return SiteLoginStats{Visits: svInt(m["visits"]), Outcomes: svCounts(m["outcomes"]), Next: svCounts(m["next"]), Fails: svCounts(m["fails"]),
		GoogleReady: svInt(m["googleReady"]), GoogleReadyAvgMs: svInt64(m["googleReadyAvgMs"]), GoogleFailed: svInt(m["googleFailed"]),
		GoogleErrors: svCounts(m["googleErrors"])}
}

// DecodeSiteOverview 寬鬆解碼 evoleap 的 AdminOverview。最外層不是物件時回傳錯誤(同 Swift 的 DecodingError)
func DecodeSiteOverview(obj any) (SiteOverview, error) {
	m, ok := obj.(map[string]any)
	if !ok {
		return SiteOverview{}, errors.New("The data couldn’t be read because it isn’t in the correct format.")
	}
	o := SiteOverview{Users: svInt(m["users"]), ActiveUsers: svInt(m["activeUsers"]), Guests: svInt(m["guests"]), Lessons: []SiteLessonStat{}}
	if arr, ok := svObjects(m["lessons"]); ok {
		for _, l := range arr {
			o.Lessons = append(o.Lessons, svLesson(l))
		}
	}
	g, _ := m["guestStats"].(map[string]any)
	gs := SiteGuestBreakdown{Devices: svCounts(g["devices"]), Sources: svCounts(g["sources"]), Errors: svCounts(g["errors"]),
		IdleVisible: svCounts(g["idleVisible"]), IdleVisits: []SiteGuestVisit{}, HomePaths: []SitePathCount{},
		HomeVisits: svInt(g["homeVisits"]), HomeStays: []SiteHomeStay{}, Login: svLogin(g["login"])}
	if arr, ok := svObjects(g["idleVisits"]); ok {
		for _, v := range arr {
			gs.IdleVisits = append(gs.IdleVisits, svVisit(v))
		}
	}
	if arr, ok := svObjects(g["homePaths"]); ok {
		for _, v := range arr {
			gs.HomePaths = append(gs.HomePaths, svPath(v))
		}
	}
	if arr, ok := svObjects(g["homeStays"]); ok {
		for _, v := range arr {
			gs.HomeStays = append(gs.HomeStays, svStay(v))
		}
	}
	o.GuestStats = gs
	return o, nil
}

// SiteRanges ?range= 的值("" 是全部)
var SiteRanges = []string{"", "30m", "12h", "1d", "7d"}

// SiteRangeID 畫面與 MCP 用的 id("" → all)
func SiteRangeID(r string) string {
	if r == "" {
		return "all"
	}
	return r
}

func SiteRangePath(r string) string {
	if r == "" {
		return "/api/admin/overview"
	}
	return "/api/admin/overview?range=" + r
}

// SitePercent 百分比字串;分母為 0 時顯示 —
func SitePercent(n, total int) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", float64(n)/float64(total)*100)
}

// SiteFmtMs 毫秒 → 「1.5 秒」「1 分 35 秒」「2 小時 3 分」
func SiteFmtMs(v int64) string {
	s := float64(v) / 1000
	if s < 60 {
		if s < 10 {
			return fmt.Sprintf("%.1f 秒", s)
		}
		return fmt.Sprintf("%.0f 秒", s)
	}
	m := int(s) / 60
	if m < 60 {
		return fmt.Sprintf("%d 分 %d 秒", m, int(s)%60)
	}
	return fmt.Sprintf("%d 小時 %d 分", m/60, m%60)
}

// SiteVisitorsConnection 網站訪客用哪個連線(預設 evoleap;拿著鎖)
func SiteVisitorsConnection(a *App) string { return a.conns().VisitorsConn }

// SiteVisitorsFetch 讀訪客統計;connection 是 nil 時用目前選的連線(不能拿著鎖)
func SiteVisitorsFetch(a *App, connection *string, rng string) (SiteOverview, error) {
	a.Mu.Lock()
	id := a.conns().VisitorsConn
	a.Mu.Unlock()
	if connection != nil {
		id = *connection
	}
	obj, err := ConnRequest(a, id, "GET", SiteRangePath(rng), nil)
	if err != nil {
		return SiteOverview{}, err
	}
	return DecodeSiteOverview(obj)
}

func init() {
	RegisterAction("site_visitors", func(a *App, args map[string]any) (any, error) {
		r, _ := args["range"].(string)
		if r == "all" {
			r = ""
		}
		valid := false
		for _, x := range SiteRanges {
			valid = valid || x == r
		}
		if !valid {
			return nil, UserError{"range 必須是 all、30m、12h、1d、7d 之一"}
		}
		var conn *string
		if c, ok := args["connection"].(string); ok && c != "" {
			conn = &c
			a.Mu.Lock()
			a.conns().VisitorsConn = c // 同 Swift:畫面選的連線記起來,MCP 也用這個
			a.Mu.Unlock()
		}
		d, err := SiteVisitorsFetch(a, conn, r)
		if err != nil {
			return nil, err
		}
		o := Object(d).(map[string]any)
		o["range"] = SiteRangeID(r)
		o["funnel"] = d.Funnel()
		o["gates"] = d.Gates()
		o["playerLessons"] = d.PlayerLessons()
		o["connection"] = SiteVisitorsConnectionLocked(a)
		return o, nil
	})
}

// SiteVisitorsConnectionLocked 同 SiteVisitorsConnection,自己拿鎖
func SiteVisitorsConnectionLocked(a *App) string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.conns().VisitorsConn
}
