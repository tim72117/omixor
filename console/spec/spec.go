// Package spec 放 Go 版必須和 Swift 版一模一樣的東西:MCP 的 initialize 回應、web 自己的工具定義。
// 從 Swift 版匯出(見 dump_spec.py),不要手改;只有 Go 版才有的工具手寫在 tools_console.json。
//
// tools.json 只有 web 自己的工具(範例資料、語音、網站訪客、GitHub、GCP、Facebook、server),和 Swift App 自己的工具同一份定義;
// 核心工具(Issue、角色與任務、文件櫃、組織鏈、頻道、事件、繪圖…,登記在 omixor-core/model 的 CoreTools)不放這裡,
// web 的 tools/list 直接用核心的 omixor-core/mcp.CoreToolDefs(見 internal/mcp/handler.go 的 webSpec)。
package spec

import _ "embed"

//go:embed tools.json
var ToolsJSON []byte

// ConsoleToolsJSON 只有 Go 版(console)才有的工具(例如 3D 虛擬辦公室 office_*)。
// 和 tools.json 分開放,因為 tools.json 是從 Swift 版匯出的(dump_spec.py 會整份覆蓋);這份手寫,Swift 版沒有。
//
//go:embed tools_console.json
var ConsoleToolsJSON []byte

//go:embed initialize.json
var InitializeJSON []byte
