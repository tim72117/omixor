package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
)

// OAuth 寫入(mcp:write):ReadOnlyTools ∪ OAuthWriteTools,其餘回 OAuthNotAvailableMessage;管理端點與統一入口共用同一個 helper;
// 伺服器管理員的 OAuth 寫入身分以一般成員運作;omx_ 與唯讀 omo_ 不變。

func (u *uni) oauthPrincipal(user model.User, readOnly bool) *model.Principal {
	return &model.Principal{User: user, Via: model.ViaOAuth, ReadOnly: readOnly}
}

func (u *uni) writeUnified(p *model.Principal) http.Handler {
	return mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(*http.Request) (backend.Backend, error) {
		return &backend.Local{Svc: u.svc, P: p}, nil
	}})
}

func (u *uni) writeAdmin(p *model.Principal, org string) http.Handler {
	return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: u.svc, P: p}, org, nil
	}})
}

func TestOAuthWriteAllowedToolsWriteToRightOrg(t *testing.T) {
	u := newUni(t)
	boss := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	// 事前:用 omx_ 建一個角色(寫入白名單外的 agent_upsert 不給 OAuth 做)
	if r := callRes(t, u.unified, "agent_upsert", map[string]any{"org": "lab", "id": "cto", "name": "研發長"}); r.isErr {
		t.Fatal(r)
	}
	for _, ep := range []struct {
		name string
		h    http.Handler
		org  func(m map[string]any) map[string]any
	}{
		{"統一入口", u.writeUnified(boss), func(m map[string]any) map[string]any { m["org"] = "lab"; return m }},
		{"管理端點", u.writeAdmin(boss, "lab"), func(m map[string]any) map[string]any { return m }},
	} {
		t.Run(ep.name, func(t *testing.T) {
			h := ep.h
			title := "OAuth寫入-" + ep.name
			r := callRes(t, h, "issue_create", ep.org(map[string]any{"title": title}))
			if r.isErr {
				t.Fatal(r)
			}
			// 落在正確的組織(lab),不在 lab2
			if !strings.Contains(callRes(t, u.unified, "issues", map[string]any{"org": "lab"}).all(), title) {
				t.Fatal("沒寫到 lab")
			}
			if strings.Contains(callRes(t, u.unified, "issues", map[string]any{"org": "lab2"}).all(), title) {
				t.Fatal("不該寫到 lab2")
			}
			id, _ := r.obj(t)["id"].(string)
			if id == "" {
				t.Fatalf("要有 id:%v", r.all())
			}
			for _, c := range []struct {
				tool string
				args map[string]any
			}{
				{"issue_update", map[string]any{"id": id, "comment": "留言"}},
				{"issue_item_add", map[string]any{"issue": id, "title": "子項"}},
				{"issue_decision_add", map[string]any{"issue": id, "title": "選哪個?", "kind": "help"}},
				{"doc_save", map[string]any{"title": "OAuth文件-" + ep.name, "section": "docs", "body": "內容"}},
				{"channel_create", map[string]any{"name": "oauth-" + strings.ReplaceAll(ep.name, "端點", "ep"), "title": "頻道"}},
			} {
				if r := callRes(t, h, c.tool, ep.org(c.args)); r.isErr {
					t.Fatalf("%s 在白名單內要能寫入:%v", c.tool, r.all())
				}
			}
			if r := callRes(t, h, "doc_save", ep.org(map[string]any{"title": "文件B-" + ep.name, "section": "docs", "body": "內容"})); r.isErr {
				t.Fatal("doc_save 要能寫", r)
			}
			if !strings.Contains(callRes(t, u.unified, "docs", map[string]any{"org": "lab", "query": "文件B-" + ep.name}).all(), "文件B-"+ep.name) {
				t.Fatal("文件沒寫到 lab")
			}
		})
	}
	// 統一入口仍然每次都要 org;lab2 是一般成員:寫得進去(成員能建 Issue),但不能做管理員的事
	h := u.writeUnified(boss)
	if r := callRes(t, h, "issue_create", map[string]any{"title": "沒帶 org"}); !r.isErr {
		t.Fatal("要 org", r)
	}
	if r := callRes(t, h, "issue_create", map[string]any{"org": "lab2", "title": "lab2 的"}); r.isErr {
		t.Fatal(r)
	}
	if !strings.Contains(callRes(t, u.unified, "issues", map[string]any{"org": "lab2"}).all(), "lab2 的") {
		t.Fatal("要寫到 lab2")
	}
}

