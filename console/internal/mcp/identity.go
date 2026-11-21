package mcp

import coremcp "omixor-core/mcp"

// 本機 /mcp(與畫面的 /api/tool)不允許「身分假冒」型的 agent 參數:署名一律是呼叫者(人 = 使用者 actor、MCP 用戶端 = MCPActor)。
// 要以角色身分工作:agent_task_claim 領任務拿角色憑證,連角色端點 /mcp/agent(身分由憑證決定)。
// 清單、固定句子與規格過濾在核心(coremcp.IdentityAgentTools、IdentityAgentError、WithoutIdentityAgent);
// 對象選擇型的 agent(agent_context、agent_tasks、agent_task_claim)照舊。
// 雲端與 server 的端點(/mcp/org、/mcp/agent)不經過這裡,行為不變。

// rejectIdentity 身分假冒型工具帶了 agent 回 400(tool 空字串 = 只檢查 args 有沒有 agent)
func rejectIdentity(tool string, args map[string]any) error {
	if tool == "" {
		return coremcp.RejectAgentArg(args)
	}
	return coremcp.RejectIdentityAgent(tool, args)
}
