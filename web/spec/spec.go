// Package spec 放 Go 版必須和 Swift 版一模一樣的東西:MCP 的 initialize 回應、tools/list 的工具定義。
// 全部從 Swift 版匯出(見 dump_spec.py),不要手改。
package spec

import _ "embed"

//go:embed tools.json
var ToolsJSON []byte

//go:embed initialize.json
var InitializeJSON []byte

