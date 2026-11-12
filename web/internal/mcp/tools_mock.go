package mcp

import (
	"strings"
	"time"

	"omixor-web/internal/core"
)

// 範例資料的工具,逐行對照 Swift MCPHandler.swift 的 overview、list、get、navigate、alertUpdate…activityPost。
// 每個工具都在鎖內算好結果並深拷貝後才回傳(回傳值在鎖外才轉成 JSON,live tick 可能同時在改資料)。

var collections = []string{
	"people", "services", "servers", "repos", "pull_requests", "work_items", "sprint",
	"pipeline_runs", "deployments", "alerts", "alert_rules", "logs", "dashboards",
	"incidents", "on_call", "automations", "automation_runs", "approvals", "activity", "integrations",
}

// locked 拿鎖執行,結果深拷貝;topics 是成功時要通知的主題
func locked(c *Call, f func(a *core.App) (any, error), topics ...string) (any, error) {
	a := c.App
	a.Mu.Lock()
	out, err := f(a)
	if out != nil {
		out = core.Clone(out)
	}
	a.Mu.Unlock()
	if len(topics) > 0 {
		a.Changed(topics...)
	}
	return out, err
}

func items(a *core.App, collection string) ([]any, error) {
	if collection == "sprint" {
		return []any{a.Sprint}, nil
	}
	for _, c := range collections {
		if c == collection {
			return core.List(a.C[collection]), nil
		}
	}
	return nil, ToolError{"沒有這個集合:" + collection + "。可用:" + strings.Join(collections, "、")}
}

func serviceID(a *core.App, id string) (string, error) {
	if _, s := a.Find("services", id); s != nil {
		return id, nil
	}
	ids := []string{}
	for _, s := range a.C["services"] {
		ids = append(ids, core.Str(s, "id"))
	}
	return "", ToolError{"找不到服務 " + id + "。可用:" + strings.Join(ids, "、")}
}

