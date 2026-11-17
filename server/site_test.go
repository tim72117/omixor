package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 形象網站:只提供白名單檔案(英文首頁與公開檔案),zh.html、og.html、README.md 不對外;不影響 /health;沒設資料夾就不提供
func TestSiteServesOnlyPublicFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"index.html":  "<!doctype html><html lang=\"en\"><title>omixor</title></html>",
		"favicon.svg": "<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "og.png": "\x89PNG\r\n\x1a\n",
		"robots.txt": "User-agent: *", "sitemap.xml": "<urlset/>", "llms.txt": "omixor",
		"zh.html": "<html lang=\"zh-Hant-TW\"></html>", "og.html": "<html></html>", "README.md": "# site",
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
		"/": "text/html; charset=utf-8", "/favicon.svg": "image/svg+xml", "/og.png": "image/png",
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
	for _, path := range []string{"/zh.html", "/og.html", "/README.md", "/index.html", "/nope", "/../server.go"} {
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
