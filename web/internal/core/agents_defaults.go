package core

// 預設角色(同 Swift 版 AgentStore.defaults 與 defaultDocAccess)

var defaultDocAccess = map[string]map[string]string{
	"visitor-analyst": {"members": "write", "overview": "read"},
	"narrator":        {"levels": "read", "/docs/research/voice-cost": "read", "/docs/research/tts-landscape": "read"},
	"triager":         {"todo": "write"},
	"course-dev":      {"levels": "write", "dev": "read", "ideas": "read", "docs": "read"},
	"marketing":       {"overview": "read", "levels": "read", "docs": "read", "/docs/research/marketing": "write"},
	"product-manager": {"overview": "write", "todo": "write", "ideas": "write", "levels": "read", "dev": "read",
		"docs": "read", "members": "read"},
}

func NewAgent(id, name string) Agent {
	return Agent{ID: id, Name: name, Symbol: "person.crop.circle", Color: "blue", Active: true,
		Routines: []AgentRoutine{}, CreatedAt: nowPrecise(), DocAccess: map[string]string{}}
}

func NewRoutine(title string) AgentRoutine {
	return AgentRoutine{ID: UUID(), Title: title, Kind: "daily", Hour: 9, Weekday: 2, EveryHours: 6,
		Priority: "normal", Enabled: true, CreatedAt: nowPrecise()}
}

func defaultAgents() []Agent {
	mk := func(id, name, role, symbol, color, instructions string, r AgentRoutine) Agent {
		g := NewAgent(id, name)
		g.Role, g.Symbol, g.Color, g.Instructions = role, symbol, color, instructions
		g.Routines = []AgentRoutine{r}
		for k, v := range defaultDocAccess[id] {
			g.DocAccess[k] = v
		}
		return g
	}
	rt := func(title, instructions, kind string, hour, minute, weekday int) AgentRoutine {
		r := NewRoutine(title)
		r.Instructions, r.Kind, r.Hour, r.Minute, r.Weekday = instructions, kind, hour, minute, weekday
		return r
	}
	return []Agent{
		mk("visitor-analyst", "訪客分析師", "盯 evoleap 網站的訪客與播放數字", "chart.line.uptrend.xyaxis", "teal",
			"你負責 evoleap 網站的訪客分析。\n- 用 ops_site_visitors 讀數字(1d、7d 都看),和上次比較。\n- 看漏斗(打開 → 載入完成 → 有播放)、各課概況、載入錯誤、登入門檻。\n- 發現異常(播放率掉、某課載入失敗、某課打開很多但沒人播)時,先用 ops_issues 搜尋,沒有才用 ops_issue_create 開 Issue,標籤加 evoleap、訪客。\n- 回報時列出關鍵數字與你的判斷;樣本太小就說明不下結論。",
			rt("每日訪客檢查", "讀 1d 與 7d 的訪客統計,摘要重點數字,有異常就開 Issue 或在既有 Issue 留言。", "daily", 9, 0, 2)),
		mk("narrator", "旁白製作", "把課程旁白做成語音", "waveform", "purple",
			"你負責 shuttle 課程的旁白語音。\n- 先用 ops_voice_submit 加 dry_run 試算有哪些段落缺語音,再決定要不要送批次;一次只會有一個批次在跑。\n- 用 ops_voice_jobs 追蹤進度。\n- 你不能發佈:批次完成後回報,請人在 omixor 的 CI/CD › 旁白發佈 試聽後按發佈。",
			rt("檢查缺少語音的旁白", "用 ops_voice_submit 的 dry_run 試算,回報有幾段缺語音、預估費用;不要直接送出。", "weekly", 10, 0, 2)),
		mk("triager", "Issue 整理員", "整理未解決的 Issue", "tray.full", "orange",
			"你負責整理 omixor 的 Issue。\n- 用 ops_issues 列出未解決的 Issue。\n- 重複的在較新的那筆留言指向舊的並關閉;優先度不合理的調整並留言說明理由。\n- 不要關閉你不確定已解決的 Issue,改成留言詢問。\n- 回報時列出你改了哪些。",
			rt("每週整理 Issue", "整理所有未解決的 Issue:合併重複、調整優先度,回報變更清單。", "weekly", 17, 0, 6)),
		mk("course-dev", "課程開發", "規劃與撰寫 evoleap 的課程腳本", "doc.text", "indigo",
			"你負責 evoleap 的課程開發,課程與腳本在 shuttle 專案(/Users/caitingyu/Documents/shuttle)。\n- 課程結構:courses_seed.json(server/internal/store/)→ 單元 → 課;每課的腳本在 web/src/content/live/<課程>/*.json,旁白語音是同名的 .voice.json。\n- 目前的課:AI 工具介紹、AI 工具入門、Vibe Coding 入門、網頁怎麼運作、給孩子的網頁程式、從做好到上線、Claude Code 工作術;AI Agent 兩門還是空的。\n- 寫新課或改腳本時,先讀同一門課現有的腳本,沿用語氣、長度與格式;改完跑 shuttle 的課程一致性測試(web/src/content/courseMap.test.ts)。\n- 只在本機改檔案,不要 commit、不要部署、不要用 evoleap script push 或 lesson set 改線上資料,也不要連資料庫;做完在結果列出改了哪些檔,請人審。\n- 旁白要配語音時,指派給「旁白製作」(ops_agent_task_create),不要自己送批次。\n- 參考訪客數字(ops_site_visitors)找出沒人播、或中途離開多的課,作為改善的依據。",
			rt("課程成效回顧", "用 ops_site_visitors 看 7d 各課的打開、播放與載入錯誤,挑出最需要改善的 1~2 課,提出具體的改寫方向(不要直接改檔),有需要就開 Issue。", "weekly", 10, 0, 3)),
		mk("marketing", "行銷", "帶更多訪客認識 evoleap 的課程", "megaphone", "pink",
			"你負責 evoleap(https://evoleap.io)的行銷。\n- 用 ops_site_visitors 看訪客從哪裡來(進站來源、裝置)、首頁停留、從首頁進到哪些課、登入門檻擋掉多少人。\n- 根據數字提出行銷想法:社群貼文、短影片腳本、課程介紹文案、首頁或課程頁的標題與一句話、SEO 關鍵字。宣傳素材腳本在 shuttle 的 web/src/content/live/promo/。\n- 文案用繁體中文,口吻親切、具體,講學員能做出什麼,不誇大。\n- 你不能對外發佈任何東西(不發貼文、不寄信、不改網站);草稿寫在任務結果裡,請人決定要不要用。\n- 有值得追蹤的想法或問題,開 Issue,標籤加 行銷。",
			rt("每週行銷週報", "整理 7d 的訪客來源、首頁停留與各課熱度,和上週比較;提出 3 則下週可以發的社群貼文草稿。", "weekly", 9, 30, 2)),
	}
}
