package model

import "strings"

// ---- actor id → 相容字串(ISS-36 C-2;契約見 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》,DOC-13 §3.5)----
//
// 「誰」欄位在資料庫與服務層內部是 actor id;對外(API、MCP、舊用戶端)過渡期仍輸出舊格式的字串,
// 同時輸出新的 actor 物件。格式依欄位而不同(舊的寫法本來就不一致),這裡是唯一的對照規則(service.Names、present.go 用)。

// LegacyForm 相容字串的格式
type LegacyForm int

const (
	// LegacyFree 人 = email、角色 = agent:<handle>、系統 = 舊名稱(Issue 建立者、留言作者、文件作者、任務建立者與領取者、事件指揮官、updatedBy…)
	LegacyFree LegacyForm = iota
	// LegacyNode 節點:user:<email>、agent:<handle>(頻道成員、頻道建立者與訊息作者、提及、組織鏈端點)
	LegacyNode
	// LegacyAgentID 只有角色的 handle(Issue 的 assignee);非角色是空字串
	LegacyAgentID
	// LegacyUserID 稽核的 userId:使用者 = omixor_users.id(給 userID)、角色 = agent:<handle>、系統 = 舊名稱
	LegacyUserID
)

// SystemLegacyName 系統 actor 的舊署名:sys_omixor → omixor、sys_routine → routine、sys_chain → chain、sys_legacy_unknown → 空字串、
// sys_legacy_mcp → mcp、其他 sys_legacy_*(遷移時對不上的舊值)→ 原值(actor 的 name)
func SystemLegacyName(id, name string) string {
	switch id {
	case SystemActorOmixor:
		return "omixor"
	case SystemActorRoutine:
		return "routine"
	case SystemActorChain:
		return "chain"
	case "sys_legacy_unknown":
		return ""
	case "sys_legacy_mcp":
		return "mcp"
	}
	return name
}

// LegacyActorString 一個 actor 的相容字串。email 是使用者的 omixor_users.email(沒有使用者列的「未註冊使用者」給空字串,改用 handle = email);
// userID 只有 LegacyUserID 用(使用者的 omixor_users.id)。actor 不存在(rec 零值)回空字串
func LegacyActorString(rec ActorRecord, email, userID string, f LegacyForm) string {
	switch rec.Kind {
	case ActorKindUser:
		who := email
		if who == "" {
			who = rec.Handle
		}
		switch f {
		case LegacyNode:
			return "user:" + who
		case LegacyAgentID:
			return ""
		case LegacyUserID:
			return userID
		}
		return who
	case ActorKindAgent:
		if f == LegacyAgentID {
			return rec.Handle
		}
		return "agent:" + rec.Handle
	case ActorKindSystem:
		if f == LegacyAgentID { // 角色已刪又對不上的 assignee 是 sys_legacy_<雜湊>(名稱 = 原值),照原值輸出;其他系統 actor 不是角色
			if rec.ID != "sys_legacy_unknown" && strings.HasPrefix(rec.ID, "sys_legacy_") {
				return rec.Name
			}
			return ""
		}
		return SystemLegacyName(rec.ID, rec.Name)
	}
	return ""
}

// LegacyEventActor 事件時間軸 actor 的舊 {kind, id}:使用者 = person/<email>、角色 = ai/<handle>、sys_omixor 與其他固定系統 actor = system/空、
// sys_legacy_mcp = ai/mcp、其他 sys_legacy_* = ai/<原值>。actor 不存在(rec 零值)回空字串
func LegacyEventActor(rec ActorRecord, email string) (kind, id string) {
	switch rec.Kind {
	case ActorKindUser:
		if email == "" {
			email = rec.Handle
		}
		return "person", email
	case ActorKindAgent:
		return "ai", rec.Handle
	case ActorKindSystem:
		if rec.ID != SystemActorOmixor && strings.HasPrefix(rec.ID, "sys_legacy_") {
			return "ai", SystemLegacyName(rec.ID, rec.Name)
		}
		return "system", ""
	}
	return "", ""
}

// ActorIDs 收集 actor id:濾掉空字串、去重,保留第一次出現的順序(批次查名稱前用)
func ActorIDs(ids ...string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