func TestOAuthWriteForbiddenToolsDenied(t *testing.T) {
	u := newUni(t)
	boss := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	// 不開放的工具:就算 boss 是 lab 的管理員、參數合法,也回「不開放」且沒有任何效果
	forbidden := map[string]map[string]any{
		"agent_upsert":        {"id": "evil", "name": "不該建立"},
		"chain_save":          {"nodes": []any{}},
		"project_save":        {"id": "evil", "name": "不該建立"},
		"cabinet_layout_save": {},
		"agent_task_create":   {"assign_to": "cto", "title": "不該排任務"},
		"agent_task_claim":    {"id": "T-1"},
		"issue_item_claim":    {"id": "ISS-1.1"},
		"agent_task_transfer": {"from": "a", "to": "b"},
		"incident_declare":    {"title": "不該宣告"},
		"incident_update":     {"id": "x"},
		"channel_messages":    {"channel": "x"},
		"agent_context":       {},
		"wireframe_render":    {},
		"commits_report":      {"repo": "a/b", "commits": []any{map[string]any{"sha": strings.Repeat("a", 40), "subject": "不該寫入"}}},
		"issue_commit_link":   {"issue": "ISS-1", "repo": "a/b", "sha": strings.Repeat("a", 40)},
		"issue_commit_unlink": {"issue": "ISS-1", "repo": "a/b", "sha": strings.Repeat("a", 40)},
	}
	for name := range model.OAuthForbiddenTools {
		if _, ok := forbidden[name]; !ok {
			t.Fatalf("測試漏了禁止清單的 %s", name)
		}
	}
	for name := range model.OAuthAdminTools {
		if _, ok := forbidden[name]; !ok {
			t.Fatalf("測試漏了管理員工具 %s(讀寫授權不能用)", name)
		}
	}
	for _, ep := range []struct {
		name string
		h    http.Handler
		org  string
	}{{"統一入口", u.writeUnified(boss), "lab"}, {"管理端點", u.writeAdmin(boss, "lab"), ""}} {
		for tool, args := range forbidden {
			a := map[string]any{}
			for k, v := range args {
				a[k] = v
			}
			if ep.org != "" {
				a["org"] = ep.org
			}
			r := callRes(t, ep.h, tool, a)
			wantMsg := mcp.OAuthNotAvailableMessage
			if mcp.OAuthAdminTools[tool] && ep.org != "" { // 組織結構工具:讀寫授權得到「需要 mcp:admin」(帶機器碼),其餘是「OAuth 不開放」
				wantMsg = mcp.OAuthAdminRequiredMessage
			}
			if !r.isErr || !strings.Contains(r.all(), wantMsg) {
				t.Fatalf("%s %s 要被擋:%v", ep.name, tool, r.all())
			}
		}
	}
	// 沒有副作用:evil 角色與專案沒被建立
	if strings.Contains(callRes(t, u.unified, "agents", map[string]any{"org": "lab"}).all(), "evil") ||
		strings.Contains(callRes(t, u.unified, "projects", map[string]any{"org": "lab"}).all(), "evil") {
		t.Fatal("被擋的工具不能有效果")
	}
	// 白名單的訊息是英文
	if !strings.HasPrefix(mcp.OAuthNotAvailableMessage, "This tool is not available over OAuth") {
		t.Fatal(mcp.OAuthNotAvailableMessage)
	}
	// flowchart_render 仍然不能存檔(要存請用 doc_save)
	if r := callRes(t, u.writeUnified(boss), "flowchart_render", map[string]any{"org": "lab", "save": true, "mermaid": "graph TD; A-->B"}); !r.isErr {
		t.Fatal(r)
	}
}

func TestOAuthWriteToolList(t *testing.T) {
	u := newUni(t)
	boss := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	ro := u.oauthPrincipal(boss.User, true)
	for name, h := range map[string]http.Handler{"統一入口": u.writeUnified(boss), "管理端點": u.writeAdmin(boss, "lab")} {
		got := toolNames(t, h)
		for n := range got {
			if !mcp.ReadOnlyTools[n] && !mcp.OAuthWriteTools[n] || mcp.OAuthAdminTools[n] {
				t.Errorf("%s 列出了不該有的工具 %s", name, n)
			}
		}
		for n := range mcp.OAuthWriteTools {
			if !got[n] {
				t.Errorf("%s 沒列出白名單工具 %s", name, n)
			}
		}
	}
	// 唯讀 token 看不到寫入工具(不變)
	for name, h := range map[string]http.Handler{"統一入口": u.writeUnified(ro), "管理端點": u.writeAdmin(ro, "lab")} {
		got := toolNames(t, h)
		if got["issue_create"] || !got["issues"] {
			t.Errorf("%s 唯讀 token 的工具清單不對", name)
		}
		if r := callRes(t, h, "issue_create", map[string]any{"org": "lab", "title": "x"}); !r.isErr || !strings.Contains(r.all(), mcp.ReadOnlyMessage) {
			t.Errorf("%s 唯讀 token 仍回唯讀訊息:%v", name, r.all())
		}
	}
}

