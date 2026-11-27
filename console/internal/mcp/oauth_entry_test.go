package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"omixor-core/config"
)

// 公開入口只在 OMIXOR_PUBLIC_URL 合格時啟用;條件不合只回原因(不擋啟動)
func TestWebEntryConditions(t *testing.T) {
	load := func(env map[string]string) *config.Config {
		return config.LoadFrom(func(k string) string { return env[k] }, config.Web)
	}
	ok := load(map[string]string{"OMIXOR_PUBLIC_URL": "https://console.test"})
	if err := ok.Validate(config.Web); err != nil {
		t.Fatal(err)
	}
	if p := webEntryProblem(ok, true); p != "" {
		t.Fatal(p)
	}
	if webEntryProblem(ok, false) == "" {
		t.Fatal("沒有資料庫不啟用")
	}
	bad := load(map[string]string{"OMIXOR_PUBLIC_URL": "http://console.test"})
	if err := bad.Validate(config.Web); err != nil {
		t.Fatalf("只設 PUBLIC_URL 不該因此拒絕啟動:%v", err)
	}
	if webEntryProblem(bad, true) == "" {
		t.Fatal("公開網址用 http 不啟用")
	}
}

// 四種情況:公開、本機(沒設對外網址或不合格)、獨立連接埠、沒有(沒資料庫、只聽特定位址)
func TestPlanEntry(t *testing.T) {
	load := func(env map[string]string) *config.Config {
		return config.LoadFrom(func(k string) string { return env[k] }, config.Web)
	}
	cases := []struct {
		env          map[string]string
		db           bool
		mode         entryMode
		public, base string
	}{
		{nil, true, entryLoopback, "http://127.0.0.1:5191", "http://127.0.0.1:5191"},
		{map[string]string{"OMIXOR_WEB_ADDR": "0.0.0.0:6000"}, true, entryLoopback, "http://127.0.0.1:6000", "http://127.0.0.1:6000"},
		{map[string]string{"OMIXOR_PUBLIC_URL": "http://console.test"}, true, entryLoopback, "http://127.0.0.1:5191", "http://127.0.0.1:5191"},
		{map[string]string{"OMIXOR_PUBLIC_URL": "https://console.test"}, true, entryPublic, "https://console.test", "http://127.0.0.1:5191"},
		{map[string]string{"OMIXOR_OAUTH_ADDR": "127.0.0.1:8771", "OMIXOR_PUBLIC_URL": "https://c.test"}, true, entrySeparate, "https://c.test", "http://127.0.0.1:5191"},
		{map[string]string{"OMIXOR_WEB_ADDR": "10.1.2.3:5191"}, true, entryNone, "", "http://10.1.2.3:5191"},
		{nil, false, entryNone, "", "http://127.0.0.1:5191"},
	}
	for _, c := range cases {
		p := planEntry(load(c.env), c.db)
		if p.mode != c.mode || p.public != c.public || p.localURL != c.base || (p.mode == entryNone) != (p.off != "") {
			t.Errorf("%v db=%v:%+v", c.env, c.db, p)
		}
	}
}

// 本機入口只收同一台電腦直接連進來、Host 是本機名稱的請求;本機角色端點在各情況都只看直接連線
func TestScreenRouterLoopbackOnly(t *testing.T) {
	code := func(n int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(n) })
	}
	serve := func(mode entryMode, path, remote, host string, hdr ...string) int {
		h := screenRouter(entryPlan{mode: mode, public: "http://127.0.0.1:5191", localHost: "127.0.0.1", off: "x"}, code(201), code(202))
		r := httptest.NewRequest("POST", path, nil)
		r.RemoteAddr, r.Host = remote, host
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	const local, lan, here = "127.0.0.1:50000", "192.168.1.9:50000", "127.0.0.1:5191"
	for i, c := range []struct{ got, want int }{
		{serve(entryLoopback, "/mcp", local, here), 201},
		{serve(entryLoopback, "/oauth/authorize", local, "localhost:5191"), 201},
		{serve(entryLoopback, "/mcp", lan, here), 403},
		{serve(entryLoopback, "/mcp", local, "console.test"), 403},
		{serve(entryLoopback, "/mcp", local, here, "X-Forwarded-For", "203.0.113.5"), 403},
		{serve(entryLoopback, "/mcp/agent", local, here), 202},
		{serve(entryLoopback, "/mcp/agent", lan, here), 403},
		{serve(entryPublic, "/mcp/agent", local, here), 202},
		{serve(entryPublic, "/mcp/agent", lan, "console.test"), 201},
		{serve(entryPublic, "/mcp/agent", local, here, "X-Forwarded-For", "203.0.113.5"), 201},
		{serve(entrySeparate, "/mcp/agent", local, here), 202},
		{serve(entrySeparate, "/mcp/agent", lan, here), 404},
		{serve(entrySeparate, "/mcp", local, here), 404},
		{serve(entryNone, "/mcp", local, here), 404},
		{serve(entryNone, "/mcp/agent", local, here), 202},
	} {
		if c.got != c.want {
			t.Errorf("第 %d 個:%d,要 %d", i, c.got, c.want)
		}
	}
}
