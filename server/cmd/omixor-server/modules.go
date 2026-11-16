//go:build !omixor_minimal

// 發行組合:這個入口程式要編進哪些選用模組(做法一,見 omixor-core/module)。一個模組一行 `import _`;
// 拿掉某一行,那個模組就完全不會編進去(路由 404、功能清單沒有它的功能)。
//
// 預設(go build ./cmd/…)編進全部模組,行為和以前一樣。
// 只要核心、不含任何選用模組:go build -tags omixor_minimal ./cmd/…(這個檔整個不編譯)。
// 要別的組合:複製這個檔,改 build tag 與 import 清單(build tag 只用在發行組合這一個檔)。
package main

import (
	_ "omixor-core/modules/integrations" // 程式碼倉庫:組織的 GitHub、GitLab 設定(/api/orgs/{org}/integrations/github|gitlab,功能 development.repos)
)
