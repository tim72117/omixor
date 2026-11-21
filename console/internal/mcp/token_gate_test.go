package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// 起一個和 Start 同樣路由的測試伺服器(newMux),權杖存在暫存資料夾
func gateServer(t *testing.T) (srv *httptest.Server, app *core.App, token string) {
	t.Helper()
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "")
	app = core.New()
	tok, err := core.MCPTokenValue(app)
	if err != nil {
		t.Fatal(err)
	}
	srv = httptest.NewServer(newMux(app))
	t.Cleanup(srv.Close)
	return srv, app, tok
}

func send(t *testing.T, method, url, auth, body string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

const toolsList = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

// 沒帶、錯的、空的、格式不對、雲端 token 格式:各路徑各方法一律 401(同樣的內容,不洩漏權杖)
func TestConsoleMCPRequiresToken(t *testing.T) {
	_, app, tok := gateServer(t)
	body := tok[len("omxm_"):]
	bad := map[string]string{
		"沒帶":            "",
		"空的":            "Bearer ",
		"錯的":            "Bearer omxm_wrongwrongwrongwrongwrong",
		"少一碼":           "Bearer " + tok[:len(tok)-1],
		"多一碼":           "Bearer " + tok + "x",
		"少了空白":          "Bearer" + tok,
		"Basic":         "Basic " + tok,
		"雲端 omx_ 格式":    "Bearer omx_" + body,
		"只有 token 沒有前綴": tok,
	}
	for _, p := range []string{"/mcp", "/mcp/worker", "/", "/mcp/", "/anything", "/mcp/agent/x"} {
		for _, m := range []string{"POST", "GET", "DELETE", "OPTIONS", "PUT"} {
			for name, h := range bad {
				srv2 := httptest.NewServer(newMux(app)) // 每組一個新的 mux(新的失敗計數):不被 429 干擾
				code, hdr, out := send(t, m, srv2.URL+p, h, toolsList)
				srv2.Close()
				if code != 401 {
					t.Errorf("%s %s(%s):要 401,得到 %d", m, p, name, code)
				}
				if hdr.Get("WWW-Authenticate") != `Bearer realm="omixor"` {
					t.Errorf("%s %s(%s):WWW-Authenticate=%q", m, p, name, hdr.Get("WWW-Authenticate"))
				}
				if strings.Contains(out, "omxm_") || strings.Contains(out, "存在") {
					t.Errorf("%s %s(%s):回應內容洩漏資訊:%s", m, p, name, out)
				}
			}
		}
	}
}

// 帶 Bearer(大小寫不拘)放行:initialize 與 tools/list 看得到工具
func TestConsoleMCPAcceptsToken(t *testing.T) {
	srv, _, tok := gateServer(t)
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		code, _, out := send(t, "POST", srv.URL+"/mcp", scheme+" "+tok, initBody)
		if code != 200 || !strings.Contains(out, "serverInfo") {
			t.Fatalf("%s initialize:%d %s", scheme, code, out)
		}
	}
	code, _, out := send(t, "POST", srv.URL+"/mcp", "Bearer "+tok, toolsList)
	if code != 200 {
		t.Fatalf("tools/list:%d %s", code, out)
	}
	var r struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Result.Tools) == 0 {
		t.Fatalf("tools/list 要看得到工具:%v %s", err, out)
	}
	// 通過驗證之後的行為維持原樣:GET 405、不存在的路徑 404
	if code, _, _ := send(t, "GET", srv.URL+"/mcp", "Bearer "+tok, ""); code != 405 {
		t.Fatalf("GET /mcp 要 405:%d", code)
	}
	if code, _, _ := send(t, "POST", srv.URL+"/other", "Bearer "+tok, toolsList); code != 404 {
		t.Fatalf("POST /other 要 404:%d", code)
	}
	// 執行者端點也放行(通過驗證;工具回應不是這裡要驗的)
	if code, _, out := send(t, "POST", srv.URL+"/mcp/worker", "Bearer "+tok, toolsList); code != 200 {
		t.Fatalf("/mcp/worker tools/list:%d %s", code, out)
	}
	// 外部來源(DNS rebinding)即使帶對權杖也 403
	req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(toolsList))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Origin", "https://evil.example")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("外部 Origin 要 403:%d", resp.StatusCode)
	}
}

// 同一來源「錯誤嘗試」太多次回 429(只擋錯的;帶正確權杖的永遠放行)
func TestConsoleMCPRateLimit(t *testing.T) {
	srv, _, tok := gateServer(t)
	got429 := false
	for i := 0; i < 15; i++ {
		code, _, _ := send(t, "POST", srv.URL+"/mcp", "Bearer omxm_nope", toolsList)
		if code == 429 {
			got429 = true
			break
		}
		if code != 401 {
			t.Fatalf("第 %d 次:%d", i, code)
		}
	}
	if !got429 {
		t.Fatal("連續失敗要出現 429")
	}
	if code, hdr, _ := send(t, "POST", srv.URL+"/mcp", "Bearer omxm_nope", toolsList); code != 429 || hdr.Get("Retry-After") == "" {
		t.Fatalf("被擋的來源再送錯的:%d Retry-After=%q", code, hdr.Get("Retry-After"))
	}
	if code, _, _ := send(t, "POST", srv.URL+"/mcp", "Bearer "+tok, toolsList); code != 200 {
		t.Fatalf("被擋的來源帶正確權杖要放行:%d", code)
	}
	// /mcp/worker 與 / 共用同一個失敗計數,同樣只擋錯的
	if code, _, _ := send(t, "POST", srv.URL+"/mcp/worker", "", toolsList); code != 429 {
		t.Fatalf("worker 沒帶權杖也要擋:%d", code)
	}
	if code, _, _ := send(t, "POST", srv.URL+"/mcp/worker", "Bearer "+tok, toolsList); code == 429 || code == 401 {
		t.Fatalf("worker 帶正確權杖要放行:%d", code)
	}
}

