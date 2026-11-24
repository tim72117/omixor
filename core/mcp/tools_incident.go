package mcp

import (
	"sort"
	"strings"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 事件工具:incidents(查)、incident_declare(宣告)、incident_update(更新)。規則在 service/incident.go(透過 Backend);
// incident_declare、incident_update 的名稱、參數沿用 Swift 版 MCPHandler 原本的定義(現在 App 轉給 core,不自己實作)。
// 執行者:管理端點是呼叫者(e.Author:伺服器管理員的 web 內建核心 / App 是畫面的人或 mcp,其他人是自己)。
// 不拿 Env 的鎖(同頻道工具):omixor-web 的 Backend 實作自己處理鎖與畫面快取。

// IncidentToolDefs 事件工具的規格(接在 CoreToolDefs 後面)
func IncidentToolDefs() []any {
	return []any{
		coreDef("incidents",
			"Queries incidents: by default lists active (unresolved) incidents, newest first. Each has id, title, severity (sev 1 is the most severe), status, affected services, commander, start time, impact, AI summary, follow-up work and related alerts; with id returns a single incident with its full timeline.", true,
			obj(map[string]any{
				"id":     pStr("Incident id, e.g. INC-2041 (when given, only this one is returned, with the full timeline)"),
				"status": pEnum("Status filter: open (default, unresolved), all, or a single status", "open", "all", "investigating", "identified", "monitoring", "resolved"),
				"sev":    pInt("List only this severity (1–4)"),
				"search": pStr("Title, impact or AI summary contains this text (case-insensitive)"),
				"limit":  pInt("Maximum number of results, default 20, at most 100"),
			})),
		coreDef("incident_declare",
			"Declares a new incident and returns the incident id. Related alerts can be linked.", false,
			obj(map[string]any{
				"title":     pStr("Title"),
				"sev":       pInt("Severity 1–4, 1 is the most severe"),
				"services":  pStrs("Ids of the affected services"),
				"impact":    pStr("Impact on users"),
				"commander": pStr("Commander's person id, default is the current primary on-call"),
				"alert_ids": pStrs("Ids of related alerts"),
			}, "title", "sev", "impact")),
		coreDef("incident_update",
			"Updates an incident's status, adds a note or an AI finding to the timeline, or writes the AI summary. Give at least one.", false,
			obj(map[string]any{
				"incident_id": pStr("Incident id"),
				"status":      pEnum("New status", model.IncidentStatuses...),
				"note":        pStr("Timeline note"),
				"finding":     pStr("AI finding to add to the timeline"),
				"ai_summary":  pStr("Replaces the incident's AI summary"),
				"impact":      pStr("Updates the impact description"),
			}, "incident_id")),
	}
}

// incidentErr 核心回的錯誤轉成工具錯誤(找不到事件的訊息同 Swift)
func incidentErr(id string, err error) error {
	if backend.NotFound(err) && id != "" {
		return Errorf("找不到事件 %s", id)
	}
	return terr(err)
}

// RegisterIncidentTools 登記事件工具。執行環境(Call.Ctx)要實作 Env。
// 規格裡沒有 incidents 的只登記 incident_declare、incident_update(相容自己組規格的使用程式)
func RegisterIncidentTools(s *Server) {
	if s.InSpec("incidents") {
		registerIncidentQuery(s)
	}
	registerIncidentWrites(s)
}

func registerIncidentQuery(s *Server) {
	s.Register("incidents", func(c *Call) (any, error) {
		e := env(c)
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Str("id"); id != "" {
			i, err := bk.GetIncident(org, id)
			if err != nil {
				return nil, incidentErr(id, err)
			}
			return i, nil
		}
		status, err := c.Args.OptionalEnum("status", append([]string{"open", "all"}, model.IncidentStatuses...))
		if err != nil {
			return nil, err
		}
		if status == "" {
			status = "open"
		}
		sev, hasSev := c.Args.Int("sev")
		q := strings.ToLower(c.Args.Str("search"))
		limit, ok := c.Args.Int("limit")
		if !ok || limit <= 0 {
			limit = 20
		}
		limit = min(limit, 100)
		list, err := bk.ListIncidents(org)
		if err != nil {
			return nil, terr(err)
		}
		hits := []model.Incident{}
		for _, i := range list {
			switch {
			case status == "open" && !i.IsOpen():
				continue
			case status != "open" && status != "all" && i.Status != status:
				continue
			case hasSev && i.Sev != sev:
				continue
			}
			if q != "" {
				sum := ""
				if i.AISummary != nil {
					sum = *i.AISummary
				}
				if !strings.Contains(strings.ToLower(i.ID+" "+i.Title+" "+i.Impact+" "+sum), q) {
					continue
				}
			}
			hits = append(hits, i)
		}
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].StartedAt.After(hits[b].StartedAt.Time) })
		total := len(hits)
		if len(hits) > limit {
			hits = hits[:limit]
		}
		out := []any{}
		for _, i := range hits { // 列表不含時間軸(用 id 查單一事件看完整時間軸)
			m := map[string]any{"id": i.ID, "title": i.Title, "sev": i.Sev, "status": i.Status, "services": i.Services,
				"commander": i.Commander, "startedAt": iso(i.StartedAt.Time), "impact": i.Impact, "followUps": i.FollowUps,
				"timelineCount": len(i.Timeline)}
			putActor(m, "commanderActor", i.CommanderActor)
			putActor(m, "createdByActor", i.CreatedByActor)
			if i.ResolvedAt != nil {
				m["resolvedAt"] = iso(i.ResolvedAt.Time)
			}
			if i.AISummary != nil {
				m["aiSummary"] = *i.AISummary
			}
			if len(i.AlertIDs) > 0 {
				m["alertIds"] = i.AlertIDs
			}
			out = append(out, m)
		}
		return map[string]any{"total": total, "incidents": out}, nil
	})
}

