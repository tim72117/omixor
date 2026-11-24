package mcp_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
)

// report_problem:建立(labels、source、優先度、內文區段、診斷)、遮蔽機密、欄位上限、去重、return=full、OAuth 與成員資格。

func reportCall(t *testing.T, h http.Handler, args map[string]any) result {
	t.Helper()
	if _, ok := args["org"]; !ok {
		args["org"] = "lab"
	}
	return callRes(t, h, "report_problem", args)
}

func reportIssue(t *testing.T, u *uni, key string) model.Issue {
	t.Helper()
	l, err := u.svc.Store.ListIssues("lab")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range l {
		if i.Display() == key {
			return i
		}
	}
	t.Fatalf("找不到 %s", key)
	return model.Issue{}
}

func TestReportProblemCreate(t *testing.T) {
	u := newUni(t)
	for _, c := range []struct{ sev, prio string }{{"blocker", "high"}, {"annoying", "normal"}, {"idea", "low"}, {"", "normal"}} {
		args := map[string]any{"title": "卡住 " + c.sev, "what_happened": "called issues", "expected": "a list", "tool": "issues", "error": "boom " + c.sev}
		if c.sev != "" {
			args["severity"] = c.sev
		}
		r := reportCall(t, u.unified, args)
		if r.isErr {
			t.Fatal(r.all())
		}
		ack := r.obj(t)
		if ack["deduplicated"] != false || ack["changed"].([]any)[0] != "created" {
			t.Fatalf("ack:%v", ack)
		}
		for _, k := range []string{"key", "id", "status", "version", "updatedAt"} {
			if ack[k] == nil {
				t.Fatalf("ack 缺 %s:%v", k, ack)
			}
		}
		i := reportIssue(t, u, ack["key"].(string))
		if i.Priority != c.prio || i.Source != "mcp report_problem" || i.Status != "open" {
			t.Fatalf("issue:%+v", i)
		}
		if len(i.Labels) != 2 || i.Labels[0] != "mcp-feedback" || !strings.HasPrefix(i.Labels[1], "fp:") || len(i.Labels[1]) != 3+12 {
			t.Fatalf("labels:%v", i.Labels)
		}
		for _, s := range []string{"## What happened", "called issues", "## Expected", "a list", "## Tool", "`issues`", "## Error", "boom", "## Diagnostics",
			"Reported at (UTC)", "Endpoint: unified", "Auth: omx-user-token", "Server name:", "Server version:", "Server env:", "Server origin:"} {
			if !strings.Contains(i.Body, s) {
				t.Fatalf("內文缺 %q:\n%s", s, i.Body)
			}
		}
	}
}

func TestReportProblemUserAgentAndNoSecrets(t *testing.T) {
	u := newUni(t)
	b := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report_problem","arguments":{"org":"lab","title":"ua"}}}`
	// 用 rpcPost 沒有 UA 欄位:直接組請求
	w := newRecorderWithUA(u.unified, b, "my-client/1.0\x01 "+strings.Repeat("x", 300), "Bearer omx_supersecrettoken")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	l, _ := u.svc.Store.ListIssues("lab")
	if len(l) != 1 {
		t.Fatal(len(l))
	}
	body := l[0].Body
	ua := ""
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(ln, "- Client user-agent: ") {
			ua = strings.TrimPrefix(ln, "- Client user-agent: ")
		}
	}
	if !strings.HasPrefix(ua, "my-client/1.0") || len([]rune(ua)) > 200 || strings.ContainsRune(ua, 1) {
		t.Fatalf("ua:%q", ua)
	}
	for _, bad := range []string{"omx_", "supersecret", "Authorization", "Bearer", "postgres://", "Cookie", "secretcookie"} {
		if strings.Contains(body, bad) {
			t.Fatalf("診斷不該有 %q:\n%s", bad, body)
		}
	}
}

