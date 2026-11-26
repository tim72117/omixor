package mcp

// 宿主工具:使用的程式(console)掛到統一入口 /mcp 的專有工具,只給 OAuth 連線,依分級開放。
//
//   - omx_ 使用者 token、/mcp/org/{org}、角色端點、執行者端點:不列、呼叫回「沒有這個工具」
//   - 唯讀(mcp:read)只有 TierRead;讀寫(mcp:write)加上 TierWrite;組織管理員(mcp:admin)再加上 TierAdmin
//   - 每次呼叫和其他工具一樣要帶 org、確認呼叫者是成員;TierAdmin 另外即時確認呼叫者是該組織的管理員
//   - 工具的 Call.Ctx 是這次呼叫專屬的 *Session(B = 呼叫者的 Backend,OrgID = 已確認的組織)。
//     宿主自己更嚴的限制(例如只能在某個組織用、要宿主的管理員)在工具實作裡檢查,這裡擋不到

// ToolTier 宿主工具要的 OAuth 授權等級
type ToolTier int

const (
	TierRead ToolTier = iota + 1
	TierWrite
	TierAdmin
)

// HostTool 一個宿主工具
type HostTool struct {
	Tool
	Tier ToolTier
}

// hostToolsInstructions 有掛宿主工具時附在統一入口說明後面(給 LLM 看,英文)
const hostToolsInstructions = " This server also exposes host-specific tools over OAuth only; each follows the same grant levels (read, read-write, organization-admin) and still requires org. " +
	"Some of them work only in the server's system working organization or only for the host's administrators, and say so when refused."

// grantLevel 這個連線的 OAuth 授權等級;0 = 不是 OAuth(omx_)
func grantLevel(bk any) ToolTier {
	switch {
	case isReadOnlyBackend(bk):
		return TierRead
	case isOAuthAdminBackend(bk):
		return TierAdmin
	case isOAuthWriteBackend(bk):
		return TierWrite
	}
	return 0
}

// restrictHostTools 在 restrictOAuth 之後套用:宿主工具的開放與否只看分級,其他工具照原本的限制
func restrictHostTools(h Hooks, bk any, host map[string]ToolTier) Hooks {
	if len(host) == 0 {
		return h
	}
	level := grantLevel(bk)
	allow, deny, denyCode := h.Allow, h.Deny, h.DenyCode
	h.Allow = func(name string) bool {
		if t, ok := host[name]; ok {
			return level >= t
		}
		return allow == nil || allow(name)
	}
	if level == 0 { // omx_:宿主工具當作不存在,其他行為不變
		return h
	}
	h.Deny = func(name string) string {
		if t, ok := host[name]; ok {
			switch {
			case level == TierRead:
				return ReadOnlyMessage
			case t == TierAdmin:
				return OAuthAdminRequiredMessage
			}
			return OAuthNotAvailableMessage
		}
		if deny == nil {
			return ""
		}
		return deny(name)
	}
	h.DenyCode = func(name string) string {
		if t, ok := host[name]; ok {
			if level == TierWrite && t == TierAdmin {
				return OAuthCodeAdminScopeRequired
			}
			return ""
		}
		if denyCode == nil {
			return ""
		}
		return denyCode(name)
	}
	return h
}
