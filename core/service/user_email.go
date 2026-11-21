package service

import (
	"strings"

	"omixor-core/model"
)

// 使用者 email 的對外入口檢查(ISS-36 審查:本機使用者 handle omixor-console 沒有 @,不能被一般登入路徑佔用)。
// 一般登入(evoleap、LDAP)、管理員新增使用者與組織成員建立的使用者,email 一律要含 @;
// 保留的本機 handle 另外明列(它們都沒有 @,本來就過不了前一關,這裡是第二道防線,也讓錯誤訊息明確)。
// 本機使用者(console 的 omixor-console、server 本機模式的 OMIXOR_LOCAL_USER)只由各自的本機入口直接寫 Store 建立,不走這裡

// reservedUserHandles 保留的本機身分與內建署名(小寫):不能當一般使用者的 email
var reservedUserHandles = map[string]bool{
	"omixor-console": true, "ops-web": true, "mcp": true, "tim": true,
	model.SystemAuthor: true, model.RoutineAuthor: true, model.ChainAuthor: true, "system": true,
}

// ReservedUserHandle 是不是保留的本機身分或內建署名(不分大小寫)
func ReservedUserHandle(s string) bool {
	return reservedUserHandles[strings.ToLower(strings.TrimSpace(s))]
}

// validUserEmail 一般登入路徑建立或更新使用者的 email 檢查:要含 @(且前後都有字)、不能是保留的本機身分
func validUserEmail(email string) error {
	e := strings.ToLower(strings.TrimSpace(email))
	if ReservedUserHandle(e) {
		return fail(400, "這個名稱是保留的本機身分,不能當成使用者 email:%s", e)
	}
	i := strings.Index(e, "@")
	if i <= 0 || i == len(e)-1 || strings.Count(e, "@") != 1 {
		return fail(400, "要給 email(含 @)")
	}
	return nil
}
