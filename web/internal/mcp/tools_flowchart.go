package mcp

import coremcp "omixor-core/mcp"

// 流程圖工具(flowchart_render)在核心(omixor-core/mcp/tools_flowchart.go,排版與渲染在 omixor-core/tools/flowchart)。
// 規格和其他核心工具一樣取自 coremcp.CoreToolDefs(見 handler.go 的 webSpec),這裡只登記實作。
// 存文件時走同一個 Server 的 doc_save;角色端點 /mcp/agent 用的是核心的工具表,已經有了。
func init() {
	coremcp.RegisterFlowchartTools(srv)
}
