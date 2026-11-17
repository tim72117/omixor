package mcp

import (
	"strings"
	"time"

	"omixor-core/service"
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
	return nil, ToolError{Message: "沒有這個集合:" + collection + "。可用:" + strings.Join(collections, "、")}
}

func serviceID(a *core.App, id string) (string, error) {
	if _, s := a.Find("services", id); s != nil {
		return id, nil
	}
	ids := []string{}
	for _, s := range a.C["services"] {
		ids = append(ids, core.Str(s, "id"))
	}
	return "", ToolError{Message: "找不到服務 " + id + "。可用:" + strings.Join(ids, "、")}
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
	Register("overview", func(c *Call) (any, error) {
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

	Register("list", func(c *Call) (any, error) {
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

	Register("get", func(c *Call) (any, error) {
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
			return nil, ToolError{Message: collection + " 裡找不到 id「" + id + "」"}
		})
	})

	Register("navigate", func(c *Call) (any, error) {
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
			notice := ""
			if section != "" {
				focus := c.Args.Str("focus_id")
				// 系統設定關掉的頁面:改去總覽,回傳 notice 說明(同 App 的 MCPHandler.navigate)
				if to, n := a.GuardSection(section); n != "" {
					section, focus, notice = to, "", n
				}
				a.Nav.Section = section
				a.Nav.FocusID = focus
			} else if f, ok := c.Args.String("focus_id"); ok {
				a.Nav.FocusID = f
			}
			out := map[string]any{"section": a.Nav.Section, "focusId": nullable(a.Nav.FocusID), "env": a.Nav.Env, "timeRange": a.Nav.TimeRange}
			if notice != "" {
				out["notice"] = notice
			}
			return out, nil
		}, "nav", "system")
	})

	Register("alert_update", func(c *Call) (any, error) {
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
				return nil, ToolError{Message: "action 與 ai_note 至少要給一項"}
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

	Register("alert_create", func(c *Call) (any, error) {
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

	// incident_declare、incident_update 由核心提供(omixor-core/mcp 的 tools_incident.go;web 的部分見 tools_incident.go)

	Register("work_item_create", func(c *Call) (any, error) {
		// 事件的後續工作:事件存在核心,建好工作項目後(鎖外)再請核心把它加進 followUps
		var incID, org string
		out, err := locked(c, func(a *core.App) (any, error) {
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
			if id, ok := c.Args.String("incident_id"); ok {
				_, inc := a.Find("incidents", id)
				if inc == nil || core.Str(inc, "id") != id {
					return nil, Errorf("找不到事件 %s", id)
				}
				incID, org = id, a.CurrentOrgID()
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
			a.Log(c.Actor, "checklist", "建立 "+id+"「"+title+"」", "development")
			return item, nil
		}, "work_items")
		if err != nil || incID == "" {
			return out, err
		}
		item, _ := out.(map[string]any)
		if _, err := c.App.UpdateIncidentCore(org, incID, service.IncidentPatch{AddFollowUps: []string{core.Str(item, "id")},
			Actor: coreActorOf(c.Actor), Author: core.ActorAuthor(c.Actor)}); err != nil {
			return nil, Errorf("工作項目 %s 建好了,但沒有加進事件 %s 的後續工作:%s", core.Str(item, "id"), incID, err.Error())
		}
		return out, nil
	})

	Register("work_item_move", func(c *Call) (any, error) {
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

	Register("pipeline_update", func(c *Call) (any, error) {
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
				return nil, ToolError{Message: "action 與 ai_diagnosis 至少要給一項"}
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

	Register("request_approval", func(c *Call) (any, error) {
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

	Register("log_append", func(c *Call) (any, error) {
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

	Register("activity_post", func(c *Call) (any, error) {
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
