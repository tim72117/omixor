package web

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"omixor-core/model"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"
)

func resultOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	r, _ := out["result"].(map[string]any)
	if r == nil {
		t.Fatalf("沒有 result:%v", out)
	}
	return r
}

func listOf(t *testing.T, out map[string]any) []any {
	t.Helper()
	l, ok := out["result"].([]any)
	if !ok {
		t.Fatalf("result 不是陣列:%v", out)
	}
	return l
}

// 專案代號、project 篩選與 counts、軟刪除、垃圾桶(看、還原、永久刪除)
func TestProjectKeyIssueQueryAndTrash(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir()) // 下面照件數比對:自己一份資料庫,不看別的測試建的 Issue
	c, app := newServer(t, auth.New(&auth.MemorySecrets{}))
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()

	// 專案代號:格式錯誤原樣帶回
	if _, out := c.do("POST", "/api/action/project_create", map[string]any{"name": "壞代號", "key": "a b!"}, nil); out["error"] == nil {
		t.Fatalf("壞的代號要報錯:%v", out)
	}
	_, out := c.do("POST", "/api/action/project_create", map[string]any{"name": "網站", "key": "web"}, nil)
	p := resultOf(t, out)
	pid, _ := p["id"].(string)
	if pid == "" || !strings.EqualFold(p["key"].(string), "WEB") {
		t.Fatalf("專案:%v", out)
	}
	if _, out := c.do("POST", "/api/action/project_create", map[string]any{"name": "另一個", "key": "WEB"}, nil); out["error"] == nil {
		t.Fatalf("重複代號要報錯:%v", out)
	}
	// view projects 含 key(回應是陣列)
	resp, err := c.c.Get(c.srv.URL + "/api/view/projects")
	if err != nil {
		t.Fatal(err)
	}
	var plist []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&plist)
	resp.Body.Close()
	found := false
	for _, m := range plist {
		if m["id"] == pid && m["key"] == "WEB" {
			found = true
		}
	}
	if !found {
		t.Fatalf("view projects 要有含 key 的專案:%v", plist)
	}

	// Issue:一筆放專案、一筆在組織層級
	_, out = c.do("POST", "/api/issues/"+org, map[string]any{"title": "專案內", "project": pid}, nil)
	in := resultOf(t, out)
	if !strings.HasPrefix(in["key"].(string), "WEB-") {
		t.Fatalf("key:%v", in)
	}
	_, out = c.do("POST", "/api/issues/"+org, map[string]any{"title": "組織層級"}, nil)
	top := resultOf(t, out)

	count := func(project string) (int, int) {
		_, o := c.do("POST", "/api/action/issue_query", map[string]any{"project": project}, nil)
		r := resultOf(t, o)
		return len(r["issues"].([]any)), int(r["counts"].(map[string]any)["all"].(float64))
	}
	if n, all := count(pid); n != 1 || all != 1 {
		t.Fatalf("project=%s:%d/%d", pid, n, all)
	}
	if n, all := count("-"); n != 1 || all != 1 || n == 0 {
		t.Fatalf("project=-:%d/%d", n, all)
	}
	if n, _ := count(""); n < 2 {
		t.Fatalf("project 空 = 全部:%d", n)
	}
	_, out = c.do("POST", "/api/action/issue_board", map[string]any{"project": pid}, nil)
	if r := resultOf(t, out); int(r["matched"].(float64)) != 1 {
		t.Fatalf("board:%v", r)
	}

	// 軟刪除:清單移除、進垃圾桶
	if _, out = c.do("POST", "/api/action/issue_delete", map[string]any{"id": in["id"]}, nil); out["error"] != nil {
		t.Fatalf("delete:%v", out)
	}
	if n, _ := count(pid); n != 0 {
		t.Fatalf("刪除後清單要移除:%d", n)
	}
	_, out = c.do("POST", "/api/action/issue_trash", map[string]any{}, nil)
	tr := listOf(t, out)
	if len(tr) != 1 || tr[0].(map[string]any)["key"] != in["key"] || tr[0].(map[string]any)["deletedAt"] == nil {
		t.Fatalf("垃圾桶:%v", tr)
	}
	// 還原
	_, out = c.do("POST", "/api/action/issue_restore", map[string]any{"id": in["id"]}, nil)
	if r := resultOf(t, out); r["deletedAt"] != nil && r["deletedAt"] != "" {
		t.Fatalf("還原:%v", r)
	}
	if n, _ := count(pid); n != 1 {
		t.Fatalf("還原後要回來:%d", n)
	}
	// 永久刪除
	c.do("POST", "/api/action/issue_delete", map[string]any{"id": top["id"]}, nil)
	_, out = c.do("POST", "/api/action/issue_purge", map[string]any{"id": top["id"]}, nil)
	if out["error"] != nil {
		t.Fatalf("purge:%v", out)
	}
	_, out = c.do("POST", "/api/action/issue_trash", map[string]any{}, nil)
	if l := listOf(t, out); len(l) != 0 {
		t.Fatalf("永久刪除後垃圾桶要空:%v", l)
	}
	if n, _ := count("-"); n != 0 {
		t.Fatalf("永久刪除後不見:%d", n)
	}

	// 專案已有 Issue:不能改代號,核心的訊息原樣帶回
	if _, out = c.do("POST", "/api/action/project_update", map[string]any{"id": pid, "key": "NEW"}, nil); out["error"] == nil {
		t.Fatalf("有 Issue 不能改代號:%v", out)
	}
	if _, out = c.do("POST", "/api/action/project_update", map[string]any{"id": pid}, nil); out["error"] == nil {
		t.Fatalf("沒給任何欄位要報錯:%v", out)
	}
}

