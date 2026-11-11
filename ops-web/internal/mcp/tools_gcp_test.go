package mcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opsweb/internal/core"
)

// GCP:假的 run / logging / monitoring.googleapis.com(httptest),照 Swift 的 GCPTests.swift 一個一個搬

const gcpTestBase = "/v2/projects/shuttle-045094509/locations/asia-east1"

func fakeGoogle(r *http.Request, body []byte) (int, any) {
	if r.Header.Get("Authorization") != "Bearer ya29.test" {
		return 401, map[string]any{"error": map[string]any{"code": 401, "message": "Request had invalid authentication credentials."}}
	}
	svc := func(n, ready, latest, created string) map[string]any {
		return map[string]any{"name": "projects/shuttle-045094509/locations/asia-east1/services/" + n,
			"uri":                   "https://" + n + "-taqycykl5q-de.a.run.app",
			"latestReadyRevision":   "projects/p/locations/asia-east1/services/" + n + "/revisions/" + latest,
			"latestCreatedRevision": "projects/p/locations/asia-east1/services/" + n + "/revisions/" + created,
			"terminalCondition":     map[string]any{"type": "Ready", "state": ready},
			"updateTime":            "2026-11-10T17:26:30.123456789Z", "lastModifier": "deploy@x.iam.gserviceaccount.com",
			"template": map[string]any{"containers": []any{map[string]any{"image": "asia-east1-docker.pkg.dev/x/y/" + n + ":f8d723e"}},
				"scaling": map[string]any{"maxInstanceCount": 2}},
			"trafficStatuses": []any{map[string]any{"type": "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST", "percent": 100}}}
	}
	pt := func(end string, v any) map[string]any {
		value := map[string]any{"int64Value": v}
		if f, ok := v.(float64); ok {
			value = map[string]any{"doubleValue": f}
		}
		return map[string]any{"interval": map[string]any{"startTime": end, "endTime": end}, "value": value}
	}
	switch r.Host + r.URL.Path {
	case "www.googleapis.com/oauth2/v3/userinfo":
		return 200, map[string]any{"email": "tim@example.com", "sub": "1"}
	case "run.googleapis.com" + gcpTestBase + "/services":
		return 200, map[string]any{"services": []any{svc("shuttle-server", "CONDITION_SUCCEEDED", "shuttle-server-00043-xnq", "shuttle-server-00043-xnq"),
			svc("tripace-server", "CONDITION_FAILED", "t-1", "t-2")}}
	case "run.googleapis.com" + gcpTestBase + "/services/shuttle-server/revisions":
		return 200, map[string]any{"revisions": []any{
			map[string]any{"name": "x/revisions/shuttle-server-00042-abc", "createTime": "2026-11-01T00:00:00Z",
				"containers": []any{map[string]any{"image": "img:old"}}, "conditions": []any{map[string]any{"type": "Ready", "state": "CONDITION_SUCCEEDED"}}},
			map[string]any{"name": "x/revisions/shuttle-server-00043-xnq", "createTime": "2026-11-10T17:20:00.5Z",
				"containers": []any{map[string]any{"image": "img:f8d723e"}}, "conditions": []any{map[string]any{"type": "Ready", "state": "CONDITION_SUCCEEDED"}}},
		}}
	case "run.googleapis.com" + gcpTestBase + "/jobs":
		return 200, map[string]any{"jobs": []any{
			map[string]any{"name": "x/jobs/shuttle-course-sync",
				"latestCreatedExecution": map[string]any{"name": "x/executions/shuttle-course-sync-9fm2q", "createTime": "2026-11-10T17:24:00Z",
					"completionTime": "2026-11-10T17:26:21.074088Z", "completionStatus": "EXECUTION_FAILED"}},
			map[string]any{"name": "x/jobs/tripace-migrate"},
		}}
	case "run.googleapis.com" + gcpTestBase + "/jobs/shuttle-course-sync/executions":
		return 200, map[string]any{"executions": []any{
			map[string]any{"name": "x/executions/shuttle-course-sync-9fm2q", "createTime": "2026-11-10T17:24:00Z", "startTime": "2026-11-10T17:24:10Z",
				"completionTime": "2026-11-10T17:26:21.074088Z", "taskCount": 1, "failedCount": 1,
				"conditions": []any{map[string]any{"type": "Completed", "state": "CONDITION_FAILED", "message": "Task failed with exit code 1"}}},
		}}
	case "logging.googleapis.com/v2/entries:list":
		return 200, map[string]any{"entries": []any{
			map[string]any{"insertId": "a", "timestamp": "2026-11-10T18:00:00.123456Z", "severity": "ERROR", "textPayload": "panic: db timeout",
				"resource": map[string]any{"labels": map[string]any{"service_name": "shuttle-server", "revision_name": "shuttle-server-00043-xnq"}}},
			map[string]any{"insertId": "b", "timestamp": "2026-11-10T17:59:00Z", "severity": "ERROR", "jsonPayload": map[string]any{"message": "lesson not found"},
				"resource": map[string]any{"labels": map[string]any{"service_name": "shuttle-server"}}},
			map[string]any{"insertId": "c", "timestamp": "2026-11-10T17:58:00Z", "severity": "ERROR",
				"httpRequest": map[string]any{"requestMethod": "GET", "requestUrl": "https://evoleap.io/api/x", "status": 503, "latency": "1.2s"},
				"resource":    map[string]any{"labels": map[string]any{"service_name": "shuttle-server"}}},
		}}
	case "monitoring.googleapis.com/v3/projects/shuttle-045094509/timeSeries":
		q := r.URL.RawQuery
		if strings.Contains(q, "request_count") {
			return 200, map[string]any{"timeSeries": []any{
				map[string]any{"metric": map[string]any{"labels": map[string]any{"response_code_class": "2xx"}},
					"points": []any{pt("2026-11-10T18:00:00Z", "50"), pt("2026-11-10T17:00:00Z", "100")}},
				map[string]any{"metric": map[string]any{"labels": map[string]any{"response_code_class": "5xx"}}, "points": []any{pt("2026-11-10T18:00:00Z", "3")}},
			}}
		}
		if strings.Contains(q, "request_latencies") {
			return 200, map[string]any{"timeSeries": []any{map[string]any{"metric": map[string]any{}, "points": []any{pt("2026-11-10T18:00:00Z", 812.5)}}}}
		}
		return 200, map[string]any{"timeSeries": []any{map[string]any{"metric": map[string]any{}, "points": []any{pt("2026-11-10T18:00:00Z", "2")}}}}
	}
	return 404, map[string]any{"error": map[string]any{"code": 404, "message": "not found " + r.Host + r.URL.Path}}
}

