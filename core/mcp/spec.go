package mcp

import (
	"encoding/json"
)

// 核心自己的工具規格(tools/list)與 initialize 資訊:給核心掛載的端點用(AgentEndpoint、AdminEndpoint)。
// omixor-web 的 /mcp 仍用它自己從 Swift 版匯出的規格,不受這裡影響。
// 欄位、說明、參數照 web/spec/tools.json 的同名工具(多了 Issue 的 assignee)。

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func pStr(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func pBool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func pInt(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func pEnum(desc string, v ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": v}
}
func pStrs(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}
}
func pObjs(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "object"}}
}

// ToolDef 一個工具的規格
func ToolDef(name, title, desc string, readOnly bool, schema map[string]any) map[string]any {
	return map[string]any{
		"name": name, "title": title, "description": desc, "inputSchema": schema,
		"annotations": map[string]any{"title": title, "readOnlyHint": readOnly, "destructiveHint": false, "openWorldHint": false},
	}
}

// CoreToolDefs 核心提供的工具規格:組織、Issue、虛擬角色與任務
func CoreToolDefs() []any {
	prio := []string{"urgent", "high", "normal", "low"}
	return []any{
		ToolDef("ops_orgs", "列出組織", "列出目前能用的組織(current 是這個連線所在的組織)。", true, obj(map[string]any{})),
		ToolDef("ops_issue_create", "建立 Issue",
			"把發現的問題或待辦存成 Issue。內文用 Markdown,寫清楚現象、依據(數字、網址、時間範圍)與建議。建立前可先用 ops_issues 搜尋,避免重複。給 assignee(角色 id)會自動替該角色排一筆任務。", false,
			obj(map[string]any{
				"agent":    pStr("以虛擬角色身分工作時填角色 id 或名稱,Issue 會署名該角色"),
				"assignee": pStr("指派給的虛擬角色 id;會自動替它排任務(角色必須存在)"),
				"body":     pStr("內文(Markdown)"),
				"labels":   pStrs("標籤,例如 [\"evoleap\", \"訪客\"]"),
				"link":     pStr("相關網址"),
				"priority": pEnum("優先度,預設 normal", prio...),
				"source":   pStr("從哪裡發現的,例如「網站訪客 7d」"),
				"title":    pStr("標題,一句話說明問題"),
			}, "title")),
		ToolDef("ops_issue_update", "更新 Issue",
			"更新 Issue:改標題、內文、優先度、標籤、指派,加留言,或關閉/重新打開。只給要改的欄位。", false,
			obj(map[string]any{
				"add_labels":  pStrs("加上的標籤"),
				"agent":       pStr("以虛擬角色身分工作時填角色 id 或名稱,留言與變更會署名該角色"),
				"append_body": pStr("接在內文後面的文字"),
				"assignee":    pStr("改指派給的角色 id(空字串 = 取消指派);會自動替新角色排任務、取消舊角色還沒領取的任務"),
				"body":        pStr("取代內文(Markdown)"),
				"comment":     pStr("加一則留言"),
				"id":          pStr("Issue id,例如 ISS-3"),
				"labels":      pStrs("取代全部標籤"),
				"priority":    pEnum("優先度", prio...),
				"status":      pEnum("open 重新打開,closed 關閉", "open", "closed"),
				"title":       pStr("新標題"),
			}, "id")),
		ToolDef("ops_issues", "查 Issue",
			"給 id 回傳完整內容(含留言);不給 id 則列出符合條件的 Issue(預設只列未解決,依優先度與時間排序)。", true,
			obj(map[string]any{
				"id":     pStr("Issue id"),
				"label":  pStr("只列有這個標籤的"),
				"limit":  pInt("最多幾筆,預設 30"),
				"query":  pStr("標題、內文或留言包含這段文字"),
				"status": pEnum("open、closed 或 all,預設 open", "open", "closed", "all"),
			})),
		ToolDef("ops_agents", "列出虛擬角色",
			"列出組織的虛擬角色:id、名稱、職責、角色設定(instructions)、例行任務,以及各狀態的任務數。以角色身分工作前先讀它的 instructions。", true,
			obj(map[string]any{"id": pStr("只看這個角色")})),
		ToolDef("ops_agent_context", "角色能讀取的資訊",
			"用角色 id 或名稱,一次取得這個角色工作時能讀取的資訊:角色設定、未完成的任務;端點有掛文件櫃時,還有能存取的區塊與文件(依角色的 docAccess)。", true,
			obj(map[string]any{
				"agent":          pStr("角色 id 或名稱"),
				"include_bodies": pBool("附上文件全文,預設 false"),
			}, "agent")),
		ToolDef("ops_agent_tasks", "查角色任務",
			"給 id 回傳任務完整內容;否則列出任務(預設未完成的:待領取、進行中、需要協助)。", true,
			obj(map[string]any{
				"agent":  pStr("只列這個角色的"),
				"id":     pStr("任務 id,例如 T-3"),
				"status": pEnum("open(預設)、all 或單一狀態", "open", "all", "queued", "inProgress", "blocked", "done", "canceled"),
			})),
		ToolDef("ops_agent_task_claim", "以角色身分領取任務",
			"以某個虛擬角色的身分領取任務(狀態改成進行中)。給 id 就領那筆;不給就領該角色最優先的待領取任務。回傳角色設定、任務說明與一組綁定這個任務的角色憑證(credential;密鑰只這一次看得到),用它連角色端點以該角色身分工作。沒有任務時 task 為 null。", false,
			obj(map[string]any{
				"agent": pStr("角色 id 或名稱"),
				"id":    pStr("任務 id(選填)"),
			}, "agent")),
		ToolDef("ops_agent_task_report", "回報任務",
			"回報任務進度或結果。note 是過程紀錄;result 是最終結果(Markdown);做完設 status=done,卡住需要人決定設 blocked 並在 note 說明。done、blocked 之後角色憑證立刻失效。", false,
			obj(map[string]any{
				"id":     pStr("任務 id"),
				"links":  pStrs("相關的 Issue id 或網址"),
				"note":   pStr("過程紀錄"),
				"result": pStr("結果(Markdown)"),
				"status": pEnum("新狀態", "inProgress", "blocked", "done"),
			}, "id")),
		ToolDef("ops_agent_upsert", "新增或修改角色",
			"新增虛擬角色,或修改既有角色(給 id 或名稱)。只給要改的欄位。routines 會取代全部例行任務(到時間會自動替角色排任務);add_routines 是加在後面。例行任務格式:{title, instructions, kind: daily|weekly|hourly, hour, minute, weekday(1=週日…7=週六), every_hours, enabled}。", false,
			obj(map[string]any{
				"active":       pBool("啟用"),
				"add_routines": pObjs("加上的例行任務"),
				"color":        pEnum("顏色", "blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"),
				"doc_access": map[string]any{"type": "object", "description": "文件櫃存取範圍,會合併進現有設定:key 是區塊 id 或頁籤網址,值是 read、write 或 none(移除)。",
					"additionalProperties": map[string]any{"type": "string", "enum": []string{"none", "read", "write"}}},
				"id":           pStr("要修改的角色 id 或名稱;新增時可指定英文 id(例如 product-manager),不給就自動產生"),
				"instructions": pStr("角色設定(給 AI 的說明,Markdown)"),
				"name":         pStr("名稱(新增時必填)"),
				"role":         pStr("一句話職責"),
				"routines":     pObjs("取代全部例行任務"),
				"symbol":       pStr("圖示名稱"),
			})),
		ToolDef("ops_agent_task_create", "指派任務給角色",
			"建立一筆任務指派給虛擬角色,等它被領取(組織管理員)。assign_to 是接任務的角色。", false,
			obj(map[string]any{
				"agent":        pStr("(忽略)署名一律是呼叫者"),
				"assign_to":    pStr("接這個任務的角色 id 或名稱"),
				"instructions": pStr("任務說明(Markdown)"),
				"priority":     pEnum("優先度,預設 normal", prio...),
				"title":        pStr("標題"),
			}, "assign_to", "title")),
	}
}

// CoreInitialize initialize 的回應內容(protocolVersion 由協定層決定)
func CoreInitialize(instructions string) map[string]any {
	return map[string]any{
		"capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
		"instructions": instructions,
		"serverInfo":   map[string]any{"name": "omixor", "title": "omixor", "version": "0.1.0"},
	}
}

// NewCoreServer 用核心的工具規格建立協定處理器,並登記組織、Issue、虛擬角色工具的實作
func NewCoreServer(instructions string) *Server {
	tj, _ := json.Marshal(CoreToolDefs())
	ij, _ := json.Marshal(CoreInitialize(instructions))
	s, err := NewServer(tj, ij)
	if err != nil {
		panic(err)
	}
	RegisterOrgTools(s)
	RegisterIssueTools(s)
	RegisterAgentTools(s)
	return s
}
