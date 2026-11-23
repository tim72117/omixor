package service

// 鎖的整理(都是 Service 的欄位,一個 Service 實例一組;只保護同一個程式、同一個實例裡的「讀了再改」,
// 多個實例或多個程式同時寫同一個存放處時,要靠存放處自己的機制,例如 PostgreSQL 的唯一索引、advisory lock)
//
//	s.agents.mu      角色、任務、組織鏈、Issue 指派去重(agents.go、chain.go、service.go 的 RemoveMember)
//	s.agents.failMu  角色憑證驗證的修剪時間(失敗計數在 failCounter 自己的鎖裡;都只鎖計數本身,裡面不拿其他鎖)
//	s.cabinetMu      文件櫃(結構、文件、範本)
//	s.channelMu      訊息頻道(成員、綁 Issue、已讀)
//	s.incidentMu     事件
//	s.decisionMu     待決定 / 需要協助(只包 Store 的讀寫與可見性判斷;來源 Issue 的留言在放掉鎖之後才寫)
//	s.ModuleLock(id) 選用模組各自的設定(例如 integrations 模組的 GitHub / GitLab 設定;s.moduleMu 只保護鎖的 map)
//	s.mcpMu          外部 MCP 伺服器登記、工具清單、角色權限
//	s.systemMu       系統設定(功能開關)
//	s.issueLock(org, id) 每個 Issue 的「讀了再改」(UpdateIssue、AddIssueComment、刪角色時取消指派;s.issueMu 只保護鎖的 map)
//	rateWindow.mu / failCounter.mu  各入口的頻率限制、失敗計數(只鎖計數本身)
//
// 取得順序(要同時拿兩把時,一律照這個順序,反過來會死結):
//
//	s.agents.mu → s.channelMu   DeleteAgent 在 agents.mu 內呼叫 pruneChannelNode
//	s.agents.mu → s.mcpMu       DeleteAgent 在 agents.mu 內呼叫 pruneAgentMCPGrants;SaveMCPGrants 也照這個順序先拿 agents.mu
//	s.agents.mu → issueLock     DeleteAgent 在 agents.mu 內呼叫 reassignIssue(UpdateIssue 的 syncAssignee 在 Issue 的鎖外才拿 agents.mu)
//	s.agents.mu → s.cabinetMu   DeleteProject 先拿 agents.mu 再拿 cabinetMu(刪專案、清角色的 docAccess)
//
// 其餘的鎖(issueLock、issueMu、cabinetMu、incidentMu、decisionMu、ModuleLock、moduleMu、systemMu、failMu、rateWindow.mu、failCounter.mu)都是最內層:拿著時不再拿任何鎖。
// 拿著 channelMu、mcpMu 或 cabinetMu 時不可以再拿 agents.mu。
//
// 和 store.PG 批次(Batch)的關係:批次是一個資料庫交易,批次裡用另一個 Service 實例
// (server/local.go 的 Import)。服務層的鎖不跨實例,所以批次和一般請求之間的互斥靠的是資料庫(交易與 advisory lock),不是這些鎖;
// 也因為不跨實例,一般請求拿著某把服務層的鎖、寫入時在等批次的資料庫鎖,批次裡的 Service 不會去等同一把鎖,不會死結。