func gcpLogin(t *testing.T, a *core.App, services []string) {
	core.GCPEnsureConnection(a)
	core.GCPUpdate(a, func(s *core.GCPSettings) {
		s.Project, s.Region, s.Services, s.Jobs = " shuttle-045094509 ", "asia-east1", services, []string{"shuttle-course-sync", "  "}
	})
	core.ConnSetBearer(a, core.GCPConnectionID, "ya29.test")
	core.ConnRefreshAccount(a, core.GCPConnectionID)
}

func TestGCPGoogleTimestampsWithNanoseconds(t *testing.T) {
	for _, s := range []string{"2026-11-10T17:26:21.074088Z", "2026-11-10T17:26:30.123456789Z", "2026-11-10T17:20:00.5Z", "2026-11-10T17:20:00Z", "2026-11-10T17:20:00+08:00"} {
		if core.GCPDate(s) == nil {
			t.Errorf("解不出 %s", s)
		}
	}
	d := core.GCPDate("2026-11-10T17:26:21.074088Z").Sub(core.GCPDate("2026-11-10T17:26:21Z").Time)
	if d != 74*time.Millisecond {
		t.Fatalf("取到毫秒:%v", d)
	}
	if core.GCPDate("") != nil || core.GCPDate(3) != nil {
		t.Fatal("空字串、不是字串要回 nil")
	}
}

func TestGCPLogFilterString(t *testing.T) {
	since := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	svc, job, has := "shuttle-server", "sync", `db "x"`
	if got := core.GCPLogFilter(&svc, nil, "ERROR", since, &has); got != `resource.type="cloud_run_revision" AND resource.labels.service_name="shuttle-server" AND severity>=ERROR AND timestamp>="2026-11-10T00:00:00Z" AND "db \"x\""` {
		t.Fatal(got)
	}
	if got := core.GCPLogFilter(nil, &job, "DEFAULT", since, nil); got != `resource.type="cloud_run_job" AND resource.labels.job_name="sync" AND timestamp>="2026-11-10T00:00:00Z"` {
		t.Fatal(got)
	}
	if got := core.GCPLogFilter(nil, nil, "WARNING", since, nil); got != `(resource.type="cloud_run_revision" OR resource.type="cloud_run_job") AND severity>=WARNING AND timestamp>="2026-11-10T00:00:00Z"` {
		t.Fatal(got)
	}
	if core.GCPQuote(`a\b`) != `"a\\b"` {
		t.Fatal(core.GCPQuote(`a\b`))
	}
}

