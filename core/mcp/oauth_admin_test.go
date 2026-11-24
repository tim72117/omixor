package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
)

// OAuth 組織管理員授權(mcp:admin):工具清單、呼叫當下的組織管理員檢查、降級立刻失效、仍不是伺服器管理員。

func (u *uni) adminPrincipal(user model.User) *model.Principal {
	return &model.Principal{User: user, Via: model.ViaOAuth, OAuthAdmin: true}
}

func TestOAuthAdminToolList(t *testing.T) {
	u := newUni(t)
	boss := model.User{ID: "u1", Email: "boss@x", Role: "member"}
	adm := toolNames(t, u.writeUnified(u.adminPrincipal(boss)))
	for n := range adm {
		if !mcp.ReadOnlyTools[n] && !mcp.OAuthWriteTools[n] && !mcp.OAuthAdminTools[n] {
			t.Errorf("管理員授權列出了不該有的工具 %s", n)
		}
	}
	for _, set := range []map[string]bool{mcp.ReadOnlyTools, mcp.OAuthWriteTools, mcp.OAuthAdminTools} {
		for n := range set {
			if !adm[n] {
				t.Errorf("管理員授權沒列出 %s", n)
			}
		}
	}
	// 讀寫授權:只有唯讀 ∪ 寫入(15 個寫入工具),沒有管理員工具;唯讀不變
	rw := toolNames(t, u.writeUnified(u.oauthPrincipal(boss, false)))
	if len(mcp.OAuthWriteTools) != 15 {
		t.Fatalf("寫入工具數 %d", len(mcp.OAuthWriteTools))
	}
	for n := range mcp.OAuthAdminTools {
		if rw[n] {
			t.Errorf("讀寫授權不該列出 %s", n)
		}
	}
	for n := range rw {
		if !mcp.ReadOnlyTools[n] && !mcp.OAuthWriteTools[n] {
			t.Errorf("讀寫授權多了 %s", n)
		}
	}
	ro := toolNames(t, u.writeUnified(u.oauthPrincipal(boss, true)))
	for n := range ro {
		if !mcp.ReadOnlyTools[n] {
			t.Errorf("唯讀授權多了 %s", n)
		}
	}
	// 組織端點(/mcp/org/{org}):管理員授權視同讀寫,不開管理員工具
	for n := range toolNames(t, u.writeAdmin(u.adminPrincipal(boss), "lab")) {
		if mcp.OAuthAdminTools[n] {
			t.Errorf("組織端點不該開管理員工具 %s", n)
		}
	}
}

