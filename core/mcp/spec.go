package mcp

import (
	"encoding/json"

	"omixor-core/model"
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

// coreDef 核心工具的規格:標題取自登記表(model.CoreTools);沒登記的名稱直接 panic(啟動時就會發現)
func coreDef(name, desc string, readOnly bool, schema map[string]any) map[string]any {
	t, ok := model.CoreToolByName(name)
	if !ok {
		panic("mcp: 工具 " + name + " 沒有登記在 model.CoreTools")
	}
	return ToolDef(name, t.Title, desc, readOnly, schema)
}

// CoreToolDefs 核心提供的工具規格:組織、Issue、虛擬角色與任務、文件櫃
func CoreToolDefs() []any {
	prio := []string{"urgent", "high", "normal", "low"}
	defs := []any{
		coreDef("orgs", "列出目前能用的組織(current 是這個連線所在的組織)。", true, obj(map[string]any{})),
		coreDef("issue_create",
			"把發現的問題或待辦存成 Issue。內文用 Markdown,寫清楚現象、依據(數字、網址、時間範圍)與建議。建立前可先用 issues 搜尋,避免重複。給 assignee(角色 id)會自動替該角色排一筆任務。", false,
			obj(map[string]any{
				"agent":    pStr("以虛擬角色身分工作時填角色 id 或名稱,Issue 會署名該角色"),
				"assignee": pStr("指派給的虛擬角色 id;會自動替它排任務(角色必須存在)"),
				"body":     pStr("內文(Markdown)"),
				"labels":   pStrs("標籤,例如 [\"evoleap\", \"訪客\"]"),
				"link":     pStr("相關網址"),
				"priority": pEnum("優先度,預設 normal", prio...),
				"repo":     pStr("所屬程式碼倉庫,例如 owner/name(GitHub)或 group/sub/project(GitLab),可加 github:、gitlab: 前綴;只能用英數與 - _ . / :"),
				"source":   pStr("從哪裡發現的,例如「網站訪客 7d」"),
				"title":    pStr("標題,一句話說明問題"),
			}, "title")),
		coreDef("issue_update",
			"更新 Issue:改標題、內文、優先度、標籤、所屬倉庫、指派,加留言,或關閉/重新打開。只給要改的欄位。", false,
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
				"repo":        pStr("改所屬程式碼倉庫(owner/name 或 group/sub/project,可加 github:、gitlab: 前綴;空字串 = 清除標記)"),
				"status":      pEnum("open 重新打開,closed 關閉", "open", "closed"),
				"title":       pStr("新標題"),
			}, "id")),
		coreDef("issues",
			"給 id 回傳完整內容(含留言);不給 id 則列出符合條件的 Issue(預設只列未解決,依優先度與時間排序)。", true,
			obj(map[string]any{
				"id":     pStr("Issue id"),
				"label":  pStr("只列有這個標籤的"),
				"limit":  pInt("最多幾筆,預設 30"),
				"query":  pStr("標題、內文或留言包含這段文字"),
				"repo":   pStr("只列標記這個程式碼倉庫的(不分大小寫;github:、gitlab: 前綴一邊沒寫就忽略)"),
				"status": pEnum("open、closed 或 all,預設 open", "open", "closed", "all"),
			})),
		coreDef("agents",
			"列出組織的虛擬角色:id、名稱、職責、角色設定(instructions)、例行任務,以及各狀態的任務數。以角色身分工作前先讀它的 instructions。", true,
			obj(map[string]any{"id": pStr("只看這個角色")})),
		coreDef("agent_context",
			"用角色 id 或名稱,一次取得這個角色工作時能讀取的資訊:角色設定、未完成的任務、文件櫃中能存取的區塊與頁籤(read / write),能讀的文件清單,以及頻道裡提到它的未讀訊息與它所在頻道的未讀數(mentions)。include_bodies 為 true 時附上文件全文(最多 20 份,其餘只列清單)。文件權限依角色的 docAccess。", true,
			obj(map[string]any{
				"agent":          pStr("角色 id 或名稱"),
				"include_bodies": pBool("附上文件全文,預設 false"),
			}, "agent")),
		coreDef("agent_tasks",
			"給 id 回傳任務完整內容;否則列出任務(預設未完成的:待領取、進行中、需要協助)。", true,
			obj(map[string]any{
				"agent":  pStr("只列這個角色的"),
				"id":     pStr("任務 id,例如 T-3"),
				"status": pEnum("open(預設)、all 或單一狀態", "open", "all", "queued", "inProgress", "blocked", "done", "canceled"),
			})),
		coreDef("agent_task_claim",
			"以某個虛擬角色的身分領取任務(狀態改成進行中)。給 id 就領那筆;不給就領該角色最優先的待領取任務。回傳角色設定、任務說明與一組綁定這個任務的角色憑證(credential;密鑰只這一次看得到),用它連角色端點以該角色身分工作。沒有任務時 task 為 null。", false,
			obj(map[string]any{
				"agent": pStr("角色 id 或名稱"),
				"id":    pStr("任務 id(選填)"),
			}, "agent")),
		coreDef("agent_task_report",
			"回報任務進度或結果。note 是過程紀錄;result 是最終結果(Markdown);做完設 status=done,卡住需要人決定設 blocked 並在 note 說明。done、blocked 之後角色憑證立刻失效。", false,
			obj(map[string]any{
				"id":     pStr("任務 id"),
				"links":  pStrs("相關的 Issue id 或網址"),
				"note":   pStr("過程紀錄"),
				"result": pStr("結果(Markdown)"),
				"status": pEnum("新狀態", "inProgress", "blocked", "done"),
			}, "id")),
		coreDef("agent_upsert",
			"新增虛擬角色,或修改既有角色(給 id 或名稱)。只給要改的欄位。routines 會取代全部例行任務(到時間會自動替角色排任務);add_routines 是加在後面。例行任務格式:{title, instructions, kind: daily|weekly|hourly, hour, minute, weekday(1=週日…7=週六), every_hours, enabled}。", false,
			obj(map[string]any{
				"active":       pBool("啟用"),
				"add_routines": pObjs("加上的例行任務"),
				"color":        pEnum("顏色", "blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"),
				"doc_access": map[string]any{"type": "object", "description": "文件櫃存取範圍,會合併進現有設定:key 是區塊 id 或名稱、頁籤網址、名稱或代號(存成區塊 id 或頁籤網址),值是 read、write 或 none(移除)。頁籤的設定優先於區塊。",
					"additionalProperties": map[string]any{"type": "string", "enum": []string{"none", "read", "write"}}},
				"id":           pStr("要修改的角色 id 或名稱;新增時可指定英文 id(例如 product-manager),不給就自動產生"),
				"instructions": pStr("角色設定(給 AI 的說明,Markdown)"),
				"name":         pStr("名稱(新增時必填)"),
				"role":         pStr("一句話職責"),
				"routines":     pObjs("取代全部例行任務"),
				"symbol":       pStr("圖示名稱"),
			})),
		coreDef("agent_task_create",
			"建立一筆任務指派給虛擬角色,等它被領取(組織管理員)。assign_to 是接任務的角色。", false,
			obj(map[string]any{
				"agent":        pStr("(忽略)署名一律是呼叫者"),
				"assign_to":    pStr("接這個任務的角色 id 或名稱"),
				"instructions": pStr("任務說明(Markdown)"),
				"priority":     pEnum("優先度,預設 normal", prio...),
				"title":        pStr("標題"),
			}, "assign_to", "title")),
		coreDef("cabinet",
			"列出文件櫃的區塊與頁籤,以及各處有幾份文件。給 agent 時只列該角色能存取的位置並標出權限(read / write)。", true,
			obj(map[string]any{"agent": pStr("以角色身分查看時填角色 id 或名稱")})),
		coreDef("docs",
			"給 id 讀一份文件;否則依區塊、頁籤、關鍵字列出文件(最多 50 筆)。給 agent 時只看得到該角色有權限的文件。"+
				"讀長文件時建議分段讀,省上下文:先 outline 看大綱(各段的 id、標題、行號範圍、字數),再用 part 讀一段、from/to 讀幾行,或 grep 找關鍵字;"+
				"回傳的 version 在局部修改(doc_save 的 edits、replace_part、append_to_part)時帶回來。只給 id 時回全文,但超過 6000 字的文件只回大綱與開頭(要全文帶 full)。", true,
			obj(map[string]any{
				"agent":   pStr("以角色身分查看時填角色 id 或名稱"),
				"id":      pStr("文件 id,例如 DOC-3"),
				"query":   pStr("標題、內文或標籤包含這段文字"),
				"section": pStr("區塊 id 或名稱"),
				"tab":     pStr("頁籤網址、名稱或代號"),
				"outline": pBool("給 id 時只回大綱與版本"),
				"part":    pStr("給 id 時只讀這一段:大綱裡的 id(例如 s3)或標題"),
				"from":    pInt("給 id 時從第幾行讀(1 起算,含)"),
				"to":      pInt("給 id 時讀到第幾行(含)"),
				"grep":    pStr("給 id 時只回含這段文字的行(含行號與前後文)"),
				"context": pInt("grep 前後各幾行,預設 2"),
				"full":    pBool("給 id 時一定回全文(長文件也是)"),
			})),
		coreDef("doc_save",
			"新增文件(不給 id)或修改文件(給 id)。內容是純文字/Markdown,放在對應的區塊與頁籤。以角色身分工作時一定要帶 agent:只能寫到該角色「可讀寫」的位置(角色端點一律是憑證的角色)。"+
				"內文開頭可以有欄位(--- 之間一行一個 key: value),kind 是文件類型:有對應範本(kind: template、for: <類型> 的文件,內含 ```html 版面與 ```css 樣式)時,畫面依範本顯示,存檔前也會檢查範本的 required 欄位。"+
				"修改長文件時建議局部修改(edits、replace_part、append_to_part 三種擇一,不必送全文),回傳只有改到的那幾行與新版本。", false,
			obj(map[string]any{
				"agent":       pStr("以角色身分工作時填角色 id 或名稱"),
				"append_body": pStr("接在內文後面的文字"),
				"body":        pStr("取代內文(Markdown)"),
				"edits": map[string]any{"type": "array", "description": "局部修改:字串取代,依序套用;old 在全文必須剛好出現一次(或設 all),任何一個失敗就都不改",
					"items": obj(map[string]any{"old": pStr("要被取代的原文(多帶前後文讓它唯一)"), "new": pStr("新的文字"), "all": pBool("取代全部出現的地方")}, "old", "new")},
				"replace_part":   pStr("局部修改:整段替換這一段(大綱的 id 或標題),保留標題那一行;內容放 content"),
				"append_to_part": pStr("局部修改:接在這一段最後(大綱的 id 或標題);內容放 content,自帶標題就是在這段後面插入新段落"),
				"content":        pStr("replace_part、append_to_part 的內容(Markdown)"),
				"base_version":   pStr("局部修改時帶讀到的 version:版本不符時,edits 會在最新內文上再套一次,replace_part、append_to_part 會被拒絕"),
				"id":             pStr("要修改的文件 id;新增時不給"),
				"section":        pStr("區塊 id 或名稱(新增時必填,或只給 tab)"),
				"tab":            pStr("頁籤網址或名稱"),
				"tags":           pStrs("標籤"),
				"title":          pStr("標題(新增時必填)"),
			})),
	}
	defs = append(defs, ChainToolDefs()...)
	defs = append(defs, ChannelToolDefs()...)
	defs = append(defs, IncidentToolDefs()...)
	defs = append(defs, WireframeToolDefs()...)
	return append(defs, FlowchartToolDefs()...)
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
	RegisterCabinetTools(s)
	RegisterChainTools(s)
	RegisterChannelTools(s)
	RegisterIncidentTools(s)
	RegisterWireframeTools(s)
	RegisterFlowchartTools(s)
	return s
}