// 非管理員:垃圾桶三個 action 都是 403,但軟刪除(issue_delete)可以
func TestTrashActionsAdminOnly(t *testing.T) {
	for _, n := range []string{"issue_trash", "issue_restore", "issue_purge"} {
		if _, ok := adminActions[n]; !ok {
			t.Errorf("%s 要在 adminActions", n)
		}
	}
	if _, ok := adminActions["issue_delete"]; ok {
		t.Error("issue_delete 一般成員可以做,不要列進 adminActions")
	}
	resetAuthJSON(t)
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)
	// 真實存在的 Issue(之後要確認非管理員的請求沒有動到它)
	app.Mu.Lock()
	real := app.CreateIssueFor("管理員才能還原", "", "", nil, "", "", "mcp", "", "")
	app.Mu.Unlock()
	if real.ID == "" {
		t.Fatalf("建立 Issue:%v", app.IssueRemoteError())
	}
	if _, out := c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "alice-pw"}, nil); out["error"] != nil {
		t.Fatal(out)
	}
	bob := &client{t: t, srv: c.srv, c: newJarClient()}
	if _, out := bob.do("POST", "/api/auth/login", map[string]any{"username": "bob", "password": "bob-pw"}, nil); out["error"] != nil {
		t.Fatal(out)
	}
	// 軟刪除任何成員都可以(真的刪得掉);之後垃圾桶三個動作對非管理員都是 403,而且沒有動到資料
	if code, out := bob.do("POST", "/api/action/issue_delete", map[string]any{"id": real.ID}, nil); code != 200 {
		t.Fatalf("issue_delete 一般成員要成功:%d %v", code, out)
	}
	for _, n := range []string{"issue_trash", "issue_restore", "issue_purge"} {
		if code, out := bob.do("POST", "/api/action/"+n, map[string]any{"id": real.ID}, nil); code != 403 {
			t.Errorf("%s:非管理員要 403,收到 %d %v", n, code, out)
		}
	}
	trashed, err := core.Action(app, "issue_trash", map[string]any{})
	inTrash := false
	for _, i := range trashed.([]model.Issue) {
		inTrash = inTrash || i.ID == real.ID
	}
	if err != nil || !inTrash {
		t.Fatalf("非管理員的 restore / purge 不能動到垃圾桶裡的 Issue:%v %v", trashed, err)
	}
	_, _ = core.Action(app, "issue_purge", map[string]any{"id": real.ID}) // 清掉,不留在共用的資料庫裡
}

// 409:版本衝突的 current、currentVersion 在 /api/tool 與 /api/action 的回應頂層
func TestVersionConflictBodies(t *testing.T) {
	os.Remove(core.DataRoot() + "/auth.json")
	c, app := newServer(t, auth.New(&auth.MemorySecrets{}))
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()
	_, out := c.do("POST", "/api/issues/"+org, map[string]any{"title": "衝突"}, nil)
	is := resultOf(t, out)
	v1 := is["version"]
	if v1 == nil {
		t.Fatalf("Issue 要有 version:%v", is)
	}
	// 第一次帶 v1 成功
	_, out = c.do("POST", "/api/tool/issue_update", map[string]any{"id": is["id"], "title": "第二版", "version": v1}, nil)
	if out["error"] != nil {
		t.Fatalf("第一次更新:%v", out)
	}
	// 再帶舊的 v1:衝突
	code, out := c.do("POST", "/api/tool/issue_update", map[string]any{"id": is["id"], "title": "第三版", "version": v1}, nil)
	if code != 409 || out["error"] == nil || out["currentVersion"] == nil || out["current"] == nil {
		t.Fatalf("/api/tool 的 409 要有 current、currentVersion:%d %v", code, out)
	}
	if cur, _ := out["current"].(map[string]any); cur["title"] != "第二版" {
		t.Fatalf("current 要是最新內容:%v", out["current"])
	}
	// /api/action:文件櫃結構用 expectedVersion
	code, out = c.do("POST", "/api/action/cabinet_layout_save", map[string]any{"layout": []any{map[string]any{"id": "x", "label": "X"}}, "expectedVersion": 999999}, nil)
	if out["error"] == nil || out["currentVersion"] == nil {
		t.Fatalf("/api/action 的 409 要有 currentVersion:%d %v", code, out)
	}
	if code != 409 {
		t.Fatalf("狀態碼要是 409:%d", code)
	}
}
