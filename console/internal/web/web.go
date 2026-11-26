// Package web 是給 React 畫面用的 HTTP API(和 MCP 分開的連接埠,預設 127.0.0.1:5191)。
//
//	GET  /api/view/<名稱>      各功能登記的資料(見 core.RegisterView)
//	GET  /api/views           有哪些 view
//	GET  /api/events          SSE:{"version":n,"topics":[…]},收到就重讀相關的 view
//	POST /api/tool/<工具名稱>   以目前使用者身分執行 MCP 工具(body 是工具參數)
//	POST /api/action/<名稱>    只有畫面能做的動作(見 core.RegisterAction)
//	/api/auth/…               登入(LDAP)、登出、認證設定(見 auth.go)
//	/api/issues/<組織>…        Issue 資料(給 macOS App(Swift 版)遠端存放用,見 issues_api.go)
//	/api/office/…、/office/    3D 虛擬辦公室:下命令、SSE、檢視器回報、狀態;檢視器靜態檔(見 office.go)
//	其他路徑                   React 畫面(ui/dist)
//
// 啟用 LDAP 時,/api 除了 /api/auth/status、/api/auth/login 都要登入(/api/issues/ 也可以用 OMIXOR_API_TOKEN);畫面的動作以登入者署名。
// 工具與動作分一般成員 / 管理員(見 access.go);非管理員的 /api/issues 只限目前的組織。
package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
	"omixor-web/internal/office"

	"omixor-core/config"
)

const DefaultPort = 5191

func ConfiguredPort() int {
	if p, err := strconv.Atoi(config.Current().Str(config.WebPort)); err == nil && p > 0 && p < 65536 {
		return p
	}
	return DefaultPort
}

