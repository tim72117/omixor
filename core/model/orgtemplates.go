package model

// 組織樣板:一次替組織建立一組角色、組織鏈、訊息頻道、文件櫃文件(與可選的例行任務)。
// 樣板只在這裡定義一次(唯一來源),App、web、MCP 都透過 GET /api/orgs/{org}/templates 取得;套用規則在 service/templates.go。
//
//	Feature:樣板受哪個功能開關控制(見 features.go);開關關閉時列表不列出、套用回 403
//	Agents:要建立的角色(id 固定;同 id 或同名稱的角色已存在就略過)
//	Chain:組織鏈的邊;節點寫 TemplateCreator(套用的人)或角色 id
//	Channels:訊息頻道;成員寫 TemplateCreator 或角色 id(同名頻道已存在就略過)
//	Section:文件櫃要新增的區塊(已存在就略過);Docs 放在這個區塊(同區塊同標題已存在就略過)
//
// 目前是框架:角色的 instructions 是簡短版,細節之後再補;情境模擬見 orgtemplates_family_scenarios.md。

// TemplateCreator 樣板裡代表「套用樣板的人」的節點(套用時換成 user:<email>)
const TemplateCreator = "creator"

// TemplateAgent 樣板裡的一個角色
type TemplateAgent struct {
	Agent
	// SuggestAutoReply 建議在「自動執行」替這個角色打開自動執行與自動回覆(每台裝置的設定,核心不替使用者打開)
	SuggestAutoReply bool `json:"suggestAutoReply,omitempty"`
}

// TemplateChannel 樣板裡的一個訊息頻道
type TemplateChannel struct {
	Name    string   `json:"name"`
	Topic   string   `json:"topic"`
	Members []string `json:"members"`
}

// TemplateDoc 樣板裡的一份文件(放在樣板的 Section)
type TemplateDoc struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

// OrgTemplate 一個組織樣板
type OrgTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Feature     string            `json:"feature"`
	Agents      []TemplateAgent   `json:"agents"`
	Chain       []ChainEdge       `json:"chain"`
	Channels    []TemplateChannel `json:"channels"`
	Section     *CabinetSection   `json:"section,omitempty"`
	Docs        []TemplateDoc     `json:"docs"`
	// Scenarios 模擬情境(套用時選「帶入模擬情境」才寫入示範對話與任務;見 orgtemplates_family_scenarios.go)
	Scenarios []TemplateScenario `json:"scenarios"`
	// ScenarioChannel 示範對話寫在哪個頻道(樣板的頻道名稱)
	ScenarioChannel string `json:"scenarioChannel,omitempty"`
	// Notes 套用後給使用者的提示(例如去自動執行打開管家的自動回覆)
	Notes []string `json:"notes"`
}

// OrgTemplates 全部樣板
var OrgTemplates = []OrgTemplate{FamilyTemplate()}

// OrgTemplateByID 找樣板
func OrgTemplateByID(id string) (OrgTemplate, bool) {
	for _, t := range OrgTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return OrgTemplate{}, false
}

// ---------------- 家庭助理 ----------------

// FamilySection 家庭手冊的文件櫃區塊 id
const FamilySection = "family"

// familyRules 每個家庭角色共同的規則(簡短版,之後再補細節)
const familyRules = `
共同規則:
- 不能付款、不能下單、不能代為聯絡家人以外的人(店家、學校、醫院、廠商)。
- 涉及金錢、健康醫療判斷、隱私的事,整理好選項後回報 blocked,請家人決定。
- 不要把家庭手冊裡的個資貼到頻道以外的地方。
- 在頻道回覆:簡短、條列;需要家人決定的事放在最後,用「需要你決定:」開頭逐條列出。`

func familyAgent(id, name, role, symbol, color, duty string, docAccess string, suggest bool) TemplateAgent {
	return TemplateAgent{
		Agent: Agent{
			ID: id, Name: name, Role: role, Symbol: symbol, Color: color, Active: true,
			Instructions: duty + "\n" + familyRules,
			DocAccess:    map[string]string{FamilySection: docAccess},
			Routines:     []AgentRoutine{},
		},
		SuggestAutoReply: suggest,
	}
}

