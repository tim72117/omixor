package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GCP:用「Google Cloud」連線(Google 網頁 OAuth,cloud-platform.read-only)讀 Cloud Run 服務、版本、Job 執行、
// Cloud Logging 的錯誤與 Cloud Monitoring 的請求量與延遲。同 Swift 的 GCP/GCP.swift。
// 只讀:Ops 不會部署、改流量、執行 Job。專案、區域、追蹤哪些服務與 Job 存在目前組織的 gcp.json。

const (
	GCPConnectionID  = "gcp"
	GCPReadOnlyScope = "https://www.googleapis.com/auth/cloud-platform.read-only"
)

var GCPSeverities = []string{"DEFAULT", "INFO", "WARNING", "ERROR", "CRITICAL"}

// GCPSettings gcp.json
type GCPSettings struct {
	Connection string   `json:"connection"`
	Jobs       []string `json:"jobs"`
	Project    string   `json:"project"`
	Region     string   `json:"region"`
	Services   []string `json:"services"`
}

func DefaultGCPSettings() GCPSettings {
	return GCPSettings{Region: "asia-east1", Services: []string{}, Jobs: []string{}, Connection: GCPConnectionID}
}

// UnmarshalJSON 寬鬆解碼(欄位壞掉就用預設值)
func (s *GCPSettings) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = DefaultGCPSettings()
	str := func(k string, dst *string) {
		var v string
		if r, ok := raw[k]; ok && json.Unmarshal(r, &v) == nil {
			*dst = v
		}
	}
	list := func(k string, dst *[]string) {
		var v []string
		if r, ok := raw[k]; ok && json.Unmarshal(r, &v) == nil && v != nil {
			*dst = v
		}
	}
	str("project", &s.Project)
	str("region", &s.Region)
	str("connection", &s.Connection)
	list("services", &s.Services)
	list("jobs", &s.Jobs)
	return nil
}

func (s GCPSettings) equal(o GCPSettings) bool {
	return s.Project == o.Project && s.Region == o.Region && s.Connection == o.Connection &&
		strings.Join(s.Services, "\x00") == strings.Join(o.Services, "\x00") && len(s.Services) == len(o.Services) &&
		strings.Join(s.Jobs, "\x00") == strings.Join(o.Jobs, "\x00") && len(s.Jobs) == len(o.Jobs)
}

type GCPTraffic struct {
	Percent  int    `json:"percent"`
	Revision string `json:"revision"`
	Tag      string `json:"tag"`
}

type GCPService struct {
	ID            string       `json:"id"`
	Image         string       `json:"image"`
	LastModifier  string       `json:"lastModifier"`
	LatestCreated string       `json:"latestCreated"`
	LatestReady   string       `json:"latestReady"`
	MaxInstances  int          `json:"maxInstances"`
	Message       string       `json:"message"`
	Ready         bool         `json:"ready"`
	State         string       `json:"state"`
	Traffic       []GCPTraffic `json:"traffic"`
	UpdatedAt     *Time        `json:"updatedAt,omitempty"`
	URL           string       `json:"url"`
}

// RolloutPending 最新建立的版本還沒好(部署中或失敗)
func (s GCPService) RolloutPending() bool {
	return s.LatestCreated != "" && s.LatestCreated != s.LatestReady
}

func (s GCPService) StateLabel() string {
	if s.State == "CONDITION_RECONCILING" || s.State == "CONDITION_PENDING" {
		return "部署中"
	}
	if !s.Ready {
		return "異常"
	}
	if s.RolloutPending() {
		return "新版本未就緒"
	}
	return "正常"
}

type GCPRevision struct {
	CreatedAt *Time  `json:"createdAt,omitempty"`
	ID        string `json:"id"`
	Image     string `json:"image"`
	Message   string `json:"message"`
	Ready     bool   `json:"ready"`
}

type GCPExecution struct {
	Cancelled   int    `json:"cancelled"`
	CompletedAt *Time  `json:"completedAt,omitempty"`
	CreatedAt   *Time  `json:"createdAt,omitempty"`
	Failed      int    `json:"failed"`
	ID          string `json:"id"`
	Message     string `json:"message"`
	Running     int    `json:"running"`
	StartedAt   *Time  `json:"startedAt,omitempty"`
	Succeeded   int    `json:"succeeded"`
	Tasks       int    `json:"tasks"`
}

