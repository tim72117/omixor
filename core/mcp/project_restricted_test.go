package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// MCP(人類端點與角色端點):受限專案的工具輸出。非成員(人、角色憑證)看不到專案、Issue、文件;
// 看不到的和不存在是同一句話;專案成員(含成員角色)看得到;project_save 可以設受限旗標與成員
func TestRestrictedProjectTools(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	for _, u := range []struct{ id, email, role string }{{"u1", "boss@x", "admin"}, {"u2", "alice@x", "member"}, {"u3", "bob@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		if u.id == "u1" {
			_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
		}
		_ = st.SaveMember(model.Member{Org: "lab", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	pr := func(id, email string) *model.Principal {
		return &model.Principal{User: model.User{ID: id, Email: email, Role: "member"}}
	}
	boss, alice, bob := pr("u1", "boss@x"), pr("u2", "alice@x"), pr("u3", "bob@x")
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{
			Resolve: func(r *http.Request) (backend.Backend, string, error) {
				return &backend.Local{Svc: svc, P: p}, "lab", nil
			},
		})
	}
	hb, ha, hbob := endpoint(boss), endpoint(alice), endpoint(bob)
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長", Active: true, DocAccess: map[string]string{"docs": "read"}}, {ID: "pm", Name: "產品經理", Active: true, DocAccess: map[string]string{"docs": "read"}}} {
		if _, err := svc.SaveAgent(boss, "lab", g); err != nil {
			t.Fatal(err)
		}
	}
	cred := func(agent string) string {
		if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: agent, Title: "做事"}); err != nil {
			t.Fatal(err)
		}
		r, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: agent})
		if err != nil || r.Credential == nil {
			t.Fatal(r, err)
		}
		return "Bearer " + agent + ":" + r.Credential.Secret
	}
	ctoAuth, pmAuth := cred("cto"), cred("pm")

	// 建立受限專案並加成員(project_save)
	p := ok(t, hb, "", "project_save", map[string]any{"name": "機密", "key": "SEC", "restricted": true, "add_members": []any{"bob@x", "agent:cto"}})
	ms, _ := p["members"].([]any)
	if p["restricted"] != true || p["myRole"] != "admin" || len(ms) != 2 {
		t.Fatal(p)
	}
	ok(t, hb, "", "project_save", map[string]any{"name": "公開", "key": "APP"})
	ok(t, hb, "", "issue_create", map[string]any{"title": "機密標題", "body": "機密內文", "project": "PRJ-1"})
	ok(t, hb, "", "issue_create", map[string]any{"title": "公開標題", "project": "PRJ-2"})
	ok(t, hb, "", "doc_save", map[string]any{"project": "PRJ-1", "title": "機密文件", "section": "docs", "body": "秘密"})
	ok(t, hb, "", "doc_save", map[string]any{"project": "PRJ-2", "title": "公開文件", "section": "docs", "body": "公開"})

	// 看不到:人類非成員與非成員角色,每個工具都一樣
	for _, who := range []struct {
		name string
		h    http.Handler
		auth string
	}{{"alice", ha, ""}, {"pm", role, pmAuth}} {
		t.Run(who.name, func(t *testing.T) {
			// 列表類:不含
			l := ok(t, who.h, who.auth, "issues", map[string]any{"status": "all"})
			if s := toStr(l); strings.Contains(s, "機密") || strings.Contains(s, "SEC-1") {
				t.Fatal("issues 洩漏", s)
			}
			if !strings.Contains(toStr(l), "公開標題") {
				t.Fatal("非受限專案照舊看得到", l)
			}
			if s := toStr(ok(t, who.h, who.auth, "issues", map[string]any{"query": "機密", "status": "all"})); strings.Contains(s, "SEC-1") || strings.Contains(s, "機密標題") {
				t.Fatal("issues 搜尋洩漏", s)
			}
			if s := toStr(ok(t, who.h, who.auth, "issues", map[string]any{"project": "PRJ-1", "status": "all"})); strings.Contains(s, "SEC-1") || strings.Contains(s, "機密標題") {
				t.Fatal("issues 依專案篩選洩漏", s)
			}
			if s := toStr(ok(t, who.h, who.auth, "projects", map[string]any{})); strings.Contains(s, "機密") || strings.Contains(s, "PRJ-1") || strings.Contains(s, "SEC") {
				t.Fatal("projects 洩漏", s)
			}
			// 單筆類:和不存在同一句話
			same := func(tool string, hidden, none map[string]any, hiddenRef, noneRef string) {
				t.Helper()
				_, he := call(t, who.h, who.auth, tool, hidden)
				_, ne := call(t, who.h, who.auth, tool, none)
				if he == "" || ne == "" {
					t.Fatalf("%s 應該失敗:%q / %q", tool, he, ne)
				}
				if strings.ReplaceAll(he, hiddenRef, "<ref>") != strings.ReplaceAll(ne, noneRef, "<ref>") {
					t.Fatalf("%s:看不到與不存在的訊息不同:%q / %q", tool, he, ne)
				}
			}
			same("issues", map[string]any{"id": "SEC-1"}, map[string]any{"id": "SEC-99"}, "SEC-1", "SEC-99")
			same("issue_update", map[string]any{"id": "SEC-1", "comment": "偷留言"}, map[string]any{"id": "SEC-99", "comment": "偷留言"}, "SEC-1", "SEC-99")
			same("issue_update", map[string]any{"id": "SEC-1", "title": "被改"}, map[string]any{"id": "SEC-99", "title": "被改"}, "SEC-1", "SEC-99")
			same("issue_create", map[string]any{"title": "塞", "project": "PRJ-1"}, map[string]any{"title": "塞", "project": "PRJ-9"}, "PRJ-1", "PRJ-9")
			same("projects", map[string]any{"id": "PRJ-1"}, map[string]any{"id": "PRJ-9"}, "PRJ-1", "PRJ-9")
			same("cabinet", map[string]any{"project": "PRJ-1"}, map[string]any{"project": "PRJ-9"}, "PRJ-1", "PRJ-9")
			same("docs", map[string]any{"project": "PRJ-1"}, map[string]any{"project": "PRJ-9"}, "PRJ-1", "PRJ-9")
			same("docs", map[string]any{"id": "DOC-1"}, map[string]any{"id": "DOC-9"}, "DOC-1", "DOC-9")
			if who.auth == "" { // 角色端點沒有寫入權限時不列 doc_save
				same("doc_save", map[string]any{"id": "DOC-1", "title": "被改"}, map[string]any{"id": "DOC-9", "title": "被改"}, "DOC-1", "DOC-9")
				same("doc_save", map[string]any{"project": "PRJ-1", "title": "塞", "section": "docs"}, map[string]any{"project": "PRJ-9", "title": "塞", "section": "docs"}, "PRJ-1", "PRJ-9")
			}
			// docs 依關鍵字(未分專案)不含受限專案的文件
			if s := toStr(ok(t, who.h, who.auth, "docs", map[string]any{"query": "機密"})); strings.Contains(s, "DOC-1") || strings.Contains(s, "機密文件") {
				t.Fatal("docs 搜尋洩漏", s)
			}
			if who.auth == "" { // 人類端點:project_save 加成員、設旗標
				_, e := call(t, who.h, who.auth, "project_save", map[string]any{"id": "PRJ-1", "add_members": []any{"alice@x"}})
				if e != "找不到專案 PRJ-1" {
					t.Fatal("非成員加成員:看不到專案", e)
				}
				_, e = call(t, who.h, who.auth, "project_save", map[string]any{"id": "PRJ-1", "restricted": false})
				if !strings.Contains(e, "管理員") {
					t.Fatal("非管理員不能改旗標", e)
				}
			}
		})
	}

	// agent_context(人類端點指定角色):角色的 docAccess 指向受限專案、手上有受限 Issue 的任務,非成員看到的 context 不含這些
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "pm", Name: "產品經理", Active: true, DocAccess: map[string]string{"docs": "read", "PRJ-1:docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	ok(t, hb, "", "issue_update", map[string]any{"id": "SEC-1", "assignee": "cto"})
	for _, ag := range []string{"pm", "cto"} {
		if s := toStr(ok(t, ha, "", "agent_context", map[string]any{"agent": ag, "include_bodies": true})); strings.Contains(s, "機密") || strings.Contains(s, "PRJ-1") || strings.Contains(s, "SEC-1") {
			t.Fatal("agent_context 洩漏受限專案", ag, s)
		}
		if s := toStr(ok(t, hbob, "", "agent_context", map[string]any{"agent": ag, "include_bodies": true})); ag == "cto" && !strings.Contains(s, "SEC-1") {
			t.Fatal("成員看得到成員角色的任務", ag, s)
		}
	}
	// 成員(人與角色)與組織管理員看得到
	for _, who := range []struct {
		name string
		h    http.Handler
		auth string
	}{{"boss", hb, ""}, {"bob", hbob, ""}, {"cto", role, ctoAuth}} {
		l := ok(t, who.h, who.auth, "issues", map[string]any{"status": "all"})
		if !strings.Contains(toStr(l), "機密標題") {
			t.Fatal(who.name, "要看得到受限專案的 Issue", l)
		}
		if i := ok(t, who.h, who.auth, "issues", map[string]any{"id": "SEC-1"}); i["key"] != "SEC-1" {
			t.Fatal(who.name, i)
		}
		if s := toStr(ok(t, who.h, who.auth, "projects", map[string]any{})); !strings.Contains(s, "PRJ-1") {
			t.Fatal(who.name, "要看得到專案", s)
		}
	}
	// 人類端點的 projects 帶 id:成員名單
	one := ok(t, hbob, "", "projects", map[string]any{"id": "PRJ-1"})
	if l := one["projects"].([]any); len(l) != 1 || len(l[0].(map[string]any)["members"].([]any)) != 2 || l[0].(map[string]any)["myRole"] != "member" {
		t.Fatal(one)
	}
	// 專案管理員:bob 升成 admin 後可以用 project_save 管成員,但改旗標仍要組織管理員
	ok(t, hb, "", "project_save", map[string]any{"id": "PRJ-1", "add_members": []any{"bob@x"}, "member_role": "admin"})
	got := ok(t, hbob, "", "project_save", map[string]any{"id": "PRJ-1", "add_members": []any{"alice@x"}})
	if len(got["members"].([]any)) != 3 {
		t.Fatal(got)
	}
	if _, e := call(t, hbob, "", "project_save", map[string]any{"id": "PRJ-1", "restricted": false}); !strings.Contains(e, "管理員") {
		t.Fatal("專案管理員不能改旗標", e)
	}
	got = ok(t, hbob, "", "project_save", map[string]any{"id": "PRJ-1", "remove_members": []any{"alice@x"}})
	if len(got["members"].([]any)) != 2 {
		t.Fatal(got)
	}
}

func toStr(m map[string]any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				b.WriteString(k + "=")
				walk(y)
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		case string:
			b.WriteString(x + ";")
		default:
			b.WriteString("~;")
		}
	}
	walk(m)
	return b.String()
}