func TestGCPRefreshFiltersServicesAndJobs(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGoogle)
	defer f.Close()
	gcpLogin(t, a, []string{"shuttle-server"})
	core.GCPRefresh(a)
	a.Mu.Lock()
	snap := core.GCPSnapshotNow(a)
	a.Mu.Unlock()
	if snap.Error != nil || len(snap.Services) != 1 || snap.Services[0].ID != "shuttle-server" {
		t.Fatalf("%+v", snap)
	}
	s := snap.Services[0]
	if !s.Ready || s.StateLabel() != "正常" || s.LatestReady != "shuttle-server-00043-xnq" || s.MaxInstances != 2 || s.Traffic[0].Percent != 100 ||
		s.Traffic[0].Revision != "最新版本" || s.UpdatedAt == nil {
		t.Fatalf("%+v", s)
	}
	if len(snap.Jobs) != 1 || snap.Jobs[0].ID != "shuttle-course-sync" || snap.Jobs[0].Latest.StatusLabel() != "失敗" {
		t.Fatalf("%+v", snap.Jobs)
	}

	// 全部服務:壞掉的那個要標出來
	core.GCPUpdate(a, func(s *core.GCPSettings) { s.Services = nil })
	core.GCPRefresh(a)
	a.Mu.Lock()
	snap = core.GCPSnapshotNow(a)
	a.Mu.Unlock()
	labels := []string{}
	for _, s := range snap.Services {
		labels = append(labels, s.StateLabel())
	}
	if strings.Join(labels, ",") != "正常,異常" {
		t.Fatal(labels)
	}

	revs, err := core.GCPRevisions(a, "shuttle-server", 10)
	if err != nil || revs[0].ID != "shuttle-server-00043-xnq" || core.GCPImageTag(revs[0].Image) != "f8d723e" {
		t.Fatalf("%+v %v", revs, err)
	}
	ex, err := core.GCPExecutions(a, "shuttle-course-sync", 10)
	if err != nil || ex[0].Status() != "failed" || ex[0].Message != "Task failed with exit code 1" {
		t.Fatalf("%+v %v", ex, err)
	}
	if d, _ := ex[0].Duration(); d < 130 || d > 132 {
		t.Fatal(d)
	}
	// 設定存在組織的 gcp.json
	b, _ := os.ReadFile(filepath.Join(core.DataRoot(), "orgs", core.DefaultOrgID, "gcp.json"))
	var saved core.GCPSettings
	if json.Unmarshal(b, &saved) != nil || saved.Project != "shuttle-045094509" || strings.Join(saved.Jobs, ",") != "shuttle-course-sync" {
		t.Fatalf("%s", b)
	}
}

func TestGCPLogsFilterAndParsing(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGoogle)
	defer f.Close()
	gcpLogin(t, a, []string{"shuttle-server"})
	svc, has := "shuttle-server", `db "x"`
	logs, err := core.GCPLogs(a, &svc, nil, "ERROR", 24, &has, 50)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{}
	for _, l := range logs {
		texts = append(texts, l.Text)
	}
	if strings.Join(texts, "|") != "panic: db timeout|lesson not found|GET https://evoleap.io/api/x → 503" {
		t.Fatal(texts)
	}
	if *logs[2].HTTPStatus != 503 || logs[0].Revision != "shuttle-server-00043-xnq" || logs[0].HTTPStatus != nil {
		t.Fatalf("%+v", logs)
	}
	sent, _ := f.Find("entries:list")
	if sent.Method != "POST" {
		t.Fatal(sent.Method)
	}
	var b map[string]any
	_ = json.Unmarshal(sent.Body, &b)
	filter := b["filter"].(string)
	for _, want := range []string{`resource.labels.service_name="shuttle-server"`, "severity>=ERROR", `"db \"x\""`} {
		if !strings.Contains(filter, want) {
			t.Fatalf("篩選字串缺 %s:%s", want, filter)
		}
	}
	if rn := b["resourceNames"].([]any); len(rn) != 1 || rn[0] != "projects/shuttle-045094509" || b["pageSize"] != 50.0 || b["orderBy"] != "timestamp desc" {
		t.Fatalf("%v", b)
	}
	// jsonPayload 沒有 message:整個 JSON(key 排序、斜線跳脫,同 Swift 的 JSONSerialization)
	e := core.GCPParseLogEntry(map[string]any{"jsonPayload": map[string]any{"z": 1, "a": "x/y"}})
	if e.Text != `{"a":"x\/y","z":1}` || e.Severity != "DEFAULT" || len(e.ID) != 36 {
		t.Fatalf("%+v", e)
	}
}

