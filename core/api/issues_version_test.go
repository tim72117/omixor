package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// rawDo 直接打 HTTP(client 包裝不能帶 If-Match 標頭),回狀態碼與解開的 JSON 物件
func (e *env) rawDo(c *client.Client, method, path string, hdr map[string]string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (e *env) issueOrg(t *testing.T) (boss, alice, bob *client.Client) {
	t.Helper()
	boss = e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveMember("lab", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	return boss, e.login("evo-alice"), e.login("evo-bob")
}

// 版本檢查經 API:body 的 version 與 If-Match 標頭兩種;409 的回應最上層有 error、current、currentVersion;沒帶版本照舊套用
func TestIssueVersionViaAPI(t *testing.T) {
	e := newEnv(t)
	_, alice, bob := e.issueOrg(t)
	i, err := alice.CreateIssue("lab", service.IssueInput{Title: "t"})
	if err != nil || i.Version != 1 {
		t.Fatal(i, err)
	}
	path := "/api/orgs/lab/issues/" + i.Key

	// body version:對的成功(Version 2)、舊的 409
	code, out := e.rawDo(alice, "POST", path, nil, map[string]any{"title": "t2", "version": 1})
	if code != 200 {
		t.Fatal(code, out)
	}
	res, _ := out["result"].(map[string]any)
	if res["version"] != float64(2) || res["title"] != "t2" || res["key"] != "ISS-1" {
		t.Fatal("成功的回應", out)
	}
	code, out = e.rawDo(bob, "POST", path, nil, map[string]any{"title": "蓋掉", "version": 1})
	if code != 409 {
		t.Fatal("body version 舊的要 409", code, out)
	}
	cur, _ := out["current"].(map[string]any)
	if out["currentVersion"] != float64(2) || cur == nil || cur["title"] != "t2" || cur["version"] != float64(2) || out["error"] == nil {
		t.Fatal("409 回應最上層要有 error、current、currentVersion:", out)
	}
	if status(func() error {
		_, err := bob.UpdateIssue("lab", i.Key, service.IssuePatch{Title: sp("蓋掉"), Version: ip(1)})
		return err
	}()) != 409 {
		t.Fatal("client 也是 409")
	}

	// If-Match:不帶引號、帶引號、W/ 都認得;過期的 409
	vs := func(n int) string { b, _ := json.Marshal(n); return string(b) }
	v := 2
	for _, h := range []func(int) string{vs, func(n int) string { return `"` + vs(n) + `"` }, func(n int) string { return `W/"` + vs(n) + `"` }} {
		code, out = e.rawDo(alice, "POST", path, map[string]string{"If-Match": h(v)}, map[string]any{"body": "b" + vs(v)})
		if code != 200 {
			t.Fatalf("If-Match %s:%d %v", h(v), code, out)
		}
		v++
		code, out = e.rawDo(bob, "POST", path, map[string]string{"If-Match": h(v - 1)}, map[string]any{"body": "蓋掉"})
		if code != 409 || out["currentVersion"] != float64(v) {
			t.Fatalf("If-Match %s 過期要 409:%d %v", h(v-1), code, out)
		}
	}
	// If-Match 不對
	code, out = e.rawDo(alice, "POST", path, map[string]string{"If-Match": `"1"`}, map[string]any{"body": "x"})
	if code != 409 || out["currentVersion"] != float64(v) {
		t.Fatal("If-Match 不符 409", code, out)
	}
	// body version 優先於 If-Match:body 對、標頭錯 → 成功;body 錯、標頭對 → 409
	code, _ = e.rawDo(alice, "POST", path, map[string]string{"If-Match": `"1"`}, map[string]any{"body": "p1", "version": v})
	if code != 200 {
		t.Fatal("body version 優先(對)", code)
	}
	v++
	code, _ = e.rawDo(alice, "POST", path, map[string]string{"If-Match": vs(v)}, map[string]any{"body": "p2", "version": 1})
	if code != 409 {
		t.Fatal("body version 優先(錯)", code)
	}
	// If-Match 不是數字:忽略(照舊套用)
	code, _ = e.rawDo(alice, "POST", path, map[string]string{"If-Match": "*"}, map[string]any{"body": "star"})
	if code != 200 {
		t.Fatal("If-Match 不是數字照舊套用", code)
	}
	v++
	// 沒帶版本:照舊套用
	code, out = e.rawDo(alice, "POST", path, nil, map[string]any{"body": "plain"})
	if code != 200 {
		t.Fatal(code, out)
	}
	v++
	if g, _ := alice.GetIssue("lab", i.Key); g.Version != v || g.Body != "plain" {
		t.Fatalf("Version 應該 %d:%+v", v, g)
	}
	// 留言不增加 Version
	if _, err := bob.AddIssueComment("lab", i.Key, service.CommentInput{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if g, _ := alice.GetIssue("lab", i.Key); g.Version != v {
		t.Fatal("留言不增加 Version", g.Version, v)
	}
}

func ip(n int) *int { return &n }

// 軟刪除、還原、永久刪除、垃圾桶列表與匯出經 API 的權限與狀態碼
func TestIssueTrashViaAPI(t *testing.T) {
	e := newEnv(t)
	boss, alice, bob := e.issueOrg(t)
	if _, err := boss.SaveMember("lab", "alice@example.com", "admin"); err != nil { // alice 升為組織管理員;bob 還是成員
		t.Fatal(err)
	}
	i, err := bob.CreateIssue("lab", service.IssueInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.DeleteIssue("lab", i.Key); err != nil {
		t.Fatal("成員可以刪", err)
	}
	if _, err := bob.GetIssue("lab", i.Key); status(err) != 404 {
		t.Fatal(err)
	}
	if l, _ := bob.ListIssues("lab"); len(l) != 0 {
		t.Fatal(l)
	}
	if _, err := bob.RestoreIssue("lab", i.Key); status(err) != 403 {
		t.Fatal("成員不能還原", err)
	}
	if err := bob.PurgeIssue("lab", i.Key); status(err) != 403 {
		t.Fatal("成員不能永久刪除", err)
	}
	code, _ := e.rawDo(bob, "GET", "/api/orgs/lab/issues?deleted=1", nil, nil)
	if code != 403 {
		t.Fatal("成員不能看垃圾桶", code)
	}
	code, _ = e.rawDo(bob, "GET", "/api/orgs/lab/issues/export?deleted=1", nil, nil)
	if code != 403 {
		t.Fatal("成員不能匯出含已刪除", code)
	}
	// 管理員:垃圾桶列表(可與 project 篩選併用)、匯出含已刪除
	l, err := alice.QueryIssues("lab", model.IssueQuery{Deleted: true})
	if err != nil || len(l) != 1 || l[0].Key != "ISS-1" || l[0].DeletedAt == nil {
		t.Fatalf("垃圾桶:%+v %v", l, err)
	}
	if l, _ := alice.QueryIssues("lab", model.IssueQuery{Deleted: true, Project: "PRJ-1"}); len(l) != 0 {
		t.Fatal("垃圾桶可以加專案篩選", l)
	}
	if l, _ := alice.ExportIssues("lab"); len(l) != 0 {
		t.Fatal("預設匯出不含已刪除", l)
	}
	code, out := e.rawDo(alice, "GET", "/api/orgs/lab/issues/export?deleted=1", nil, nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	if r, err := alice.RestoreIssue("lab", i.Key); err != nil || r.DeletedAt != nil || r.Version != 3 {
		t.Fatal("還原", r, err)
	}
	if _, err := alice.RestoreIssue("lab", i.Key); status(err) != 404 {
		t.Fatal("不在垃圾桶 404", err)
	}
	if err := alice.PurgeIssue("lab", i.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.GetIssue("lab", i.ID); status(err) != 404 {
		t.Fatal("永久刪除後 404", err)
	}
	if err := alice.PurgeIssue("lab", i.Key); status(err) != 404 {
		t.Fatal(err)
	}
}
