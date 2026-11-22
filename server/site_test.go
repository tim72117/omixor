package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 形象網站:只提供白名單檔案(英文首頁與公開檔案),zh.html、pricing.zh.html、og.html、README.md 不對外;不影響 /health;沒設資料夾就不提供
func TestSiteServesOnlyPublicFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"index.html":  "<!doctype html><html lang=\"en\"><title>omixor</title></html>",
		"favicon.svg": "<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "og.png": "\x89PNG\r\n\x1a\n",
		"robots.txt": "User-agent: *", "sitemap.xml": "<urlset/>", "llms.txt": "omixor",
		"pricing.html":    "<!doctype html><html lang=\"en\"><title>Pricing</title><h1>Self-hosted</h1></html>",
		"pricing.zh.html": "<html lang=\"zh-Hant-TW\"></html>",
		"zh.html":         "<html lang=\"zh-Hant-TW\"></html>", "og.html": "<html></html>", "README.md": "# site",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	if !mountSite(mux, dir) {
		t.Fatal("有 index.html 的資料夾要掛上")
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	cases := map[string]string{
		"/": "text/html; charset=utf-8", "/pricing": "text/html; charset=utf-8", "/favicon.svg": "image/svg+xml", "/og.png": "image/png",
		"/robots.txt": "text/plain; charset=utf-8", "/sitemap.xml": "application/xml; charset=utf-8", "/llms.txt": "text/plain; charset=utf-8",
	}
	for path, ct := range cases {
		w := get(path)
		if w.Code != 200 || w.Header().Get("Content-Type") != ct || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s:%d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := get("/"); !strings.Contains(w.Body.String(), `lang="en"`) {
		t.Fatalf("/ 要是英文首頁:%s", w.Body.String())
	}
	if w := get("/pricing"); !strings.Contains(w.Body.String(), "Self-hosted") || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("/pricing 要是英文方案頁:%s %v", w.Body.String(), w.Header())
	}
	for _, path := range []string{"/zh.html", "/pricing.zh.html", "/pricing.html", "/pricing/", "/og.html", "/README.md", "/index.html", "/nope", "/../server.go"} {
		if w := get(path); w.Code == 200 {
			t.Fatalf("%s 不能對外:%d %s", path, w.Code, w.Body.String())
		}
	}
	if w := get("/health"); w.Code != 200 || w.Body.String() != "ok" {
		t.Fatal("不能影響 /health")
	}

	if mountSite(http.NewServeMux(), "") || mountSite(http.NewServeMux(), filepath.Join(dir, "沒有")) {
		t.Fatal("沒設或資料夾不存在就不掛")
	}
}

// 寫一個測試用的網站資料夾(含 404.html)
func writeTestSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	big := strings.Repeat("<p>omixor agents claim tasks and report back.</p>\n", 200) // 夠大,壓縮後明顯變小
	files := map[string]string{
		"index.html":   "<!doctype html><html lang=\"en\"><title>omixor</title>" + big + "</html>",
		"pricing.html": "<!doctype html><html lang=\"en\"><title>Pricing</title></html>",
		"favicon.svg":  "<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "og.png": "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 500),
		"robots.txt": "User-agent: *", "sitemap.xml": "<urlset/>", "llms.txt": "omixor",
		"404.html": "<!doctype html><html lang=\"en\"><meta name=\"robots\" content=\"noindex\"><h1>This page doesn't exist.</h1></html>",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("不是 gzip:%v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// 文字檔:帶 Accept-Encoding: gzip 回壓縮版,沒帶回原文;都有 Vary;png 不壓;HEAD 照樣可以;原本的標頭都還在
func TestSiteGzip(t *testing.T) {
	dir := writeTestSite(t)
	mux := http.NewServeMux()
	if !mountSite(mux, dir) {
		t.Fatal("要掛上")
	}
	do := func(method, path, ae string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		mux.ServeHTTP(w, r)
		return w
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	for _, path := range []string{"/", "/pricing", "/favicon.svg", "/robots.txt", "/sitemap.xml", "/llms.txt"} {
		w := do("GET", path, "gzip, deflate, br")
		h := w.Header()
		if w.Code != 200 || h.Get("Content-Encoding") != "gzip" || h.Get("Vary") != "Accept-Encoding" {
			t.Fatalf("%s 要壓縮:%d %v", path, w.Code, h)
		}
		if h.Get("Content-Type") == "" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") == "" || h.Get("Cache-Control") == "" {
			t.Fatalf("%s 標頭不見了:%v", path, h)
		}
		if h.Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
			t.Fatalf("%s Content-Length 要是壓縮後的長度:%s vs %d", path, h.Get("Content-Length"), w.Body.Len())
		}
		got := gunzip(t, w.Body.Bytes())
		want, _ := os.ReadFile(filepath.Join(dir, sitePublicFiles[path]))
		if got != string(want) {
			t.Fatalf("%s 解壓後內容不對", path)
		}
	}
	w := do("GET", "/", "gzip")
	if w.Body.Len() >= len(raw) || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("首頁壓縮後要變小、頁面標頭要在:%d/%d %v", w.Body.Len(), len(raw), w.Header())
	}
	// 沒帶、q=0、只接受其他編碼:回原文,但仍有 Vary
	for _, ae := range []string{"", "identity", "gzip;q=0", "br"} {
		w := do("GET", "/", ae)
		if w.Code != 200 || w.Header().Get("Content-Encoding") != "" || w.Header().Get("Vary") != "Accept-Encoding" || w.Body.String() != string(raw) {
			t.Fatalf("Accept-Encoding %q 要回原文:%v", ae, w.Header())
		}
	}
	// png 不壓、不加 Vary
	if w := do("GET", "/og.png", "gzip"); w.Code != 200 || w.Header().Get("Content-Encoding") != "" || w.Header().Get("Vary") != "" || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("png 不壓:%v", w.Header())
	}
	// HEAD:沒有內容,但標頭(含 Content-Encoding、Content-Length)同 GET
	g := do("GET", "/", "gzip")
	hd := do("HEAD", "/", "gzip")
	if hd.Code != 200 || hd.Body.Len() != 0 || hd.Header().Get("Content-Encoding") != "gzip" || hd.Header().Get("Content-Length") != g.Header().Get("Content-Length") {
		t.Fatalf("HEAD:%d %d %v", hd.Code, hd.Body.Len(), hd.Header())
	}
	// 帶 Range 又接受 gzip:回整份壓縮版(不分段)
	{
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		r.Header.Set("Range", "bytes=0-9")
		mux.ServeHTTP(w, r)
		if w.Code != 200 || gunzip(t, w.Body.Bytes()) != string(raw) {
			t.Fatalf("gzip + Range 要回整份:%d", w.Code)
		}
	}
	if hd := do("HEAD", "/sitemap.xml", ""); hd.Code != 200 || hd.Body.Len() != 0 || hd.Header().Get("Content-Length") != "9" {
		t.Fatalf("HEAD 原文:%v", hd.Header())
	}
}

