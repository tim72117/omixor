package web

import (
	"strings"
	"testing"

	"omixor-web/internal/core"
	"omixor-web/internal/mcptoken"
)

// 啟用 LDAP 時:一般 web 使用者看不到完整權杖、不能換新(403);管理員可以;view 只有 tokenHint
func TestMCPTokenActionsAdminOnly(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir()) // 認證設定存在資料夾:不和別的測試共用
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "")
	bob, alice, app := adminTestClients(t)
	tok, err := core.MCPTokenValue(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mcp_token_reveal", "mcp_token_rotate"} {
		if _, ok := adminActions[name]; !ok {
			t.Fatalf("%s 要列進 adminActions", name)
		}
		code, out := bob.do("POST", "/api/action/"+name, map[string]any{}, nil)
		if code != 403 || strings.Contains(toJSON(out), tok) {
			t.Fatalf("%s:非管理員要 403 且不含權杖,得到 %d %v", name, code, out)
		}
	}
	// 非管理員被擋之後權杖沒變
	if cur, _ := core.MCPTokenValue(app); cur != tok {
		t.Fatal("非管理員呼叫 rotate 不該換掉權杖")
	}
	// view:兩種人都只看到遮罩
	for _, c := range []*client{bob, alice} {
		code, v := c.do("GET", "/api/view/mcp_access", nil, nil)
		if code != 200 || v["tokenSet"] != true || v["tokenHint"] != mcptoken.Hint(tok) || strings.Contains(toJSON(v), tok) {
			t.Fatalf("view:%d %v", code, v)
		}
	}
	// 管理員:reveal 拿到完整權杖,rotate 換新
	code, out := alice.do("POST", "/api/action/mcp_token_reveal", map[string]any{}, nil)
	res, _ := out["result"].(map[string]any)
	if code != 200 || res == nil || res["token"] != tok {
		t.Fatalf("管理員 reveal:%d %v", code, out)
	}
	code, out = alice.do("POST", "/api/action/mcp_token_rotate", map[string]any{}, nil)
	res, _ = out["result"].(map[string]any)
	if code != 200 || res == nil || res["token"] == tok || res["token"] == nil {
		t.Fatalf("管理員 rotate:%d %v", code, out)
	}
	if core.MCPTokenVerify(app, tok, func(a, b string) bool { return a == b }) {
		t.Fatal("換新後舊的要失效")
	}
}

// 沒啟用登入(本機單人模式):web 使用者就是本機管理員,reveal 可用
func TestMCPTokenLocalModeAdmin(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir()) // 認證設定存在資料夾:不和別的測試共用
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "")
	c, app := newServer(t, nil)
	tok, _ := core.MCPTokenValue(app)
	code, out := c.do("POST", "/api/action/mcp_token_reveal", map[string]any{}, nil)
	res, _ := out["result"].(map[string]any)
	if code != 200 || res == nil || res["token"] != tok {
		t.Fatalf("本機模式 reveal:%d %v", code, out)
	}
}

func toJSON(m map[string]any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				b.WriteString(k + "=")
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			b.WriteString(x + ";")
		}
	}
	walk(m)
	return b.String()
}