func (e GCPExecution) Status() string {
	if e.CompletedAt == nil {
		if e.Running > 0 || e.StartedAt != nil {
			return "running"
		}
		return "pending"
	}
	if e.Cancelled > 0 && e.Failed == 0 && e.Succeeded < e.Tasks {
		return "cancelled"
	}
	if e.Failed > 0 || e.Succeeded < e.Tasks {
		return "failed"
	}
	return "succeeded"
}

func (e GCPExecution) StatusLabel() string {
	switch e.Status() {
	case "running":
		return "執行中"
	case "pending":
		return "等待中"
	case "cancelled":
		return "已取消"
	case "failed":
		return "失敗"
	}
	return "成功"
}

func (e GCPExecution) Duration() (float64, bool) {
	s := e.StartedAt
	if s == nil {
		s = e.CreatedAt
	}
	if s == nil || e.CompletedAt == nil {
		return 0, false
	}
	return e.CompletedAt.Sub(s.Time).Seconds(), true
}

type GCPJob struct {
	Executions []GCPExecution `json:"executions"`
	ID         string         `json:"id"`
	Latest     *GCPExecution  `json:"latest,omitempty"`
}

type GCPLogEntry struct {
	HTTPStatus *int   `json:"httpStatus,omitempty"`
	ID         string `json:"id"`
	Latency    string `json:"latency"`
	Method     string `json:"method"`
	RequestURL string `json:"requestUrl"`
	Resource   string `json:"resource"`
	Revision   string `json:"revision"`
	Severity   string `json:"severity"`
	Text       string `json:"text"`
	Timestamp  *Time  `json:"timestamp,omitempty"`
}

type GCPPoint struct {
	Series string  `json:"series"`
	Time   Time    `json:"time"`
	Value  float64 `json:"value"`
}

type GCPMetrics struct {
	Hours      int        `json:"hours"`
	Instances  []GCPPoint `json:"instances"`
	LatencyP95 []GCPPoint `json:"latencyP95"`
	Requests   []GCPPoint `json:"requests"`
}

func (m GCPMetrics) Total(cls string) int {
	sum := 0.0
	for _, p := range m.Requests {
		if p.Series == cls {
			sum += p.Value
		}
	}
	return int(sum)
}

func (m GCPMetrics) TotalRequests() int {
	sum := 0.0
	for _, p := range m.Requests {
		sum += p.Value
	}
	return int(sum)
}

func (m GCPMetrics) ErrorRate() float64 {
	if t := m.TotalRequests(); t > 0 {
		return float64(m.Total("5xx")) / float64(t)
	}
	return 0
}

func maxValue(ps []GCPPoint) float64 {
	if len(ps) == 0 {
		return 0
	}
	mx := ps[0].Value
	for _, p := range ps[1:] {
		if p.Value > mx {
			mx = p.Value
		}
	}
	return mx
}

func (m GCPMetrics) MaxP95() float64 { return maxValue(m.LatencyP95) }

type GCPSnapshot struct {
	Busy      bool         `json:"busy"`
	Error     *string      `json:"error"`
	FetchedAt *Time        `json:"fetchedAt"`
	Jobs      []GCPJob     `json:"jobs"`
	Services  []GCPService `json:"services"`
}

func emptyGCPSnapshot() GCPSnapshot { return GCPSnapshot{Jobs: []GCPJob{}, Services: []GCPService{}} }

// GCPPresetConnection 預設的 Google Cloud 連線
func GCPPresetConnection() Connection {
	c := NewConnection(GCPConnectionID, "Google Cloud", "https://www.googleapis.com")
	c.IsProduction = true
	c.TestPath = "/oauth2/v3/userinfo"
	c.Note = "伺服器與服務 › GCP 讀 Cloud Run、日誌與監控(只讀)"
	c.OAuth = OAuthSettings{Issuer: "https://accounts.google.com", AuthorizationEndpoint: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenEndpoint: "https://oauth2.googleapis.com/token", Scopes: "openid email " + GCPReadOnlyScope,
		ExtraParams: "access_type=offline&prompt=consent"}
	return c
}

// GCPCreateClientURL Google Cloud Console 建 OAuth 用戶端(桌面應用程式)的頁面
func GCPCreateClientURL(project string) string {
	u := "https://console.cloud.google.com/auth/clients/create"
	if project != "" {
		u += "?project=" + queryEscape(project)
	}
	return u
}

