package mcp

import (
	"strings"

	"opsweb/internal/core"
)

// GCP 工具,逐行對照 Swift MCPHandler.swift 的 gcpStore、gcpSummary、gcpLogs、gcpMetrics。只讀。

func gcpReady(c *Call) error {
	if !core.GCPLoggedIn(c.App) {
		return ToolError{"omixor 還沒登入 Google Cloud。請人在 omixor 的 伺服器與服務 › GCP 設定並登入"}
	}
	c.App.Mu.Lock()
	ok := core.GCPConfigured(c.App)
	c.App.Mu.Unlock()
	if !ok {
		return ToolError{"還沒設定 GCP 專案,請人在 omixor 的 伺服器與服務 › GCP 設定"}
	}
	return nil
}

func gcpFail(err error) error { return ToolError{"GCP 回應錯誤:" + core.GCPExplain(err)} }

func optArg(c *Call, k string) *string {
	if s, ok := c.Args.String(k); ok {
		return &s
	}
	return nil
}

func init() {
	Register("ops_gcp", func(c *Call) (any, error) {
		if err := gcpReady(c); err != nil {
			return nil, err
		}
		a := c.App
		a.Mu.Lock()
		fetched := core.GCPSnapshotNow(a).FetchedAt != nil
		a.Mu.Unlock()
		if b, _ := c.Args.Bool("refresh"); b || !fetched {
			core.GCPRefresh(a)
		}
		a.Mu.Lock()
		snap := core.GCPSnapshotNow(a)
		settings := core.GCPSettingsNow(a)
		account := connAccount(a, settings.Connection)
		a.Mu.Unlock()
		if snap.Error != nil {
			return nil, ToolError{*snap.Error}
		}
		services := []any{}
		for _, s := range snap.Services {
			o, _ := core.Object(s).(map[string]any)
			o["state"] = s.StateLabel()
			services = append(services, o)
		}
		jobs := []any{}
		for _, j := range snap.Jobs {
			var latest any
			if j.Latest != nil {
				latest = core.GCPExecutionJSON(*j.Latest, false)
			}
			jobs = append(jobs, map[string]any{"name": j.ID, "latest": latest})
		}
		var acc, fetchedAt any
		if account != nil {
			acc = *account
		}
		if snap.FetchedAt != nil {
			fetchedAt = core.ISO(snap.FetchedAt.Time)
		}
		out := map[string]any{"project": settings.Project, "region": settings.Region, "account": acc, "fetchedAt": fetchedAt,
			"services": services, "jobs": jobs}
		if svc, ok := c.Args.String("service"); ok {
			revs, err := core.GCPRevisions(a, svc, 10)
			if err != nil {
				return nil, gcpFail(err)
			}
			out["revisions"] = core.Object(revs)
		}
		if job, ok := c.Args.String("job"); ok {
			ex, err := core.GCPExecutions(a, job, 10)
			if err != nil {
				return nil, gcpFail(err)
			}
			list := []any{}
			for _, e := range ex {
				list = append(list, core.GCPExecutionJSON(e, true))
			}
			out["executions"] = list
		}
		return out, nil
	})

	Register("ops_gcp_logs", func(c *Call) (any, error) {
		if err := gcpReady(c); err != nil {
			return nil, err
		}
		sev := "ERROR"
		if s, ok := c.Args.String("severity"); ok {
			sev = s
		}
		sev = strings.ToUpper(sev)
		valid := false
		for _, s := range core.GCPSeverities {
			valid = valid || s == sev
		}
		if !valid {
			return nil, ToolError{"severity 必須是 " + strings.Join(core.GCPSeverities, "、") + " 之一"}
		}
		hours := 24
		if n, ok := c.Args.Int("hours"); ok {
			hours = n
		}
		hours = min(720, max(1, hours))
		limit := 50
		if n, ok := c.Args.Int("limit"); ok {
			limit = n
		}
		logs, err := core.GCPLogs(c.App, optArg(c, "service"), optArg(c, "job"), sev, hours, optArg(c, "contains"), min(500, max(1, limit)))
		if err != nil {
			return nil, gcpFail(err)
		}
		return map[string]any{"count": len(logs), "hours": hours, "severity": sev, "entries": core.Object(logs)}, nil
	})

	Register("ops_gcp_metrics", func(c *Call) (any, error) {
		if err := gcpReady(c); err != nil {
			return nil, err
		}
		a := c.App
		service, ok := c.Args.String("service")
		if !ok {
			a.Mu.Lock()
			fetched := core.GCPSnapshotNow(a).FetchedAt != nil
			a.Mu.Unlock()
			if !fetched {
				core.GCPRefresh(a)
			}
			a.Mu.Lock()
			services := core.GCPSnapshotNow(a).Services
			a.Mu.Unlock()
			if len(services) != 1 {
				names := []string{}
				for _, s := range services {
					names = append(names, s.ID)
				}
				return nil, ToolError{"請給 service,可用:" + strings.Join(names, "、")}
			}
			service = services[0].ID
		}
		hours := 24
		if n, ok := c.Args.Int("hours"); ok {
			hours = n
		}
		hours = min(720, max(1, hours))
		m, err := core.GCPMetricsGet(a, service, hours)
		if err != nil {
			return nil, gcpFail(err)
		}
		return map[string]any{"service": service, "hours": hours, "totals": core.GCPMetricsTotals(m),
			"requests": core.Object(m.Requests), "latencyP95": core.Object(m.LatencyP95), "instances": core.Object(m.Instances)}, nil
	})
}
