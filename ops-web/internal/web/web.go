// Package web 是給 React 畫面用的 HTTP API(和 MCP 分開的連接埠,預設 127.0.0.1:5191)。
//
//	GET  /api/view/<名稱>      各功能登記的資料(見 core.RegisterView)
//	GET  /api/views           有哪些 view
//	GET  /api/events          SSE:{"version":n,"topics":[…]},收到就重讀相關的 view
//	POST /api/tool/<工具名稱>   以目前使用者身分執行 MCP 工具(body 是工具參數)
//	POST /api/action/<名稱>    只有畫面能做的動作(見 core.RegisterAction)
//	/api/auth/…               登入(LDAP)、登出、認證設定(見 auth.go)
//	其他路徑                   React 畫面(web/dist)
//
// 啟用 LDAP 時,/api 除了 /api/auth/status、/api/auth/login 都要登入;畫面的動作以登入者署名。
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"opsweb/internal/auth"
	"opsweb/internal/core"
	"opsweb/internal/mcp"
)

const DefaultPort = 5191

func ConfiguredPort() int {
	if p, err := strconv.Atoi(os.Getenv("OPS_WEB_PORT")); err == nil && p > 0 && p < 65536 {
		return p
	}
	return DefaultPort
}

// ConfiguredAddr:OPS_WEB_ADDR(例如 0.0.0.0:5191),否則 127.0.0.1:<OPS_WEB_PORT>
func ConfiguredAddr() string {
	if a := os.Getenv("OPS_WEB_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:" + strconv.Itoa(ConfiguredPort())
}

func IsLoopbackAddr(addr string) bool {
	h := hostOnly(addr)
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Handler:dist 是 React 建好的檔案(可以是 nil,只開 API)
func Handler(app *core.App, dist fs.FS, au *auth.Service) http.Handler {
	mux := http.NewServeMux()
	registerAuth(mux, app, au)
	mux.HandleFunc("GET /api/views", func(w http.ResponseWriter, r *http.Request) {
		names := core.ViewNames()
		sort.Strings(names)
		writeJSON(w, 200, names)
	})
	mux.HandleFunc("GET /api/view/{name}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := core.View(app, r.PathValue("name"))
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "沒有這個 view:" + r.PathValue("name")})
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("POST /api/tool/{name}", func(w http.ResponseWriter, r *http.Request) {
		args, err := readArgs(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		out, err := mcp.Run(app, r.PathValue("name"), args, core.Person(userID(r, app)))
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"result": core.Object(out)})
	})
	mux.HandleFunc("POST /api/action/{name}", func(w http.ResponseWriter, r *http.Request) {
		args, err := readArgs(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		args[core.UserKey] = userID(r, app)
		out, err := core.Action(app, r.PathValue("name"), args)
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"result": core.Object(out)})
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		ch := app.Subscribe()
		defer app.Unsubscribe(ch)
		fmt.Fprintf(w, "data: %s\n\n", core.Marshal(core.Event{Version: app.Version(), Topics: []string{"*"}}))
		fl.Flush()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", core.Marshal(ev))
				fl.Flush()
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
		}
	})
	if dist != nil {
		if _, err := fs.Stat(dist, "index.html"); err != nil {
			// 從原始碼直接 go build、還沒建畫面時
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(503)
				_, _ = io.WriteString(w, "畫面還沒建置:在 ops-web 執行 make build(會先 npm run build 再編 Go)。API 與 MCP 照常可用。")
			})
			dist = nil
		}
	}
	if dist != nil {
		files := http.FileServerFS(dist)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// 單頁應用:找不到的路徑都回 index.html
			p := strings.TrimPrefix(r.URL.Path, "/")
			if p == "" {
				p = "index.html"
			}
			if _, err := fs.Stat(dist, p); err != nil {
				r.URL.Path = "/"
			}
			files.ServeHTTP(w, r)
		})
	}
	return guard(mux, au)
}

// guard:
//   - Host 只接受本機名稱與 OPS_WEB_HOSTS 列的主機(防 DNS rebinding)
//   - 帶 Origin 的請求,Origin 也要是這些主機(防其他網站跨站呼叫)
//   - POST /api 一律要 Content-Type: application/json(表單無法跨站送,等於 CSRF 防護)
//   - 啟用 LDAP 時,/api 要登入(/api/auth/status、/api/auth/login 除外)
func guard(next http.Handler, au *auth.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !AllowedHost(hostOnly(r.Host)) {
			http.Error(w, "Forbidden host", 403)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !AllowedHost(u.Hostname()) {
				http.Error(w, "Forbidden origin", 403)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeJSON(w, 415, map[string]any{"error": "要用 Content-Type: application/json"})
				return
			}
			public := r.URL.Path == "/api/auth/status" || r.URL.Path == "/api/auth/login"
			if au != nil && au.Enabled() && !public {
				u, ok := sessionUser(r, au)
				if !ok {
					writeJSON(w, 401, map[string]any{"error": "請先登入", "login": true})
					return
				}
				r = r.WithContext(withUser(r.Context(), u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hostOnly(h string) string {
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i > 0 {
			return h[1:i]
		}
	}
	if i := strings.LastIndex(h, ":"); i >= 0 {
		return h[:i]
	}
	return h
}

// AllowedHost:本機名稱,或 OPS_WEB_HOSTS(逗號分隔)列的主機
func AllowedHost(h string) bool {
	h = strings.ToLower(h)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	for _, x := range strings.Split(os.Getenv("OPS_WEB_HOSTS"), ",") {
		if x = strings.ToLower(strings.TrimSpace(x)); x != "" && x == h {
			return true
		}
	}
	return false
}

func readArgs(r *http.Request) (map[string]any, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	args := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return args, nil
	}
	if err := json.Unmarshal(b, &args); err != nil {
		return nil, fmt.Errorf("參數不是 JSON 物件")
	}
	return args, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(core.Marshal(v))
}
