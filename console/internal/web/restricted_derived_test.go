package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// 沒有 project 欄位的衍生資料也不能洩漏受限專案(core/restricted_hidden.go):動態牆(含搬走前的舊編號)、
// total 與 counts、看板、徽章、角色任務、待決定;view me 是登入者本人。bob 不是 web 管理員,alice 是
func TestRestrictedDerivedDataHiddenFromNonAdmin(t *testing.T) {
	bob, alice, app := adminTestClients(t)
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()

	call := func(c *client, method, path string, body map[string]any) any {
		t.Helper()
		code, out := c.do(method, path, body, nil)
		if code != 200 || out["error"] != nil {
			t.Fatalf("%s:%d %v", path, code, out)
		}
		return out["result"]
	}
	post := func(c *client, path string, body map[string]any) map[string]any {
		t.Helper()
		r, _ := call(c, "POST", path, body).(map[string]any)
		return r
	}
	counts := func(c *client) (query, tool any) {
		q := post(c, "/api/action/issue_query", map[string]any{"q": "衍生"})
		board := post(c, "/api/action/issue_board", map[string]any{})
		list := post(c, "/api/tool/issues", map[string]any{"query": "衍生", "status": "all"})
		return []any{q["total"], q["counts"], board["total"], board["labels"]}, list["total"]
	}

	_, before := bob.do("GET", "/api/view/badges", nil, nil)
	post(alice, "/api/issues/"+org, map[string]any{"title": "衍生公開", "priority": "urgent"})
	wantQuery, wantTool := counts(bob)

	sec := post(alice, "/api/action/project_create", map[string]any{"name": "衍生機密", "key": "DRV", "restricted": true})["id"].(string)
	// 先在組織層級建立(動態牆記的是舊編號)再搬進受限專案
	moved := post(alice, "/api/issues/"+org, map[string]any{"title": "衍生搬走", "priority": "urgent", "labels": []any{"衍生標籤"}})
	oldKey := moved["key"].(string)
	post(alice, "/api/issues/"+org+"/"+oldKey, map[string]any{"project": sec})
	post(alice, "/api/issues/"+org, map[string]any{"title": "衍生直接建", "priority": "urgent", "project": sec})
	post(alice, "/api/tool/doc_save", map[string]any{"project": sec, "title": "衍生機密文件", "section": "docs", "body": "內文"})
	// 受限專案 Issue 的角色任務、待決定、綁 Issue 的頻道(名稱就是標題)
	hidden := post(alice, "/api/issues/"+org, map[string]any{"title": "衍生任務來源", "project": sec})
	key := hidden["key"].(string)
	post(alice, "/api/tool/agent_upsert", map[string]any{"name": "衍生角色", "instructions": "x"})
	post(alice, "/api/tool/agent_task_create", map[string]any{"assign_to": "衍生角色", "title": "衍生任務標題", "issue": key})
	post(alice, "/api/tool/issue_decision_add", map[string]any{"kind": "decision", "source_issue": key, "title": "衍生待決定", "options": []any{"甲", "乙"}})
	post(alice, "/api/action/channel_create", map[string]any{"name": "衍生頻道名", "issueId": hidden["id"]})

	gotQuery, gotTool := counts(bob)
	if !reflect.DeepEqual(gotQuery, wantQuery) || !reflect.DeepEqual(gotTool, wantTool) {
		t.Errorf("受限專案的 Issue 算進了數字:%v / %v,原本 %v / %v", gotQuery, gotTool, wantQuery, wantTool)
	}
	if adminQuery, adminTool := counts(alice); reflect.DeepEqual(adminQuery, wantQuery) || reflect.DeepEqual(adminTool, wantTool) {
		t.Errorf("管理員照舊算全部:%v / %v", adminQuery, adminTool)
	}
	_, after := bob.do("GET", "/api/view/badges", nil, nil)
	if b, a := before["development"], after["development"]; asNumber(a)-asNumber(b) != 1 {
		t.Errorf("徽章只該多公開的那一張:%v → %v", b, a)
	}

	// 動態牆:view、action、overview、list
	leaks := []string{"衍生搬走", "衍生直接建", "衍生機密文件", "衍生任務", "衍生待決定", "衍生頻道名"}
	feeds := []struct {
		method, path string
		body         map[string]any
	}{
		{"GET", "/api/view/agents", nil},
		{"POST", "/api/tool/agents", map[string]any{}},
		{"POST", "/api/tool/agent_tasks", map[string]any{}},
		{"POST", "/api/tool/issue_decisions", map[string]any{"status": "all"}},
		{"GET", "/api/view/issue_repos", nil},
		{"GET", "/api/view/activity", nil},
		{"POST", "/api/action/activity_history", map[string]any{}},
		{"POST", "/api/tool/overview", map[string]any{}},
		{"POST", "/api/tool/list", map[string]any{"collection": "activity", "limit": 200}},
		{"POST", "/api/tool/list", map[string]any{"collection": "activity", "search": "衍生搬走"}},
	}
	for _, f := range feeds {
		text := rawBody(t, bob, f.method, f.path, f.body)
		for _, leak := range leaks {
			if strings.Contains(text, leak) {
				t.Errorf("%s %v 洩漏 %s:%s", f.path, f.body, leak, text)
			}
		}
	}
	if text := rawBody(t, bob, "GET", "/api/view/activity", nil); !strings.Contains(text, "衍生公開") {
		t.Error("公開的動態照舊看得到", text)
	}
	adminFeed := rawBody(t, alice, "GET", "/api/view/activity", nil) + rawBody(t, alice, "GET", "/api/view/agents", nil)
	for _, want := range []string{"衍生搬走", "衍生直接建", "衍生機密文件", "衍生任務標題", "衍生頻道名"} {
		if !strings.Contains(adminFeed, want) {
			t.Errorf("管理員照舊看得到 %s:%s", want, adminFeed)
		}
	}

	// 登入者看到的「我」是自己,不是本機身分
	_, me := bob.do("GET", "/api/view/me", nil, nil)
	if me["id"] != "bob" || me["signedIn"] != true {
		t.Errorf("view me:%v", me)
	}
}

// rawBody 回應原文(view 可能回陣列)
func rawBody(t *testing.T, c *client, method, path string, body map[string]any) string {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s:%d %s", path, resp.StatusCode, out)
	}
	return string(out)
}

func asNumber(v any) float64 { n, _ := v.(float64); return n }

// 登入者不在人員清單也能指派給自己、當指揮官、加入審查;記的是他本人
func TestSignedInUserCanActAsSelf(t *testing.T) {
	bob, _, app := adminTestClients(t)
	app.Mu.Lock()
	pr, _ := core.ScalarString(app.C["pull_requests"][0]["id"])
	app.Mu.Unlock()
	for path, body := range map[string]map[string]any{
		"/api/tool/work_item_create":           {"title": "後續", "type": "incident", "assignee": "bob"},
		"/api/tool/incident_declare":           {"title": "本人指揮", "sev": 3, "impact": "x", "commander": "bob"},
		"/api/action/pull_request_join_review": {"id": pr, "person": "bob"},
	} {
		if code, out := bob.do("POST", path, body, nil); code != 200 || out["error"] != nil {
			t.Errorf("%s:%d %v", path, code, out)
		}
	}
	// 別人的帳號不在人員清單仍然不行
	if code, _ := bob.do("POST", "/api/tool/work_item_create", map[string]any{"title": "x", "type": "incident", "assignee": "nobody"}, nil); code == 200 {
		t.Error("不存在的人要擋")
	}
}