// GCPConsoleURL Cloud Run 主控台
func GCPConsoleURL(project, region, service, job string) string {
	if service != "" {
		return fmt.Sprintf("https://console.cloud.google.com/run/detail/%s/%s/metrics?project=%s", region, service, project)
	}
	if job != "" {
		return fmt.Sprintf("https://console.cloud.google.com/run/jobs/details/%s/%s/executions?project=%s", region, job, project)
	}
	return "https://console.cloud.google.com/run?project=" + project
}

func gcpLastPart(s string) string {
	parts := []string{}
	for _, p := range strings.Split(s, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return s
	}
	return parts[len(parts)-1]
}

// GCPImageTag 映像只顯示 tag 或 digest 的前幾碼(同 Swift 的 GCPImage.tag)
func GCPImageTag(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		return prefixRunes(image[i+1:], 19)
	}
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		return prefixRunes(image[i+1:], 12)
	}
	return gcpLastPart(image)
}

func prefixRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// MARK: 解析

// GCPDate Google 的時間常有 9 位小數;和 Swift 一樣只取到毫秒
func GCPDate(v any) *Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	t, err := ParseTime(s)
	if err != nil {
		return nil
	}
	return timePtr(t.Truncate(time.Millisecond))
}

func GCPParseService(d map[string]any) GCPService {
	cond, tmpl := jMap(d["terminalCondition"]), jMap(d["template"])
	containers := jMaps(tmpl["containers"])
	image := ""
	if len(containers) > 0 {
		image = jStr(containers[0]["image"])
	}
	traffic := []GCPTraffic{}
	for _, t := range jMaps(d["trafficStatuses"]) {
		rev := gcpLastPart(jStr(t["revision"]))
		if rev == "" {
			rev = "最新版本"
		}
		traffic = append(traffic, GCPTraffic{Revision: rev, Percent: jIntOrString(t["percent"]), Tag: jStr(t["tag"])})
	}
	return GCPService{ID: gcpLastPart(jStr(d["name"])), URL: jStr(d["uri"]), Ready: jStr(cond["state"]) == "CONDITION_SUCCEEDED",
		State: jStr(cond["state"]), Message: jStr(cond["message"]), LatestReady: gcpLastPart(jStr(d["latestReadyRevision"])),
		LatestCreated: gcpLastPart(jStr(d["latestCreatedRevision"])), Image: image,
		MaxInstances: jIntOrString(jMap(tmpl["scaling"])["maxInstanceCount"]), UpdatedAt: GCPDate(d["updateTime"]),
		LastModifier: jStr(d["lastModifier"]), Traffic: traffic}
}

func GCPParseRevision(d map[string]any) GCPRevision {
	var ready map[string]any
	for _, c := range jMaps(d["conditions"]) {
		if jStr(c["type"]) == "Ready" {
			ready = c
			break
		}
	}
	image := ""
	if cs := jMaps(d["containers"]); len(cs) > 0 {
		image = jStr(cs[0]["image"])
	}
	return GCPRevision{ID: gcpLastPart(jStr(d["name"])), CreatedAt: GCPDate(d["createTime"]), Image: image,
		Ready: ready != nil && jStr(ready["state"]) == "CONDITION_SUCCEEDED", Message: jStr(ready["message"])}
}

func GCPParseExecution(d map[string]any) GCPExecution {
	var failed map[string]any
	for _, c := range jMaps(d["conditions"]) {
		if jStr(c["state"]) == "CONDITION_FAILED" {
			failed = c
			break
		}
	}
	return GCPExecution{ID: gcpLastPart(jStr(d["name"])), CreatedAt: GCPDate(d["createTime"]), StartedAt: GCPDate(d["startTime"]),
		CompletedAt: GCPDate(d["completionTime"]), Succeeded: jIntOrString(d["succeededCount"]), Failed: jIntOrString(d["failedCount"]),
		Running: jIntOrString(d["runningCount"]), Cancelled: jIntOrString(d["cancelledCount"]), Tasks: max(1, jIntOrString(d["taskCount"])),
		Message: jStr(failed["message"])}
}