func registerIncidentWrites(s *Server) {

	s.Register("incident_declare", func(c *Call) (any, error) {
		e := env(c)
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		sev, err := c.Args.RequireInt("sev")
		if err != nil {
			return nil, err
		}
		if sev < model.MinIncidentSev || sev > model.MaxIncidentSev {
			return nil, Errorf("sev 必須是 1–4")
		}
		impact, err := c.Args.Require("impact")
		if err != nil {
			return nil, err
		}
		services, _ := c.Args.Strings("services")
		alerts, _ := c.Args.Strings("alert_ids")
		author, err := e.Author("")
		if err != nil {
			return nil, err
		}
		inc, err := e.Backend().DeclareIncident(e.Org(), service.IncidentInput{Title: title, Sev: sev, Services: services,
			Impact: impact, Commander: c.Args.Str("commander"), AlertIDs: alerts, Author: author})
		if err != nil {
			return nil, terr(err)
		}
		return inc, nil
	})

	s.Register("incident_update", func(c *Call) (any, error) {
		e := env(c)
		id, err := c.Args.Require("incident_id")
		if err != nil {
			return nil, err
		}
		status, err := c.Args.OptionalEnum("status", model.IncidentStatuses)
		if err != nil {
			return nil, err
		}
		p := service.IncidentPatch{Note: c.Args.Opt("note"), Finding: c.Args.Opt("finding"),
			AISummary: c.Args.Opt("ai_summary"), Impact: c.Args.Opt("impact")}
		if status != "" {
			p.Status = &status
		}
		if p.Empty() {
			return nil, Errorf("status、note、finding、ai_summary、impact 至少要給一項")
		}
		if p.Author, err = e.Author(""); err != nil {
			return nil, err
		}
		inc, err := e.Backend().UpdateIncident(e.Org(), id, p)
		if err != nil {
			return nil, incidentErr(id, err)
		}
		return inc, nil
	})
}
