package web

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"opsweb/internal/auth"
	"opsweb/internal/auth/authtest"
	"opsweb/internal/core"
)

func issueList(t *testing.T, c *client, org string) []any {
	t.Helper()
	code, out := c.do("GET", "/api/issues/"+org, nil, nil)
	if code != 200 {
		t.Fatalf("GET %s:%d %v", org, code, out)
	}
	list, _ := out["result"].([]any)
	return list
}

// Swift 版透過這組 API 讀寫 Issue:原始格式(作者字串)、作者照 body、狀態變了有關閉時間、存到組織的 issues.json
func TestIssueAPICurrentOrg(t *testing.T) {
	os.Remove(core.DataRoot() + "/auth.json")
	c, app := newServer(t, auth.New(&auth.MemorySecrets{}))
	app.Mu.Lock()
	org := app.CurrentOrgID()
	before := len(app.Issues())
	app.Mu.Unlock()

	_, out := c.do("POST", "/api/issues/"+org, map[string]any{
		"title": "API 建立", "body": "內文", "priority": "high", "labels": []string{"a", " a ", "b"},
		"source": "Swift", "link": "https://example.com", "author": "mcp",
	}, nil)
	is, _ := out["result"].(map[string]any)
	if is == nil || is["createdBy"] != "mcp" || is["priority"] != "high" || len(is["labels"].([]any)) != 2 {
		t.Fatalf("建立結果不對:%v", out)
	}
	id := is["id"].(string)
	app.Mu.Lock()
	if len(app.Issues()) != before+1 || app.Issues()[0].ID != id {
		t.Fatal("app 的清單沒更新")
	}
	if !strings.Contains(app.C["activity"][0]["text"].(string), "建立 "+id) {
		t.Fatal("目前組織要記動態牆")
	}
	app.Mu.Unlock()
	if list := issueList(t, c, org); len(list) != before+1 || list[0].(map[string]any)["id"] != id {
		t.Fatalf("清單不對:%v", list)
	}
	if _, out := c.do("POST", "/api/issues/"+org, map[string]any{"title": "x", "priority": "p0"}, nil); out["error"] == nil {
		t.Fatal("錯的優先度要報錯")
	}
	if _, out := c.do("POST", "/api/issues/"+org, map[string]any{"body": "沒有標題"}, nil); out["error"] == nil {
		t.Fatal("沒有標題要報錯")
	}

	// 修改:給的欄位整個取代;關閉時記時間;留言照給的內容
	_, out = c.do("POST", "/api/issues/"+org+"/"+strings.ToLower(id), map[string]any{
		"author": "agent:pm", "status": "closed", "labels": []string{"c"},
		"comments": []any{map[string]any{"id": "C1", "author": "agent:pm", "text": "看過了", "at": "2026-11-12T01:02:03Z"}},
	}, nil)
	is, _ = out["result"].(map[string]any)
	if is == nil || is["status"] != "closed" || is["closedAt"] == nil || is["title"] != "API 建立" || is["body"] != "內文" {
		t.Fatalf("修改結果不對:%v", out)
	}
	cm := is["comments"].([]any)[0].(map[string]any)
	if cm["id"] != "C1" || cm["at"] != "2026-11-12T01:02:03Z" || cm["author"] != "agent:pm" {
		t.Fatalf("留言不對:%v", cm)
	}
	app.Mu.Lock()
	if !strings.Contains(app.C["activity"][0]["text"].(string), "關閉 "+id) {
		t.Fatal("關閉要記動態牆")
	}
	app.Mu.Unlock()
	if _, out := c.do("POST", "/api/issues/"+org+"/"+id, map[string]any{"status": "done"}, nil); out["error"] == nil {
		t.Fatal("錯的狀態要報錯")
	}
	if _, out := c.do("POST", "/api/issues/"+org+"/"+id, map[string]any{"title": " "}, nil); out["error"] == nil {
		t.Fatal("空標題要報錯")
	}
	if code, _ := c.do("POST", "/api/issues/"+org+"/ISS-99999", map[string]any{"title": "x"}, nil); code != 404 {
		t.Fatal("找不到要 404")
	}

	// 存檔:issues.json 是原始格式
	var saved []core.OpsIssue
	if err := core.ReadJSON(filepath.Join(core.OrgDir(org), "issues.json"), &saved); err != nil || saved[0].ID != id || saved[0].Status != "closed" {
		t.Fatalf("issues.json 沒存:%v", err)
	}

	_, out = c.do("POST", "/api/issues/"+org+"/"+id+"/delete", map[string]any{}, nil)
	if out["result"] != true || len(issueList(t, c, org)) != before {
		t.Fatalf("刪除不對:%v", out)
	}
}

