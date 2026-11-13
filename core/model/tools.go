package model

// 核心 MCP 工具的登記表:每個工具只在這裡登記一次。
//   - 標題:工具規格(mcp 的 tools/list)的 title
//   - 端點:Admin 管理端點(/mcp/org/{org}、web 的 /mcp)、Agent 角色端點(/mcp/agent)、Worker 執行者端點(/mcp/worker/{org})
//   - Needs:角色端點的開放條件(角色設定不符合時 tools/list 不列,見 service.AgentTools)
// 工具的參數與說明在 mcp 套件(ToolDefs);mcp 的測試會檢查兩邊的名稱一致。

// 角色端點的開放條件
const (
	NeedDocRead    = "docRead"    // 文件權限至少有一個位置
	NeedDocWrite   = "docWrite"   // 文件權限至少有一個位置可寫
	NeedDownstream = "downstream" // 組織鏈上有下游角色
	NeedChannel    = "channel"    // 是任何一個頻道的成員
)

// CoreTool 一個核心工具
type CoreTool struct {
	Name   string
	Title  string
	Group  string
	Admin  bool
	Agent  bool
	Worker bool
	Needs  string
}

// CoreTools 全部核心工具(順序就是 tools/list 與畫面的順序)
var CoreTools = []CoreTool{
	{Name: "orgs", Title: "列出組織", Group: "組織", Admin: true},

	{Name: "issues", Title: "查 Issue", Group: "Issue", Admin: true, Agent: true},
	{Name: "issue_create", Title: "建立 Issue", Group: "Issue", Admin: true, Agent: true},
	{Name: "issue_update", Title: "更新 Issue", Group: "Issue", Admin: true, Agent: true},

	{Name: "agents", Title: "列出虛擬角色", Group: "角色與任務", Admin: true},
	{Name: "agent_context", Title: "角色能讀取的資訊", Group: "角色與任務", Admin: true, Agent: true},
	{Name: "agent_tasks", Title: "查角色任務", Group: "角色與任務", Admin: true, Agent: true},
	{Name: "agent_task_claim", Title: "以角色身分領取任務", Group: "角色與任務", Admin: true},
	{Name: "agent_task_report", Title: "回報任務", Group: "角色與任務", Admin: true, Agent: true},
	{Name: "agent_upsert", Title: "新增或修改角色", Group: "角色與任務", Admin: true},
	{Name: "agent_task_create", Title: "指派任務給角色", Group: "角色與任務", Admin: true},
	{Name: "agent_task_delegate", Title: "依組織鏈委派任務", Group: "角色與任務", Agent: true, Needs: NeedDownstream},

	{Name: "cabinet", Title: "文件櫃結構", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocRead},
	{Name: "docs", Title: "查文件", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocRead},
	{Name: "doc_save", Title: "存文件到文件櫃", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocWrite},

	{Name: "chain", Title: "組織鏈", Group: "組織鏈", Admin: true},
	{Name: "chain_save", Title: "修改組織鏈", Group: "組織鏈", Admin: true},

	{Name: "channels", Title: "列出對話頻道", Group: "訊息頻道", Admin: true, Agent: true, Needs: NeedChannel},
	{Name: "channel_create", Title: "建立對話頻道", Group: "訊息頻道", Admin: true},
	{Name: "channel_messages", Title: "讀頻道訊息", Group: "訊息頻道", Admin: true, Agent: true, Needs: NeedChannel},
	{Name: "channel_post", Title: "在頻道發訊息", Group: "訊息頻道", Admin: true, Agent: true, Needs: NeedChannel},

	{Name: "incidents", Title: "查事件", Group: "事件", Admin: true},
	{Name: "incident_declare", Title: "宣告事件", Group: "事件", Admin: true},
	{Name: "incident_update", Title: "更新事件", Group: "事件", Admin: true},

	{Name: "wireframe_render", Title: "繪製線框圖", Group: "設計工具", Admin: true, Agent: true},
	{Name: "flowchart_render", Title: "繪製流程圖", Group: "設計工具", Admin: true, Agent: true},

	{Name: "dispatch_queue", Title: "列出可領的任務", Group: "派發", Worker: true},
	{Name: "dispatch_claim", Title: "領取任務", Group: "派發", Worker: true},
	{Name: "dispatch_status", Title: "我領取中的任務", Group: "派發", Worker: true},
	{Name: "dispatch_report", Title: "以角色身分回報任務", Group: "派發", Worker: true},
	{Name: "dispatch_comment", Title: "以角色身分在 Issue 留言", Group: "派發", Worker: true},
	{Name: "dispatch_release", Title: "放棄領取", Group: "派發", Worker: true},
	{Name: "dispatch_delegate", Title: "依組織鏈委派", Group: "派發", Worker: true},
}

// CoreToolByName 找登記的核心工具
func CoreToolByName(name string) (CoreTool, bool) {
	for _, t := range CoreTools {
		if t.Name == name {
			return t, true
		}
	}
	return CoreTool{}, false
}

// CoreToolSet 符合條件的工具名稱
func CoreToolSet(keep func(CoreTool) bool) map[string]bool {
	out := map[string]bool{}
	for _, t := range CoreTools {
		if keep(t) {
			out[t.Name] = true
		}
	}
	return out
}
