package mcp

import "strings"

// identityDescReplacer 外部規格(console 的 spec/tools.json,Swift 匯出、不手改)裡提到「帶 agent」的句子;
// Swift 改了句子要同步改這裡(console/internal/mcp/identity_spec_test.go 擋下對不上的情況)
var identityDescReplacer = strings.NewReplacer(
	"When working as a role, pass agent and the draft is attributed to that role.", "The draft is signed by the caller; an agent identity cannot be specified.",
)

// 「身分假冒」型的 agent 參數:讓呼叫者以某角色的身分署名、或依該角色的 docAccess 讀寫。
// 人類呼叫的端點(目前是 console 本機的 /mcp)不允許:收到就回 400,不靜默忽略(靜默忽略會讓人以為署名了角色)。
// 要以角色身分工作:agent_task_claim 領任務拿角色憑證,連角色端點(/mcp/agent,身分由憑證決定)。
//
// 對象選擇型的 agent(agent_context、agent_tasks、agent_task_claim、agents 的 id、dispatch_ 的 agent)不在這裡:
// 它們只是「要看哪個角色」,不會讓呼叫者取得那個角色的身分或權限。
//
// 所有人類端點(AdminEndpoint、console 的 /mcp)都套用;角色端點(AgentEndpoint)不受影響:Guard 強制成憑證的角色。
// 共用工具本身也擋(identityAgent、Session.Author),沒掛 Guard 或 RunNested 也繞不過。

// IdentityAgentTools 帶 agent 就是「以該角色身分」的工具
var IdentityAgentTools = map[string]bool{
	"issue_create": true, "issue_update": true,
	"docs": true, "doc_save": true, "cabinet": true, "cabinet_layout_save": true,
	"channels": true, "channel_messages": true, "channel_post": true, "channel_create": true,
	"flowchart_render": true, "wireframe_render": true,
	"agent_task_create": true, "agent_task_delegate": true,
	"fb_draft_save": true,
}

// IdentityAgentError 人類端點收到身分假冒型 agent 參數的固定錯誤句子(狀態碼 400)
const IdentityAgentError = "An agent identity cannot be specified here; to work as an agent, first claim a task (agent_task_claim) to get an agent credential, then connect to the agent endpoint (/mcp/agent)"

// RejectIdentityAgent 人類端點的檢查:身分假冒型的工具帶了非空的 agent 就回 400。
// agent_task_delegate 的委派者只能由角色憑證決定,人類端點一律拒絕(不論有沒有帶 agent)。
func RejectIdentityAgent(tool string, args map[string]any) error {
	if !IdentityAgentTools[tool] {
		return nil
	}
	if tool == "agent_task_delegate" {
		return ToolError{Message: IdentityAgentError, Status: 400}
	}
	return RejectAgentArg(args)
}

// RejectAgentArg args 帶了 agent(非字串、或字串但不是空白)就回 400;沒給、空字串、空白放行。
// 與工具名稱無關(console 的 issueAuthor 這類只看 args 的檢查共用)
func RejectAgentArg(args map[string]any) error {
	if v, ok := args["agent"]; ok {
		if s, isStr := v.(string); !isStr || strings.TrimSpace(s) != "" {
			return ToolError{Message: IdentityAgentError, Status: 400}
		}
	}
	return nil
}

// HumanHiddenTools 人類端點的 tools/list 不列的工具(呼叫一律被擋):委派者只能由角色憑證決定
var HumanHiddenTools = map[string]bool{"agent_task_delegate": true}

// WithoutIdentityAgent tools/list 規格:身分假冒型工具拿掉 agent 參數(複製,不改原本的)
func WithoutIdentityAgent(defs []any) []any {
	out := make([]any, 0, len(defs))
	for _, d := range defs {
		m, ok := d.(map[string]any)
		name, _ := m["name"].(string)
		if !ok || !IdentityAgentTools[name] {
			out = append(out, d)
			continue
		}
		cp := map[string]any{}
		for k, v := range m {
			cp[k] = v
		}
		if desc, ok := m["description"].(string); ok {
			cp["description"] = identityDescReplacer.Replace(desc)
		}
		if sch, ok := m["inputSchema"].(map[string]any); ok {
			s2 := map[string]any{}
			for k, v := range sch {
				s2[k] = v
			}
			if props, ok := sch["properties"].(map[string]any); ok {
				p2 := map[string]any{}
				for k, v := range props {
					if k != "agent" {
						p2[k] = v
					}
				}
				s2["properties"] = p2
			}
			switch req := sch["required"].(type) {
			case []string: // 核心規格(Go 寫的)
				r2 := []string{}
				for _, k := range req {
					if k != "agent" {
						r2 = append(r2, k)
					}
				}
				s2["required"] = r2
			case []any: // 外部 JSON 規格解出來的型別
				r2 := []any{}
				for _, k := range req {
					if k != "agent" {
						r2 = append(r2, k)
					}
				}
				s2["required"] = r2
			}
			cp["inputSchema"] = s2
		}
		out = append(out, cp)
	}
	return out
}

// identityAgent 這次呼叫的 agent 參數(身分假冒型):角色端點是 Guard 強制成憑證角色的值;
// 人類端點帶了非空的 agent 就回 400(核心工具自己再擋一次,端點沒掛 Guard 或工具被 RunNested 呼叫時也不能繞過)
func identityAgent(c *Call) (string, error) {
	name := strings.TrimSpace(c.Args.Str("agent"))
	if name == "" {
		return "", nil
	}
	if _, ok := RoleOf(c); !ok {
		return "", ToolError{Message: IdentityAgentError, Status: 400}
	}
	return name, nil
}
