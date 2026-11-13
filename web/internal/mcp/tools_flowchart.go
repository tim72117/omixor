package mcp

import coremcp "omixor-core/mcp"

// 流程圖工具(flowchart_render)在核心(omixor-core/mcp/tools_flowchart.go,排版與渲染在 omixor-core/tools/flowchart)。
// spec/tools.json 是 Swift 版匯出的規格,沒有這個工具,所以由核心連規格一起加到 /mcp(RegisterFlowchartTools 會用 Server.Add)。
// 存文件時走同一個 Server 的 doc_save;角色端點 /mcp/agent 用的是核心的工具表,已經有了。
func init() {
	coremcp.RegisterFlowchartTools(srv)
}