// 不是 ops-web 目前組織的 Issue:讀寫那個組織自己的資料,不動目前組織的清單與動態牆
func TestIssueAPIOtherOrg(t *testing.T) {
	os.Remove(core.DataRoot() + "/auth.json")
	c, app := newServer(t, auth.New(&auth.MemorySecrets{}))
	other := "other-org-api"
	app.Mu.Lock()
	cur := len(app.Issues())
	acts := len(app.C["activity"])
	app.Mu.Unlock()

	_, out := c.do("POST", "/api/issues/"+other, map[string]any{"title": "別的組織", "author": "tim"}, nil)
	is, _ := out["result"].(map[string]any)
	if is == nil || is["createdBy"] != "tim" {
		t.Fatal(out)
	}
	_, out = c.do("POST", "/api/issues/"+other, map[string]any{"title": "第二筆"}, nil)
	if out["result"].(map[string]any)["id"] != "ISS-"+strconv.Itoa(len(issueList(t, c, other))) {
		t.Fatalf("編號要接著那個組織的:%v", out)
	}
	list := issueList(t, c, other)
	if list[0].(map[string]any)["title"] != "第二筆" {
		t.Fatal("新的在前")
	}
	app.Mu.Lock()
	if len(app.Issues()) != cur || len(app.C["activity"]) != acts {
		t.Fatal("不該動目前組織")
	}
	app.Mu.Unlock()
	if !core.Exists(filepath.Join(core.OrgDir(other), "issues.json")) {
		t.Fatal("要存在那個組織的資料夾")
	}
	if code, _ := c.do("GET", "/api/issues/..%2Fx", nil, nil); code != 400 && code != 404 {
		t.Fatalf("不合法的組織 id:%d", code)
	}
	if code, _ := c.do("GET", "/api/issues/a.b", nil, nil); code != 400 {
		t.Fatalf("不合法的組織 id 要 400:%d", code)
	}
}

// 啟用 LDAP 後:沒登入要 401;OPS_API_TOKEN 可以用(作者照 body);登入者呼叫時作者是登入者
func TestIssueAPIAuth(t *testing.T) {
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)
	defer func() { os.Remove(core.DataRoot() + "/auth.json") }()
	_, out := c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "alice-pw"}, nil)
	if out["error"] != nil {
		t.Fatal(out)
	}
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()
	if code, _ := c.do("GET", "/api/issues/"+org, nil, nil); code != 401 {
		t.Fatalf("沒登入要 401:%d", code)
	}
	t.Setenv("OPS_API_TOKEN", "test-token-0123456789")
	if code, _ := c.do("GET", "/api/issues/"+org, nil, map[string]string{"Authorization": "Bearer wrong-token-0123456789"}); code != 401 {
		t.Fatalf("錯的 token 要 401:%d", code)
	}
	if code, _ := c.do("GET", "/api/view/alerts", nil, map[string]string{"Authorization": "Bearer test-token-0123456789"}); code != 401 {
		t.Fatalf("token 只能用在 /api/issues/:%d", code)
	}
	tok := map[string]string{"Authorization": "Bearer test-token-0123456789"}
	_, out = c.do("POST", "/api/issues/"+org, map[string]any{"title": "token 建立", "author": "mcp"}, tok)
	if is, _ := out["result"].(map[string]any); is == nil || is["createdBy"] != "mcp" {
		t.Fatalf("token 呼叫作者照 body:%v", out)
	}
	c.do("POST", "/api/auth/login", map[string]any{"username": "bob", "password": "bob-pw"}, nil)
	_, out = c.do("POST", "/api/issues/"+org, map[string]any{"title": "登入者建立", "author": "mcp"}, nil)
	if is, _ := out["result"].(map[string]any); is == nil || is["createdBy"] != "bob" {
		t.Fatalf("登入者呼叫作者是登入者:%v", out)
	}
}
