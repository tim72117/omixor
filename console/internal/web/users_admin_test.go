package web

import (
	"strings"
	"testing"
)

// 停用使用者與撤銷 MCP 授權:只有 web 管理員;不存在的使用者不會被建出來
func TestUserAdminActionsNeedAdmin(t *testing.T) {
	resetAuthJSON(t)
	bob, alice, _ := adminTestClients(t)
	for _, name := range []string{"user_disable", "user_oauth_revoke"} {
		if code, _ := bob.do("POST", "/api/action/"+name, map[string]any{"email": "x@corp.test"}, nil); code != 403 {
			t.Errorf("%s:非管理員要 403,得到 %d", name, code)
		}
		code, out := alice.do("POST", "/api/action/"+name, map[string]any{"email": "nobody@corp.test"}, nil)
		if msg, _ := out["error"].(string); code != 404 || !strings.Contains(msg, "沒有這位使用者") {
			t.Errorf("%s:%d %v", name, code, out)
		}
	}
}
