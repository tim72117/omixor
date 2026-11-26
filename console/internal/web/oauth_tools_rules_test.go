package web

import (
	"testing"

	"omixor-web/internal/mcp"

	coremcp "omixor-core/mcp"
)

// OAuth 入口的專有工具分類不能和畫面的規則(access.go、orgscope.go)走岔:網頁限管理員的要是 TierAdmin,
// 網頁只能在系統工作組織用的不能標成 OrgScoped
func TestOAuthConsoleToolsMatchWebRules(t *testing.T) {
	rules := mcp.OAuthConsoleTools()
	if len(rules) == 0 {
		t.Fatal("沒有專有工具,測試失效")
	}
	for name, rule := range rules {
		_, admin := adminTools[name]
		if admin != (rule.Tier == coremcp.TierAdmin) || !admin && !memberTools[name] {
			t.Errorf("%s:網頁的管理員分類 %v、OAuth 分級 %d 不一致", name, admin, rule.Tier)
		}
		if orgTools[name] != rule.OrgScoped {
			t.Errorf("%s:網頁分組織 %v、OAuth %v", name, orgTools[name], rule.OrgScoped)
		}
	}
}
