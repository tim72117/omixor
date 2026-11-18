package server

import (
	"net/http"
	"os"
	"path/filepath"
)

// 對外的形象網站(repo 根目錄的 site/,英文版):omixor.io 的 / 與幾個公開檔案由 omixor server 直接提供。
//
//	OMIXOR_SITE_DIR   網站檔案所在的資料夾(Docker 映像裡是 /site,見 server/Dockerfile);沒設或資料夾不存在就不提供網站
//
// 只提供 sitePublicFiles 列出的檔案(白名單):site/ 裡的 zh.html、pricing.zh.html(中文對照,內部用)、og.html(og.png 的來源頁)、
// README.md 不對外;映像也只複製這些檔案。新增對外檔案時,這裡、server/Dockerfile、site/README.md 要一起改。
// 路由只有「/」與這些固定路徑,不會蓋到 /api/、/mcp/、/health。
var sitePublicFiles = map[string]string{
	"/":            "index.html",
	"/pricing":     "pricing.html",
	"/favicon.svg": "favicon.svg",
	"/og.png":      "og.png",
	"/robots.txt":  "robots.txt",
	"/sitemap.xml": "sitemap.xml",
	"/llms.txt":    "llms.txt",
}

var siteContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".txt":  "text/plain; charset=utf-8",
	".xml":  "application/xml; charset=utf-8",
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
		path := filepath.Join(dir, name)
		pattern := "GET " + route
		if route == "/" {
			pattern = "GET /{$}" // 只有根目錄;其他沒登記的路徑照樣 404
		}
		ct := siteContentTypes[filepath.Ext(name)]
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if ct != "" {
				h.Set("Content-Type", ct)
			}
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if filepath.Ext(name) == ".html" { // 頁面(首頁、/pricing):不給嵌入、快取短一點
				h.Set("X-Frame-Options", "DENY")
				h.Set("Cache-Control", "public, max-age=300")
			} else {
				h.Set("Cache-Control", "public, max-age=3600")
			}
			http.ServeFile(w, r, path)
		})
	}
	return true
}
