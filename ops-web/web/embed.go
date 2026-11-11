// Package webui 把 React 建好的檔案(web/dist)包進 Go 執行檔。先 npm run build 再 go build。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func Dist() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}
