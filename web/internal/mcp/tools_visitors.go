package mcp

import (
	"sort"

	"omixor-web/internal/core"
)

// ops_site_visitors,逐行對照 Swift MCPHandler.swift 的 siteVisitors。

func init() {
	Register("ops_site_visitors", func(c *Call) (any, error) {
		r := "1d"
		if s, ok := c.Args.String("range"); ok {
			r = s
		}
		if r == "all" {
			r = ""
		}
		valid := false
		for _, x := range core.SiteRanges {
			valid = valid || x == r
		}
		if !valid {
			return nil, ToolError{"range 必須是 all、30m、12h、1d、7d 之一"}
		}
		d, err := core.SiteVisitorsFetch(c.App, optArg(c, "connection"), r)
		if err != nil {
			return nil, ToolError{"讀不到訪客統計:" + err.Error() + "(請在 omixor 登入對應的連線)"}
		}
		f := d.Funnel()
		out, _ := core.Object(d).(map[string]any)
		out["range"] = core.SiteRangeID(r)
		out["funnel"] = map[string]any{"tracked": f.Tracked, "ready": f.Ready, "played": f.Played, "failed": f.Failed, "bots": f.Bots}
		gates := []any{}
		for _, g := range d.Gates() {
			gates = append(gates, map[string]any{"path": g.Name, "views": g.Count})
		}
		out["gates"] = gates
		// 只留有紀錄的課,依訪客打開次數排序
		lessons := []core.SiteLessonStat{}
		for _, l := range d.PlayerLessons() {
			if l.GuestTracked+l.Learners+l.GuestBots > 0 {
				lessons = append(lessons, l)
			}
		}
		sort.SliceStable(lessons, func(i, j int) bool { return lessons[i].GuestTracked > lessons[j].GuestTracked })
		out["lessons"] = core.Object(lessons)
		if gs, ok := out["guestStats"].(map[string]any); ok {
			gs["homePaths"] = firstN(gs["homePaths"], 10)
			gs["homeStays"] = firstN(gs["homeStays"], 20)
			gs["idleVisits"] = firstN(gs["idleVisits"], 15)
			gs["homeStaysTotal"] = len(d.GuestStats.HomeStays)
			gs["idleVisitsTotal"] = len(d.GuestStats.IdleVisits)
		}
		return out, nil
	})
}

func firstN(v any, n int) []any {
	arr, _ := v.([]any)
	if len(arr) > n {
		arr = arr[:n]
	}
	if arr == nil {
		arr = []any{}
	}
	return arr
}