func TestReportProblemMasking(t *testing.T) {
	u := newUni(t)
	hex40 := strings.Repeat("ab12", 10)
	secretText := "token omx_abcDEF123_xyz and omo_Zz9-yy.q Bearer eyJhbGciOi.payload.sig " +
		"url postgres://user:pw@db.example.com:5432/omixor?sslmode=disable Authorization: Basic dXNlcjpwdw==\nblob " + hex40 + " client oc_myclient"
	r := reportCall(t, u.unified, map[string]any{"title": "t omx_titletoken", "what_happened": secretText, "expected": secretText, "tool": "issues", "error": secretText})
	if r.isErr {
		t.Fatal(r.all())
	}
	i := reportIssue(t, u, r.obj(t)["key"].(string))
	for _, bad := range []string{"omx_abc", "omo_Zz9", "omx_titletoken", "eyJhbGciOi", "user:pw", "postgres://", "dXNlcjpwdw", hex40} {
		if strings.Contains(i.Body, bad) || strings.Contains(i.Title, bad) {
			t.Fatalf("機密 %q 沒遮蔽:\n%s\n%s", bad, i.Title, i.Body)
		}
	}
	if !strings.Contains(i.Body, "[masked]") || !strings.Contains(i.Body, "Bearer [masked]") || !strings.Contains(i.Body, "Authorization: [masked]") || !strings.Contains(i.Body, "oc_myclient") {
		t.Fatalf("遮蔽結果不對(oc_ client id 要保留):\n%s", i.Body)
	}
}

func TestReportProblemFieldCaps(t *testing.T) {
	u := newUni(t)
	r := reportCall(t, u.unified, map[string]any{"title": strings.Repeat("題", 500) + "\nline2\x07", "what_happened": strings.Repeat("w ", 5000), "expected": strings.Repeat("e ", 5000), "error": strings.Repeat("err ", 5000), "tool": "issues"})
	if r.isErr {
		t.Fatal(r.all())
	}
	i := reportIssue(t, u, r.obj(t)["key"].(string))
	if n := len([]rune(i.Title)); n != 200 || strings.ContainsAny(i.Title, "\n\x07") {
		t.Fatalf("標題長度/內容:%d %q", n, i.Title)
	}
	section := func(name string) string {
		_, rest, _ := strings.Cut(i.Body, "## "+name+"\n\n")
		s, _, _ := strings.Cut(rest, "\n\n## ")
		return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "~text\n"))
	}
	for _, n := range []string{"What happened", "Expected", "Error"} {
		if l := len([]rune(section(n))); l > 4000 || l < 3000 {
			t.Fatalf("%s 長度 %d", n, l)
		}
	}
	// 空標題被拒
	if r := reportCall(t, u.unified, map[string]any{"title": " \x07 "}); !r.isErr {
		t.Fatal("空標題要被拒")
	}
	if r := reportCall(t, u.unified, map[string]any{"title": "x", "severity": "meh"}); !r.isErr {
		t.Fatal("severity 不合法要被拒")
	}
}

func TestReportProblemDedup(t *testing.T) {
	u := newUni(t)
	first := reportCall(t, u.unified, map[string]any{"title": "A", "tool": "Issues", "error": "request 123 failed at 2026-11-24T10:00:00Z", "what_happened": "first"})
	if first.isErr {
		t.Fatal(first.all())
	}
	k1 := first.obj(t)["key"].(string)
	// 同 tool(大小寫不同)與同 error(數字不同、空白不同):去重成留言
	second := reportCall(t, u.unified, map[string]any{"title": "B", "tool": "issues", "error": "request  987  failed at 2027-01-01T00:00:00Z", "what_happened": "second try", "severity": "blocker"})
	if second.isErr {
		t.Fatal(second.all())
	}
	a := second.obj(t)
	if a["key"] != k1 || a["deduplicated"] != true || a["comment_id"] == nil || fmt.Sprint(a["changed"]) != "[priority comment]" {
		t.Fatalf("要去重:%v", a)
	}
	i := reportIssue(t, u, k1)
	if len(i.Comments) != 1 || !strings.Contains(i.Comments[0].Text, "Reported again") || !strings.Contains(i.Comments[0].Text, "Severity: blocker") || !strings.Contains(i.Comments[0].Text, "second try") {
		t.Fatalf("留言:%+v", i.Comments)
	}
	if l, _ := u.svc.Store.ListIssues("lab"); len(l) != 1 {
		t.Fatalf("不該多開 Issue:%d", len(l))
	}
	// 不同 error 開新的
	third := reportCall(t, u.unified, map[string]any{"title": "C", "tool": "issues", "error": "totally different"})
	if t3 := third.obj(t); t3["deduplicated"] != false || t3["key"] == k1 {
		t.Fatalf("不同 error 要開新的:%v", t3)
	}
	// 結案的不重用
	if r := callRes(t, u.unified, "issue_update", map[string]any{"org": "lab", "id": k1, "status": "closed"}); r.isErr {
		t.Fatal(r.all())
	}
	fourth := reportCall(t, u.unified, map[string]any{"title": "D", "tool": "issues", "error": "request 5 failed at 2030-01-01T00:00:00Z"})
	if t4 := fourth.obj(t); t4["deduplicated"] != false || t4["key"] == k1 {
		t.Fatalf("結案的不能重用:%v", t4)
	}
	// tool 與 error 都空:一律新開、沒有 fp 標籤
	e1 := reportCall(t, u.unified, map[string]any{"title": "same"}).obj(t)
	e2 := reportCall(t, u.unified, map[string]any{"title": "same"}).obj(t)
	if e1["key"] == e2["key"] || e2["deduplicated"] != false {
		t.Fatal("沒有 tool 與 error 不去重")
	}
	for _, l := range reportIssue(t, u, e1["key"].(string)).Labels {
		if strings.HasPrefix(l, "fp:") {
			t.Fatal("不該有 fp 標籤")
		}
	}
}