// 換新權杖後舊的立刻 401、新的通過;權杖不進 log
func TestConsoleMCPRotateRevokesOld(t *testing.T) {
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	srv, app, old := gateServer(t)
	if code, _, _ := send(t, "POST", srv.URL+"/mcp", "Bearer "+old, toolsList); code != 200 {
		t.Fatalf("舊的先通過:%d", code)
	}
	nu, err := core.MCPTokenRotate(app)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := send(t, "POST", srv.URL+"/mcp", "Bearer "+old, toolsList); code != 401 {
		t.Fatalf("換新後舊的要 401:%d", code)
	}
	if code, _, _ := send(t, "POST", srv.URL+"/mcp/worker", "Bearer "+old, toolsList); code != 401 {
		t.Fatalf("worker 舊的要 401:%d", code)
	}
	if code, _, _ := send(t, "POST", srv.URL+"/mcp", "Bearer "+nu, toolsList); code != 200 {
		t.Fatalf("新的要通過:%d", code)
	}
	if s := logs.String(); strings.Contains(s, old) || strings.Contains(s, nu) {
		t.Fatalf("權杖不能出現在 log:%s", s)
	}
}

// /mcp/agent 照舊驗角色憑證:console 的權杖在那裡沒用,沒帶憑證一樣 401(realm 是 omixor-agent)
func TestConsoleMCPAgentEndpointUnchanged(t *testing.T) {
	srv, _, tok := gateServer(t)
	for _, auth := range []string{"", "Bearer " + tok} {
		code, hdr, _ := send(t, "POST", srv.URL+"/mcp/agent", auth, toolsList)
		if code != 401 || hdr.Get("WWW-Authenticate") != `Bearer realm="omixor-agent"` {
			t.Fatalf("/mcp/agent(%q):%d %q", auth, code, hdr.Get("WWW-Authenticate"))
		}
	}
}

// Host 白名單:整個連接埠(含 /mcp/agent)Host 不是本機一律 403,且不計驗證失敗;本機 Host 帶正確權杖照常放行
func TestConsoleMCPHostWhitelist(t *testing.T) {
	_, app, tok := gateServer(t)
	mux := newMux(app)
	do := func(host, path, auth string) int {
		r := httptest.NewRequest("POST", "http://"+host+path, strings.NewReader(toolsList))
		r.RemoteAddr = "127.0.0.1:4000"
		r.Host = host
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	for _, host := range []string{"evil.example", "evil.example:3765", "localhost.evil.example:3765", "10.0.0.5:3765", ""} {
		for _, p := range []string{"/mcp", "/mcp/worker", "/mcp/agent", "/"} {
			if got := do(host, p, "Bearer "+tok); got != 403 {
				t.Errorf("Host %q %s 要 403:%d", host, p, got)
			}
		}
	}
	// 上面的 403 不計失敗:接著連續送正確權杖都放行,錯誤的也還是 401(沒被 429)
	for _, host := range []string{"localhost:3765", "127.0.0.1:3765", "[::1]:3765", "localhost"} {
		if got := do(host, "/mcp", "Bearer "+tok); got != 200 {
			t.Errorf("Host %q 帶正確權杖要放行:%d", host, got)
		}
	}
	if got := do("localhost:3765", "/mcp", "Bearer omxm_wrongwrongwrongwrongwrongwrong"); got != 401 {
		t.Errorf("本機 Host 錯誤權杖要 401(Host 不符沒計失敗):%d", got)
	}
	// 錯誤嘗試灌到被擋:正確權杖仍放行
	for i := 0; i < 30; i++ {
		do("localhost:3765", "/mcp", "Bearer omxm_wrongwrongwrongwrongwrongwrong")
	}
	if got := do("localhost:3765", "/mcp", "Bearer omxm_wrongwrongwrongwrongwrongwrong"); got != 429 {
		t.Errorf("錯誤嘗試太多要 429:%d", got)
	}
	if got := do("localhost:3765", "/mcp", "Bearer "+tok); got != 200 {
		t.Errorf("被擋的來源帶正確權杖要放行:%d", got)
	}
}

// OMIXOR_MCP_ADDR 設了非本機位址(例如 Docker 以外的內網位址)時,該位址的 Host 也放行;0.0.0.0 不算
func TestConsoleMCPHostWhitelistConfiguredAddr(t *testing.T) {
	t.Setenv("OMIXOR_MCP_ADDR", "10.1.2.3")
	_, app, tok := gateServer(t)
	mux := newMux(app)
	for host, want := range map[string]int{"10.1.2.3:3765": 200, "10.1.2.4:3765": 403, "localhost:3765": 200} {
		r := httptest.NewRequest("POST", "http://"+host+"/mcp", strings.NewReader(toolsList))
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q:要 %d,得到 %d", host, want, w.Code)
		}
	}
}
