package web

import (
	"encoding/json"
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// 受限專案照核心的一條規則(專案成員、組織管理員看得到),畫面用登入者本人的身分判斷。
// alice 是 web 管理員;bob 是組織成員(不是 web 管理員)。機密甲 bob 之後加入,機密乙一直不是成員
func TestRestrictedProjectsFollowCoreMembership(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	bobUser := joinOrg(t, e.app, e.au, "bob")
	bob := e.login("bob")
	org := e.org

	_, badgesBefore := bob.do("GET", "/api/view/badges", nil, nil)
	project := func(name, key string) string {
		return e.ok(alice, "/api/action/project_create", map[string]any{"name": name, "key": key, "restricted": true})["id"].(string)
	}
	pa, pb := project("甲案", "AAA"), project("乙案", "BBB")
	// 動態牆記到組織層級的舊編號再搬進去(衍生資料沒有 project 欄位)
	mk := func(title, p string) map[string]any {
		i := e.ok(alice, "/api/issues/"+org, map[string]any{"title": title, "priority": "urgent"})
		return e.ok(alice, "/api/issues/"+org+"/"+i["key"].(string), map[string]any{"project": p})
	}
	ia, ib := mk("甲案票", pa), mk("乙案票", pb)
	docA := e.ok(alice, "/api/tool/doc_save", map[string]any{"project": pa, "title": "甲案文件", "section": "docs", "body": "甲案內文"})["id"].(string)
	docB := e.ok(alice, "/api/tool/doc_save", map[string]any{"project": pb, "title": "乙案文件", "section": "docs", "body": "乙案內文"})["id"].(string)
	e.ok(alice, "/api/tool/agent_upsert", map[string]any{"name": "權限角色", "instructions": "x", "doc_access": map[string]any{pa + ":docs": "read", pb + ":docs": "read"}})

	reads := func(c *client) string {
		var b strings.Builder
		for _, r := range []struct {
			method, path string
			body         map[string]any
		}{
			{"GET", "/api/view/issues", nil},
			{"GET", "/api/view/projects", nil},
			{"GET", "/api/view/agents", nil},
			{"GET", "/api/view/activity", nil},
			{"GET", "/api/issues/" + org, nil},
			{"POST", "/api/action/issue_query", map[string]any{}},
			{"POST", "/api/action/issue_board", map[string]any{}},
			{"POST", "/api/action/activity_history", map[string]any{}},
			{"POST", "/api/tool/issues", map[string]any{"status": "all"}},
			{"POST", "/api/tool/projects", map[string]any{}},
			{"POST", "/api/tool/agents", map[string]any{}},
			{"POST", "/api/tool/overview", map[string]any{}},
		} {
			b.WriteString(rawBody(t, c, r.method, r.path, r.body))
		}
		return b.String()
	}
	total := func() any { return e.ok(bob, "/api/action/issue_query", map[string]any{})["total"] }
	urgent := func() float64 {
		_, v := bob.do("GET", "/api/view/badges", nil, nil)
		return asNumber(v["development"]) - asNumber(badgesBefore["development"])
	}
	hidden := func(stage string, words ...string) {
		t.Helper()
		got := reads(bob)
		for _, w := range words {
			if strings.Contains(got, w) {
				i := strings.Index(got, w)
				t.Errorf("%s:洩漏 %s:…%s…", stage, w, got[max(0, i-120):min(len(got), i+60)])
			}
		}
	}
	// 專案的單筆讀寫:和不存在的一樣
	denied := func(stage, p, doc string, issue map[string]any) {
		t.Helper()
		key, id := issue["key"].(string), issue["id"].(string)
		toolLikeMissing(t, bob, "issues", key, nil)
		toolLikeMissing(t, bob, "issue_update", key, map[string]any{"comment": "偷留言"})
		for _, r := range []struct {
			path string
			body map[string]any
		}{
			{"/api/issues/" + org + "/" + key, map[string]any{"title": "被改"}},
			{"/api/issues/" + org + "/" + id + "/delete", map[string]any{}},
			{"/api/action/issue_resolve", map[string]any{"ref": key}},
			{"/api/action/issue_delete", map[string]any{"id": key}},
			{"/api/action/cabinet_query", map[string]any{"project": p}},
			{"/api/tool/docs", map[string]any{"project": p}},
			{"/api/tool/issue_create", map[string]any{"title": "塞", "project": p}},
			{"/api/issues/" + org, map[string]any{"title": "塞", "project": p}},
		} {
			code, out := bob.do("POST", r.path, r.body, nil)
			if code == 200 && out["error"] == nil {
				t.Errorf("%s:%s %v 應該失敗:%d %v", stage, r.path, r.body, code, out)
			}
			if s := core2s(out); strings.Contains(s, "案票") || strings.Contains(s, "案文件") {
				t.Errorf("%s:%s 洩漏:%v", stage, r.path, out)
			}
		}
		// 錯誤訊息和不存在的一樣
		_, got := bob.do("POST", "/api/issues/"+org+"/"+key+"/delete", map[string]any{}, nil)
		_, want := bob.do("POST", "/api/issues/"+org+"/ISS-999999/delete", map[string]any{}, nil)
		if g, w := strings.ReplaceAll(core2s(got["error"]), key, "<id>"), strings.ReplaceAll(core2s(want["error"]), "ISS-999999", "<id>"); g != w {
			t.Errorf("%s:刪除的錯誤和不存在的不同:%s / %s", stage, g, w)
		}
		// 指到看不到的專案、文件,和指到不存在的回一樣的內容
		for _, r := range []struct {
			path, key, hidden, missing string
			extra                      map[string]any
		}{
			{"/api/action/cabinet_query", "project", p, "PRJ-999", nil},
			{"/api/action/issue_query", "project", p, "PRJ-999", nil},
			{"/api/tool/docs", "project", p, "PRJ-999", nil},
			{"/api/tool/cabinet", "project", p, "PRJ-999", nil},
			{"/api/tool/docs", "id", doc, "DOC-999", nil},
			{"/api/tool/doc_save", "id", doc, "DOC-999", map[string]any{"title": "被改"}},
			{"/api/tool/issue_create", "project", p, "PRJ-999", map[string]any{"title": "塞"}},
			{"/api/issues/" + org, "project", p, "PRJ-999", map[string]any{"title": "塞"}},
			{"/api/action/issue_resolve", "ref", key, "ISS-999999", nil},
		} {
			call := func(v string) (int, string) {
				body := map[string]any{r.key: v}
				for k, x := range r.extra {
					body[k] = x
				}
				code, out := bob.do("POST", r.path, body, nil)
				return code, strings.ReplaceAll(core2sSorted(out), v, "<x>")
			}
			code, got := call(r.hidden)
			wantCode, want := call(r.missing)
			if code != wantCode || got != want {
				t.Errorf("%s:%s %s=%s:%d %s,不存在的是 %d %s", stage, r.path, r.key, r.hidden, code, got, wantCode, want)
			}
		}
		_, out := bob.do("POST", "/api/action/cabinet_doc_get", map[string]any{"id": doc}, nil)
		if out["result"] != nil || out["error"] != nil {
			t.Errorf("%s:cabinet_doc_get 要和不存在一樣(null):%v", stage, out)
		}
		_, out = bob.do("POST", "/api/action/cabinet_doc_html", map[string]any{"id": doc}, nil)
		if strings.Contains(core2s(out), "案內文") {
			t.Errorf("%s:cabinet_doc_html 洩漏:%v", stage, out)
		}
	}

	all := []string{"甲案", "乙案", "AAA", "BBB", pa, pb}
	hidden("加入前", all...)
	denied("加入前", pa, docA, ia)
	if n := total(); asNumber(n) != 0 || urgent() != 0 {
		t.Errorf("非成員的 total、徽章不算受限專案的:%v / %v", n, urgent())
	}

	// 加入甲案:甲案看得到(清單、計數、動態、文件),乙案照樣看不到
	e.ok(alice, "/api/action/project_member_set", map[string]any{"id": pa, "actor": e.userOf("bob").ActorID, "role": "member"})
	got := reads(bob)
	for _, w := range []string{"甲案票", "甲案文件", "甲案", pa} {
		if !strings.Contains(got, w) {
			t.Errorf("專案成員要看得到 %s", w)
		}
	}
	hidden("加入甲案後", "乙案", "BBB", pb)
	denied("加入甲案後", pb, docB, ib)
	if n := total(); asNumber(n) != 1 || urgent() != 1 {
		t.Errorf("成員的 total、徽章只多甲案那一張:%v / %v", n, urgent())
	}
	keyA := ia["key"].(string)
	e.ok(bob, "/api/tool/issues", map[string]any{"id": keyA})
	e.ok(bob, "/api/action/issue_resolve", map[string]any{"ref": keyA})
	e.ok(bob, "/api/action/cabinet_query", map[string]any{"project": pa})
	e.ok(bob, "/api/tool/docs", map[string]any{"id": docA})
	if r := e.ok(bob, "/api/action/cabinet_doc_get", map[string]any{"id": docA}); r["title"] != "甲案文件" {
		t.Error("成員讀專案文件", r)
	}
	e.ok(bob, "/api/tool/issue_update", map[string]any{"id": keyA, "comment": "bob 的工具留言"})
	e.ok(bob, "/api/issues/"+org+"/"+keyA, map[string]any{"title": "甲案票改"})
	e.ok(bob, "/api/tool/issue_create", map[string]any{"title": "甲案成員開的", "project": pa})
	if c := e.creator("甲案成員開的"); c != e.userOf("bob").ActorID {
		t.Error("成員的寫入署名本人", c)
	}
	// 寫入照核心的權限:一般專案成員不能把受限專案的 Issue 搬出去
	if code, out := bob.do("POST", "/api/issues/"+org+"/"+keyA, map[string]any{"project": ""}, nil); code != 403 {
		t.Error("搬出受限專案要專案管理員:", code, out)
	}

	// 移出專案:下一個請求就看不到
	e.ok(alice, "/api/action/project_member_remove", map[string]any{"id": pa, "actor": e.userOf("bob").ActorID})
	hidden("移出後", append(all, "bob 的工具留言")...)
	if n := total(); asNumber(n) != 0 {
		t.Errorf("移出後的 total:%v", n)
	}

	// 組織管理員(不是 web 管理員):全部看得到
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "admin"})
	got = reads(bob)
	for _, w := range []string{"甲案票改", "甲案文件", "乙案票", "乙案", pa, pb} {
		if !strings.Contains(got, w) {
			t.Errorf("組織管理員要看得到 %s", w)
		}
	}
	e.ok(bob, "/api/tool/docs", map[string]any{"id": docB})

	// 查不出呼叫者看得到哪些(沒有呼叫者的分支以外):不是組織成員時全部當作看不到
	if set, unknown := e.app.HiddenProjects(org, core.NewCaller("carol", "Carol", "", false, e.au.UserSettings(e.au.Config()))); !unknown || len(set) != 0 {
		t.Errorf("對不到組織身分要回 unknown:%v %v", set, unknown)
	}
}

// core2sSorted JSON 值的固定寫法(欄位排序),給比對兩個回應用
func core2sSorted(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