func TestReportProblemReturnFull(t *testing.T) {
	u := newUni(t)
	m := reportCall(t, u.unified, map[string]any{"title": "full", "tool": "docs", "error": "e", "return": "full"}).obj(t)
	if m["deduplicated"] != false || m["body"] == nil || m["comments"] == nil || m["title"] != "full" || m["source"] != "mcp report_problem" {
		t.Fatalf("full:%v", m)
	}
	d := reportCall(t, u.unified, map[string]any{"title": "full", "tool": "docs", "error": "e", "return": "full"}).obj(t)
	if d["deduplicated"] != true || len(d["comments"].([]any)) != 1 || d["id"] != m["id"] {
		t.Fatalf("dedup full:%v", d)
	}
}

func TestReportProblemPermissions(t *testing.T) {
	u := newUni(t)
	ro := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, true)
	rw := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	// 唯讀 OAuth:被拒、沒寫入
	r := reportCall(t, u.writeUnified(ro), map[string]any{"title": "ro"})
	if !r.isErr || !strings.Contains(r.all(), mcp.ReadOnlyMessage) {
		t.Fatalf("唯讀要被拒:%v", r.all())
	}
	if l, _ := u.svc.Store.ListIssues("lab"); len(l) != 0 {
		t.Fatal("唯讀不該寫入")
	}
	// 讀寫 OAuth:可用,診斷標示 oauth-read-write
	r = reportCall(t, u.writeUnified(rw), map[string]any{"title": "rw"})
	if r.isErr {
		t.Fatal(r.all())
	}
	if b := reportIssue(t, u, r.obj(t)["key"].(string)).Body; !strings.Contains(b, "Auth: oauth-read-write") {
		t.Fatal(b)
	}
	// 舊的管理端點(不帶 org)也可用,診斷標示 admin 端點、伺服器識別不可得
	r = callRes(t, u.old, "report_problem", map[string]any{"title": "old"})
	if r.isErr {
		t.Fatal(r.all())
	}
	if b := reportIssue(t, u, r.obj(t)["key"].(string)).Body; !strings.Contains(b, "Endpoint: admin") || !strings.Contains(b, "Auth: omx-user-token") {
		t.Fatal(b)
	}
	// 非成員的組織:和 issue_create 同一句錯誤
	a := callRes(t, u.unified, "issue_create", map[string]any{"org": "other", "title": "x"})
	b := reportCall(t, u.unified, map[string]any{"org": "other", "title": "x"})
	if !a.isErr || !b.isErr || a.all() != b.all() {
		t.Fatalf("非成員要同樣被拒:\n%s\n%s", a.all(), b.all())
	}
	if l, _ := u.svc.Store.ListIssues("other"); len(l) != 0 {
		t.Fatal("不該寫到別人的組織")
	}
}

func TestReportProblemNotInAgentEndpoint(t *testing.T) {
	if mcp.AgentToolAllow["report_problem"] || !mcp.AdminToolAllow["report_problem"] || mcp.ReadOnlyTools["report_problem"] || !mcp.OAuthWriteTools["report_problem"] {
		t.Fatal("report_problem 的分類不對")
	}
}