func GCPParseLogEntry(d map[string]any) GCPLogEntry {
	labels := jMap(jMap(d["resource"])["labels"])
	http, hasHTTP := d["httpRequest"].(map[string]any)
	text := jStr(d["textPayload"])
	if j, ok := d["jsonPayload"].(map[string]any); ok && text == "" {
		text = jStr(j["message"])
		if text == "" {
			text = jStr(j["msg"])
		}
		if text == "" {
			// 同 Swift 的 JSONSerialization(.sortedKeys):斜線跳脫成 \/
			text = strings.ReplaceAll(string(Marshal(j)), "/", `\/`)
		}
	}
	if text == "" && hasHTTP {
		text = fmt.Sprintf("%s %s → %d", jStr(http["requestMethod"]), jStr(http["requestUrl"]), jIntOrString(http["status"]))
	}
	e := GCPLogEntry{ID: jStr(d["insertId"]), Timestamp: GCPDate(d["timestamp"]), Severity: jStr(d["severity"]), Text: text,
		Resource: jStr(labels["service_name"]), Revision: jStr(labels["revision_name"]),
		Method: jStr(http["requestMethod"]), RequestURL: jStr(http["requestUrl"]), Latency: jStr(http["latency"])}
	if e.ID == "" {
		e.ID = UUID()
	}
	if e.Severity == "" {
		e.Severity = "DEFAULT"
	}
	if e.Resource == "" {
		e.Resource = jStr(labels["job_name"])
	}
	if hasHTTP {
		st := jIntOrString(http["status"])
		e.HTTPStatus = &st
	}
	return e
}

// GCPPoints Cloud Monitoring 的 timeSeries → 點;series 取 groupBy 的標籤值(沒有就用 fallback)
func GCPPoints(obj any, label, fallback string) []GCPPoint {
	out := []GCPPoint{}
	for _, ts := range jMaps(jMap(obj)["timeSeries"]) {
		labels := jMap(jMap(ts["metric"])["labels"])
		name := fallback
		if label != "" {
			if s := jStr(labels[label]); s != "" {
				name = s
			}
		}
		for _, p := range jMaps(ts["points"]) {
			t := GCPDate(jMap(p["interval"])["endTime"])
			v, ok := p["value"].(map[string]any)
			if t == nil || !ok {
				continue
			}
			value, isNum := jNum(v["doubleValue"])
			if !isNum {
				value = float64(jIntOrString(v["int64Value"]))
			}
			out = append(out, GCPPoint{Time: *t, Series: name, Value: value})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time.Time) })
	return out
}

// GCPQuote Logging 的篩選字串:跳脫雙引號
func GCPQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// GCPLogFilter 同 Swift 的 GCP.logFilter
func GCPLogFilter(service, job *string, severity string, since time.Time, contains *string) string {
	f := []string{}
	switch {
	case service != nil:
		f = append(f, `resource.type="cloud_run_revision" AND resource.labels.service_name=`+GCPQuote(*service))
	case job != nil:
		f = append(f, `resource.type="cloud_run_job" AND resource.labels.job_name=`+GCPQuote(*job))
	default:
		f = append(f, `(resource.type="cloud_run_revision" OR resource.type="cloud_run_job")`)
	}
	if severity != "" && severity != "DEFAULT" {
		f = append(f, "severity>="+severity)
	}
	f = append(f, "timestamp>="+GCPQuote(ISO(since)))
	if contains != nil && *contains != "" {
		f = append(f, GCPQuote(*contains))
	}
	return strings.Join(f, " AND ")
}

// GCPExplain 常見的錯誤講清楚一點
func GCPExplain(err error) string {
	e, ok := AsConnError(err)
	if !ok {
		return err.Error()
	}
	switch e.Status {
	case 401:
		return "Google 登入已失效,請重新登入"
	case 403:
		return "沒有權限:登入的帳號要有這個專案的檢視權限(roles/viewer),且專案要啟用 Cloud Run / Logging / Monitoring API。(" + e.Message + ")"
	case 404:
		return "找不到:確認專案 id 和區域。(" + e.Message + ")"
	}
	return e.Error()
}

// MARK: - 狀態(拿著鎖)

func (a *App) gcpFile() string { return filepath.Join(a.DataDir(), "gcp.json") }

func gcpLoad(a *App) {
	s := a.conns()
	s.GCP = DefaultGCPSettings()
	if b, err := ReadData(a.gcpFile()); err == nil {
		var g GCPSettings
		if json.Unmarshal(b, &g) == nil {
			s.GCP = g
		}
	}
	s.GCPSnap = emptyGCPSnapshot()
}

func gcpSave(a *App) { _ = WriteJSON(a.gcpFile(), a.conns().GCP) }

// GCPSettingsNow 目前的設定(拿著鎖)
func GCPSettingsNow(a *App) GCPSettings {
	g := a.conns().GCP
	g.Services = append([]string{}, g.Services...)
	g.Jobs = append([]string{}, g.Jobs...)
	return g
}

func GCPSnapshotNow(a *App) GCPSnapshot { return a.conns().GCPSnap }

