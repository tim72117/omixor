package model

// 家庭助理的模擬情境:樣板資料的一部分。套用樣板時勾「帶入模擬情境」,會在「家庭」頻道寫入示範對話,
// 並建立對應的示範任務(完成、需要協助等狀態與回報、依組織鏈的委派與回報),讓使用者在對話、派發、需要協助的畫面
// 看到家庭助理實際運作的樣子。全部是假的對話,標題與訊息都標「模擬」,不含任何真實個資。
// 說明版見同目錄的 orgtemplates_family_scenarios.md。

// ScenarioTag 模擬資料的標記(任務標題、訊息開頭)
const ScenarioTag = "模擬"

// ScenarioStep 管家委派給專職角色的一筆任務
type ScenarioStep struct {
	Agent string `json:"agent"` // 角色 id
	Title string `json:"title"`
	// Status done 或 blocked
	Status string `json:"status"`
	Report string `json:"report"`
}

// TemplateScenario 一個情境:家人訊息 → 管家回覆與分派 → 專職角色的任務與回報 → 需要家人決定的點 → 最後的頻道回覆
type TemplateScenario struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Request     string         `json:"request"`     // 家人在頻道說的話
	ButlerReply string         `json:"butlerReply"` // 管家的回覆與分派
	Steps       []ScenarioStep `json:"steps"`
	Decisions   []string       `json:"decisions"`  // 需要家人決定的點(有的話管家的任務是 blocked)
	FinalReply  string         `json:"finalReply"` // 管家彙整後的最後回覆
	// Refusal 這個情境是示範「拒絕/不能代做」的情況
	Refusal bool `json:"refusal,omitempty"`
}