func TestOAuthAdminCallTimeEnforcement(t *testing.T) {
	u := newUni(t)
	boss := model.User{ID: "u1", Email: "boss@x", Role: "member"} // lab 管理員、lab2 一般成員

	// 讀寫授權呼叫管理員工具:英文訊息 + 機器碼,沒有效果
	r := callRes(t, u.writeUnified(u.oauthPrincipal(boss, false)), "agent_upsert", map[string]any{"org": "lab", "id": "a1", "name": "甲"})
	if !r.isErr || !strings.Contains(r.all(), mcp.OAuthAdminRequiredMessage) || !strings.Contains(r.all(), mcp.OAuthCodeAdminScopeRequired) {
		t.Fatalf("讀寫授權要被擋並帶機器碼:%v", r.all())
	}

	h := u.writeUnified(u.adminPrincipal(boss))
	// 管理員授權 + 該組織管理員:允許(agent_upsert、project_save、incident_declare)
	if r := callRes(t, h, "agent_upsert", map[string]any{"org": "lab", "id": "a1", "name": "甲"}); r.isErr {
		t.Fatalf("lab 管理員要能用:%v", r.all())
	}
	if !strings.Contains(callRes(t, u.unified, "agents", map[string]any{"org": "lab"}).all(), "甲") {
		t.Fatal("角色沒建立")
	}
	if r := callRes(t, h, "project_save", map[string]any{"org": "lab", "name": "官網", "key": "WEB"}); r.isErr {
		t.Fatalf("project_save:%v", r.all())
	}
	if r := callRes(t, h, "incident_declare", map[string]any{"org": "lab", "title": "測試事件", "sev": 3.0, "impact": "無"}); r.isErr {
		t.Fatalf("incident_declare:%v", r.all())
	}

	// 管理員授權 + 呼叫的組織只是一般成員:被擋,機器碼 org_admin_required,沒有效果(incident_declare 在 service 層成員就可以,這裡是明確的第一道檢查)
	for tool, args := range map[string]map[string]any{
		"agent_upsert":     {"id": "b1", "name": "乙"},
		"incident_declare": {"title": "不該宣告", "sev": 3.0, "impact": "無"},
		"project_save":     {"name": "不該建立", "key": "NO"},
	} {
		a := map[string]any{"org": "lab2"}
		for k, v := range args {
			a[k] = v
		}
		r := callRes(t, h, tool, a)
		if !r.isErr || !strings.Contains(r.all(), mcp.OAuthCodeOrgAdminRequired) {
			t.Fatalf("%s 在 lab2(成員)要被擋:%v", tool, r.all())
		}
	}
	if strings.Contains(callRes(t, u.unified, "agents", map[string]any{"org": "lab2"}).all(), "乙") ||
		strings.Contains(callRes(t, u.unified, "incidents", map[string]any{"org": "lab2"}).all(), "不該宣告") {
		t.Fatal("被擋的呼叫不能有效果")
	}
	// 不是成員的組織:一樣被擋(不洩漏存在與否)
	if r := callRes(t, h, "agent_upsert", map[string]any{"org": "other", "id": "c1", "name": "丙"}); !r.isErr {
		t.Fatal("非成員的組織要被擋", r.all())
	}

	// 降級:同一個授權,使用者之後被降為一般成員 → 立刻失效
	if err := u.svc.Store.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "member", AddedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	if r := callRes(t, h, "agent_upsert", map[string]any{"org": "lab", "id": "a2", "name": "丁"}); !r.isErr || !strings.Contains(r.all(), mcp.OAuthCodeOrgAdminRequired) {
		t.Fatalf("降級後要失效:%v", r.all())
	}
	// 讀寫工具不受影響(仍是成員)
	if r := callRes(t, h, "issue_create", map[string]any{"org": "lab", "title": "降級後仍可建 Issue"}); r.isErr {
		t.Fatal(r.all())
	}
}

// 管理員授權仍然開不了:角色憑證、替角色排工作、commit 寫入等(OAuthForbiddenTools);組織端點把它當讀寫
func TestOAuthAdminStillForbiddenTools(t *testing.T) {
	u := newUni(t)
	boss := model.User{ID: "u1", Email: "boss@x", Role: "member"}
	h := u.writeUnified(u.adminPrincipal(boss))
	for name := range model.OAuthForbiddenTools {
		r := callRes(t, h, name, map[string]any{"org": "lab"})
		if !r.isErr || !strings.Contains(r.all(), mcp.OAuthNotAvailableMessage) {
			t.Errorf("%s 在管理員授權下仍要被擋:%v", name, r.all())
		}
	}
	if r := callRes(t, u.writeAdmin(u.adminPrincipal(boss), "lab"), "agent_upsert", map[string]any{"id": "z", "name": "z"}); !r.isErr || !strings.Contains(r.all(), mcp.OAuthNotAvailableMessage) {
		t.Fatalf("組織端點不開管理員工具:%v", r.all())
	}
}

// 伺服器管理員用 mcp:admin 也沒有萬能權限:不是成員的組織進不去,schema_check(伺服器管理員專屬)不能用
func TestOAuthAdminIsNotServerAdmin(t *testing.T) {
	u := newUni(t)
	root := model.User{ID: "u9", Email: "root@x", Role: "admin"}
	_ = u.svc.Store.SaveUser(root)
	p := u.adminPrincipal(root)
	if p.ServerAdmin() {
		t.Fatal("OAuth 管理員授權不是伺服器管理員")
	}
	var h http.Handler = u.writeUnified(p)
	if r := callRes(t, h, "agent_upsert", map[string]any{"org": "other", "id": "x", "name": "x"}); !r.isErr {
		t.Fatalf("沒有萬能權限:%v", r.all())
	}
	if r := callRes(t, h, "schema_check", map[string]any{}); !r.isErr {
		t.Fatal("schema_check 要被擋", r.all())
	}
}
