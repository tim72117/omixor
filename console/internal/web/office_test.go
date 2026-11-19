package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omixor-web/internal/office"
)

const officeTestPlaces = `{"agents":[{"id":"ceo","name":"CEO","aliases":["ceo"],"seat":"seat_ceo"},{"id":"engineer","name":"工程師","aliases":["engineer","工程師"],"seat":"seat_engineer"}],
"places":[{"id":"seat_ceo","kind":"seat","name":"CEO的座位","aliases":[]},{"id":"seat_engineer","kind":"seat","name":"工程師的座位","aliases":[]},{"id":"meeting","kind":"shared","name":"會議室","aliases":["會議室","meeting"]}]}`

// officeServer 只掛辦公室的路由(經過同一個 guard),用自己的 Hub 與暫存的 office3d 資料夾
func officeServer(t *testing.T, opt office.Options) (*httptest.Server, *office.Hub, string) {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "places.json"), []byte(officeTestPlaces), 0o644))
	must(os.WriteFile(filepath.Join(dir, "office.blend"), []byte("secret"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "build_office.py"), []byte("secret"), 0o644))
	must(os.MkdirAll(filepath.Join(dir, "viewer", "js"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "viewer", "index.html"), []byte("<!doctype html><title>office</title>"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "viewer", "js", "office.js"), []byte("export {}"), 0o644))
	// 指到資料夾外面的符號連結:os.Root 擋
	outside := filepath.Join(t.TempDir(), "outside.js")
	must(os.WriteFile(outside, []byte("leak"), 0o644))
	must(os.Symlink(outside, filepath.Join(dir, "viewer", "js", "evil.js")))
	opt.Dir = dir
	opt.Logf = func(string, ...any) {}
	hub := office.NewHub(opt)
	mux := http.NewServeMux()
	registerOffice(mux, hub)
	srv := httptest.NewServer(guard(mux, nil))
	t.Cleanup(srv.Close)
	return srv, hub, dir
}

func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

type sseEvent struct{ event, data string }

// openSSE 連 /api/office/events,事件送進 channel
func openSSE(t *testing.T, ctx context.Context, url string) (<-chan sseEvent, *http.Response) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/office/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan sseEvent, 64)
	if resp.StatusCode != 200 {
		close(ch)
		return ch, resp
	}
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.event != "":
				ch <- ev
				ev = sseEvent{}
			}
		}
	}()
	return ch, resp
}

func nextEvent(t *testing.T, ch <-chan sseEvent, want string) sseEvent {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("SSE 結束了,還在等 %s", want)
			}
			if ev.event == want {
				return ev
			}
		case <-timeout:
			t.Fatalf("等不到 %s", want)
		}
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等不到:", what)
}

func cmdStatus(t *testing.T, srv *httptest.Server, id string) string {
	t.Helper()
	st := getJSON(t, srv.URL+"/api/office/state")
	for _, c := range st["commands"].([]any) {
		m := c.(map[string]any)
		if m["id"] == id {
			return m["status"].(string)
		}
	}
	return ""
}

func TestOfficeCommandValidationAndNoViewer(t *testing.T) {
	srv, _, _ := officeServer(t, office.Options{})
	for _, bad := range []map[string]any{
		{"action": "goto", "agents": []string{"nobody"}, "target": "meeting"},
		{"action": "goto", "agents": []string{"ceo"}, "target": "mars"},
		{"text": "ceo 去 火星"},
		{},
		{"action": "goto", "agents": []string{"ceo"}, "target": "meeting", "wait": 500},
	} {
		code, out := postJSON(t, srv.URL+"/api/office/command", bad)
		if code != 400 || out["error"] == nil {
			t.Errorf("%v → %d %v", bad, code, out)
		}
	}
	// 沒有檢視器:回 200,但狀態是 no_viewer
	code, out := postJSON(t, srv.URL+"/api/office/command", map[string]any{"text": "工程師 去 會議室"})
	if code != 200 {
		t.Fatal(code, out)
	}
	res := out["result"].(map[string]any)
	c := res["commands"].([]any)[0].(map[string]any)
	if c["status"] != office.StatusNoViewer || res["viewers"].(float64) != 0 || res["id"] != c["id"] {
		t.Fatal(res)
	}
	// POST 要 JSON(guard)
	resp, _ := http.Post(srv.URL+"/api/office/command", "text/plain", strings.NewReader(`{"text":"ceo 回座位"}`))
	if resp.StatusCode != 415 {
		t.Fatal("POST 不是 JSON 要 415:", resp.StatusCode)
	}
	resp.Body.Close()
}

