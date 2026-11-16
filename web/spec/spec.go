// Package spec 放 Go 版必須和 Swift 版一模一樣的東西:MCP 的 initialize 回應、web 自己的工具定義。
// 從 Swift 版匯出(見 dump_spec.py),不要手改。
//
// tools.json 只有 web 自己的工具(範例資料、語音、網站訪客、GitHub、GCP、Facebook、server),和 Swift App 自己的工具同一份定義;
// 核心工具(Issue、角色與任務、文件櫃、組織鏈、頻道、事件、繪圖…,登記在 omixor-core/model 的 CoreTools)不放這裡,
// web 的 tools/list 直接用核心的 omixor-core/mcp.CoreToolDefs(見 internal/mcp/handler.go 的 webSpec)。
package spec

import _ "embed"

//go:embed tools.json
var ToolsJSON []byte

//go:embed initialize.json
var InitializeJSON []byte
