package web

import (
	"strings"
	"testing"
)

// 受限專案在 web 層:web 管理員(alice)看得到全部;登入的非管理員(bob)在 view、action、tool、Issue API 都看不到受限專案的資料,
// 單筆回 404、引數指到受限專案回 404、搬進受限專案回 404;非受限專案照舊
func TestRestrictedProjectsHiddenFromNonAdminWebUsers(t *testing.T) {
	bob, alice, app := adminTestClients(t)
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()

	mkProject := func(name, key string, restricted bool) string {
		code, out := alice.do("POST", "/api/action/project_create", map[string]any{"name": name, "key": key, "restricted": restricted}, nil)
		r, _ := out["result"].(map[string]any)
		if code != 200 || r == nil || r["restricted"] != restricted {
			t.Fatalf("project_create %s:%d %v", name, code, out)
		}
		return r["id"].(string)
	}
	sec, pub := mkProject("機密", "SEC", true), mkProject("公開", "APP", false)
	mkIssue := func(title, project string) map[string]any {
		code, out := alice.do("POST", "/api/issues/"+org, map[string]any{"title": title, "body": title + "內文", "project": project}, nil)
		r, _ := out["result"].(map[string]any)
		if code != 200 || r == nil {
			t.Fatalf("建立 Issue:%d %v", code, out)
		}
		return r
	}
	secIssue, pubIssue := mkIssue("機密標題", sec), mkIssue("公開標題", pub)
	if code, out := alice.do("POST", "/api/tool/doc_save", map[string]any{"project": sec, "title": "機密文件", "section": "docs", "body": "秘密"}, nil); code != 200 || out["error"] != nil {
		t.Fatal(code, out)
	}

	has := func(v any, s string) bool { return strings.Contains(core2s(v), s) }
	// 管理員:全部看得到
	if l := issueList(t, alice, org); !has(l, "機密標題") || !has(l, "公開標題") {
		t.Fatal("管理員看得到全部", l)
	}
	// bob:Issue API 的列表沒有受限專案的
	if l := issueList(t, bob, org); has(l, "機密標題") || !has(l, "公開標題") {
		t.Fatal("非管理員的 Issue 列表", l)
	}
	// view issues、projects
	if _, out := bob.do("GET", "/api/view/issues", nil, nil); has(out, "機密標題") {
		t.Fatal("view issues 洩漏", out)
	}
	code, _ := bob.do("GET", "/api/view/projects", nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	// tool:列表、單筆(受限專案的 Issue 404)、搬進受限專案、指到受限專案的 project
	if _, out := bob.do("POST", "/api/tool/issues", map[string]any{"status": "all"}, nil); has(out, "機密標題") || has(out, secIssue["key"].(string)) {
		t.Fatal("tool issues 洩漏", out)
	}
	// 單筆讀、改受限專案的 Issue:回應和不存在的編號一模一樣
	toolLikeMissing(t, bob, "issues", secIssue["key"].(string), nil)
	toolLikeMissing(t, bob, "issue_update", secIssue["key"].(string), map[string]any{"comment": "偷留言"})
	toolLikeMissing(t, bob, "issue_accept", secIssue["key"].(string), nil)
	if code, _ := bob.do("POST", "/api/tool/issue_create", map[string]any{"title": "塞", "project": sec}, nil); code != 404 {
		t.Fatal("指到受限專案 404", code)
	}
	if code, _ := bob.do("POST", "/api/tool/projects", map[string]any{}, nil); code != 200 {
		t.Fatal(code)
	}
	if _, out := bob.do("POST", "/api/tool/projects", map[string]any{}, nil); has(out, "機密") || has(out, sec) {
		t.Fatal("tool projects 洩漏", out)
	}
	if code, _ := bob.do("POST", "/api/tool/docs", map[string]any{"project": sec}, nil); code != 404 {
		t.Fatal("受限專案的文件 404", code)
	}
	// action:cabinet_get 指到受限專案
	if code, _ := bob.do("POST", "/api/action/cabinet_get", map[string]any{"project": sec}, nil); code != 404 {
		t.Fatal("cabinet_get 受限專案 404", code)
	}
	// Issue API:單筆更新、刪除、建立
	if code, _ := bob.do("POST", "/api/issues/"+org+"/"+secIssue["id"].(string), map[string]any{"title": "被改"}, nil); code != 404 {
		t.Fatal("Issue API 改受限 Issue 404", code)
	}
	if code, _ := bob.do("POST", "/api/issues/"+org+"/"+secIssue["key"].(string)+"/delete", map[string]any{}, nil); code != 404 {
		t.Fatal("Issue API 刪受限 Issue 404", code)
	}
	if code, _ := bob.do("POST", "/api/issues/"+org, map[string]any{"title": "塞", "project": sec}, nil); code != 404 {
		t.Fatal("Issue API 建在受限專案 404", code)
	}
	if code, _ := bob.do("POST", "/api/issues/"+org+"/"+pubIssue["id"].(string), map[string]any{"project": sec}, nil); code != 404 {
		t.Fatal("Issue API 搬進受限專案 404", code)
	}
	// 非受限專案照舊
	if code, out := bob.do("POST", "/api/tool/issues", map[string]any{"id": pubIssue["key"]}, nil); code != 200 || out["error"] != nil {
		t.Fatal("非受限專案照舊可讀", code, out)
	}
	if code, out := bob.do("POST", "/api/issues/"+org, map[string]any{"title": "新的", "project": pub}, nil); code != 200 {
		t.Fatal("非受限專案照舊可建", code, out)
	}
	// 專案成員的管理:非管理員 403(核心看不出是哪位專案管理員)
	for _, n := range []string{"project_members", "project_member_set", "project_member_remove"} {
		if code, _ := bob.do("POST", "/api/action/"+n, map[string]any{"id": sec}, nil); code != 403 {
			t.Fatal(n, "非管理員 403", code)
		}
	}
	// 管理員看得到受限專案的單筆
	if code, out := alice.do("POST", "/api/tool/issues", map[string]any{"id": secIssue["key"]}, nil); code != 200 || out["error"] != nil {
		t.Fatal("管理員看得到", code, out)
	}
}

// toolLikeMissing 工具對看不到的 Issue(ref)的回應,和對不存在的編號(同樣形式)一模一樣(只差回顯的編號),而且是失敗
func toolLikeMissing(t *testing.T, c *client, tool, ref string, extra map[string]any) {
	t.Helper()
	const missing = "ISS-999999"
	call := func(id string) (int, string) {
		body := map[string]any{"id": id}
		for k, v := range extra {
			body[k] = v
		}
		code, out := c.do("POST", "/api/tool/"+tool, body, nil)
		return code, strings.ReplaceAll(core2s(out), id, "<id>")
	}
	code, got := call(ref)
	wantCode, want := call(missing)
	if code == 200 || code != wantCode || got != want {
		t.Fatalf("%s %s:%d %s,不存在的是 %d %s", tool, ref, code, got, wantCode, want)
	}
}

// core2s 把任何 JSON 值攤平成字串,方便找關鍵字
func core2s(v any) string {
	var b strings.Builder
	var walk func(x any)
	walk = func(x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, z := range y {
				b.WriteString(k + "=")
				walk(z)
			}
		case []any:
			for _, z := range y {
				walk(z)
			}
		case string:
			b.WriteString(y + ";")
		default:
			b.WriteString("~;")
		}
	}
	walk(v)
	return b.String()
}