func TestGCPMetricsQueryAndTotals(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGoogle)
	defer f.Close()
	gcpLogin(t, a, []string{"shuttle-server"})
	m, err := core.GCPMetricsGet(a, "shuttle-server", 24)
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalRequests() != 153 || m.Total("5xx") != 3 || m.MaxP95() != 812.5 || m.Instances[0].Value != 2 {
		t.Fatalf("%+v", m)
	}
	if !m.Requests[0].Time.Before(m.Requests[1].Time.Time) {
		t.Fatal("點要依時間排序")
	}
	r, _ := f.Find("request_count")
	u, _ := url.Parse(r.URL)
	q := u.Query()
	if q.Get("filter") != `metric.type="run.googleapis.com/request_count" AND resource.labels.service_name="shuttle-server"` ||
		q.Get("aggregation.groupByFields") != "metric.labels.response_code_class" || q.Get("aggregation.alignmentPeriod") != "3600s" ||
		q.Get("aggregation.crossSeriesReducer") != "REDUCE_SUM" || q.Get("aggregation.perSeriesAligner") != "ALIGN_DELTA" {
		t.Fatalf("%v", q)
	}
	// 查詢編碼:英數字與 -._~ 以外都編碼(空白是 %20、引號 %22、冒號 %3A)
	if !strings.Contains(u.RawQuery, "filter=metric.type%3D%22run.googleapis.com%2Frequest_count%22%20AND%20resource.labels.service_name%3D%22shuttle-server%22") ||
		!strings.Contains(u.RawQuery, "interval.startTime=") || strings.Contains(u.RawQuery, "+") {
		t.Fatal(u.RawQuery)
	}
	if _, err := time.Parse(time.RFC3339, q.Get("interval.endTime")); err != nil {
		t.Fatal(q.Get("interval.endTime"))
	}
	if r, _ := f.Find("instance_count"); !strings.Contains(r.URL, "aggregation.perSeriesAligner=ALIGN_MAX") || strings.Contains(r.URL, "groupByFields") {
		t.Fatal(r.URL)
	}
	// 期間:6 小時內 5 分鐘、48 小時內 1 小時、更久 6 小時
	for hours, want := range map[int]string{6: "300s", 48: "3600s", 49: "21600s"} {
		if _, err := core.GCPMetricsGet(a, "shuttle-server", hours); err != nil {
			t.Fatal(err)
		}
		r, _ := f.Find("request_count")
		u, _ := url.Parse(r.URL)
		if u.Query().Get("aggregation.alignmentPeriod") != want {
			t.Fatalf("%d 小時:%s", hours, u.Query().Get("aggregation.alignmentPeriod"))
		}
	}
}