// FamilyTemplate 家庭助理樣板:管家接話與分派,專職角色處理各類家務;做完回報管家,管家彙整回家人
func FamilyTemplate() OrgTemplate {
	butler := familyAgent("butler", "管家", "接話與分派", "house", "indigo",
		`你是這個家的管家。家人在「家庭」頻道 @ 你時:
- 先確認需求,能直接回答的就回答(例如查家庭手冊)。
- 需要專職角色處理的,用組織鏈委派給對應角色(採買、帳務、行事曆、家務、健康、學習),並在頻道說明派給了誰。
- 專職角色回報後,彙整結果回到頻道。`, "write", true)
	butler.Routines = []AgentRoutine{{
		ID: "weekly-plan", Title: "整理下週行程與採買清單", Kind: "weekly", Weekday: 1, Hour: 20, Minute: 0, Priority: "normal",
		Enabled:      false, // 預設關閉,家人在角色設定確認後再開
		Instructions: "每週日晚上:向行事曆與採買角色要下週的行程與採買清單,彙整後貼到「家庭」頻道,請家人確認。",
	}}
	agents := []TemplateAgent{
		butler,
		familyAgent("shopping", "採買與家用品", "採買清單", "cart", "green",
			"負責採買清單與家用品存量:整理要買的東西、比較選項、提醒快用完的。只列清單與建議,不下單。", "read", false),
		familyAgent("billing", "帳務與繳費", "帳單提醒", "creditcard", "orange",
			"負責帳單清單與繳費提醒:整理到期日與金額、提醒快到期的。不付款、不碰帳號密碼;金額異常回報 blocked。", "read", false),
		familyAgent("calendar", "行事曆與接送", "行程安排", "calendar", "blue",
			"負責家庭行事曆與接送安排:整理行程、找出衝突、建議誰去接送。不代為聯絡學校或其他家長。", "read", false),
		familyAgent("housework", "家務與維修", "家務分工", "wrench.and.screwdriver", "teal",
			"負責家務分工與維修追蹤:列出要做的家務、維修事項與可能的處理方式。不代為聯絡廠商;要花錢的維修回報 blocked。", "read", false),
		familyAgent("health", "健康與用藥提醒", "用藥提醒", "cross.case", "pink",
			"負責用藥與就醫提醒:依家人提供的用藥表提醒時間、整理要問醫生的問題。不做任何醫療判斷,症狀相關一律回報 blocked 請家人決定或就醫。", "read", false),
		familyAgent("learning", "孩子學習", "學習陪伴", "book", "purple",
			"負責孩子的學習安排:整理作業與考試時間、建議練習方式。不替孩子寫作業;和老師溝通由家人處理。", "read", false),
	}
	chain := []ChainEdge{{From: TemplateCreator, To: "butler", Label: "需求"}}
	members := []string{TemplateCreator}
	for _, g := range agents {
		members = append(members, g.ID)
		if g.ID != "butler" {
			chain = append(chain, ChainEdge{From: "butler", To: g.ID, Label: "任務"})
		}
	}
	return OrgTemplate{
		ID:          "family",
		Name:        "家庭助理",
		Description: "像管家一樣:在「家庭」頻道 @ 管家,由管家分派給採買、帳務、行事曆、家務、健康、學習等專職角色,做完回報管家、管家彙整回家人。",
		Feature:     "familyAssistant",
		Agents:      agents,
		Chain:       chain,
		Channels:    []TemplateChannel{{Name: "家庭", Topic: "家裡的大小事:@管家 就會協助;需要家人決定的事會清楚列出", Members: members}},
		Section:     &CabinetSection{ID: FamilySection, Label: "家庭手冊", Tabs: []CabinetTab{}},
		Docs: []TemplateDoc{
			{Title: "家庭成員與聯絡方式", Tags: []string{"家庭手冊"}, Body: `# 家庭成員與聯絡方式

(範本:請家人自行填寫,只寫角色需要知道的資訊)

| 稱呼 | 關係 | 平常的行程 | 備註 |
|------|------|------------|------|
| | | | |
`},
			{Title: "常用店家與帳單清單", Tags: []string{"家庭手冊"}, Body: `# 常用店家與帳單清單

(範本:不要寫帳號、卡號或密碼)

## 常用店家
| 店家 | 買什麼 | 備註 |
|------|--------|------|
| | | |

## 帳單
| 帳單 | 大約金額 | 每月到期日 | 繳費方式(只寫類型,例如自動扣款) |
|------|----------|------------|------------------------------------|
| | | | |
`},
			{Title: "每週例行事項", Tags: []string{"家庭手冊"}, Body: `# 每週例行事項

(範本)

| 星期 | 事項 | 誰負責 |
|------|------|--------|
| 一 | | |
| 日 | 整理下週行程與採買清單 | 管家(例行任務預設關閉) |
`},
			{Title: "緊急聯絡與醫療資訊", Tags: []string{"家庭手冊"}, Body: `# 緊急聯絡與醫療資訊

(範本:由家人自行決定要寫多少;角色不會把這裡的內容貼到外面)

## 緊急聯絡
-

## 用藥表
| 家人 | 藥名 | 時間 | 備註 |
|------|------|------|------|
| | | | |

## 過敏與注意事項
-
`},
		},
		Scenarios:       FamilyScenarios(),
		ScenarioChannel: "家庭",
		Notes: []string{
			"到「AI 自動化 › 自動執行」替管家打開自動執行與「在頻道被 @ 時自動回覆」(每台 Mac 各自設定)。",
			"家庭手冊是空白範本,請家人自行填寫;不要寫帳號、卡號或密碼。",
			"管家的例行任務「整理下週行程與採買清單」預設關閉,確認後再到角色設定打開。",
		},
	}
}