func picks(list []map[string]any, keys ...string) []any {
	out := []any{}
	for _, it := range list {
		out = append(out, core.Pick(it, keys...))
	}
	return out
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func init() {
	Register("ops_overview", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			now := time.Now()
			unhealthy, failed := 0, 0
			for _, s := range a.C["services"] {
				if core.Str(s, "health") != "healthy" {
					unhealthy++
				}
			}
			for _, r := range a.C["pipeline_runs"] {
				if t, err := core.ParseTime(core.Str(r, "startedAt")); err == nil && core.Str(r, "status") == "failed" && now.Sub(t).Seconds() < 86400 {
					failed++
				}
			}
			active := []map[string]any{}
			for _, d := range a.C["deployments"] {
				if s := core.Str(d, "status"); s == "running" || s == "waitingApproval" {
					active = append(active, d)
				}
			}
			onCall := []any{}
			for _, s := range a.C["on_call"] {
				start, e1 := core.ParseTime(core.Str(s, "start"))
				end, e2 := core.ParseTime(core.Str(s, "end"))
				if e1 != nil || e2 != nil || start.After(now) || !end.After(now) {
					continue
				}
				p := core.Str(s, "person")
				name := p
				if _, pp := a.Find("people", p); pp != nil {
					name = core.Str(pp, "name")
				}
				onCall = append(onCall, map[string]any{"role": s["role"], "person": p, "name": name, "until": core.ISO(end)})
			}
			recent := []any{}
			for i, it := range a.C["activity"] {
				if i >= 10 {
					break
				}
				recent = append(recent, map[string]any{"date": it["date"], "by": a.Name(core.ActorFrom(it["actor"])), "text": it["text"], "section": it["section"]})
			}
			firing, open, pending, running := a.FiringAlerts(), a.OpenIncidents(), a.PendingApprovals(), a.RunningPipelines()
			return map[string]any{
				"now": core.ISO(now),
				"ui":  map[string]any{"section": a.Nav.Section, "env": a.Nav.Env, "timeRange": a.Nav.TimeRange, "focusId": nullable(a.Nav.FocusID)},
				"counts": map[string]any{
					"services": len(a.C["services"]), "unhealthyServices": unhealthy, "firingAlerts": len(firing),
					"openIncidents": len(open), "pendingApprovals": len(pending), "runningPipelines": len(running),
					"failedPipelines24h": failed,
				},
				"services":          picks(a.C["services"], "id", "name", "health", "tier", "errorRate", "latencyP95", "rps", "errorBudgetRemaining"),
				"firingAlerts":      picks(firing, "id", "title", "severity", "service", "value", "startedAt", "incidentId"),
				"openIncidents":     picks(open, "id", "title", "sev", "status", "services", "commander", "startedAt"),
				"pendingApprovals":  picks(pending, "id", "title", "risk", "automation", "action", "expiresAt"),
				"runningPipelines":  picks(running, "id", "pipeline", "branch", "status", "startedAt"),
				"activeDeployments": picks(active, "id", "service", "version", "env", "status", "strategy", "progress"),
				"onCall":            onCall,
				"recentActivity":    recent,
			}, nil
		})
	})

	Register("ops_list", func(c *Call) (any, error) {
		collection, err := c.Args.Require("collection")
		if err != nil {
			return nil, err
		}
		return locked(c, func(a *core.App) (any, error) {
			rows, err := items(a, collection)
			if err != nil {
				return nil, err
			}
			if conds, ok := c.Args.Raw["where"].(map[string]any); ok {
				kept := []any{}
				for _, row := range rows {
					all := true
					for k, v := range conds {
						f, present := core.ValueAt(k, row)
						if !core.Matches(f, present, v) {
							all = false
							break
						}
					}
					if all {
						kept = append(kept, row)
					}
				}
				rows = kept
			}
			if q, ok := c.Args.String("search"); ok {
				if q = strings.ToLower(q); q != "" {
					kept := []any{}
					for _, row := range rows {
						if strings.Contains(strings.ToLower(string(core.Marshal(row))), q) {
							kept = append(kept, row)
						}
					}
					rows = kept
				}
			}
			total := len(rows)
			offset, _ := c.Args.Int("offset")
			offset = max(0, offset)
			limit, ok := c.Args.Int("limit")
			if !ok {
				limit = 30
			}
			limit = min(200, max(1, limit))
			page := []any{}
			for i := offset; i < len(rows) && len(page) < limit; i++ {
				page = append(page, core.Compact(rows[i]))
			}
			return map[string]any{"collection": collection, "total": total, "offset": offset, "count": len(page), "items": page}, nil
		})
	})

	Register("ops_get", func(c *Call) (any, error) {
		collection, err := c.Args.Require("collection")
		if err != nil {
			return nil, err
		}
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		return locked(c, func(a *core.App) (any, error) {
			rows, err := items(a, collection)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				v, _ := core.ValueAt("id", r)
				if s, ok := core.ScalarString(v); ok && strings.EqualFold(s, id) {
					return r, nil
				}
			}
			return nil, ToolError{collection + " 裡找不到 id「" + id + "」"}
		})
	})

	Register("ops_navigate", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			env, err := c.Args.OptionalEnum("env", core.DeployEnvs)
			if err != nil {
				return nil, err
			}
			if env != "" {
				a.Nav.Env = env
			}
			tr, err := c.Args.OptionalEnum("time_range", core.TimeRanges)
			if err != nil {
				return nil, err
			}
			if tr != "" {
				a.Nav.TimeRange = tr
			}
			section, err := c.Args.OptionalEnum("section", core.AppSections)
			if err != nil {
				return nil, err
			}
			if section != "" {
				a.Nav.Section = section
				a.Nav.FocusID = c.Args.Str("focus_id")
			} else if f, ok := c.Args.String("focus_id"); ok {
				a.Nav.FocusID = f
			}
			return map[string]any{"section": a.Nav.Section, "focusId": nullable(a.Nav.FocusID), "env": a.Nav.Env, "timeRange": a.Nav.TimeRange}, nil
		}, "nav")
	})

	Register("ops_alert_update", func(c *Call) (any, error) {
		id, err := c.Args.Require("alert_id")
		if err != nil {
			return nil, err
		}
		return locked(c, func(a *core.App) (any, error) {
			_, al := a.Find("alerts", id)
			if al == nil || core.Str(al, "id") != id {
				return nil, Errorf("找不到告警 %s", id)
			}
			action, hasAction := c.Args.String("action")
			note, hasNote := c.Args.String("ai_note")
			if !hasAction && !hasNote {
				return nil, ToolError{"action 與 ai_note 至少要給一項"}
			}
			if hasNote {
				al["aiNote"] = note
			}
			if hasAction {
				switch action {
				case "acknowledge":
					a.AcknowledgeAlert(id, c.Actor)
				case "resolve":
					a.ResolveAlert(id, c.Actor)
				case "silence":
					a.SilenceAlert(id, c.Actor)
				default:
					return nil, Errorf("action 必須是 acknowledge、resolve 或 silence,收到「%s」", action)
				}
			}
			return al, nil
		}, "alerts")
	})

	Register("ops_alert_create", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			sid, err := c.Args.Require("service")
			if err != nil {
				return nil, err
			}
			service, err := serviceID(a, sid)
			if err != nil {
				return nil, err
			}
			num := a.NextNumber("alerts", 5000)
			title, err := c.Args.Require("title")
			if err != nil {
				return nil, err
			}
			sev, err := c.Args.RequireEnum("severity", core.Severities)
			if err != nil {
				return nil, err
			}
			rule, err := c.Args.Require("rule")
			if err != nil {
				return nil, err
			}
			value, err := c.Args.Require("value")
			if err != nil {
				return nil, err
			}
			al := map[string]any{
				"id": "ALT-" + itoa(num), "title": title, "severity": sev, "service": service, "state": "firing",
				"rule": rule, "value": value, "startedAt": core.ISO(time.Now()),
			}
			if note, ok := c.Args.String("ai_note"); ok {
				al["aiNote"] = note
			}
			a.C["alerts"] = append([]map[string]any{al}, a.C["alerts"]...)
			a.Log(c.Actor, "bell.badge.fill", "新告警「"+title+"」("+core.SeverityLabel[sev]+")", "monitoring")
			return al, nil
		}, "alerts")
	})

	Register("ops_incident_declare", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			title, err := c.Args.Require("title")
			if err != nil {
				return nil, err
			}
			sev, err := c.Args.RequireInt("sev")
			if err != nil {
				return nil, err
			}
			if sev < 1 || sev > 4 {
				return nil, ToolError{"sev 必須是 1–4"}
			}
			services := []any{}
			if list, ok := c.Args.Strings("services"); ok {
				for _, s := range list {
					id, err := serviceID(a, s)
					if err != nil {
						return nil, err
					}
					services = append(services, id)
				}
			}
			commander, ok := c.Args.String("commander")
			if !ok {
				commander = a.Me
				if oc := a.CurrentOnCall(); oc != nil {
					commander = core.Str(oc, "person")
				}
			}
			_, person := a.Find("people", commander)
			if person == nil || core.Str(person, "id") != commander {
				return nil, Errorf("找不到 person %s", commander)
			}
			alertIDs, _ := c.Args.Strings("alert_ids")
			for _, id := range alertIDs {
				if _, al := a.Find("alerts", id); al == nil || core.Str(al, "id") != id {
					return nil, Errorf("找不到告警 %s", id)
				}
			}
			num := a.NextNumber("incidents", 2000)
			id := "INC-" + itoa(num)
			sevS := itoa(sev)
			timeline := []any{map[string]any{"id": core.UUID(), "date": core.ISO(time.Now()), "actor": core.Object(c.Actor),
				"kind": "status", "text": "宣告 SEV" + sevS + " 事件,指揮官:" + core.Str(person, "name")}}
			changedAlerts := false
			for _, aid := range alertIDs {
				if _, al := a.Find("alerts", aid); al != nil {
					al["incidentId"] = id
					changedAlerts = true
					timeline = append(timeline, map[string]any{"id": core.UUID(), "date": core.ISO(time.Now()), "actor": core.Object(c.Actor),
						"kind": "alert", "text": "關聯告警「" + core.Str(al, "title") + "」"})
				}
			}
			impact, err := c.Args.Require("impact")
			if err != nil {
				// Swift 版在這裡才檢查 impact,前面關聯告警的變更已經生效
				if changedAlerts {
					go a.Changed("alerts")
				}
				return nil, err
			}
			inc := map[string]any{
				"id": id, "title": title, "sev": sev, "status": "investigating", "services": services, "commander": commander,
				"startedAt": core.ISO(time.Now()), "impact": impact, "timeline": timeline, "followUps": []any{},
			}
			a.C["incidents"] = append([]map[string]any{inc}, a.C["incidents"]...)
			a.Log(c.Actor, "flame.fill", "宣告 SEV"+sevS+" 事件 "+id+"「"+title+"」", "incidents")
			return inc, nil
		}, "incidents", "alerts")
	})

	Register("ops_incident_update", func(c *Call) (any, error) {
		id, err := c.Args.Require("incident_id")
		if err != nil {
			return nil, err
		}
		return locked(c, func(a *core.App) (any, error) {
			_, inc := a.Find("incidents", id)
			if inc == nil || core.Str(inc, "id") != id {
				return nil, Errorf("找不到事件 %s", id)
			}
			status, err := c.Args.OptionalEnum("status", core.IncidentStatuses)
			if err != nil {
				return nil, err
			}
			note, hasNote := c.Args.String("note")
			finding, hasFinding := c.Args.String("finding")
			summary, hasSummary := c.Args.String("ai_summary")
			impact, hasImpact := c.Args.String("impact")
			if status == "" && !hasNote && !hasFinding && !hasSummary && !hasImpact {
				return nil, ToolError{"status、note、finding、ai_summary、impact 至少要給一項"}
			}
			if status != "" {
				var n *string
				if hasNote {
					n = &note
				}
				a.UpdateIncident(id, status, n, c.Actor)
			} else if hasNote {
				a.AddIncidentNote(id, note, c.Actor)
			}
			if hasFinding {
				a.AppendIncidentEvent(inc, c.Actor, "aiFinding", finding)
			}
			if hasSummary {
				inc["aiSummary"] = summary
			}
			if hasImpact {
				inc["impact"] = impact
			}
			return inc, nil
		}, "incidents")
	})

	Register("ops_work_item_create", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			title, err := c.Args.Require("title")
			if err != nil {
				return nil, err
			}
			typ, err := c.Args.RequireEnum("type", core.WorkTypes)
			if err != nil {
				return nil, err
			}
			status, err := c.Args.OptionalEnum("status", core.WorkStatuses)
			if err != nil {
				return nil, err
			}
			if status == "" {
				status = "todo"
			}
			var service *string
			if s, ok := c.Args.String("service"); ok {
				id, err := serviceID(a, s)
				if err != nil {
					return nil, err
				}
				service = &id
			}
			assignee, hasAssignee := c.Args.String("assignee")
			if hasAssignee {
				if _, p := a.Find("people", assignee); p == nil || core.Str(p, "id") != assignee {
					return nil, Errorf("找不到 person %s", assignee)
				}
			}
			var inc map[string]any
			if incID, ok := c.Args.String("incident_id"); ok {
				_, inc = a.Find("incidents", incID)
				if inc == nil || core.Str(inc, "id") != incID {
					return nil, Errorf("找不到事件 %s", incID)
				}
			}
			points, ok := c.Args.Int("points")
			if !ok {
				points = 3
			}
			id := "OPS-" + itoa(a.NextNumber("work_items", 100))
			item := map[string]any{"id": id, "title": title, "type": typ, "status": status, "points": points, "createdByAI": true}
			if hasAssignee {
				item["assignee"] = core.Object(core.Person(assignee))
			}
			if service != nil {
				item["service"] = *service
			}
			a.C["work_items"] = append(a.C["work_items"], item)
			if inc != nil {
				f, _ := inc["followUps"].([]any)
				inc["followUps"] = append(f, id)
			}
			a.Log(c.Actor, "checklist", "建立 "+id+"「"+title+"」", "development")
			return item, nil
		}, "work_items", "incidents")
	})

	Register("ops_work_item_move", func(c *Call) (any, error) {
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		return locked(c, func(a *core.App) (any, error) {
			_, it := a.Find("work_items", id)
			if it == nil || core.Str(it, "id") != id {
				return nil, Errorf("找不到工作項目 %s", id)
			}
			status, err := c.Args.RequireEnum("status", core.WorkStatuses)
			if err != nil {
				return nil, err
			}
			who := c.Actor
			a.MoveWorkItem(id, status, &who)
			return it, nil
		}, "work_items")
	})

	Register("ops_pipeline_update", func(c *Call) (any, error) {
		id, err := c.Args.RequireInt("run_id")
		if err != nil {
			return nil, err
		}
		return locked(c, func(a *core.App) (any, error) {
			var run map[string]any
			for _, r := range a.C["pipeline_runs"] {
				if v, ok := r["id"].(float64); ok && v == float64(id) {
					run = r
					break
				}
			}
			if run == nil {
				return nil, Errorf("找不到 pipeline run #%d", id)
			}
			action, hasAction := c.Args.String("action")
			diagnosis, hasDiag := c.Args.String("ai_diagnosis")
			if !hasAction && !hasDiag {
				return nil, ToolError{"action 與 ai_diagnosis 至少要給一項"}
			}
			if hasDiag {
				run["aiDiagnosis"] = diagnosis
			}
			if hasAction {
				switch action {
				case "retry":
					nr := a.RetryRun(id, c.Actor)
					return map[string]any{"retried": id, "newRun": core.Compact(nr)}, nil
				case "cancel":
					s := core.Str(run, "status")
					if s != "running" && s != "queued" && s != "waitingApproval" {
						return nil, Errorf("#%d 目前是「%s」,不能取消", id, core.RunStatusLabel[s])
					}
					a.CancelRun(id, c.Actor)
				default:
					return nil, Errorf("action 必須是 retry 或 cancel,收到「%s」", action)
				}
			}
			return core.Compact(run), nil
		}, "pipeline_runs")
	})

	Register("ops_request_approval", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			automation, ok := c.Args.String("automation")
			if !ok {
				automation = core.MCPActorID
			}
			if automation != core.MCPActorID {
				if _, au := a.Find("automations", automation); au == nil || core.Str(au, "id") != automation {
					return nil, Errorf("找不到自動化 %s", automation)
				}
			}
			minutes, ok := c.Args.Int("expires_in_minutes")
			if !ok {
				minutes = 60
			}
			minutes = max(1, minutes)
			num := a.NextNumber("approvals", 300)
			title, err := c.Args.Require("title")
			if err != nil {
				return nil, err
			}
			risk, err := c.Args.RequireEnum("risk", core.RiskLevels)
			if err != nil {
				return nil, err
			}
			action, err := c.Args.Require("action")
			if err != nil {
				return nil, err
			}
			rationale, err := c.Args.Require("rationale")
			if err != nil {
				return nil, err
			}
			evidence := []any{}
			if ev, ok := c.Args.Strings("evidence"); ok {
				for _, e := range ev {
					evidence = append(evidence, e)
				}
			}
			rollback, err := c.Args.Require("rollback_plan")
			if err != nil {
				return nil, err
			}
			now := time.Now()
			req := map[string]any{
				"id": "APV-" + itoa(num), "title": title, "automation": automation, "risk": risk, "action": action,
				"rationale": rationale, "evidence": evidence, "rollbackPlan": rollback, "createdAt": core.ISO(now),
				"expiresAt": core.ISO(now.Add(time.Duration(minutes) * time.Minute)), "state": "pending",
			}
			a.C["approvals"] = append([]map[string]any{req}, a.C["approvals"]...)
			a.Log(c.Actor, "hand.raised", "送出待核准請求「"+title+"」", "automation")
			return req, nil
		}, "approvals")
	})

	Register("ops_log_append", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			level, err := c.Args.RequireEnum("level", core.LogLevels)
			if err != nil {
				return nil, err
			}
			sid, err := c.Args.Require("service")
			if err != nil {
				return nil, err
			}
			service, err := serviceID(a, sid)
			if err != nil {
				return nil, err
			}
			host, ok := c.Args.String("host")
			if !ok {
				host = "mcp"
			}
			message, err := c.Args.Require("message")
			if err != nil {
				return nil, err
			}
			entry := map[string]any{"id": core.UUID(), "date": core.ISO(time.Now()), "level": level, "service": service, "host": host, "message": message}
			if t, ok := c.Args.String("trace_id"); ok {
				entry["traceId"] = t
			}
			a.C["logs"] = append([]map[string]any{entry}, a.C["logs"]...)
			return entry, nil
		}, "logs")
	})

	Register("ops_activity_post", func(c *Call) (any, error) {
		return locked(c, func(a *core.App) (any, error) {
			section, err := c.Args.OptionalEnum("section", core.AppSections)
			if err != nil {
				return nil, err
			}
			if section == "" {
				section = "overview"
			}
			icon, ok := c.Args.String("icon")
			if !ok {
				icon = "sparkles"
			}
			text, err := c.Args.Require("text")
			if err != nil {
				return nil, err
			}
			a.Log(c.Actor, icon, text, section)
			return a.C["activity"][0], nil
		})
	})
}

func itoa(n int) string { s, _ := core.ScalarString(n); return s }