func GCPConnection(a *App) (Connection, bool) { return ConnFind(a, a.conns().GCP.Connection) }

func gcpLoggedIn(a *App) bool {
	a.Mu.Lock()
	c, ok := GCPConnection(a)
	a.Mu.Unlock()
	return ok && ConnHasToken(a, c)
}

// GCPLoggedIn 同 Swift 的 GCPStore.isLoggedIn(不能拿著鎖)
func GCPLoggedIn(a *App) bool { return gcpLoggedIn(a) }

func gcpConfiguredLocked(a *App) bool {
	s := a.conns().GCP
	return s.Project != "" && s.Region != ""
}

func gcpConfigured(a *App) bool {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return gcpConfiguredLocked(a)
}

// GCPConfigured 有專案與區域(拿著鎖)
func GCPConfigured(a *App) bool { return gcpConfiguredLocked(a) }

// GCPUpdate 改設定;有變才存檔並清掉快照(不能拿著鎖)
func GCPUpdate(a *App, change func(*GCPSettings)) {
	a.Mu.Lock()
	s := a.conns()
	n := GCPSettingsNow(a)
	change(&n)
	n.Project = strings.TrimSpace(n.Project)
	n.Region = strings.TrimSpace(n.Region)
	clean := func(in []string) []string {
		out := []string{}
		for _, x := range in {
			if x = strings.Trim(x, " \t"); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	n.Services, n.Jobs = clean(n.Services), clean(n.Jobs)
	if n.equal(s.GCP) {
		a.Mu.Unlock()
		return
	}
	s.GCP = n
	s.GCPSnap = emptyGCPSnapshot()
	gcpSave(a)
	a.Mu.Unlock()
	a.Changed("gcp")
}

func GCPEnsureConnection(a *App) Connection {
	a.Mu.Lock()
	if c, ok := GCPConnection(a); ok {
		a.Mu.Unlock()
		return c
	}
	a.Mu.Unlock()
	c := GCPPresetConnection()
	ConnUpsert(a, c)
	a.Mu.Lock()
	s := a.conns()
	if s.GCP.Connection != c.ID {
		s.GCP.Connection = c.ID
		gcpSave(a)
	}
	a.Mu.Unlock()
	a.Changed("gcp")
	return c
}

// MARK: 請求(不能拿著鎖)

func gcpCall(a *App, method, u string, body any) (any, error) {
	a.Mu.Lock()
	_, ok := GCPConnection(a)
	id := a.conns().GCP.Connection
	a.Mu.Unlock()
	if !ok {
		return nil, connErr("還沒設定 Google Cloud 連線(伺服器與服務 › GCP)")
	}
	var data []byte
	if body != nil {
		data = Marshal(body)
	}
	return ConnRequest(a, id, method, u, data)
}

// gcpNeed 檢查設定並回傳 (專案, Cloud Run 的網址開頭)
func gcpNeed(a *App) (GCPSettings, string, error) {
	a.Mu.Lock()
	s := GCPSettingsNow(a)
	ok := gcpConfiguredLocked(a)
	a.Mu.Unlock()
	if !ok {
		return s, "", connErr("還沒設定 GCP 專案與區域")
	}
	return s, "https://run.googleapis.com/v2/projects/" + EscapePath(s.Project) + "/locations/" + EscapePath(s.Region), nil
}

// GCPRefresh 讀服務與 Job 狀態
func GCPRefresh(a *App) {
	a.Mu.Lock()
	s := a.conns()
	if s.GCPSnap.Busy {
		a.Mu.Unlock()
		return
	}
	s.GCPSnap.Busy = true
	s.GCPSnap.Error = nil
	a.Mu.Unlock()
	a.Changed("gcp")
	defer func() {
		a.Mu.Lock()
		a.conns().GCPSnap.Busy = false
		a.Mu.Unlock()
		a.Changed("gcp")
	}()
	setErr := func(msg string) {
		a.Mu.Lock()
		a.conns().GCPSnap.Error = &msg
		a.Mu.Unlock()
	}
	if !gcpLoggedIn(a) {
		setErr("還沒登入 Google Cloud")
		return
	}
	services, err := GCPListServices(a)
	if err != nil {
		setErr(GCPExplain(err))
		return
	}
	jobs, err := GCPListJobs(a)
	if err != nil {
		setErr(GCPExplain(err))
		return
	}
	a.Mu.Lock()
	a.conns().GCPSnap = GCPSnapshot{Services: services, Jobs: jobs, FetchedAt: timePtr(time.Now()), Busy: true}
	a.Mu.Unlock()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func GCPListServices(a *App) ([]GCPService, error) {
	s, base, err := gcpNeed(a)
	if err != nil {
		return nil, err
	}
	obj, err := gcpCall(a, "GET", base+"/services?pageSize=100", nil)
	if err != nil {
		return nil, err
	}
	out := []GCPService{}
	for _, d := range jMaps(jMap(obj)["services"]) {
		svc := GCPParseService(d)
		if len(s.Services) == 0 || contains(s.Services, svc.ID) {
			out = append(out, svc)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func GCPListJobs(a *App) ([]GCPJob, error) {
	s, base, err := gcpNeed(a)
	if err != nil {
		return nil, err
	}
	obj, err := gcpCall(a, "GET", base+"/jobs?pageSize=100", nil)
	if err != nil {
		return nil, err
	}
	jobs := []GCPJob{}
	for _, d := range jMaps(jMap(obj)["jobs"]) {
		name := gcpLastPart(jStr(d["name"]))
		if len(s.Jobs) > 0 && !contains(s.Jobs, name) {
			continue
		}
		j := GCPJob{ID: name, Executions: []GCPExecution{}}
		if e, ok := d["latestCreatedExecution"].(map[string]any); ok {
			x := GCPParseExecution(e)
			// 摘要裡只有 completionStatus
			switch jStr(e["completionStatus"]) {
			case "EXECUTION_FAILED":
				x.Failed = max(1, x.Failed)
			case "EXECUTION_SUCCEEDED":
				x.Succeeded = max(x.Tasks, x.Succeeded)
			case "EXECUTION_CANCELLED":
				x.Cancelled = max(1, x.Cancelled)
			}
			j.Latest = &x
		}
		jobs = append(jobs, j)
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs, nil
}

func GCPRevisions(a *App, service string, limit int) ([]GCPRevision, error) {
	_, base, err := gcpNeed(a)
	if err != nil {
		return nil, err
	}
	obj, err := gcpCall(a, "GET", fmt.Sprintf("%s/services/%s/revisions?pageSize=%d", base, EscapePath(service), limit), nil)
	if err != nil {
		return nil, err
	}
	out := []GCPRevision{}
	for _, d := range jMaps(jMap(obj)["revisions"]) {
		out = append(out, GCPParseRevision(d))
	}
	sort.SliceStable(out, func(i, j int) bool { return timeOrPast(out[i].CreatedAt).After(timeOrPast(out[j].CreatedAt)) })
	return out, nil
}

func GCPExecutions(a *App, job string, limit int) ([]GCPExecution, error) {
	_, base, err := gcpNeed(a)
	if err != nil {
		return nil, err
	}
	obj, err := gcpCall(a, "GET", fmt.Sprintf("%s/jobs/%s/executions?pageSize=%d", base, EscapePath(job), limit), nil)
	if err != nil {
		return nil, err
	}
	out := []GCPExecution{}
	for _, d := range jMaps(jMap(obj)["executions"]) {
		out = append(out, GCPParseExecution(d))
	}
	sort.SliceStable(out, func(i, j int) bool { return timeOrPast(out[i].CreatedAt).After(timeOrPast(out[j].CreatedAt)) })
	return out, nil
}

// GCPLogs Cloud Logging(entries:list 是 POST,但只讀)
func GCPLogs(a *App, service, job *string, severity string, hours int, contains *string, limit int) ([]GCPLogEntry, error) {
	s, _, err := gcpNeed(a)
	if err != nil {
		return nil, err
	}
	filter := GCPLogFilter(service, job, severity, time.Now().Add(-time.Duration(hours)*time.Hour), contains)
	obj, err := gcpCall(a, "POST", "https://logging.googleapis.com/v2/entries:list", map[string]any{
		"resourceNames": []string{"projects/" + s.Project}, "filter": filter, "orderBy": "timestamp desc",
		"pageSize": min(1000, max(1, limit)),
	})
	if err != nil {
		return nil, err
	}
	out := []GCPLogEntry{}
	for _, d := range jMaps(jMap(obj)["entries"]) {
		out = append(out, GCPParseLogEntry(d))
	}
	return out, nil
}

// gcpSeriesURL Cloud Monitoring 的 timeSeries 查詢網址
func gcpSeriesURL(project, metric, service string, hours int, aligner, reducer, groupBy string, end time.Time) string {
	start := end.Add(-time.Duration(hours) * time.Hour)
	period := 6 * 3600
	if hours <= 6 {
		period = 300
	} else if hours <= 48 {
		period = 3600
	}
	type kv struct{ k, v string }
	q := []kv{
		{"filter", `metric.type="` + metric + `" AND resource.labels.service_name=` + GCPQuote(service)},
		{"interval.startTime", ISO(start)},
		{"interval.endTime", ISO(end)},
		{"aggregation.alignmentPeriod", fmt.Sprintf("%ds", period)},
		{"aggregation.perSeriesAligner", aligner},
	}
	if reducer != "" {
		q = append(q, kv{"aggregation.crossSeriesReducer", reducer})
	}
	if groupBy != "" {
		q = append(q, kv{"aggregation.groupByFields", groupBy})
	}
	parts := []string{}
	for _, x := range q {
		parts = append(parts, x.k+"="+percentEncode(x.v, "-._~"))
	}
	return "https://monitoring.googleapis.com/v3/projects/" + EscapePath(project) + "/timeSeries?" + strings.Join(parts, "&")
}

func GCPMetricsGet(a *App, service string, hours int) (GCPMetrics, error) {
	s, _, err := gcpNeed(a)
	if err != nil {
		return GCPMetrics{}, err
	}
	m := GCPMetrics{Hours: hours}
	series := func(metric, aligner, reducer, groupBy string) (any, error) {
		return gcpCall(a, "GET", gcpSeriesURL(s.Project, metric, service, hours, aligner, reducer, groupBy, time.Now()), nil)
	}
	obj, err := series("run.googleapis.com/request_count", "ALIGN_DELTA", "REDUCE_SUM", "metric.labels.response_code_class")
	if err != nil {
		return m, err
	}
	m.Requests = GCPPoints(obj, "response_code_class", "其他")
	if obj, err = series("run.googleapis.com/request_latencies", "ALIGN_DELTA", "REDUCE_PERCENTILE_95", ""); err != nil {
		return m, err
	}
	m.LatencyP95 = GCPPoints(obj, "", "p95")
	if obj, err = series("run.googleapis.com/container/instance_count", "ALIGN_MAX", "REDUCE_SUM", ""); err != nil {
		return m, err
	}
	m.Instances = GCPPoints(obj, "", "執行個體")
	return m, nil
}

// GCPMetricsTotals 同 Swift MCP 的 totals
func GCPMetricsTotals(m GCPMetrics) map[string]any {
	return map[string]any{"requests": m.TotalRequests(), "2xx": m.Total("2xx"), "4xx": m.Total("4xx"), "5xx": m.Total("5xx"),
		"errorRate5xx": math.Round(m.ErrorRate()*10000) / 10000, "maxP95ms": math.Round(m.MaxP95()),
		"maxInstances": int(maxValue(m.Instances))}
}

// GCPExecutionJSON 執行 + 狀態文字(+ 時間長度)
func GCPExecutionJSON(e GCPExecution, withDuration bool) map[string]any {
	o, _ := Object(e).(map[string]any)
	o["status"] = e.StatusLabel()
	if withDuration {
		if d, ok := e.Duration(); ok {
			o["durationSeconds"] = int(d)
		}
	}
	return o
}

// MARK: - 計時器、畫面

func init() {
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(300 * time.Second) {
				a.Mu.Lock()
				busy := a.conns().GCPSnap.Busy
				a.Mu.Unlock()
				if !busy && gcpLoggedIn(a) && gcpConfigured(a) {
					GCPRefresh(a)
				}
			}
		}()
	})

	RegisterView("gcp", func(a *App) any {
		s := a.conns()
		snap := Object(s.GCPSnap).(map[string]any)
		services := []any{}
		for _, svc := range s.GCPSnap.Services {
			o := Object(svc).(map[string]any)
			o["stateLabel"] = svc.StateLabel()
			o["rolloutPending"] = svc.RolloutPending()
			o["imageTag"] = GCPImageTag(svc.Image)
			o["consoleURL"] = GCPConsoleURL(s.GCP.Project, s.GCP.Region, svc.ID, "")
			services = append(services, o)
		}
		snap["services"] = services
		jobs := []any{}
		for _, j := range s.GCPSnap.Jobs {
			o := Object(j).(map[string]any)
			if j.Latest != nil {
				l := GCPExecutionJSON(*j.Latest, true)
				l["statusCode"] = j.Latest.Status()
				o["latest"] = l
			}
			o["consoleURL"] = GCPConsoleURL(s.GCP.Project, s.GCP.Region, "", j.ID)
			jobs = append(jobs, o)
		}
		snap["jobs"] = jobs
		out := map[string]any{"settings": s.GCP, "snapshot": snap, "connection": nil, "loggedIn": false,
			"configured": gcpConfiguredLocked(a), "state": nil, "severities": GCPSeverities,
			"createClientURL": GCPCreateClientURL(s.GCP.Project), "consoleURL": GCPConsoleURL(s.GCP.Project, s.GCP.Region, "", "")}
		if c, ok := GCPConnection(a); ok {
			out["connection"] = c
			out["loggedIn"] = connHasTokenCached(a, c)
			out["state"] = connStateCopy(a, c.ID)
		}
		return out
	})

	RegisterAction("gcp_ensure_connection", func(a *App, args map[string]any) (any, error) {
		return GCPEnsureConnection(a), nil
	})
	strList := func(v any) ([]string, bool) {
		switch x := v.(type) {
		case []any:
			out := []string{}
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			return out, true
		case string: // 逗號或換行分隔
			return strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == '\n' }), true
		}
		return nil, false
	}
	RegisterAction("gcp_settings", func(a *App, args map[string]any) (any, error) {
		GCPUpdate(a, func(s *GCPSettings) {
			if v, ok := args["project"].(string); ok {
				s.Project = v
			}
			if v, ok := args["region"].(string); ok {
				s.Region = v
			}
			if v, ok := args["connection"].(string); ok && v != "" {
				s.Connection = v
			}
			if v, ok := strList(args["services"]); ok {
				s.Services = v
			}
			if v, ok := strList(args["jobs"]); ok {
				s.Jobs = v
			}
		})
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GCPSettingsNow(a), nil
	})
	RegisterAction("gcp_refresh", func(a *App, args map[string]any) (any, error) {
		GCPRefresh(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GCPSnapshotNow(a), nil
	})
	explain := func(err error) error { return UserError{GCPExplain(err)} }
	RegisterAction("gcp_revisions", func(a *App, args map[string]any) (any, error) {
		svc, _ := args["service"].(string)
		if svc == "" {
			return nil, UserError{"要給 service"}
		}
		revs, err := GCPRevisions(a, svc, 10)
		if err != nil {
			return nil, explain(err)
		}
		out := []any{}
		for _, r := range revs {
			o := Object(r).(map[string]any)
			o["imageTag"] = GCPImageTag(r.Image)
			out = append(out, o)
		}
		return out, nil
	})
	RegisterAction("gcp_executions", func(a *App, args map[string]any) (any, error) {
		job, _ := args["job"].(string)
		if job == "" {
			return nil, UserError{"要給 job"}
		}
		ex, err := GCPExecutions(a, job, 10)
		if err != nil {
			return nil, explain(err)
		}
		out := []any{}
		for _, e := range ex {
			o := GCPExecutionJSON(e, true)
			o["statusCode"] = e.Status()
			out = append(out, o)
		}
		return out, nil
	})
	num := func(v any, def int) int {
		if f, ok := v.(float64); ok {
			return int(f)
		}
		return def
	}
	optStr := func(v any) *string {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			t := strings.TrimSpace(s)
			return &t
		}
		return nil
	}
	RegisterAction("gcp_logs", func(a *App, args map[string]any) (any, error) {
		sev, _ := args["severity"].(string)
		if sev == "" {
			sev = "ERROR"
		}
		sev = strings.ToUpper(sev)
		if !contains(GCPSeverities, sev) {
			return nil, UserError{"severity 必須是 " + strings.Join(GCPSeverities, "、") + " 之一"}
		}
		hours := min(720, max(1, num(args["hours"], 24)))
		logs, err := GCPLogs(a, optStr(args["service"]), optStr(args["job"]), sev, hours, optStr(args["contains"]), min(500, max(1, num(args["limit"], 50))))
		if err != nil {
			return nil, explain(err)
		}
		return logs, nil
	})
	RegisterAction("gcp_metrics", func(a *App, args map[string]any) (any, error) {
		svc, _ := args["service"].(string)
		if svc == "" {
			return nil, UserError{"要給 service"}
		}
		hours := min(720, max(1, num(args["hours"], 24)))
		m, err := GCPMetricsGet(a, svc, hours)
		if err != nil {
			return nil, explain(err)
		}
		o := Object(m).(map[string]any)
		o["totals"] = GCPMetricsTotals(m)
		return o, nil
	})
}