func TestGCPMCPTools(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGoogle)
	defer f.Close()
	n := 0
	for _, name := range ToolNames() {
		if strings.HasPrefix(name, "ops_gcp") {
			n++
		}
	}
	if n != 3 {
		t.Fatal(n)
	}
	want := "Ops 還沒登入 Google Cloud。請人在 Ops 的 伺服器與服務 › GCP 設定並登入"
	for _, name := range []string{"ops_gcp", "ops_gcp_logs", "ops_gcp_metrics"} {
		if msg := extErr(t, a, name, map[string]any{"severity": "LOUD"}); msg != want {
			t.Fatalf("%s:%s", name, msg)
		}
	}
	// 登入了但沒設定專案
	core.GCPEnsureConnection(a)
	core.ConnSetBearer(a, core.GCPConnectionID, "ya29.test")
	core.GCPUpdate(a, func(s *core.GCPSettings) { s.Region = "" })
	if msg := extErr(t, a, "ops_gcp", nil); msg != "還沒設定 GCP 專案,請人在 Ops 的 伺服器與服務 › GCP 設定" {
		t.Fatal(msg)
	}

	gcpLogin(t, a, []string{"shuttle-server"})
	sum := extOK(t, a, "ops_gcp", map[string]any{"refresh": true, "service": "shuttle-server", "job": "shuttle-course-sync"})
	if sum["account"] != "tim@example.com" || sum["project"] != "shuttle-045094509" || sum["region"] != "asia-east1" {
		t.Fatalf("%v", sum)
	}
	if s := sum["services"].([]any)[0].(map[string]any); s["state"] != "正常" || s["updatedAt"] != "2026-11-10T17:26:30Z" {
		t.Fatalf("%v", s)
	}
	if len(sum["revisions"].([]any)) != 2 {
		t.Fatal(sum["revisions"])
	}
	ex := sum["executions"].([]any)[0].(map[string]any)
	if ex["status"] != "失敗" || ex["durationSeconds"] != 131.0 {
		t.Fatalf("%v", ex)
	}
	if j := sum["jobs"].([]any)[0].(map[string]any); j["name"] != "shuttle-course-sync" || j["latest"].(map[string]any)["status"] != "失敗" {
		t.Fatalf("%v", j)
	}

	logs := extOK(t, a, "ops_gcp_logs", map[string]any{"service": "shuttle-server", "severity": "warning", "hours": 5000})
	if logs["count"] != 3.0 || logs["severity"] != "WARNING" || logs["hours"] != 720.0 {
		t.Fatalf("%v", logs)
	}
	if msg := extErr(t, a, "ops_gcp_logs", map[string]any{"severity": "LOUD"}); msg != "severity 必須是 DEFAULT、INFO、WARNING、ERROR、CRITICAL 之一" {
		t.Fatal(msg)
	}

	// 只有一個服務時可以省略 service
	m := extOK(t, a, "ops_gcp_metrics", nil)
	totals := m["totals"].(map[string]any)
	if totals["5xx"] != 3.0 || totals["requests"] != 153.0 || totals["2xx"] != 150.0 || totals["errorRate5xx"] != 0.0196 ||
		totals["maxP95ms"] != 813.0 || totals["maxInstances"] != 2.0 || m["service"] != "shuttle-server" || m["hours"] != 24.0 {
		t.Fatalf("%v", m)
	}
	if p := m["latencyP95"].([]any)[0].(map[string]any); p["series"] != "p95" || p["value"] != 812.5 {
		t.Fatalf("%v", p)
	}
	// 兩個服務時要給 service
	core.GCPUpdate(a, func(s *core.GCPSettings) { s.Services = nil })
	if msg := extErr(t, a, "ops_gcp_metrics", nil); msg != "請給 service,可用:shuttle-server、tripace-server" {
		t.Fatal(msg)
	}
	// Google 的錯誤講清楚
	core.ConnSetBearer(a, core.GCPConnectionID, "expired")
	if msg := extErr(t, a, "ops_gcp_logs", nil); msg != "GCP 回應錯誤:Google 登入已失效,請重新登入" {
		t.Fatal(msg)
	}
	if msg := extErr(t, a, "ops_gcp", map[string]any{"refresh": true}); msg != "Google 登入已失效,請重新登入" {
		t.Fatal(msg)
	}
	// 只讀:除了 entries:list(讀日誌也是 POST)以外都是 GET
	for _, r := range f.Requests() {
		if r.Method != "GET" && !strings.HasSuffix(r.URL, "/v2/entries:list") {
			t.Fatalf("%s %s", r.Method, r.URL)
		}
	}
}

func TestGCPExplain(t *testing.T) {
	for status, want := range map[int]string{
		403: "沒有權限:登入的帳號要有這個專案的檢視權限(roles/viewer),且專案要啟用 Cloud Run / Logging / Monitoring API。(m)",
		404: "找不到:確認專案 id 和區域。(m)",
		500: "HTTP 500:m",
	} {
		if got := core.GCPExplain(core.ConnError{Status: status, Message: "m"}); got != want {
			t.Errorf("%d:%s", status, got)
		}
	}
}

func TestGCPOrgsKeepSeparateProjects(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGoogle)
	defer f.Close()
	gcpLogin(t, a, []string{"shuttle-server"})
	settings := func() core.GCPSettings {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return core.GCPSettingsNow(a)
	}
	a.Mu.Lock()
	blank := a.CreateOrg("Side", "", "", "blank")
	a.SwitchOrg(blank.ID)
	a.Mu.Unlock()
	if s := settings(); s.Project != "" || s.Region != "asia-east1" {
		t.Fatalf("%+v", s)
	}
	a.Mu.Lock()
	a.SwitchOrg(core.DefaultOrgID)
	a.Mu.Unlock()
	if settings().Project != "shuttle-045094509" {
		t.Fatal(settings())
	}
	a.Mu.Lock()
	cp := a.CreateOrg("Copy", "", "", "copyCurrent")
	a.SwitchOrg(cp.ID)
	a.Mu.Unlock()
	if strings.Join(settings().Services, ",") != "shuttle-server" {
		t.Fatal(settings())
	}
}
