package web

import (
	"os"
	"strconv"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
)

func newIssueAPIClient(t *testing.T) (*client, string) {
	t.Helper()
	os.Remove(core.DataRoot() + "/auth.json")
	c, app := newServer(t, auth.New(&auth.MemorySecrets{}))
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()
	return c, org
}

func newAPIIssue(t *testing.T, c *client, org, title string) map[string]any {
	t.Helper()
	code, out := c.do("POST", "/api/issues/"+org, map[string]any{"title": title}, nil)
	if code != 200 {
		t.Fatalf("建立:%d %v", code, out)
	}
	return resultOf(t, out)
}

func itoa(n int) string { return strconv.Itoa(n) }

// getAPIIssue 從 GET /api/issues/{org} 找一筆
func getAPIIssue(t *testing.T, c *client, org, id string) map[string]any {
	t.Helper()
	code, out := c.do("GET", "/api/issues/"+org, nil, nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	for _, x := range listOf(t, out) {
		if m := x.(map[string]any); m["id"] == id {
			return m
		}
	}
	t.Fatalf("找不到 %s", id)
	return nil
}

// REST:If-Match(含引號、W/)與 body version;舊版 → 409 帶 current、currentVersion;最新版成功
func TestIssueAPIIfMatchAndBodyVersion(t *testing.T) {
	c, org := newIssueAPIClient(t)
	is := newAPIIssue(t, c, org, "樂觀鎖")
	id := is["id"].(string)
	path := "/api/issues/" + org + "/" + id
	v1 := int(is["version"].(float64))

	// If-Match 最新版:成功,版本往前
	code, out := c.do("POST", path, map[string]any{"title": "第二版"}, map[string]string{"If-Match": `"` + itoa(v1) + `"`})
	if code != 200 {
		t.Fatalf("If-Match 最新版要成功:%d %v", code, out)
	}
	v2 := int(resultOf(t, out)["version"].(float64))
	if v2 <= v1 {
		t.Fatalf("版本要往前:%d → %d", v1, v2)
	}
	// If-Match 舊版:409 + current、currentVersion
	code, out = c.do("POST", path, map[string]any{"title": "第三版"}, map[string]string{"If-Match": itoa(v1)})
	if code != 409 || out["currentVersion"] == nil || out["current"] == nil {
		t.Fatalf("If-Match 舊版要 409:%d %v", code, out)
	}
	if cur, _ := out["current"].(map[string]any); cur["title"] != "第二版" {
		t.Fatalf("current 要是最新內容:%v", out["current"])
	}
	// 弱 ETag:W/"版本" 照版本比;* 不檢查
	if code, _ = c.do("POST", path, map[string]any{"title": "弱"}, map[string]string{"If-Match": `W/"` + itoa(v1) + `"`}); code != 409 {
		t.Fatalf("W/ 舊版要 409:%d", code)
	}
	if code, out = c.do("POST", path, map[string]any{"priority": "high"}, map[string]string{"If-Match": "*"}); code != 200 {
		t.Fatalf("If-Match * 不檢查:%d %v", code, out)
	}
	// body version 舊版 → 409
	if code, out = c.do("POST", path, map[string]any{"title": "x", "version": v1}, nil); code != 409 || out["current"] == nil {
		t.Fatalf("body 舊版要 409:%d %v", code, out)
	}
	// body version(最新)優先於 If-Match(舊)
	cur := int(getAPIIssue(t, c, org, id)["version"].(float64))
	if code, out = c.do("POST", path, map[string]any{"title": "ok", "version": cur}, map[string]string{"If-Match": itoa(v1)}); code != 200 {
		t.Fatalf("body version 優先:%d %v", code, out)
	}
}

// L3:version / If-Match 的邊界:null、0 = 沒給(不檢查);負數、非整數 400
func TestIssueAPIVersionEdges(t *testing.T) {
	c, org := newIssueAPIClient(t)
	is := newAPIIssue(t, c, org, "邊界")
	path := "/api/issues/" + org + "/" + is["id"].(string)
	if code, out := c.do("POST", path, map[string]any{"priority": "high"}, nil); code != 200 { // 版本往前,讓標頭的 1 變成舊版
		t.Fatalf("先改一次:%d %v", code, out)
	}
	for name, tc := range map[string]struct {
		body map[string]any
		hdr  map[string]string
		want int
	}{
		"body null":   {map[string]any{"title": "a", "version": nil}, nil, 200},
		"body 0":      {map[string]any{"title": "b", "version": 0}, nil, 200},
		"body -1":     {map[string]any{"title": "c", "version": -1}, nil, 400},
		"body 字串":     {map[string]any{"title": "d", "version": "x"}, nil, 400},
		"If-Match 0":  {map[string]any{"title": "e"}, map[string]string{"If-Match": "0"}, 200},
		"If-Match -3": {map[string]any{"title": "f"}, map[string]string{"If-Match": "-3"}, 400},
		"If-Match 文字": {map[string]any{"title": "g"}, map[string]string{"If-Match": "abc"}, 400},
		"body 0 看標頭":  {map[string]any{"title": "h", "version": 0}, map[string]string{"If-Match": "1"}, 409},
	} {
		if code, out := c.do("POST", path, tc.body, tc.hdr); code != tc.want {
			t.Errorf("%s:要 %d,收到 %d %v", name, tc.want, code, out)
		}
	}
}

// H2:給 comments(整份取代)也要檢查 version / If-Match
func TestIssueAPICommentsReplaceChecksVersion(t *testing.T) {
	c, org := newIssueAPIClient(t)
	is := newAPIIssue(t, c, org, "留言取代")
	id := is["id"].(string)
	path := "/api/issues/" + org + "/" + id
	v1 := int(is["version"].(float64))
	if code, out := c.do("POST", path, map[string]any{"title": "別人改過"}, nil); code != 200 {
		t.Fatalf("先改一次:%d %v", code, out)
	}
	comments := []any{map[string]any{"text": "整份取代", "author": "mcp"}}
	// body 舊版
	code, out := c.do("POST", path, map[string]any{"comments": comments, "version": v1}, nil)
	if code != 409 || out["current"] == nil || out["currentVersion"] == nil {
		t.Fatalf("comments + 舊 version 要 409:%d %v", code, out)
	}
	// If-Match 舊版
	code, out = c.do("POST", path, map[string]any{"comments": comments}, map[string]string{"If-Match": itoa(v1)})
	if code != 409 || out["current"] == nil {
		t.Fatalf("comments + 舊 If-Match 要 409:%d %v", code, out)
	}
	// 沒有被取代
	got := getAPIIssue(t, c, org, id)
	if cs, _ := got["comments"].([]any); len(cs) != 0 {
		t.Fatalf("409 時留言不能被改:%v", cs)
	}
	// 最新版成功
	cur := int(got["version"].(float64))
	code, out = c.do("POST", path, map[string]any{"comments": comments, "version": cur}, nil)
	if code != 200 {
		t.Fatalf("最新版要成功:%d %v", code, out)
	}
	if cs, _ := resultOf(t, out)["comments"].([]any); len(cs) != 1 {
		t.Fatalf("留言要被取代:%v", out)
	}
	// 沒給 version:照舊直接套用
	if code, out = c.do("POST", path, map[string]any{"comments": []any{}}, nil); code != 200 {
		t.Fatalf("沒給 version 照舊套用:%d %v", code, out)
	}
}

// M2:REST delete 不吞錯:不存在 404;存在的移到垃圾桶(軟刪除)、再刪一次 404
func TestIssueAPIDeleteReportsErrors(t *testing.T) {
	c, org := newIssueAPIClient(t)
	if code, out := c.do("POST", "/api/issues/"+org+"/NOPE-999/delete", nil, nil); code != 404 || out["error"] == nil {
		t.Fatalf("不存在要 404:%d %v", code, out)
	}
	is := newAPIIssue(t, c, org, "要刪的")
	path := "/api/issues/" + org + "/" + is["id"].(string) + "/delete"
	if code, out := c.do("POST", path, nil, nil); code != 200 {
		t.Fatalf("刪除:%d %v", code, out)
	}
	_, out := c.do("POST", "/api/action/issue_trash", map[string]any{}, nil)
	found := false
	for _, x := range listOf(t, out) {
		found = found || x.(map[string]any)["id"] == is["id"]
	}
	if !found {
		t.Fatalf("REST delete 要走垃圾桶(軟刪除):%v", out)
	}
	if code, _ := c.do("POST", path, nil, nil); code != 404 {
		t.Fatalf("已經在垃圾桶,再刪要 404:%d", code)
	}
	c.do("POST", "/api/action/issue_purge", map[string]any{"id": is["id"]}, nil)
}

// L5:issue_restore / issue_delete / issue_purge 不存在的 id 回 404
func TestTrashActionsNotFoundStatus(t *testing.T) {
	c, _ := newIssueAPIClient(t)
	for _, n := range []string{"issue_restore", "issue_delete", "issue_purge"} {
		if code, out := c.do("POST", "/api/action/"+n, map[string]any{"id": "NOPE-999"}, nil); code != 404 {
			t.Errorf("%s 不存在要 404:%d %v", n, code, out)
		}
	}
}