// SSE:hello / snapshot / command,送達後是 delivered;回報 ack、arrived;wait 等抵達;斷線清理
func TestOfficeSSEFlow(t *testing.T) {
	srv, hub, _ := officeServer(t, office.Options{Heartbeat: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, resp := openSSE(t, ctx, srv.URL)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	var hello struct {
		ViewerID string `json:"viewerId"`
		Primary  bool   `json:"primary"`
	}
	_ = json.Unmarshal([]byte(nextEvent(t, ch, "hello").data), &hello)
	if hello.ViewerID == "" || !hello.Primary {
		t.Fatal(hello)
	}
	nextEvent(t, ch, "snapshot")
	nextEvent(t, ch, "ping") // 心跳

	// wait:另一個 goroutine 等抵達
	type result struct {
		code int
		out  map[string]any
	}
	waited := make(chan result, 1)
	go func() {
		code, out := postJSON(t, srv.URL+"/api/office/command", map[string]any{"action": "goto", "agents": []string{"engineer"}, "target": "meeting", "wait": 5})
		waited <- result{code, out}
	}()
	ev := nextEvent(t, ch, "command")
	var cmd struct {
		ID     string `json:"id"`
		Action struct {
			Type   string   `json:"type"`
			Agents []string `json:"agents"`
			Target struct{ Kind, ID string }
		} `json:"action"`
	}
	_ = json.Unmarshal([]byte(ev.data), &cmd)
	if cmd.Action.Type != "goto" || cmd.Action.Target.ID != "meeting" || cmd.Action.Agents[0] != "engineer" {
		t.Fatal(ev.data)
	}
	waitFor(t, "delivered", func() bool { return cmdStatus(t, srv, cmd.ID) == office.StatusDelivered })
	code, out := postJSON(t, srv.URL+"/api/office/report", map[string]any{"viewerId": hello.ViewerID, "type": "ack", "commandId": cmd.ID,
		"results": []any{map[string]any{"agent": "engineer", "ok": true}},
		"agents":  []any{map[string]any{"id": "engineer", "state": "walking", "position": []float64{1, 0, 1}, "seated": false, "yaw": 0}}})
	if code != 200 || out["result"].(map[string]any)["accepted"] != true {
		t.Fatal(code, out)
	}
	if s := cmdStatus(t, srv, cmd.ID); s != office.StatusRunning {
		t.Fatal(s)
	}
	postJSON(t, srv.URL+"/api/office/report", map[string]any{"viewerId": hello.ViewerID, "type": "arrived", "commandId": cmd.ID, "agent": "engineer",
		"arrived": map[string]any{"target": map[string]any{"kind": "place", "id": "meeting"}, "position": []float64{2, 0, 3}, "seated": true, "where": "會議室"}})
	select {
	case r := <-waited:
		c := r.out["result"].(map[string]any)["commands"].([]any)[0].(map[string]any)
		if r.code != 200 || c["status"] != office.StatusArrived || r.out["result"].(map[string]any)["finished"] != true {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait 沒有在抵達時回傳")
	}
	st := getJSON(t, srv.URL+"/api/office/state")
	if st["viewerCount"].(float64) != 1 || st["hasPrimary"] != true || strings.Contains(fmt.Sprint(st), hello.ViewerID) {
		t.Fatal(st)
	}
	for _, a := range st["agents"].([]any) {
		m := a.(map[string]any)
		if m["id"] == "engineer" && (m["place"] != "meeting" || m["seated"] != true || m["state"] != "arrived") {
			t.Fatal(m)
		}
	}
	// 不合法的回報
	if code, _ := postJSON(t, srv.URL+"/api/office/report", map[string]any{"viewerId": hello.ViewerID, "type": "nope"}); code != 400 {
		t.Fatal("type 不合法要 400:", code)
	}
	// 進行中的命令,client 斷線 → 清掉連線,命令算失敗
	_, out = postJSON(t, srv.URL+"/api/office/command", map[string]any{"text": "ceo 去 會議室"})
	id := out["result"].(map[string]any)["id"].(string)
	nextEvent(t, ch, "command")
	cancel()
	waitFor(t, "斷線清理", func() bool { return hub.Viewers() == 0 })
	if s := cmdStatus(t, srv, id); s != office.StatusFailed {
		t.Fatal(s)
	}
}

// 連線上限:超過回 503;斷掉一個就能再連
func TestOfficeCommandTooMany(t *testing.T) {
	srv, _, _ := officeServer(t, office.Options{})
	code, out := postJSON(t, srv.URL+"/api/office/command", map[string]any{"text": strings.Repeat("ceo stop;", office.MaxCommandsPerSubmit+1)})
	if code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "最多") {
		t.Fatal(code, out)
	}
	code, out = postJSON(t, srv.URL+"/api/office/command", map[string]any{"text": strings.Repeat("工", office.MaxTextRunes+1)})
	if code != 400 {
		t.Fatal(code, out)
	}
}

// 寫出失敗(死連線):handler 要結束並把檢視器移除
type deadWriter struct {
	hdr      http.Header
	deadline time.Time
	setCalls int
	block    bool
}

func (d *deadWriter) Header() http.Header { return d.hdr }
func (d *deadWriter) WriteHeader(int)     {}
func (d *deadWriter) Flush()              {}
func (d *deadWriter) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	d.setCalls++
	return nil
}
func (d *deadWriter) Write(b []byte) (int, error) {
	if d.block { // 模擬半斷的連線:卡到寫入期限才失敗
		time.Sleep(time.Until(d.deadline))
	}
	return 0, errors.New("broken pipe")
}

func TestOfficeSSEWriteFailureUnsubscribes(t *testing.T) {
	old := officeWriteTimeout
	officeWriteTimeout = 100 * time.Millisecond
	t.Cleanup(func() { officeWriteTimeout = old })
	for _, block := range []bool{false, true} {
		hub := office.NewHub(office.Options{Dir: t.TempDir(), Logf: func(string, ...any) {}})
		mux := http.NewServeMux()
		registerOffice(mux, hub)
		w := &deadWriter{hdr: http.Header{}, block: block}
		req := httptest.NewRequest("GET", "/api/office/events", nil)
		done := make(chan struct{})
		go func() { mux.ServeHTTP(w, req); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("block=%v:寫入失敗後 handler 沒有結束", block)
		}
		if hub.Viewers() != 0 {
			t.Fatalf("block=%v:handler 離開後檢視器要被移除", block)
		}
		if w.setCalls == 0 {
			t.Fatal("寫出前沒有設定 write deadline")
		}
	}
}

func TestOfficeSSELimit(t *testing.T) {
	srv, hub, _ := officeServer(t, office.Options{MaxViewers: 1})
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := openSSE(t, ctx, srv.URL)
	nextEvent(t, ch, "hello")
	_, resp := openSSE(t, context.Background(), srv.URL)
	if resp.StatusCode != 503 {
		t.Fatal("超過上限要 503:", resp.StatusCode)
	}
	resp.Body.Close()
	cancel()
	waitFor(t, "斷線清理", func() bool { return hub.Viewers() == 0 })
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	ch2, _ := openSSE(t, ctx2, srv.URL)
	nextEvent(t, ch2, "hello")
}

// 兩個檢視器:副檢視器的回報不採用
func TestOfficeSecondaryViewer(t *testing.T) {
	srv, _, _ := officeServer(t, office.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch1, _ := openSSE(t, ctx, srv.URL)
	nextEvent(t, ch1, "hello")
	ch2, _ := openSSE(t, ctx, srv.URL)
	var h2 struct {
		ViewerID string `json:"viewerId"`
		Primary  bool   `json:"primary"`
	}
	_ = json.Unmarshal([]byte(nextEvent(t, ch2, "hello").data), &h2)
	if h2.Primary {
		t.Fatal("第二個檢視器不是主檢視器")
	}
	_, out := postJSON(t, srv.URL+"/api/office/report", map[string]any{"viewerId": h2.ViewerID, "type": "state"})
	if r := out["result"].(map[string]any); r["accepted"] != false {
		t.Fatal(r)
	}
}

// 靜態檔:白名單、路徑穿越、符號連結
func TestOfficeStatic(t *testing.T) {
	srv, _, _ := officeServer(t, office.Options{})
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/office", "/office/", "/office/viewer"} {
		resp, err := noRedirect.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "/office/viewer/" {
			t.Errorf("%s → %d %s", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	get := func(p string) (int, string, string) {
		req, _ := http.NewRequest("GET", srv.URL+"/", nil)
		req.URL.RawPath = "" // 原封不動送出(不讓 client 清理路徑)
		req.URL.Opaque = p
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}
	if code, ct, body := get("/office/viewer/"); code != 200 || !strings.HasPrefix(ct, "text/html") || !strings.Contains(body, "office") {
		t.Fatal(code, ct, body)
	}
	if code, ct, _ := get("/office/places.json"); code != 200 || !strings.HasPrefix(ct, "application/json") {
		t.Fatal(code, ct)
	}
	if code, ct, _ := get("/office/viewer/js/office.js"); code != 200 || !strings.HasPrefix(ct, "text/javascript") {
		t.Fatal(code, ct)
	}
	for _, p := range []string{
		"/office/office.blend", "/office/build_office.py", "/office/viewer/js/evil.js", "/office/navgrid.json", // navgrid.json 在白名單但不存在 → 404
		"/office/viewer/js/../../office.blend", "/office/..%2foffice.blend", "/office/%2e%2e/%2e%2e/etc/passwd",
		"/office/viewer/js/..%2f..%2fbuild_office.py", "/office/viewer%2fjs%2f..%2f..%2foffice.blend", "/office/../../etc/passwd",
	} {
		code, _, body := get(p)
		if code == 200 || strings.Contains(body, "secret") || strings.Contains(body, "leak") {
			t.Errorf("%s → %d %q", p, code, body)
		}
	}
	// 非本機 Host 一樣擋(guard)
	req, _ := http.NewRequest("GET", srv.URL+"/office/places.json", nil)
	req.Host = "evil.example.com"
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatal("非本機 Host 要 403:", resp.StatusCode)
	}
	resp.Body.Close()
}

// 完整的 Handler 有掛辦公室;找不到 office3d 時 API 回 503、靜態檔 404,state 照常
func TestOfficeMountedWithoutDir(t *testing.T) {
	t.Setenv("OMIXOR_OFFICE_DIR", filepath.Join(t.TempDir(), "nope"))
	c, _ := newServer(t, nil)
	code, out := c.do("GET", "/api/office/state", nil, nil)
	if code != 200 || out["available"] != false {
		t.Fatal(code, out)
	}
	if code, _ := c.do("POST", "/api/office/command", map[string]any{"text": "ceo 回座位"}, nil); code != 503 {
		t.Fatal("沒有 office3d 要 503:", code)
	}
	resp, err := http.Get(c.srv.URL + "/office/places.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("沒有 office3d 要 404:", resp.StatusCode)
	}
}
