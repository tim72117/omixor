package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 對外的形象網站(repo 根目錄的 site/,英文版):omixor.io 的 / 與幾個公開檔案由 omixor server 直接提供。
//
//	OMIXOR_SITE_DIR   網站檔案所在的資料夾(Docker 映像裡是 /site,見 server/Dockerfile);沒設或資料夾不存在就不提供網站
//
// 只提供 sitePublicFiles 列出的檔案(白名單):site/ 裡的 zh.html、pricing.zh.html(中文對照,內部用)、og.html(og.png 的來源頁)、
// README.md 不對外;映像也只複製這些檔案與 404.html。新增對外檔案時,這裡、server/Dockerfile、site/README.md 要一起改。
// 路由只有「/」與這些固定路徑,不會蓋到 /api/、/mcp/、/health。
//
// 檔案在掛載時讀進記憶體一次(映像裡是靜態的;改了檔案要重啟),文字檔(html、xml、txt、svg)同時預先壓好 gzip,
// 請求帶 Accept-Encoding: gzip 就回壓縮版(Content-Encoding: gzip、Vary: Accept-Encoding)。png 不壓。
var sitePublicFiles = map[string]string{
	"/":            "index.html",
	"/pricing":     "pricing.html",
	"/favicon.svg": "favicon.svg",
	"/og.png":      "og.png",
	"/robots.txt":  "robots.txt",
	"/sitemap.xml": "sitemap.xml",
	"/llms.txt":    "llms.txt",
}

// site404File 找不到頁面時回的頁面(狀態碼 404,見 siteNotFound);不在 sitePublicFiles,/404.html 本身也是 404
const site404File = "404.html"

var siteContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".txt":  "text/plain; charset=utf-8",
	".xml":  "application/xml; charset=utf-8",
}

// 要壓縮的副檔名(文字類);png 本身已壓縮
var siteCompressible = map[string]bool{".html": true, ".svg": true, ".txt": true, ".xml": true}

// siteAsset 一個網站檔案:原始內容與(文字檔才有的)gzip 版
type siteAsset struct {
	ext     string
	mod     time.Time
	raw, gz []byte
}

func loadSiteAsset(path string) (*siteAsset, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a := &siteAsset{ext: filepath.Ext(path), mod: st.ModTime(), raw: raw}
	if siteCompressible[a.ext] {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(raw)
		_ = zw.Close()
		a.gz = buf.Bytes()
	}
	return a, nil
}

// acceptsGzip 請求的 Accept-Encoding 有 gzip(或 *)且 q 不是 0
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		return q > 0
	}
	return false
}

// setHeaders 網站共用的標頭;回傳這次要送的內容(壓縮版或原始版)
func (a *siteAsset) setHeaders(w http.ResponseWriter, r *http.Request) []byte {
	h := w.Header()
	if ct := siteContentTypes[a.ext]; ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	if a.ext == ".html" { // 頁面:不給嵌入、快取短一點
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "public, max-age=300")
	} else {
		h.Set("Cache-Control", "public, max-age=3600")
	}
	body := a.raw
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = a.gz
		}
	}
	return body
}

// serve 回 200(ServeContent 處理 HEAD、If-Modified-Since、Range)。
// 送壓縮版時 ServeContent 不會設 Content-Length,這裡自己設;同時不接 Range(回整份),避免長度與分段對不上
func (a *siteAsset) serve(w http.ResponseWriter, r *http.Request) {
	body := a.setHeaders(w, r)
	if w.Header().Get("Content-Encoding") != "" {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Header.Get("Range") != "" {
			r = r.Clone(r.Context())
			r.Header.Del("Range")
		}
	}
	http.ServeContent(w, r, "", a.mod, bytes.NewReader(body))
}

// serveStatus 用指定狀態碼回整份內容(404 頁用);HEAD 不送內容
func (a *siteAsset) serveStatus(w http.ResponseWriter, r *http.Request, code int) {
	body := a.setHeaders(w, r)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// mountSite 把網站掛到 mux;dir 是空字串或不存在時不掛(本機守護服務、測試都不提供網站)。回傳有沒有掛上
func mountSite(mux *http.ServeMux, dir string) bool {
	if dir == "" {
		return false
	}
	if st, err := os.Stat(filepath.Join(dir, "index.html")); err != nil || st.IsDir() {
		return false
	}
	for route, name := range sitePublicFiles {
		a, err := loadSiteAsset(filepath.Join(dir, name))
		if err != nil { // 檔案缺了:這條路徑照樣 404,其他照常提供
			continue
		}
		pattern := "GET " + route // GET 也接 HEAD
		if route == "/" {
			pattern = "GET /{$}" // 只有根目錄;其他沒登記的路徑照樣 404
		}
		mux.HandleFunc(pattern, a.serve)
	}
	return true
}

// siteNotFound 包住已掛上網站的 mux:GET/HEAD 請求在 mux 找不到任何路由、原本會回純文字 404 時,改回 site/404.html(狀態碼一樣是 404)。
// 只在 mountSite 回 true 時用(見 server.go)。不能用 mux 的 "GET /" 萬用路由:會和沒指定方法的 "/api/"、"/mcp/agent" 衝突。
// 不碰的情況(行為完全不變):
//   - 有對應路由的請求(包含尾斜線、路徑清理的轉址)
//   - /api、/mcp 底下與 /health(給程式用的端點,維持純文字錯誤)
//   - GET/HEAD 以外的方法、405(只換掉 404)
//
// dir 裡沒有 404.html 就原樣回傳 mux。
func siteNotFound(mux *http.ServeMux, dir string) http.Handler {
	page, err := loadSiteAsset(filepath.Join(dir, site404File))
	if err != nil {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead || !siteOwnsPath(r.URL.Path) {
			mux.ServeHTTP(w, r)
			return
		}
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(&siteNotFoundWriter{ResponseWriter: w, r: r, page: page}, r)
	})
}

// siteOwnsPath 網站可以接手 404 的路徑(不是 API、MCP、健康檢查)
func siteOwnsPath(p string) bool {
	for _, prefix := range []string{"/api", "/mcp"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return false
		}
	}
	return p != "/health"
}

// siteNotFoundWriter 攔下 404:改回 404 頁,原本的純文字內容丟掉;其他狀態碼原樣送出
type siteNotFoundWriter struct {
	http.ResponseWriter
	r        *http.Request
	page     *siteAsset
	wrote    bool
	replaced bool
}

func (w *siteNotFoundWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if code == http.StatusNotFound {
		w.replaced = true
		h := w.ResponseWriter.Header()
		h.Del("Content-Type")
		h.Del("Content-Length")
		w.page.serveStatus(w.ResponseWriter, w.r, http.StatusNotFound)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *siteNotFoundWriter) Write(b []byte) (int, error) {
	if w.replaced {
		return len(b), nil
	}
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