// 找不到的頁面回 404.html(狀態 404、可壓縮、HEAD 沒內容);/api、/mcp、/health、既有路由、非 GET 方法行為不變
func TestSiteNotFoundPage(t *testing.T) {
	dir := writeTestSite(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) })
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "api 404", 404) }))
	mux.Handle("/mcp/agent", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("mcp")) }))
	mux.HandleFunc("POST /hook", func(w http.ResponseWriter, r *http.Request) {})
	if !mountSite(mux, dir) {
		t.Fatal("要掛上")
	}
	h := siteNotFound(mux, dir)
	do := func(method, path, ae string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, nil)
		if ae != "" {
			r.Header.Set("Accept-Encoding", ae)
		}
		h.ServeHTTP(w, r)
		return w
	}
	page, _ := os.ReadFile(filepath.Join(dir, "404.html"))
	for _, path := range []string{"/nope", "/zh.html", "/404.html", "/pricing/", "/index.html", "/a/b/c"} {
		w := do("GET", path, "")
		if w.Code != 404 || w.Body.String() != string(page) || w.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s 要回 404 頁:%d %v %q", path, w.Code, w.Header(), w.Body.String())
		}
	}
	if w := do("GET", "/nope", "gzip"); w.Code != 404 || w.Header().Get("Content-Encoding") != "gzip" || gunzip(t, w.Body.Bytes()) != string(page) {
		t.Fatalf("404 頁也要壓縮:%v", w.Header())
	}
	if w := do("HEAD", "/nope", ""); w.Code != 404 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(page)) {
		t.Fatalf("HEAD 404:%d %v", w.Code, w.Header())
	}
	// 不變的:API、MCP、健康檢查、既有頁面、其他方法、405
	for _, path := range []string{"/api/nope", "/mcp/nope", "/mcp", "/api", "/mcp/agent/x"} {
		w := do("GET", path, "")
		raw := httptest.NewRecorder()
		mux.ServeHTTP(raw, httptest.NewRequest("GET", path, nil))
		if w.Code != raw.Code || w.Body.String() != raw.Body.String() || strings.Contains(w.Body.String(), "This page doesn't exist") {
			t.Fatalf("%s 要維持原本行為:%d %q(原本 %d %q)", path, w.Code, w.Body.String(), raw.Code, raw.Body.String())
		}
	}
	if w := do("GET", "/api/nope", ""); w.Body.String() != "api 404\n" {
		t.Fatalf("/api/ 的 404 不能被換掉:%q", w.Body.String())
	}
	for path, body := range map[string]string{"/health": "ok", "/api/version": "{}", "/mcp/agent": "mcp"} {
		if w := do("GET", path, ""); w.Code != 200 || w.Body.String() != body {
			t.Fatalf("%s 不能受影響:%d %q", path, w.Code, w.Body.String())
		}
	}
	if w := do("GET", "/", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>omixor</title>") {
		t.Fatal("首頁不能受影響")
	}
	if w := do("POST", "/nope", ""); w.Code != 404 || strings.Contains(w.Body.String(), "This page") {
		t.Fatalf("POST 不換頁:%d %q", w.Code, w.Body.String())
	}
	if w := do("GET", "/hook", ""); w.Code != 405 {
		t.Fatalf("405 維持原樣:%d", w.Code)
	}
	if w := do("DELETE", "/", ""); w.Code != 405 {
		t.Fatalf("405 維持原樣:%d", w.Code)
	}

	// 沒有 404.html:原樣回傳 mux
	empty := t.TempDir()
	_ = os.WriteFile(filepath.Join(empty, "index.html"), []byte("x"), 0o644)
	m2 := http.NewServeMux()
	mountSite(m2, empty)
	if got := siteNotFound(m2, empty); got != http.Handler(m2) {
		t.Fatal("沒有 404.html 要回原本的 mux")
	}
}
