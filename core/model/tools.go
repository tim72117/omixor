package model

// 核心 MCP 工具的登記表:每個工具只在這裡登記一次。
//   - 標題:工具規格(mcp 的 tools/list)的 title
//   - 端點:Admin 管理端點(/mcp/org/{org}、web 的 /mcp)、Agent 角色端點(/mcp/agent)、Worker 執行者端點(/mcp/worker/{org})
//   - Needs:角色端點的開放條件(角色設定不符合時 tools/list 不列,見 service.AgentTools)
//   - ReadOnly:唯讀授權(OAuth)可用的工具;一定要是純讀取,新增工具預設不是
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
	// ReadOnly 純讀取、沒有任何寫入副作用(包含「讀了算已讀」這種):唯讀授權(OAuth 的 mcp:read)只開放這些。預設 false
	ReadOnly bool
}

// CoreTools 全部核心工具(順序就是 tools/list 與畫面的順序)
var CoreTools = []CoreTool{
	{Name: "orgs", Title: "列出組織", Group: "組織", Admin: true, ReadOnly: true},
	{Name: "schema_check", Title: "檢核資料庫結構", Group: "組織", Admin: true, ReadOnly: true},

	{Name: "issues", Title: "查 Issue", Group: "Issue", Admin: true, Agent: true, ReadOnly: true},
	{Name: "issue_create", Title: "建立 Issue", Group: "Issue", Admin: true, Agent: true},
	{Name: "report_problem", Title: "回報系統問題", Group: "Issue", Admin: true},
	{Name: "issue_update", Title: "更新 Issue", Group: "Issue", Admin: true, Agent: true},
	{Name: "issue_accept", Title: "接受或放回 Issue", Group: "Issue", Admin: true, Agent: true},

	{Name: "agents", Title: "列出虛擬角色", Group: "角色與任務", Admin: true, ReadOnly: true},
	{Name: "agent_context", Title: "角色能讀取的資訊", Group: "角色與任務", Admin: true, Agent: true},
	{Name: "agent_tasks", Title: "查角色任務", Group: "角色與任務", Admin: true, Agent: true, ReadOnly: true},
	{Name: "agent_task_claim", Title: "以角色身分領取任務", Group: "角色與任務", Admin: true},
	{Name: "agent_task_report", Title: "回報任務", Group: "角色與任務", Admin: true, Agent: true},
	{Name: "agent_upsert", Title: "新增或修改角色", Group: "角色與任務", Admin: true},
	{Name: "agent_task_create", Title: "指派任務給角色", Group: "角色與任務", Admin: true},
	{Name: "agent_task_transfer", Title: "轉移角色的任務", Group: "角色與任務", Admin: true},
	{Name: "agent_task_delegate", Title: "依組織鏈委派任務", Group: "角色與任務", Agent: true, Needs: NeedDownstream},

	{Name: "projects", Title: "列出專案", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocRead, ReadOnly: true},
	{Name: "project_save", Title: "新增或修改專案", Group: "文件櫃", Admin: true},
	{Name: "cabinet", Title: "文件櫃結構", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocRead, ReadOnly: true},
	{Name: "docs", Title: "查文件", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocRead, ReadOnly: true},
	{Name: "doc_save", Title: "存文件到文件櫃", Group: "文件櫃", Admin: true, Agent: true, Needs: NeedDocWrite},
	{Name: "cabinet_layout_save", Title: "修改文件櫃結構", Group: "文件櫃", Admin: true},

	{Name: "chain", Title: "組織鏈", Group: "組織鏈", Admin: true, ReadOnly: true},
	{Name: "chain_save", Title: "修改組織鏈", Group: "組織鏈", Admin: true},

	{Name: "channels", Title: "列出對話頻道", Group: "訊息頻道", Admin: true, Agent: true, Needs: NeedChannel, ReadOnly: true},
	{Name: "channel_create", Title: "建立對話頻道", Group: "訊息頻道", Admin: true},
	{Name: "channel_messages", Title: "讀頻道訊息", Group: "訊息頻道", Admin: true, Agent: true, Needs: NeedChannel},
	{Name: "channel_post", Title: "在頻道發訊息", Group: "訊息頻道", Admin: true, Agent: true, Needs: NeedChannel},

	{Name: "incidents", Title: "查事件", Group: "事件", Admin: true, ReadOnly: true},
	{Name: "incident_declare", Title: "宣告事件", Group: "事件", Admin: true},
	{Name: "incident_update", Title: "更新事件", Group: "事件", Admin: true},

	{Name: "issue_decision_add", Title: "提出待決定或需要協助", Group: "待決定", Admin: true, Agent: true},
	{Name: "issue_decisions", Title: "查待決定與需要協助", Group: "待決定", Admin: true, Agent: true, ReadOnly: true},
	{Name: "issue_decision_answer", Title: "處理待決定或需要協助", Group: "待決定", Admin: true},
	{Name: "issue_decision_to_issue", Title: "由待決定開出問題 Issue", Group: "待決定", Admin: true},

	{Name: "issue_item_add", Title: "新增 Issue 子項", Group: "子項", Admin: true, Agent: true},
	{Name: "issue_items", Title: "查 Issue 子項", Group: "子項", Admin: true, Agent: true, ReadOnly: true},
	{Name: "issue_item_update", Title: "修改或取消 Issue 子項", Group: "子項", Admin: true},
	{Name: "issue_item_claim", Title: "領取 Issue 子項", Group: "子項", Admin: true},
	{Name: "issue_item_report", Title: "回報 Issue 子項", Group: "子項", Admin: true, Agent: true},
	{Name: "issue_item_promote", Title: "把子項提升為 Issue", Group: "子項", Admin: true},

	{Name: "wireframe_render", Title: "繪製線框圖", Group: "設計工具", Admin: true, Agent: true},
	{Name: "flowchart_render", Title: "繪製流程圖", Group: "設計工具", Admin: true, Agent: true, ReadOnly: true},

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

// OAuthWriteTools OAuth 寫入授權(scope mcp:write)額外開放的工具:只有協作類寫入,明確列在這裡(不是「所有非唯讀」);
// 實際可用 = ReadOnly ∪ OAuthWriteTools,而且 service 層照既有授權表檢查(成員角色、受限專案),OAuth 不繞過任何一項,也不是伺服器管理員(Principal.ServerAdmin)。
// 新增 Admin 工具時必須分類:ReadOnly、這裡、或 OAuthForbiddenTools(model 的測試會檢查)
var OAuthWriteTools = map[string]bool{
	"issue_create": true, "issue_update": true, "issue_accept": true, "report_problem": true,
	"issue_decision_add": true, "issue_decision_answer": true, "issue_decision_to_issue": true,
	"issue_item_add": true, "issue_item_update": true, "issue_item_report": true, "issue_item_promote": true,
	"doc_save":       true,
	"channel_create": true, "channel_post": true,
	"agent_task_create": true, "agent_task_report": true,
}

// OAuthForbiddenTools 明確不開放給 OAuth(含 mcp:write)的 Admin 工具與原因(只是紀錄與測試用;開放與否以 OAuthWriteTools 為準)
var OAuthForbiddenTools = map[string]string{
	"agent_task_claim":    "會簽發角色憑證(可用該角色身分工作),OAuth 不發憑證",
	"issue_item_claim":    "負責人是角色時會自動建立任務並簽發角色憑證",
	"agent_upsert":        "角色與組織結構管理",
	"agent_task_transfer": "轉移角色任務(角色與任務歸屬管理),先不開放",
	"chain_save":          "組織鏈(組織結構)管理",
	"project_save":        "專案與專案成員、可見性管理",
	"cabinet_layout_save": "文件櫃結構管理",
	"incident_declare":    "事件管理,先不開放",
	"incident_update":     "事件管理,先不開放",
	"agent_context":       "角色的完整設定與文件內容,未登記為唯讀,先不開放",
	"channel_messages":    "讀了會標記已讀(有寫入副作用),先不開放",
	"wireframe_render":    "未登記為唯讀(可存檔),先不開放",
}
