package model

import "strings"

// ---- 「誰」的節點規範化規則 ----
//
// 資料裡「誰」的欄位(作者、建立者、指派對象、成員…)格式不一:角色 agent:<id>、真人 user:<email>、
// 純 email、本機身分(omixor-console、舊的 ops-web)、mcp / routine / chain 這類內建署名,還有空字串。
// 規範化節點(下面的 PrincipalNode):
//
//	agent:<id>        角色(id 原樣)
//	user:<email>      真人(email 去空白、轉小寫;帶 user: 前綴或不帶、只要含 @ 都歸這裡)
//	local:<名稱>      其他一切(本機身分、內建署名)
//
// 資料庫層的身分登記表(omixor_principals、<欄位>_ref 產生欄位與外鍵)不部署;ISS-36 直接進 actor 欄位(雲端 DOC-11),
// 這份規則是 actor 遷移的對照來源,所有「誰」的欄位清單在 core/store/identity_fields.go。

const (
	// LocalPrefix 本機身分、內建署名的節點前綴
	LocalPrefix = "local:"

	PrincipalKindAgent = "agent"
	PrincipalKindUser  = "user"
	PrincipalKindLocal = "local"
)

// PrincipalNode 把欄位裡存的值換成規範化節點;空白(空字串)回 ""(= 沒有身分)
func PrincipalNode(raw string) string {
	t := strings.Trim(raw, " ")
	switch {
	case t == "":
		return ""
	case strings.HasPrefix(t, AgentPrefix) && len(t) > len(AgentPrefix):
		return t
	case strings.HasPrefix(t, UserPrefix) && strings.Trim(t[len(UserPrefix):], " ") != "":
		return UserPrefix + strings.ToLower(strings.Trim(t[len(UserPrefix):], " "))
	case strings.HasPrefix(t, LocalPrefix) && len(t) > len(LocalPrefix):
		return t
	case strings.Contains(t, "@"):
		return UserPrefix + strings.ToLower(t)
	}
	return LocalPrefix + t
}

// PrincipalKind 規範化節點的種類:agent、user,其餘 local
func PrincipalKind(node string) string {
	switch {
	case strings.HasPrefix(node, AgentPrefix):
		return PrincipalKindAgent
	case strings.HasPrefix(node, UserPrefix):
		return PrincipalKindUser
	}
	return PrincipalKindLocal
}

// AssigneeNode Issue 的 assignee 存的是純角色 id(不帶 agent: 前綴),換成規範化節點;空字串回 ""
func AssigneeNode(assignee string) string {
	a := strings.Trim(assignee, " ")
	if a == "" {
		return ""
	}
	return PrincipalNode(AgentPrefix + a)
}

// ActorNode 事件時間軸執行者(kind + id)的規範化節點:ai 是角色(id 是 mcp 時是 local:mcp)、person 同一般的值、system 沒有節點("")
func ActorNode(kind, id string) string {
	switch kind {
	case "ai":
		if strings.Trim(id, " ") == MCPActorID {
			return LocalPrefix + MCPActorID
		}
		return AssigneeNode(id)
	case "person":
		return PrincipalNode(id)
	}
	return ""
}
