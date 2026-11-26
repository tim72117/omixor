package web

import (
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
	"omixor-web/internal/core/coretest"
	"omixor-web/internal/mcp"
)

// 空資料庫:畫面開得起來,資料 API 與工具回清楚的錯誤,建立第一個組織後照常
func TestNoOrgHTTPAndTools(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	core.UseTestStores(coretest.NewEmptyStores().Get)
	t.Cleanup(func() { core.UseTestStores(coretest.NewStores().Get) })
	c, app := newServer(t, auth.New(&auth.MemorySecrets{}))

	code, v := c.do("GET", "/api/view/orgs", nil, nil)
	if list, ok := v["orgs"].([]any); code != 200 || !ok || len(list) != 0 || v["current"] != "" {
		t.Fatalf("%d %v", code, v)
	}
	for _, path := range []string{"/api/issues/default", "/api/attachments"} {
		if code, v := c.do("POST", path, map[string]any{"title": "x", "data": ""}, nil); code != 409 {
			t.Fatalf("%s:%d %v", path, code, v)
		}
	}
	if code, v := c.do("POST", "/api/tool/issue_create", map[string]any{"title": "x"}, nil); code != 409 {
		t.Fatalf("issue_create:%d %v", code, v)
	}
	if _, err := mcp.Run(app, "orgs", map[string]any{}, core.MCPActor); err != nil {
		t.Fatal(err)
	}
	if code, v := c.do("POST", "/api/action/org_create", map[string]any{"name": "Acme", "switch": true}, nil); code != 200 {
		t.Fatalf("org_create:%d %v", code, v)
	}
	if code, v := c.do("POST", "/api/tool/issue_create", map[string]any{"title": "x"}, nil); code != 200 {
		t.Fatalf("建立組織後 issue_create:%d %v", code, v)
	}
	if code, v := c.do("GET", "/api/issues/default", nil, nil); code != 200 || len(v["result"].([]any)) != 1 {
		t.Fatalf("%d %v", code, v)
	}
}
