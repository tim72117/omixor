package mcp_test

import (
	"net/http"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// MCP:project_save 的 link_repos / unlink_repos、projects 的 repos、issue_create 的預設專案與空字串、issues 依 repo 篩選考慮連結
func TestProjectRepoTools(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.SaveUser(model.User{ID: "u2", Email: "bob@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u2", Email: "bob@x", Role: "member", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	bob := &model.Principal{User: model.User{ID: "u2", Email: "bob@x", Role: "member"}}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: boss}, "lab", nil
		},
	})
	member := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: bob}, "lab", nil
		},
	})

	// 新增專案同時連結
	p := ok(t, admin, "", "project_save", map[string]any{"name": "官網", "key": "WEB", "link_repos": []any{"Acme/Web", "gitlab:g/sub/web"}})
	if repos := p["repos"].([]any); len(repos) != 2 || repos[0] != "Acme/Web" || repos[1] != "gitlab:g/sub/web" {
		t.Fatal(p)
	}
	// 修改:取消一個、再連一個(只給連結參數也算有改動);重複連結略過
	p = ok(t, admin, "", "project_save", map[string]any{"id": "PRJ-1", "link_repos": "acme/web", "unlink_repos": []any{"GITLAB:g/sub/web"}})
	if repos := p["repos"].([]any); len(repos) != 1 || repos[0] != "Acme/Web" {
		t.Fatal(p)
	}
	fails(t, admin, "", "project_save", map[string]any{"id": "PRJ-1", "unlink_repos": []any{"no/such"}}, "專案 PRJ-1 沒有連結 no/such")
	fails(t, admin, "", "project_save", map[string]any{"id": "PRJ-1", "link_repos": []any{"bad repo!"}}, "repo 只能用英數與 - _ . / :,最長 200 字(例如 owner/name、gitlab:group/sub/project)")
	fails(t, member, "", "project_save", map[string]any{"id": "PRJ-1", "link_repos": []any{"acme/x"}}, "要組織 lab 的管理員才能做")
	// projects 列出 repos
	l := ok(t, admin, "", "projects", map[string]any{})["projects"].([]any)
	if r := l[0].(map[string]any)["repos"].([]any); len(r) != 1 || r[0] != "Acme/Web" {
		t.Fatal(l)
	}

	// issue_create:沒給 project 依連結預設;空字串不分專案;明確指定照給
	i := ok(t, admin, "", "issue_create", map[string]any{"title": "預設", "repo": "acme/web"})
	if i["project"] != "PRJ-1" || i["key"] != "WEB-1" {
		t.Fatal("依連結預設", i)
	}
	i = ok(t, admin, "", "issue_create", map[string]any{"title": "不分專案", "repo": "acme/web", "project": ""})
	if i["project"] != "" || i["key"] != "ISS-1" {
		t.Fatal("空字串 = 不分專案", i)
	}
	ok(t, admin, "", "issue_create", map[string]any{"title": "沒標 repo", "project": "PRJ-1"})

	// issues 依 repo 篩選:標 repo 的兩張 + 專案內沒標的一張
	out := ok(t, admin, "", "issues", map[string]any{"repo": "ACME/web", "status": "all"})
	if out["total"] != float64(3) {
		t.Fatal("連結的專案裡沒標 repo 的也算", out)
	}
	// 取消連結後只剩自己標的兩張
	ok(t, admin, "", "project_save", map[string]any{"id": "PRJ-1", "unlink_repos": []any{"acme/web"}})
	if out = ok(t, admin, "", "issues", map[string]any{"repo": "acme/web", "status": "all"}); out["total"] != float64(2) {
		t.Fatal(out)
	}
}
