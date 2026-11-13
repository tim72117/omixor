package mcp

import (
	"slices"
	"sort"
	"strings"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 事件工具:incidents(查)、incident_declare(宣告)、incident_update(更新)。規則在 service/incident.go(透過 Backend);
// incident_declare、incident_update 的名稱、參數同 Swift 版 MCPHandler(web/spec/tools.json)。
// 執行者:管理端點是呼叫者(e.Author:伺服器管理員的 web 內建核心 / App 是畫面的人或 mcp,其他人是自己)。
// 不拿 Env 的鎖(同頻道工具):omixor-web 的 Backend 實作自己處理鎖與畫面快取。

// IncidentToolDefs 事件工具的規格(接在 CoreToolDefs 後面)
func IncidentToolDefs() []any {
	return []any{
		coreDef("incidents",
			"查事件:預設列出進行中(未解決)的事件,新的在前。每筆有 id、標題、嚴重度(sev 1 最嚴重)、狀態、受影響服務、指揮官、開始時間、影響、AI 摘要、後續工作與關聯告警;給 id 回傳單一事件與完整時間軸。", true,
			obj(map[string]any{
				"id":     pStr("事件 id,例如 INC-2041(給了就只回這一筆,含完整時間軸)"),
				"status": pEnum("狀態篩選:open(預設,未解決)、all 或單一狀態", "open", "all", "investigating", "identified", "monitoring", "resolved"),
				"sev":    pInt("只列這個嚴重度(1–4)"),
				"search": pStr("標題、影響、AI 摘要包含這段文字(不分大小寫)"),
				"limit":  pInt("最多幾筆,預設 20、最多 100"),
			})),
		coreDef("incident_declare",
			"宣告新事件,回傳事件 id。可關聯告警。", false,
			obj(map[string]any{
				"title":     pStr("標題"),
				"sev":       pInt("嚴重度 1–4,1 最嚴重"),
				"services":  pStrs("受影響的服務 id"),
				"impact":    pStr("對使用者的影響"),
				"commander": pStr("指揮官 person id,預設為目前的 Primary 值班"),
				"alert_ids": pStrs("關聯的告警 id"),
			}, "title", "sev", "impact")),
		coreDef("incident_update",
			"更新事件狀態、在時間軸加註記或 AI 發現、或寫入 AI 摘要。至少給一項。", false,
			obj(map[string]any{
				"incident_id": pStr("事件 id"),
				"status":      pEnum("新狀態", model.IncidentStatuses...),
				"note":        pStr("時間軸註記"),
				"finding":     pStr("加到時間軸的 AI 發現"),
				"ai_summary":  pStr("取代事件的 AI 摘要"),
				"impact":      pStr("更新影響說明"),
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
// 規格裡沒有 incidents 的(omixor-web 的 /mcp 用 Swift 匯出的規格)只登記 incident_declare、incident_update
func RegisterIncidentTools(s *Server) {
	if slices.Contains(s.ToolNames(), "incidents") {
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
