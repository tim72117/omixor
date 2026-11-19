package office

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
)

// 靜態檔:/office/ 底下只開放檢視器與它要載入的資料(白名單),其他(.blend、.py、renders…)一律 404。
// 讀檔用 os.Root,符號連結也跑不出 office3d 資料夾。檔案每次從磁碟讀,不嵌進執行檔。

var staticFiles = map[string]string{
	"office.glb":        "model/gltf-binary",
	"characters.glb":    "model/gltf-binary",
	"navgrid.json":      "application/json; charset=utf-8",
	"places.json":       "application/json; charset=utf-8",
	"viewer/index.html": "text/html; charset=utf-8",
}

var viewerJS = regexp.MustCompile(`^viewer/js/[A-Za-z0-9_-]+\.js$`)

// staticPath 請求路徑(/office/ 之後的部分)→ 檔案相對路徑與 Content-Type;不在白名單回 ok = false
func staticPath(p string) (string, string, bool) {
	if p == "viewer/" {
		p = "viewer/index.html"
	}
	if strings.Contains(p, "\\") || strings.Contains(p, "\x00") || path.Clean("/"+p) != "/"+p {
		return "", "", false
	}
	if ct, ok := staticFiles[p]; ok {
		return p, ct, true
	}
	if viewerJS.MatchString(p) {
		return p, "text/javascript; charset=utf-8", true
	}
	return "", "", false
}

// ServeStatic 處理 GET /office 與 /office/…
func (h *Hub) ServeStatic(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/office", "/office/", "/office/viewer":
		http.Redirect(w, r, "/office/viewer/", http.StatusFound)
		return
	}
	rel, ct, ok := staticPath(strings.TrimPrefix(r.URL.Path, "/office/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	dir, err := h.src.Dir()
	if err != nil {
		http.Error(w, "虛擬辦公室:"+err.Error(), http.StatusNotFound)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		h.src.logfn("虛擬辦公室:開不了 %s:%v", dir, err)
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			h.src.logfn("虛擬辦公室:讀 %s 失敗:%v", rel, err)
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", ct)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "no-cache") // 改了 office3d 重新整理就看得到(仍可用 Last-Modified 回 304)
	http.ServeContent(w, r, rel, st.ModTime(), f)
}
