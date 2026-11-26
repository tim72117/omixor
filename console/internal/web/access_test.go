package web

import (
	"strings"
	"testing"

	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
	_ "omixor-web/internal/modules/integrations" // 發行版編進的選用模組:它的 action、工具也要分類
)

// 守門:每個登記的 action、工具都要剛好列在「一般成員」或「管理員」一張表,表裡也不能有不存在的名稱(見 access.go)
func TestEveryActionAndToolIsClassified(t *testing.T) {
	check := func(kind string, registered []string, member map[string]bool, admin map[string]string) {
		known := map[string]bool{}
		for _, n := range registered {
			if strings.HasPrefix(n, "zz_") { // 其他測試自己登記的
				continue
			}
			known[n] = true
			_, isAdminOnly := admin[n]
			switch {
			case member[n] && isAdminOnly:
				t.Errorf("%s %s 同時列在一般成員與管理員", kind, n)
			case !member[n] && !isAdminOnly:
				t.Errorf("%s %s 沒有分類:在 web/access.go 列進一般成員或管理員", kind, n)
			}
		}
		for n := range member {
			if !known[n] {
				t.Errorf("%s:一般成員表的 %s 不存在", kind, n)
			}
		}
		for n := range admin {
			if !known[n] {
				t.Errorf("%s:管理員表的 %s 不存在", kind, n)
			}
		}
	}
	check("action", core.ActionNames(), memberActions, adminActions)
	check("工具", mcp.ToolNames(), memberTools, adminTools)
	if m := mcp.Missing(); len(m) > 0 {
		t.Errorf("有工具沒實作,分類檢查看不到:%v", m)
	}
}

// 沒分類的(之後新增、忘了列的)只有管理員能用;不存在的名稱照舊 404
func TestUnclassifiedIsAdminOnly(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir()) // 認證設定存在資料夾:不和別的測試共用
	core.RegisterAction("zz_unclassified", func(*core.App, map[string]any) (any, error) { return nil, nil })
	bob, alice, _ := adminTestClients(t)
	if code, out := bob.do("POST", "/api/action/zz_unclassified", map[string]any{}, nil); code != 403 {
		t.Errorf("沒分類的 action,非管理員要 403:%d %v", code, out)
	}
	if code, out := alice.do("POST", "/api/action/zz_unclassified", map[string]any{}, nil); code != 200 {
		t.Errorf("管理員可以用:%d %v", code, out)
	}
	for _, p := range []string{"/api/action/zz_nothing", "/api/tool/zz_nothing"} {
		if code, _ := bob.do("POST", p, map[string]any{}, nil); code != 404 {
			t.Errorf("%s:不存在要 404:%d", p, code)
		}
	}
}

// 一般成員的日常操作照常;非管理員的 /api/issues 只限目前的組織,API token 不受限
func TestMemberDailyUseAndIssueAPIOrg(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	bob, alice, app := adminTestClients(t)
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()

	code, out := bob.do("POST", "/api/tool/issue_create", map[string]any{"title": "成員建立"}, nil)
	if code != 200 {
		t.Fatalf("issue_create:%d %v", code, out)
	}
	for _, p := range []string{"/api/tool/issues", "/api/tool/docs", "/api/tool/channels", "/api/action/issue_query"} {
		if code, out := bob.do("POST", p, map[string]any{}, nil); code != 200 {
			t.Errorf("%s:一般成員要能用:%d %v", p, code, out)
		}
	}
	if code, out := bob.do("GET", "/api/issues/"+org, nil, nil); code != 200 {
		t.Errorf("目前組織:%d %v", code, out)
	}
	const other = "/api/issues/other-org-access"
	for _, c := range [][2]string{{"GET", other}, {"POST", other}, {"POST", other + "/ISS-1"}, {"POST", other + "/ISS-1/delete"}} {
		if code, out := bob.do(c[0], c[1], map[string]any{"title": "x"}, nil); code != 403 {
			t.Errorf("%s %s:非管理員要 403:%d %v", c[0], c[1], code, out)
		}
	}
	if code, out := alice.do("GET", other, nil, nil); code != 200 {
		t.Errorf("管理員可以讀其他組織:%d %v", code, out)
	}
	const token = "test-token-0123456789"
	t.Setenv("OMIXOR_API_TOKEN", token)
	anon := &client{t: t, srv: bob.srv, c: newJarClient()}
	if code, out := anon.do("GET", other, nil, map[string]string{"Authorization": "Bearer " + token}); code != 200 {
		t.Errorf("API token 不受限:%d %v", code, out)
	}
}