// ConfiguredAddr:OMIXOR_WEB_ADDR(例如 0.0.0.0:5191),否則 127.0.0.1:<OMIXOR_WEB_PORT>
func ConfiguredAddr() string {
	if a := config.Current().Str(config.WebAddr); a != "" {
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
	registerIssueAPI(mux, app)
	registerOffice(mux, office.For(app))
	mux.HandleFunc("GET /api/views", func(w http.ResponseWriter, r *http.Request) {
		names := core.ViewNames()
		sort.Strings(names)
		writeJSON(w, 200, names)
	})
	mux.HandleFunc("GET /api/view/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		setDataVersion(w, app)
		v, ok := core.View(app, name)
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "沒有這個 view:" + name})
			return
		}
		if name == "system" || name == "members" || name == "account" {
			v = forViewer(name, v, isAdmin(r, app, au))
		}
		if set, unk, active := restrictedSetFor(r, app, au, ""); active { // 不是 web 管理員:受限專案的資料拿掉(見 restricted.go)
			nv, drop := scrubRestricted(core.Object(v), set, unk)
			if drop {
				notFoundRestricted(w)
				return
			}
			v = nv
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("POST /api/tool/{name}", func(w http.ResponseWriter, r *http.Request) {
		// 一般成員只能用 memberTools 列的工具,其他(含還沒分類的)要 web 管理員(見 access.go)
		if what, need := toolNeedsAdmin(r.PathValue("name")); need && denyNonAdmin(w, r, app, au, what) {
			return
		}
		if !mcp.HasTool(r.PathValue("name")) { // 不存在的工具與不存在的動作一致:404(不是 400)
			writeJSON(w, 404, map[string]any{"error": "沒有這個工具:" + core.ShortName(r.PathValue("name"))})
			return
		}
		args, err := readArgs(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		set, unk, active := restrictedSetFor(r, app, au, "")
		if active && (restrictedArg(args, set, unk) || restrictedTarget(app, r.PathValue("name"), args, set, unk)) {
			notFoundRestricted(w)
			return
		}
		out, err := mcp.Run(app, r.PathValue("name"), args, core.Person(userID(r, app)))
		if err != nil {
			writeFail(w, r, err)
			return
		}
		res := core.Object(out)
		if active {
			var drop bool
			if res, drop = scrubRestricted(res, set, unk); drop {
				notFoundRestricted(w)
				return
			}
		}
		if !isAdmin(r, app, au) {
			stripCredential(res)
		}
		writeJSON(w, 200, map[string]any{"result": res})
	})
	mux.HandleFunc("POST /api/action/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		// 一般成員只能用 memberActions 列的動作,其他(含還沒分類的)要 web 管理員(見 access.go)
		if what, need := actionNeedsAdmin(name); need && denyNonAdmin(w, r, app, au, what) {
			return
		}
		args, err := readArgs(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		set, unk, active := restrictedSetFor(r, app, au, "")
		if active && (restrictedArg(args, set, unk) || restrictedTarget(app, name, args, set, unk)) {
			notFoundRestricted(w)
			return
		}
		args[core.UserKey] = userID(r, app)
		setDataVersion(w, app)
		out, err := core.Action(app, name, args)
		if err != nil {
			writeFail(w, r, err)
			return
		}
		res := core.Object(out)
		if active {
			var drop bool
			if res, drop = scrubRestricted(res, set, unk); drop {
				notFoundRestricted(w)
				return
			}
		}
		if v, ok := actionView[name]; ok {
			res = forViewer(v, res, isAdmin(r, app, au))
		}
		writeJSON(w, 200, map[string]any{"result": res})
	})
	// 附件:上傳也走 JSON(維持「POST 一律 JSON」的 CSRF 防護),data 是 base64;只收圖片
	mux.HandleFunc("POST /api/attachments", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Data string `json:"data"`
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, core.MaxAttachment*4/3+4096))
		if err != nil || json.Unmarshal(b, &req) != nil {
			writeJSON(w, 400, map[string]any{"error": "格式錯誤,要 {\"data\": \"<base64>\"}"})
			return
		}
		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "data 不是 base64"})
			return
		}
		if len(data) > core.MaxAttachment { // 太大:413(格式與空檔案是 400,走 failStatus)
			writeErr(w, r, 413, core.UserError{Message: "圖片太大,上限 10 MB"})
			return
		}
		id, err := core.SaveAttachment(app, data)
		if err != nil {
			writeFail(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"result": map[string]any{"id": id, "url": core.AttachmentURL(id)}})
	})
	mux.HandleFunc("GET /api/attachments/{id}", func(w http.ResponseWriter, r *http.Request) {
		data, mime, err := core.ReadAttachment(app, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", mime)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'")
		// id 是雜湊,內容不會變;附件屬於組織、可能要登入,只給瀏覽器自己快取
		h.Set("Cache-Control", "private, max-age=86400")
		_, _ = w.Write(data)
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
				_, _ = io.WriteString(w, "畫面還沒建置:在 omixor-web 執行 make build(會先 npm run build 再編 Go)。API 與 MCP 照常可用。")
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
//   - Host 只接受本機名稱與 OMIXOR_WEB_HOSTS 列的主機(防 DNS rebinding)
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
				if !ok && strings.HasPrefix(r.URL.Path, "/api/issues/") && apiToken(r) {
					// Issue 資料 API 可以用 OMIXOR_API_TOKEN(給 macOS App(Swift 版)連)
					next.ServeHTTP(w, r)
					return
				}
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

// AllowedHost:本機名稱,或 OMIXOR_WEB_HOSTS(逗號分隔)列的主機
func AllowedHost(h string) bool {
	h = strings.ToLower(h)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	for _, x := range strings.Split(config.Current().Str(config.WebHosts), ",") {
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

// setDataVersion:在算 view / 執行 action 之前記下資料版本,放在 X-Omixor-Version 標頭。
// 回應內容至少反映到這個版本(版本 ≤ 它的變更都已經做完),畫面收到版本 ≤ 它的 SSE 通知(包括連線時的 "*")就不用再重讀。
func setDataVersion(w http.ResponseWriter, app *core.App) {
	w.Header().Set("X-Omixor-Version", strconv.FormatUint(app.Version(), 10))
}
