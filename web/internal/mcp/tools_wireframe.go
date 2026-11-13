package mcp

import coremcp "omixor-core/mcp"

// 線框圖工具(wireframe_render)在核心(omixor-core/mcp/tools_wireframe.go,渲染器在 omixor-core/tools/wireframe)。
// spec/tools.json 是 Swift 版匯出的規格,沒有這個工具,所以由核心連規格一起加到 /mcp(RegisterWireframeTools 會用 Server.Add)。
// 存文件時走同一個 Server 的 doc_save(本機模式存本機核心,遠端模式存到雲端);角色端點 /mcp/agent 用的是核心的工具表,已經有了。
func init() {
	coremcp.RegisterWireframeTools(srv)
}