// FamilyScenarios 家庭助理的情境(框架:內容是示範,之後可以再補)
func FamilyScenarios() []TemplateScenario {
	return []TemplateScenario{
		{
			ID: "weekly-shopping", Title: "週末採買清單",
			Request:     "@管家 這週末要去大賣場,幫我列一下要買什麼",
			ButlerReply: "收到,我請「採買與家用品」對照家用品存量和常用店家整理清單,好了回你。",
			Steps: []ScenarioStep{{Agent: "shopping", Title: "整理週末採買清單", Status: "done",
				Report: "- 衛生紙、洗碗精快用完\n- 早餐:牛奶、吐司、雞蛋\n- 上週有人說要補電池(3 號)"}},
			FinalReply: "週末採買清單:\n- 衛生紙、洗碗精\n- 牛奶、吐司、雞蛋\n- 3 號電池\n\n我只列清單,不會下單;要調整直接在這裡說。",
		},
		{
			ID: "bill-reminder", Title: "本月帳單提醒",
			Request:     "@管家 這個月有哪些帳單還沒繳?",
			ButlerReply: "我請「帳務與繳費」對照帳單清單整理到期日。",
			Steps: []ScenarioStep{{Agent: "billing", Title: "整理本月帳單到期日", Status: "blocked",
				Report: "- 電費:月底到期(自動扣款)\n- 網路費:下週三到期(需要手動繳)\n- 這期水費比上期高很多,可能要確認是否漏水"}},
			Decisions:  []string{"網路費要由誰繳、什麼時候繳", "水費偏高要不要請人檢查"},
			FinalReply: "本月帳單:\n- 電費:月底,自動扣款\n- 網路費:下週三到期\n- 水費:比上期高很多\n\n需要你決定:\n1. 網路費由誰繳\n2. 水費偏高要不要請人檢查(我可以請「家務與維修」先列檢查方式)",
		},
		{
			ID: "pickup-conflict", Title: "接送時間衝突",
			Request:     "@管家 週四下午弟弟的才藝課和姊姊的家長會撞到了",
			ButlerReply: "我請「行事曆與接送」看週四的行程,整理可能的安排。",
			Steps: []ScenarioStep{{Agent: "calendar", Title: "週四接送衝突的安排", Status: "blocked",
				Report: "- 才藝課 16:00–17:00,家長會 16:30 開始\n- 方案 A:一位家人去家長會,另一位接送\n- 方案 B:才藝課請假一次"}},
			Decisions:  []string{"選方案 A 或 B;方案 A 要決定誰去家長會"},
			FinalReply: "週四 16:00–17:00 兩件事重疊。\n- 方案 A:一人去家長會、一人接送\n- 方案 B:才藝課請假一次\n\n需要你決定:\n1. 選 A 或 B(A 的話誰去家長會)\n\n請假或聯絡老師要由家人自己處理,我不會代為聯絡。",
		},
		{
			ID: "leaky-faucet", Title: "廚房水龍頭漏水",
			Request:     "@管家 廚房水龍頭一直滴水",
			ButlerReply: "我請「家務與維修」整理可能原因和處理方式。",
			Steps: []ScenarioStep{{Agent: "housework", Title: "廚房水龍頭滴水", Status: "blocked",
				Report: "- 常見原因:墊片老化、閥芯磨損\n- 自己處理:先關下方止水閥,更換墊片\n- 請師傅:需要費用,要家人決定"}},
			Decisions:  []string{"自己換墊片,還是請師傅(會有費用)"},
			FinalReply: "水龍頭滴水多半是墊片或閥芯的問題,先把水槽下方的止水閥關小。\n\n需要你決定:\n1. 自己換墊片,還是請師傅(有費用,我不會代為聯絡師傅)",
		},
		{
			ID: "medicine-reminder", Title: "用藥提醒設定",
			Request:     "@管家 醫生開了一週的藥,一天三次飯後,幫我提醒",
			ButlerReply: "我請「健康與用藥提醒」依用藥表排提醒。",
			Steps: []ScenarioStep{{Agent: "health", Title: "排一週的飯後用藥提醒", Status: "done",
				Report: "- 已依「緊急聯絡與醫療資訊」的用藥表格式整理:早、午、晚飯後\n- 共 7 天,最後一天提醒回診時間要由家人確認"}},
			FinalReply: "已排好一週、一天三次飯後的提醒。\n- 我只提醒時間,不會判斷要不要停藥或加藥;有不舒服請問醫生。",
		},
		{
			ID: "homework-plan", Title: "期中考複習計畫",
			Request:     "@管家 姊姊下週期中考,幫忙排一下複習",
			ButlerReply: "我請「孩子學習」依考試科目排複習時間。",
			Steps: []ScenarioStep{{Agent: "learning", Title: "期中考複習計畫", Status: "done",
				Report: "- 週一:國語、週二:數學、週三:英文、週四:自然、週五:總複習\n- 每天 40 分鐘,中間休息 10 分鐘"}},
			FinalReply: "期中考複習計畫:\n- 一 國語、二 數學、三 英文、四 自然、五 總複習\n- 每天 40 分鐘\n\n我不會替孩子寫作業或考卷。",
		},
		{
			ID: "sick-day", Title: "家人生病請假",
			Request:     "@管家 弟弟發燒了,明天應該要請假",
			ButlerReply: "先照顧弟弟。我請「行事曆與接送」看明天要調整的行程,請「健康與用藥提醒」整理就醫要準備的事。",
			Steps: []ScenarioStep{
				{Agent: "calendar", Title: "明天的行程調整", Status: "done", Report: "- 明天的才藝課需要請假\n- 姊姊的接送不受影響"},
				{Agent: "health", Title: "就醫準備清單", Status: "blocked", Report: "- 記錄體溫與時間\n- 帶健保卡與目前的用藥\n- 要不要就醫、去哪裡看,要由家人判斷"},
			},
			Decisions:  []string{"要不要就醫、什麼時候去", "向學校請假由家人聯絡"},
			FinalReply: "整理好了:\n- 明天才藝課要請假,姊姊的接送照常\n- 就醫準備:體溫紀錄、健保卡、目前用藥\n\n需要你決定:\n1. 要不要就醫、什麼時候去(我不做醫療判斷)\n2. 向學校請假請家人聯絡",
		},
		{
			ID: "typhoon-day", Title: "颱風停班停課",
			Request:     "@管家 明天颱風停班停課,家裡要準備什麼?",
			ButlerReply: "我請「採買與家用品」列防颱物資,請「行事曆與接送」把明天的行程標成取消。",
			Steps: []ScenarioStep{
				{Agent: "shopping", Title: "防颱物資清單", Status: "done", Report: "- 飲用水、乾糧、手電筒與電池\n- 行動電源充飽"},
				{Agent: "calendar", Title: "明天行程取消", Status: "done", Report: "- 明天的上學、上班、才藝課都標成取消\n- 後天的行程照常,颱風後再確認"},
			},
			FinalReply: "防颱準備:\n- 飲用水、乾糧、手電筒與電池、行動電源\n- 明天行程都已標成取消\n\n外出請注意安全,有停電或淹水狀況在這裡說。",
		},
		{
			ID: "refuse-payment", Title: "代為付款(拒絕)", Refusal: true,
			Request:     "@管家 直接幫我把網路費繳掉,卡號在手冊裡",
			ButlerReply: "抱歉,我不能付款,也不會讀取或使用卡號。我請「帳務與繳費」整理繳費方式給你。",
			Steps: []ScenarioStep{{Agent: "billing", Title: "網路費繳費方式", Status: "blocked",
				Report: "- 不能代為付款\n- 可以的方式:業者網站、超商、設定自動扣款(由家人操作)\n- 建議不要把卡號寫在家庭手冊"}},
			Decisions:  []string{"由家人選擇繳費方式並自行繳費"},
			FinalReply: "我不能付款,也不會使用卡號。\n- 繳費方式:業者網站、超商、自動扣款\n- 建議把卡號從家庭手冊移除\n\n需要你決定:\n1. 用哪個方式繳(請家人自己操作)",
		},
		{
			ID: "refuse-privacy", Title: "分享家人資料(拒絕)", Refusal: true,
			Request:     "@管家 把大家的生日和身分資料整理好傳給保險業務",
			ButlerReply: "這涉及家人的隱私,我不能代為聯絡外部的人或傳送個資。這件事需要家人自己決定與處理。",
			Steps:       []ScenarioStep{},
			Decisions:   []string{"要不要提供、提供哪些資料,由家人自己決定並自行聯絡"},
			FinalReply:  "我不會整理或傳送家人的個資給外部的人。\n\n需要你決定:\n1. 要不要提供、提供哪些(請家人自己聯絡業務)",
		},
	}
}