func toolNames(t *testing.T, h http.Handler) map[string]bool {
	t.Helper()
	w := rpcPost(h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, "")
	out := map[string]bool{}
	var m struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
		t.Fatal(w.Code, err)
	}
	for _, x := range m.Result.Tools {
		out[x.Name] = true
	}
	return out
}

// 非成員的組織被拒;退出組織後立刻失效(同一個連線);伺服器管理員的 OAuth 寫入身分沒有萬能權限
func TestOAuthWriteMembershipAndServerAdmin(t *testing.T) {
	u := newUni(t)
	boss := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	h := u.writeUnified(boss)
	for _, org := range []string{"other", "nope"} {
		if r := callRes(t, h, "issue_create", map[string]any{"org": org, "title": "闖入"}); !r.isErr || !strings.Contains(r.all(), "You do not have access to organization "+org) {
			t.Fatalf("%s:%v", org, r.all())
		}
	}
	if strings.Contains(callRes(t, u.unified, "issues", map[string]any{"org": "lab"}).all(), "闖入") {
		t.Fatal("闖入")
	}
	if r := callRes(t, h, "issue_create", map[string]any{"org": "lab2", "title": "退出前"}); r.isErr {
		t.Fatal(r)
	}
	if err := u.svc.Store.RemoveMember("lab2", "u1"); err != nil {
		t.Fatal(err)
	}
	if r := callRes(t, h, "issue_create", map[string]any{"org": "lab2", "title": "退出後"}); !r.isErr {
		t.Fatal("退出組織後要立刻失效", r)
	}
	// 管理端點:路徑組織的成員資格由 service 檢查
	if r := callRes(t, u.writeAdmin(boss, "other"), "issue_create", map[string]any{"title": "闖入"}); !r.isErr {
		t.Fatal("非成員的組織(管理端點)要被拒", r)
	}

	// 伺服器管理員 root(不是任何組織的成員)
	rootUser := model.User{ID: "u9", Email: "root@x", Role: "admin"}
	_ = u.svc.Store.SaveUser(rootUser)
	rw := u.writeUnified(u.oauthPrincipal(rootUser, false))
	ro := u.writeUnified(u.oauthPrincipal(rootUser, true))
	omx := u.writeUnified(&model.Principal{User: rootUser, Via: model.ViaLogin})
	if r := callRes(t, rw, "orgs", map[string]any{}); r.isErr || !strings.Contains(r.all(), `"orgs":[]`) {
		t.Fatal("OAuth 寫入的伺服器管理員只看得到自己是成員的組織(沒有)", r.all())
	}
	if r := callRes(t, rw, "issue_create", map[string]any{"org": "other", "title": "管理員闖入"}); !r.isErr {
		t.Fatal("OAuth 寫入身分沒有萬能權限", r.all())
	}
	if r := callRes(t, rw, "schema_check", map[string]any{}); !r.isErr {
		t.Fatal("schema_check 是伺服器管理員專屬,OAuth 寫入身分不能做", r.all())
	}
	if r := callRes(t, rw, "agent_upsert", map[string]any{"org": "other", "id": "x", "name": "x"}); !r.isErr || !strings.Contains(r.all(), mcp.OAuthAdminRequiredMessage) {
		t.Fatal(r.all())
	}
	// 對照:同一個管理員用 omx_ 登入照舊有全部組織;唯讀的 OAuth 不變
	if r := callRes(t, omx, "orgs", map[string]any{}); r.isErr || !strings.Contains(r.all(), "other") {
		t.Fatal("omx_ 不變", r.all())
	}
	if r := callRes(t, omx, "issue_create", map[string]any{"org": "other", "title": "管理員 omx"}); r.isErr {
		t.Fatal("omx_ 不變", r.all())
	}
	if r := callRes(t, ro, "orgs", map[string]any{}); r.isErr || !strings.Contains(r.all(), "other") {
		t.Fatal("唯讀 OAuth 的行為不變", r.all())
	}
}

// omx_(沒有 OAuth)的寫入與工具不受影響
func TestOAuthWriteDoesNotAffectUserToken(t *testing.T) {
	u := newUni(t)
	if r := callRes(t, u.unified, "agent_upsert", map[string]any{"org": "lab", "id": "ok1", "name": "OK"}); r.isErr {
		t.Fatal(r)
	}
	if !toolNames(t, u.unified)["agent_upsert"] || !toolNames(t, u.old)["project_save"] {
		t.Fatal("omx_ 的工具清單不變")
	}
}