// WorkerToolDefs 執行者端點(WorkerEndpoint)的工具規格:只有派發需要的,前綴 dispatch_
func WorkerToolDefs() []any {
	return []any{
		coreDef("dispatch_queue",
			"列出我可以領的任務:全部啟用中角色的待領取(queued)任務、需要協助(blocked)可續做的任務,以及我自己領取中(inProgress)的任務。"+
				"依優先度與建立時間排序。每筆有 id、標題、角色、優先度、狀態、links(相關 Issue id);被鎖定的會標 claimable=false 與 lockedUntil、claimedBy。", true,
			obj(map[string]any{
				"agent": pStr("只列這個角色(id 或名稱)的任務"),
				"limit": pInt("最多幾筆,預設 30,最多 100"),
			})),
		coreDef("dispatch_claim",
			"領取任務並取得角色資訊與憑證:給 id 就領那一筆;不給 id 就從全部啟用中角色的待領取任務挑優先度最高(同優先度最舊)的一筆領走(給 agent 則限定該角色)。"+
				"回傳 task、agent(id、名稱、職責、instructions)、credential(agentId、secret、code、endpoint、expiresAt;密鑰只這一次看得到;code 是 6 位數領取驗證碼,dispatch_report、dispatch_comment 要帶)、lockedUntil 與 nextSteps(接下來怎麼做)。"+
				"領取即鎖定,鎖定期間別人不能領(被鎖時回錯誤並說明鎖到幾點);沒有可領的任務時 task 為 null。", false,
			obj(map[string]any{
				"agent": pStr("角色 id 或名稱(選填;限定角色)"),
				"id":    pStr("任務 id,例如 T-3(選填;不給就自動挑最優先的)"),
			})),
		coreDef("dispatch_status",
			"列出我領取中(inProgress)的任務:鎖定到期時間、剩餘分鐘、是否已逾時。逾時的任務會被自動退回待領取;要續約請用 dispatch_report(status: inProgress)。", true,
			obj(map[string]any{})),
		coreDef("dispatch_report",
			"回報我領取中的任務(不需要角色憑證,伺服器確認我是領取者後以該角色身分回報):status inProgress(附 note 回報進度並續約)、"+
				"blocked(需要人決定,在 note 提問)或 done(在 result 寫結果)。done、blocked 後任務結束、憑證作廢。任務要是進行中且租約還沒到期。", false,
			obj(map[string]any{
				"id":     pStr("任務 id,例如 T-3"),
				"code":   pStr("領取驗證碼(dispatch_claim 回傳的 credential.code,6 位數)"),
				"status": map[string]any{"type": "string", "enum": []string{"inProgress", "blocked", "done"}, "description": "狀態(選填;不給就只加備註或結果)"},
				"result": pStr("結果(done 時寫)"),
				"note":   pStr("備註(進度、問題),署名該角色"),
				"links":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "相關 Issue id(選填,附加)"},
			}, "id", "code")),
		coreDef("dispatch_comment",
			"在 Issue 留言,署名是我領取中任務的角色(agent:<角色 id>),不需要角色憑證。任務要是我領取的、進行中且租約還沒到期;只能留在任務 links 裡的 Issue。", false,
			obj(map[string]any{
				"id":      pStr("我領取中的任務 id(決定以哪個角色署名)"),
				"code":    pStr("領取驗證碼(dispatch_claim 回傳的 credential.code,6 位數)"),
				"issue":   pStr("Issue id,例如 ISS-13"),
				"comment": pStr("留言內容(Markdown)"),
			}, "id", "code", "issue", "comment")),
		coreDef("dispatch_release",
			"放棄一筆我領取中的任務:退回待領取、解鎖、憑證作廢,並在任務留一則備註(署名放棄者,含原因)。只有領取者本人或組織管理員可以。", false,
			obj(map[string]any{
				"id":     pStr("任務 id"),
				"reason": pStr("放棄的原因(選填,會寫進備註)"),
			}, "id")),
		coreDef("dispatch_delegate",
			"以我領取中任務的角色身分,把工作委派給組織鏈上的下游角色(鏈上要有 agent:<這個角色> → agent:<to>;可以派給誰見 dispatch_claim 的 nextSteps)。"+
				"新任務署名這個角色,links 帶上這筆任務與它的 links,label(目標、任務…)寫在任務說明開頭;對方做完 done 或 blocked 會自動替這個角色排一筆「檢視回報」任務。不需要角色憑證。", false,
			obj(map[string]any{
				"id":           pStr("我領取中的任務 id(決定以哪個角色委派)"),
				"code":         pStr("領取驗證碼(dispatch_claim 回傳的 credential.code,6 位數)"),
				"to":           pStr("接任務的角色 id 或名稱(要是鏈上的下游)"),
				"title":        pStr("標題"),
				"instructions": pStr("任務說明(Markdown)"),
				"priority":     pEnum("優先度,預設 normal", "urgent", "high", "normal", "low"),
			}, "id", "code", "to", "title")),
	}
}

// WorkerInitialize 執行者端點 initialize 的回應內容
func WorkerInitialize(instructions string) map[string]any {
	m := CoreInitialize(instructions)
	m["serverInfo"] = map[string]any{"name": "omixor-worker", "title": "omixor 派發", "version": "0.1.0"}
	return m
}

// NewWorkerServer 執行者端點的協定處理器:只有 dispatch_ 工具
func NewWorkerServer(instructions string) *Server {
	tj, _ := json.Marshal(WorkerToolDefs())
	ij, _ := json.Marshal(WorkerInitialize(instructions))
	s, err := NewServer(tj, ij)
	if err != nil {
		panic(err)
	}
	RegisterDispatchTools(s)
	return s
}
