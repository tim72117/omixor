package mcp

import (
	"encoding/json"

	"omixor-core/model"
)

// 核心自己的工具規格(tools/list)與 initialize 資訊:給核心掛載的端點用(AgentEndpoint、AdminEndpoint)。
// omixor-web 的 /mcp 也用這裡的核心工具定義(加上 web 自己的工具,見 console/internal/mcp/handler.go 的 webSpec),
// Swift App 的 /mcp 則把核心工具轉給 core server 的管理端點,列出的也是這一份。核心工具的定義只在這裡維護。

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

// projectParam 文件櫃工具的 project 參數說明
const projectParam = "專案 id(例如 PRJ-1,見 projects);省略 = 未分專案(組織原本的文件櫃)"

// CoreToolDefs 核心提供的工具規格:組織、Issue、虛擬角色與任務、專案、文件櫃
func CoreToolDefs() []any {
	prio := []string{"urgent", "high", "normal", "low"}
	defs := []any{
		coreDef("orgs", "列出目前能用的組織(current 是這個連線所在的組織)。", true, obj(map[string]any{})),
		coreDef("issue_create",
			"把發現的問題或待辦存成 Issue。內文用 Markdown,寫清楚現象、依據(數字、網址、時間範圍)與建議。建立前可先用 issues 搜尋,避免重複。給 assignee(角色 id)會自動替該角色排一筆任務。", false,
			obj(map[string]any{
				"assignee": pStr("指派給的虛擬角色 id;會自動替它排任務(角色必須存在)"),
				"body":     pStr("內文(Markdown)"),
				"labels":   pStrs("標籤,例如 [\"evoleap\", \"訪客\"]"),
				"link":     pStr("相關網址"),
				"priority": pEnum("優先度,預設 normal", prio...),
				"project":  pStr("放進的專案 id(例如 PRJ-1;專案要先設定代號,編號會是 <代號>-<n>);不給 = repo 只連到一個專案(見 project_save 的 link_repos)就放進那個專案,否則組織層級(ISS-<n>);空字串 = 明確不分專案(組織層級)"),
				"repo":     pStr("所屬程式碼倉庫,例如 owner/name(GitHub)或 group/sub/project(GitLab),可加 github:、gitlab: 前綴;只能用英數與 - _ . / :"),
				"source":   pStr("從哪裡發現的,例如「網站訪客 7d」"),
				"title":    pStr("標題,一句話說明問題"),
			}, "title")),
		coreDef("issue_update",
			"更新 Issue:改標題、內文、優先度、標籤、所屬倉庫、指派、所屬專案(搬移),加留言,或關閉/重新打開。只給要改的欄位。id 用 key 或內部 id 都可以;回傳的 key 是顯示用編號。", false,
			obj(map[string]any{
				"add_labels":  pStrs("加上的標籤"),
				"append_body": pStr("接在內文後面的文字"),
				"assignee":    pStr("改指派給的角色 id(空字串 = 取消指派);會自動替新角色排任務、取消舊角色還沒領取的任務"),
				"body":        pStr("取代內文(Markdown)"),
				"comment":     pStr("加一則留言"),
				"id":          pStr("Issue 編號:key(例如 ISS-3、APP-12,舊的 key 也可以)或內部 id"),
				"labels":      pStrs("取代全部標籤"),
				"priority":    pEnum("優先度", prio...),
				"project":     pStr("搬到這個專案(專案 id,例如 PRJ-1;空字串 = 搬回組織層級)。會發新的 key,舊的 key 仍然查得到"),
				"version":     pInt("Issue 目前的 version(issues 回傳的);有給就檢查版本,別人已經改過時回錯誤(不會蓋掉),不給 = 不檢查"),
				"repo":        pStr("改所屬程式碼倉庫(owner/name 或 group/sub/project,可加 github:、gitlab: 前綴;空字串 = 清除標記)"),
				"status":      pEnum("open 重新打開,closed 關閉", "open", "closed"),
				"title":       pStr("新標題"),
			}, "id")),
		coreDef("issues",
			"給 id 回傳完整內容(含留言);不給 id 則列出符合條件的 Issue(預設只列未解決,依優先度與時間排序)。"+
				"顯示與引用 Issue 請用 key(ISS-<n>,專案裡是 <專案代號>-<n>);id 是內部 id,兩種(含舊的 key)都能當參數。已刪除(垃圾桶)的不會列出。", true,
			obj(map[string]any{
				"id":      pStr("Issue 編號:key(例如 ISS-3、APP-12)或內部 id"),
				"project": pStr("只列這個專案的(專案 id,例如 PRJ-1);\"-\" = 只列組織層級的;不給 = 全部"),
				"label":   pStr("只列有這個標籤的"),
				"limit":   pInt("最多幾筆,預設 30"),
				"query":   pStr("搜尋標題、內文、留言、編號與標籤(不分大小寫;以空白分隔的每個字都要出現)"),
				"repo":    pStr("只列標記這個程式碼倉庫的(不分大小寫;github:、gitlab: 前綴一邊沒寫就忽略)"),
				"status":  pEnum("open、closed 或 all,預設 open", "open", "closed", "all"),
			})),
		coreDef("agents",
			"列出組織的虛擬角色:id、名稱、職責、角色設定(instructions)、例行任務,以及各狀態的任務數。要以某角色身分工作,先讀它的 instructions,再用 agent_task_claim 領任務拿角色憑證、連角色端點。", true,
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
				"doc_access": map[string]any{"type": "object", "description": "文件櫃存取範圍,會合併進現有設定:key 是區塊 id 或名稱、頁籤網址、名稱或代號(存成區塊 id 或頁籤網址),值是 read、write 或 none(移除)。頁籤的設定優先於區塊。" +
					"沒有前綴的 key 只適用未分專案;專案的位置寫成「<專案 id>:<區塊或頁籤>」(例如 PRJ-1:docs、PRJ-1:/docs/1),依那個專案的結構解析。",
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
				"assign_to":    pStr("接這個任務的角色 id 或名稱"),
				"instructions": pStr("任務說明(Markdown)"),
				"priority":     pEnum("優先度,預設 normal", prio...),
				"title":        pStr("標題"),
			}, "assign_to", "title")),
		coreDef("agent_task_transfer",
			"把一個角色名下的任務轉給另一個角色,不刪角色(組織管理員)。要刪有任務的角色,先用這個轉移(或在刪除時指定轉給誰)。"+
				"scope=open(預設)只轉待領取與需要協助;scope=all 全部都轉:進行中(已被領走)的先退回待領取、鎖與憑證作廢,已完成與已取消的也一起轉。每筆任務會加一則「由 <舊角色> 轉移」備註。目標角色不能是自己、不存在或已停用。", false,
			obj(map[string]any{
				"from":  pStr("來源角色 id 或名稱"),
				"scope": pEnum("範圍,預設 open", "open", "all"),
				"to":    pStr("目標角色 id 或名稱"),
			}, "from", "to")),
		coreDef("projects",
			"列出組織的專案(id、名稱、代號 key、描述、連結的 repo、restricted(受限)、myRole、文件數)。受限專案只有專案成員與組織管理員看得到,看不到的不會出現在清單裡。給 id 只看那個專案並附成員名單(人類端點;看不到的專案回找不到)。專案用來分文件櫃與 Issue:每個專案有自己的文件櫃結構與文件;有代號的專案可以放 Issue(issue_create、issue_update 帶 project);不屬於任何專案的是「未分專案」/組織層級。"+
				"文件櫃工具(cabinet、docs、doc_save、cabinet_layout_save)帶 project 參數操作那個專案,不帶就是未分專案。", true,
			obj(map[string]any{"id": pStr("只看這個專案(含成員名單 members);不給 = 全部")})),
		coreDef("project_save",
			"新增專案(不給 id;name 必填)或修改專案的名稱、代號、描述(給 id)(組織管理員)。名稱在組織內不能重複;新專案的 id 是 PRJ-<n>,文件櫃結構是空白的(用 cabinet_layout_save 帶 project 建區塊)。"+
				"代號(key,例如 APP)設定後才能在專案裡建 Issue,編號是 <代號>-<n>;專案裡已經有 Issue 就不能再改代號。", false,
			obj(map[string]any{
				"id":             pStr("要修改的專案 id,例如 PRJ-1;新增時不給"),
				"key":            pStr("專案代號:2 到 10 個大寫英數字、英文字母開頭,組織內不重複;ISS、PRJ、T、DOC、CH、INC 是保留字"),
				"name":           pStr("專案名稱(新增時必填)"),
				"description":    pStr("專案描述"),
				"link_repos":     pStrs("連結這些程式碼倉庫(owner/name 或 group/sub/project,可加 github:、gitlab: 前綴,不分大小寫;已經連過的略過)。連結只用於預設值與篩選:建立 Issue 沒指定 project、repo 只連到一個專案時預設放進那個專案;issues 依 repo 篩選時,連到該 repo 的專案裡沒標 repo 的 Issue 也算。沒連任何專案的 repo = 組織共用;不是權限來源,也不會搬動既有 Issue"),
				"unlink_repos":   pStrs("取消連結這些程式碼倉庫(沒連過的回錯誤)"),
				"restricted":     pBool("受限專案(只有組織管理員能改):true = 只有專案成員、組織管理員看得到(Issue、文件櫃、專案本身;其他人看到的和不存在一樣),false = 組織成員都看得到(預設)。改成受限時,不是專案成員的角色手上還沒領取的、這個專案 Issue 的任務會取消"),
				"add_members":    pStrs("加入專案成員(組織管理員或專案管理員):使用者(email、user:<email>)或角色(agent:<角色 id>)。已經是成員就是改角色。受限專案的 Issue 只能指派給成員角色"),
				"remove_members": pStrs("移除專案成員(組織管理員或專案管理員;不是成員回錯誤)。移除角色成員時,它在受限專案還沒領取的任務一併取消"),
				"member_role":    pEnum("add_members 的角色,預設 member(不給且對方已是成員 = 角色不變)", "member", "admin"),
			})),
		coreDef("cabinet",
			"列出文件櫃的區塊與頁籤,以及各處有幾份文件。給 project 看那個專案的文件櫃(省略 = 未分專案)。", true,
			obj(map[string]any{
				"project": pStr(projectParam),
			})),
		coreDef("docs",
			"給 id 讀一份文件(文件 id 在組織內唯一,不用帶 project;回傳的 project 是所屬專案);否則依專案、區塊、頁籤、關鍵字列出文件(最多 50 筆;只列 project 那個專案的,省略 = 未分專案)。"+
				"讀長文件時建議分段讀,省上下文:先 outline 看大綱(各段的 id、標題、行號範圍、字數),再用 part 讀一段、from/to 讀幾行,或 grep 找關鍵字;"+
				"回傳的 version 在局部修改(doc_save 的 edits、replace_part、append_to_part)時帶回來。只給 id 時回全文,但超過 6000 字的文件只回大綱與開頭(要全文帶 full)。", true,
			obj(map[string]any{
				"project": pStr(projectParam + ";列文件時只列這個專案的,section、tab 依它的結構解析(給 id 時不用)"),
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
			"新增文件(不給 id)或修改文件(給 id)。內容是純文字/Markdown,放在對應的區塊與頁籤。署名是呼叫者(角色端點是憑證的角色,只能寫到角色「可讀寫」的位置)。"+
				"專案:新增時 project 是放進的專案(省略 = 未分專案),section、tab 依那個專案的結構解析;修改時給 project 就是把文件搬到那個專案(空字串 = 搬回未分專案;沒給 section 時沿用原本的區塊與頁籤,目標專案要有),不給就不搬。"+
				"內文開頭可以有欄位(--- 之間一行一個 key: value),kind 是文件類型:有對應範本(kind: template、for: <類型> 的文件,內含 ```html 版面與 ```css 樣式)時,畫面依範本顯示,存檔前也會檢查範本的 required 欄位。"+
				"修改長文件時建議局部修改(edits、replace_part、append_to_part 三種擇一,不必送全文),回傳只有改到的那幾行與新版本。", false,
			obj(map[string]any{
				"append_body": pStr("接在內文後面的文字"),
				"body":        pStr("取代內文(Markdown)"),
				"edits": map[string]any{"type": "array", "description": "局部修改:字串取代,依序套用;old 在全文必須剛好出現一次(或設 all),任何一個失敗就都不改",
					"items": obj(map[string]any{"old": pStr("要被取代的原文(多帶前後文讓它唯一)"), "new": pStr("新的文字"), "all": pBool("取代全部出現的地方")}, "old", "new")},
				"replace_part":   pStr("局部修改:整段替換這一段(大綱的 id 或標題),保留標題那一行;內容放 content"),
				"append_to_part": pStr("局部修改:接在這一段最後(大綱的 id 或標題);內容放 content,自帶標題就是在這段後面插入新段落"),
				"content":        pStr("replace_part、append_to_part 的內容(Markdown)"),
				"base_version":   pStr("局部修改時帶讀到的 version:版本不符時,edits 會在最新內文上再套一次,replace_part、append_to_part 會被拒絕"),
				"id":             pStr("要修改的文件 id;新增時不給"),
				"project":        pStr(projectParam + ";新增時是放進的專案,修改時給了就是搬到這個專案(空字串 = 未分專案)"),
				"section":        pStr("區塊 id 或名稱(新增時必填,或只給 tab)"),
				"tab":            pStr("頁籤網址或名稱"),
				"tags":           pStrs("標籤"),
				"title":          pStr("標題(新增時必填)"),
			})),
		coreDef("cabinet_layout_save",
			"新增、改名、移除文件櫃的區塊與頁籤(組織管理員)。給 project 改那個專案的結構(每個專案各自一份;省略 = 未分專案)。依序套用 add_sections → add_tabs → update → remove,全部成功才存(一次存檔)。"+
				"新區塊不給 id 時自動編 s<n>;新頁籤不給 path 時自動編 /<區塊 id>/<n>。被移除的區塊、頁籤裡不能還有文件(先用 doc_save 搬走)。回傳新的結構、這次新增的 id 與新的 layoutVersion。"+
				"樂觀鎖:cabinet 回傳 layoutVersion;存檔帶 expectedVersion(用 cabinet 讀到的那個)時,結構在這之間被別人改過(版本不符)就拒絕並回目前版本,請重新 cabinet 再重做;不帶時以這次呼叫開頭讀到的版本為準。", false,
			obj(map[string]any{
				"project":         pStr(projectParam),
				"expectedVersion": pInt("選填:你讀到結構時 cabinet 回傳的 layoutVersion;版本不符(結構已被別人更新)就拒絕,不覆蓋"),
				"add_sections":    pObjs("新增區塊:[{label, id?}];接在最後"),
				"add_tabs":        pObjs("新增頁籤:[{section, label, code?, group?, path?}];section 是區塊 id 或名稱(可以是這次新增的)"),
				"update":          pObjs("改名:[{section 或 tab, label?, code?, group?}];section 給區塊 id 或名稱,tab 給頁籤網址、名稱或代號(可再帶 section 限定區塊);code、group 給空字串是清掉"),
				"remove":          pStrs("移除的區塊(id 或名稱)或頁籤(網址、名稱或代號);區塊會連同它的頁籤一起移除"),
			})),
	}
	defs = append(defs, ChainToolDefs()...)
	defs = append(defs, ChannelToolDefs()...)
	defs = append(defs, IncidentToolDefs()...)
	defs = append(defs, DecisionToolDefs()...)
	defs = append(defs, ItemToolDefs()...)
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
	RegisterDecisionTools(s)
	RegisterItemTools(s)
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
