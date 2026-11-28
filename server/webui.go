package server

// webui.go 在 /console/ 提供操作畫面(console/ui 建好的檔案,建置時 base = /console/)。
//
//	OMIXOR_UI_DIR   畫面檔案的資料夾(Docker 映像裡是 /ui)。沒設(預設)= 整組不掛:沒有 /console、沒有 /api/auth/web/*、REST 不收 cookie,
//	                行為和沒有這個功能時完全相同。要同時設 OMIXOR_PUBLIC_URL(登入回呼網址與 cookie 的 Secure 由它決定)
//
// 路由:/console → /console/;/console/ 底下有的檔案原樣給(assets/ 檔名帶雜湊,長快取),沒有副檔名的其他路徑回 index.html(深連結),
// 有副檔名但不存在的回 404。檔案在掛載時讀進記憶體(請求路徑只拿來查表,不碰檔案系統)。
// 瀏覽器登入與 cookie 認證的限制在 webauth.go。

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"

	"omixor-core/config"
)

// uiPrefix 畫面掛載的路徑(要和建置時的 OMIXOR_UI_BASE 相同)
const uiPrefix = "/console/"

// uiCSP 畫面的 Content-Security-Policy:腳本只收本站;style 要 inline(React 的行內樣式);Markdown 圖片可以是 https
const uiCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

var uiContentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".json": "application/json", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon",
	".woff2": "font/woff2", ".txt": "text/plain; charset=utf-8",
}

type uiAsset struct{ raw, gz []byte }

type webUI struct {
	files  map[string]uiAsset // 相對路徑(index.html、assets/…)
	public string             // 對外網址(不含結尾斜線)
}

// loadWebUI 沒啟用回 nil;設了但不能用時記 log 說明原因(不讓伺服器啟動失敗)
func loadWebUI(cfg *config.Config) *webUI {
	dir := cfg.Str(config.UIDir)
	if dir == "" {
		return nil
	}
	public := strings.TrimRight(cfg.Str(config.PublicURL), "/")
	if public == "" {
		log.Printf("沒有提供操作畫面:設了 OMIXOR_UI_DIR,但沒有設 OMIXOR_PUBLIC_URL")
		return nil
	}
	u := &webUI{files: map[string]uiAsset{}, public: public}
	root := os.DirFS(dir)
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || uiContentTypes[path.Ext(p)] == "" {
			return err
		}
		raw, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		a := uiAsset{raw: raw}
		if ext := path.Ext(p); ext != ".png" && ext != ".woff2" {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(raw)
			_ = zw.Close()
			a.gz = buf.Bytes()
		}
		u.files[p] = a
		return nil
	})
	if err != nil {
		log.Printf("沒有提供操作畫面:讀不到 OMIXOR_UI_DIR(%s):%v", dir, err)
		return nil
	}
	if _, ok := u.files["index.html"]; !ok {
		log.Printf("沒有提供操作畫面:OMIXOR_UI_DIR(%s)裡沒有 index.html", dir)
		return nil
	}
	return u
}

func (u *webUI) https() bool { return strings.HasPrefix(u.public, "https://") }

func (u *webUI) securityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", uiCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if u.https() {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

func (u *webUI) serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	u.securityHeaders(h)
	name := strings.TrimPrefix(r.URL.Path, uiPrefix)
	a, ok := u.files[name]
	if !ok {
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		a = u.files[name]
	}
	h.Set("Content-Type", uiContentTypes[path.Ext(name)])
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	body := a.raw
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = a.gz
		}
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (u *webUI) mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /console", func(w http.ResponseWriter, r *http.Request) {
		u.securityHeaders(w.Header())
		to := uiPrefix
		if r.URL.RawQuery != "" {
			to += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, to, http.StatusPermanentRedirect)
	})
	mux.HandleFunc("GET "+uiPrefix, u.serve)
}
