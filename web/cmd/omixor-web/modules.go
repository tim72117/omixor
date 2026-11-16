//go:build !omixor_minimal

// 發行組合:omixor-web 要編進哪些選用模組(做法一,見 internal/core/modules.go 與 omixor-core/module)。一個模組一行 `import _`;
// 拿掉某一行,那個模組就完全不會編進去(view、action、MCP 工具、核心的路由與功能都沒有,畫面依功能清單與 modules 隱藏)。
//
// 預設(go build ./cmd/omixor-web)編進全部模組,行為和以前一樣。
// 只要核心、不含任何選用模組:go build -tags omixor_minimal ./cmd/omixor-web(這個檔整個不編譯)。
// 要別的組合:複製這個檔,改 build tag 與 import 清單(build tag 只用在發行組合這一個檔)。
package main

import (
	_ "omixor-web/internal/modules/integrations" // 程式碼倉庫:GitHub、GitLab(PR、Actions、MR、Pipeline、設定 › 程式碼倉庫、github* MCP 工具)
)