func newRecorderWithUA(h http.Handler, body, ua, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Authorization", auth)
	req.Header.Set("Cookie", "sid=secretcookie")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestMaskSecretsRules(t *testing.T) {
	ghp := "ghp_" + strings.Repeat("a", 20)
	cases := []struct{ name, in, want string }{
		{"cookie", "x\nCookie: sid=abc; theme=dark\ny", "x\nCookie: [masked]\ny"},
		{"set-cookie", "Set-Cookie: sid=abc; HttpOnly", "Set-Cookie: [masked]"},
		{"kv equals", "GET /cb?access_token=abc123&x=1", "GET /cb?access_token=[masked]&x=1"},
		{"kv colon", "password: hunter2 next", "password: [masked] next"},
		{"kv json", `{"token": "abc", "n": 1}`, `{"token": "[masked]", "n": 1}`},
		{"kv single quote", "client_secret='s3cr3t'", "client_secret='[masked]'"},
		{"kv case", "API-KEY=xyz;", "API-KEY=[masked];"},
		{"kv session", "sessionid=zzz,", "sessionid=[masked],"},
		{"github", "tok " + ghp + " end", "tok [masked] end"},
		{"github pat", "github_pat_11ABCDEFG0abcdefgh", "[masked]"},
		{"sk", "key sk-abcdefghijklmnopqrst", "key [masked]"},
		{"stripe", "sk_live_abcdefgh1234", "[masked]"},
		{"aws", "AKIAABCDEFGHIJKLMNOP", "[masked]"},
		{"google", "AIza" + strings.Repeat("a", 35), "[masked]"},
		{"slack", "xoxb-1234567890-abc", "[masked]"},
		{"jwt", "eyJhbGciOiJI.eyJzdWIiOiIx.abcdefghij", "[masked]"},
		{"oc client id kept", "client oc_myclient", "client oc_myclient"},
		{"plain token text", "token limit exceeded for tool issues", "token limit exceeded for tool issues"},
		{"plain password text", "password policy is strict", "password policy is strict"},
		{"path", "/Users/me/omixor/core/mcp/tools_report.go:12", "/Users/me/omixor/core/mcp/tools_report.go:12"},
		{"word inside", "my_token_count=3 tasks-skip", "my_token_count=3 tasks-skip"},
	}
	for _, c := range cases {
		if got := mcp.MaskSecrets(c.in); got != c.want {
			t.Errorf("%s: %q -> %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestReportProblemMaskBeforeCap(t *testing.T) {
	u := newUni(t)
	// 機密剛好跨過 4000 字上限:先遮蔽再截斷,不會留下半截
	what := strings.Repeat("a ", 1990) + "password=supersecretvalue ghp_" + strings.Repeat("b", 30)
	r := reportCall(t, u.unified, map[string]any{"title": "t", "what_happened": what})
	if r.isErr {
		t.Fatal(r.all())
	}
	i := reportIssue(t, u, r.obj(t)["key"].(string))
	if strings.Contains(i.Body, "supersecret") || strings.Contains(i.Body, "ghp_b") || !strings.Contains(i.Body, "password=[masked]") {
		t.Fatalf("遮蔽要先於截斷:%s", i.Body[3900:])
	}
}

func TestReportProblemDedupEscalation(t *testing.T) {
	u := newUni(t)
	args := func(sev string) map[string]any {
		return map[string]any{"title": "x", "tool": "issues", "error": "boom", "severity": sev}
	}
	first := reportCall(t, u.unified, args("idea")).obj(t)
	key := first["key"].(string)
	if reportIssue(t, u, key).Priority != "low" {
		t.Fatal("idea 應為 low")
	}
	a := reportCall(t, u.unified, args("blocker")).obj(t)
	ch := fmt.Sprint(a["changed"])
	if a["deduplicated"] != true || !strings.Contains(ch, "priority") || !strings.Contains(ch, "comment") {
		t.Fatalf("要升級:%v", a)
	}
	if p := reportIssue(t, u, key).Priority; p != "high" {
		t.Fatalf("priority=%s", p)
	}
	b := reportCall(t, u.unified, args("idea")).obj(t)
	if got := fmt.Sprint(b["changed"]); got != "[comment]" {
		t.Fatalf("不能降:%v", b)
	}
	if p := reportIssue(t, u, key).Priority; p != "high" {
		t.Fatalf("priority=%s", p)
	}
}

func TestUnifiedInstructionsOrgWording(t *testing.T) {
	if !strings.Contains(mcp.UnifiedInstructions, "of the organization named by the org parameter") || strings.Contains(mcp.UnifiedInstructions, "of the current organization") {
		t.Fatal("unifiedInstructions 的組織措辭不對(endpoints_unified.go 的 strings.Replace 沒套到 adminInstructions?)")
	}
}
