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

// commits 工具(ISS-69 phase 1):commits_report、commits、issue_commit_link / issue_commit_unlink。
// 回報的精簡結果、查詢、連結、issues(id) 附 commits;錯誤訊息是英文;OAuth 讀寫仍被拒;角色端點與執行者端點沒有這些工具。

func sha40(c byte) string { return strings.Repeat(string(c), 40) }

func commitEnv(t *testing.T) (admin http.Handler, svc *service.Service, issueKey string) {
	t.Helper()
	st := storetest.New(t)
	svc = &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	iss, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "登入壞了"})
	if err != nil {
		t.Fatal(err)
	}
	admin = mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: boss}, "lab", nil
	}})
	return admin, svc, iss.Key
}

func TestCommitTools(t *testing.T) {
	admin, svc, k := commitEnv(t)

	// 回報:精簡結果 {reported, created, linked[], unmatched[]}
	r := ok(t, admin, "", "commits_report", map[string]any{"repo": "GitHub:acme/app", "source": "ci", "commits": []any{
		map[string]any{"sha": sha40('a'), "subject": "Fixes " + k + ": login crash", "body": "details", "author_name": "Amy",
			"authored_at": "2026-01-02T03:04:05Z", "parents": []any{sha40('0')}, "branch": "main"},
		map[string]any{"sha": sha40('b'), "subject": "typo ISS-999 and " + k},
	}})
	if r["reported"] != 2.0 || r["created"] != 2.0 {
		t.Fatal(r)
	}
	linked, _ := r["linked"].([]any)
	if len(linked) != 2 || linked[0].(map[string]any)["issue"] != k {
		t.Fatal(r)
	}
	if un, _ := r["unmatched"].([]any); len(un) != 1 || un[0] != "ISS-999" {
		t.Fatal(r)
	}
	if len(r) != 4 {
		t.Fatalf("結果只有 reported、created、linked、unmatched:%v", r)
	}
	// 重送冪等
	r = ok(t, admin, "", "commits_report", map[string]any{"repo": "github:acme/app", "commits": []any{map[string]any{"sha": sha40('a'), "subject": "Fixes " + k + ": login crash"}}})
	if r["created"] != 0.0 || len(r["linked"].([]any)) != 0 {
		t.Fatal(r)
	}

	// 查詢:sha 前綴時附 body;issues 列出連結種類
	q := ok(t, admin, "", "commits", map[string]any{"sha": "aaaaaaa"})
	cs, _ := q["commits"].([]any)
	if q["total"] != 1.0 || len(cs) != 1 {
		t.Fatal(q)
	}
	c := cs[0].(map[string]any)
	iss := c["issues"].([]any)
	if c["sha"] != sha40('a') || c["sha7"] != "aaaaaaa" || c["body"] != "details" || c["author"] != "Amy" || c["verified"] != false || c["repo"] != "github:acme/app" ||
		c["authoredAt"] != "2026-01-02T03:04:05Z" || len(iss) != 1 || iss[0].(map[string]any)["kind"] != "fixes" {
		t.Fatal(c)
	}
	q = ok(t, admin, "", "commits", map[string]any{"repo": "acme/app", "query": "typo"})
	if q["total"] != 1.0 || q["commits"].([]any)[0].(map[string]any)["body"] != nil {
		t.Fatal("沒給 sha 時不附 body", q)
	}
	if q = ok(t, admin, "", "commits", map[string]any{"issue": k}); q["total"] != 2.0 {
		t.Fatal(q)
	}
	if q = ok(t, admin, "", "commits", map[string]any{"limit": 1}); q["total"] != 2.0 || len(q["commits"].([]any)) != 1 {
		t.Fatal(q)
	}

	// 手動連結 / 取消(sha 前綴可取消)
	l := ok(t, admin, "", "issue_commit_link", map[string]any{"issue": k, "repo": "acme/app", "sha": sha40('c'), "kind": "found_in", "note": "seen in v1"})
	if l["kind"] != "found_in" || l["origin"] != "manual" || l["issue"] != k || l["note"] != "seen in v1" || l["sha7"] != "ccccccc" {
		t.Fatal(l)
	}
	u := ok(t, admin, "", "issue_commit_unlink", map[string]any{"issue": k, "repo": "acme/app", "sha": "ccccccc"})
	if u["ok"] != true {
		t.Fatal(u)
	}

	// issues(id):commits(最近 10 筆)與 commits_total;list 模式與 return:"ack" 回執不變
	g := ok(t, admin, "", "issues", map[string]any{"id": k})
	cm, _ := g["commits"].([]any)
	if g["commits_total"] != 2.0 || len(cm) != 2 {
		t.Fatal(g["commits_total"], cm)
	}
	row := cm[0].(map[string]any)
	if row["sha7"] == nil || row["subject"] == nil || row["kind"] == nil || row["repo"] != "github:acme/app" || len(row) > 6 {
		t.Fatal(row)
	}
	lst := ok(t, admin, "", "issues", map[string]any{})
	if _, has := lst["commits"]; has || lst["total"] == nil {
		t.Fatal("list 模式不變", lst)
	}
	ack := ok(t, admin, "", "issue_update", map[string]any{"id": k, "comment": "hi"})
	if _, has := ack["commits"]; has || ack["key"] != k {
		t.Fatal("ack 回執不變", ack)
	}
	_ = svc
}

func TestCommitToolErrorsAreEnglish(t *testing.T) {
	admin, _, k := commitEnv(t)
	good := []any{map[string]any{"sha": sha40('a'), "subject": "x"}}
	fails(t, admin, "", "commits_report", map[string]any{"commits": good}, "Missing parameter repo")
	for name, args := range map[string]map[string]any{
		"bad repo":       {"repo": "a b", "commits": good},
		"empty commits":  {"repo": "a/b", "commits": []any{}},
		"short sha":      {"repo": "a/b", "commits": []any{map[string]any{"sha": "abc", "subject": "x"}}},
		"bad time":       {"repo": "a/b", "commits": []any{map[string]any{"sha": sha40('a'), "subject": "x", "authored_at": "yesterday"}}},
		"not an object":  {"repo": "a/b", "commits": []any{"x"}},
		"unknown source": {"repo": "a/b", "source": "evil", "commits": good},
	} {
		_, e := call(t, admin, "", "commits_report", args)
		if e == "" || hasCJK(e) {
			t.Errorf("%s:要英文錯誤,得到 %q", name, e)
		}
	}
	var many []any
	for range 101 {
		many = append(many, map[string]any{"sha": sha40('a'), "subject": "x"})
	}
	if _, e := call(t, admin, "", "commits_report", map[string]any{"repo": "a/b", "commits": many}); !strings.Contains(e, "at most 100") {
		t.Fatal(e)
	}
	if _, e := call(t, admin, "", "commits", map[string]any{"sha": "abc"}); !strings.Contains(e, "at least 7") || hasCJK(e) {
		t.Fatal(e)
	}
	if _, e := call(t, admin, "", "issue_commit_link", map[string]any{"issue": k, "repo": "a/b", "sha": "abc"}); !strings.Contains(e, "40-character") {
		t.Fatal(e)
	}
	if _, e := call(t, admin, "", "issue_commit_link", map[string]any{"issue": "ISS-404", "repo": "a/b", "sha": sha40('a')}); !strings.Contains(e, "Not found: issue ISS-404") {
		t.Fatal(e)
	}
	if _, e := call(t, admin, "", "issue_commit_link", map[string]any{"issue": k, "repo": "a/b", "sha": sha40('a'), "kind": "weird"}); e == "" {
		t.Fatal("kind 要驗證")
	}
	if _, e := call(t, admin, "", "issue_commit_unlink", map[string]any{"issue": k, "repo": "a/b", "sha": sha40('a')}); !strings.Contains(e, "Not found") {
		t.Fatal(e)
	}
}

func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// 角色端點:tools/list 沒有 commits 工具、呼叫被拒;issues(id) 不附 commits;登記表旗標:Admin 有、Agent 與 Worker 沒有
func TestCommitToolsNotOnAgentOrWorkerEndpoints(t *testing.T) {
	_, svc, k := commitEnv(t)
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	tk, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	auth := "Bearer cto:" + res.Credential.Secret
	names := map[string]bool{}
	for _, n := range listTools(t, role, auth) {
		names[n] = true
	}
	for _, n := range []string{"commits", "commits_report", "issue_commit_link", "issue_commit_unlink"} {
		if names[n] {
			t.Errorf("角色端點不該列 %s", n)
		}
		w := rpcPost(role, rpcReq(1, n, map[string]any{"repo": "a/b", "issue": k, "sha": sha40('a'), "commits": []any{}}), auth)
		if !strings.Contains(w.Body.String(), "-32602") {
			t.Errorf("角色端點呼叫 %s 要被拒:%s", n, w.Body.String())
		}
		ct, ok := model.CoreToolByName(n)
		if !ok || !ct.Admin || ct.Agent || ct.Worker || mcp.AgentToolAllow[n] || !mcp.AdminToolAllow[n] {
			t.Errorf("%s 登記表旗標不對:%+v", n, ct)
		}
	}
	// 角色端點讀 issues(id):沒有 commits 欄位(角色一律不能讀 commits)
	g := ok(t, role, auth, "issues", map[string]any{"id": k})
	if _, has := g["commits"]; has {
		t.Fatal("角色端點的 issues(id) 不附 commits", g["commits"])
	}
	// service 層也擋:角色憑證 403(就算有人用 REST 直接打)
	cred, err := svc.AuthenticateAgent("cto", res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReportCommits(cred, "lab", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('a'), Subject: "x"}}}); service.Code(err) != 403 {
		t.Fatal(err)
	}
}

// OAuth:唯讀連線能用 commits、不能用寫入工具;讀寫連線(mcp:write)也不能用 commits_report / issue_commit_*(不在白名單,只收 omx_ token)
func TestCommitToolsOAuth(t *testing.T) {
	u := newUni(t)
	boss := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	ro := u.readOnlyEndpoint()
	if r := callRes(t, ro, "commits", map[string]any{"org": "lab"}); r.isErr {
		t.Fatalf("唯讀連線要能查 commits:%v", r.all())
	}
	if !mcp.ReadOnlyTools["commits"] || mcp.ReadOnlyTools["commits_report"] || mcp.OAuthWriteTools["commits_report"] || mcp.OAuthWriteTools["issue_commit_link"] || mcp.OAuthWriteTools["issue_commit_unlink"] {
		t.Fatal("白名單不對")
	}
	args := map[string]any{"org": "lab", "repo": "a/b", "issue": "ISS-1", "sha": sha40('a'), "commits": []any{map[string]any{"sha": sha40('a'), "subject": "x"}}}
	for _, tool := range []string{"commits_report", "issue_commit_link", "issue_commit_unlink"} {
		if r := callRes(t, ro, tool, args); !r.isErr || !strings.Contains(r.all(), mcp.ReadOnlyMessage) {
			t.Fatalf("唯讀連線 %s 要被擋:%v", tool, r.all())
		}
		if r := callRes(t, u.writeUnified(boss), tool, args); !r.isErr || !strings.Contains(r.all(), mcp.OAuthNotAvailableMessage) {
			t.Fatalf("OAuth 讀寫連線 %s 要被擋:%v", tool, r.all())
		}
	}
	// 沒有寫入
	if r := callRes(t, u.unified, "commits", map[string]any{"org": "lab"}); r.isErr || strings.Contains(r.all(), sha40('a')) {
		t.Fatal(r.all())
	}
}

// repo 歧義、裸參照上限、頻率限制:錯誤都是英文,429 帶重試時間
func TestCommitToolLimitErrorsAreEnglish(t *testing.T) {
	admin, svc, k := commitEnv(t)
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	for _, repo := range []string{"github:x/y", "gitlab:x/y"} {
		ok(t, admin, "", "commits_report", map[string]any{"repo": repo, "commits": []any{map[string]any{"sha": sha40('a'), "subject": "s"}}})
	}
	_, e := call(t, admin, "", "commits", map[string]any{"repo": "x/y"})
	if !strings.Contains(e, "github:x/y") || !strings.Contains(e, "gitlab:x/y") || !strings.Contains(e, "prefix") || hasCJK(e) {
		t.Fatal(e)
	}
	if r := ok(t, admin, "", "commits", map[string]any{"repo": "gitlab:x/y"}); r["total"] != 1.0 {
		t.Fatal(r)
	}

	for i := 0; i < model.CommitMaxBareRefsPerIssue; i++ {
		if _, err := svc.LinkIssueCommit(boss, "lab", k, service.CommitLinkInput{Repo: "a/b", SHA: strings.Repeat("0", 31) + strings.Repeat("a", 6) + string(rune('0'+i/100)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10))}); err != nil {
			t.Fatal(i, err)
		}
	}
	_, e = call(t, admin, "", "issue_commit_link", map[string]any{"issue": k, "repo": "a/b", "sha": sha40('e')})
	if !strings.Contains(e, "unreported commit references") || !strings.Contains(e, "commits_report") || hasCJK(e) {
		t.Fatal(e)
	}

	// 回報次數用完 → 429(英文、有重試提示)。已用掉 1 次(上面兩次回報),再補到上限
	for i := 0; i < model.CommitReportMaxCalls; i++ {
		_, err := svc.ReportCommits(boss, "lab", service.CommitReport{Repo: "c/d", Commits: []service.CommitInput{{SHA: sha40('c'), Subject: "x"}}})
		if err != nil {
			break
		}
	}
	_, e = call(t, admin, "", "commits_report", map[string]any{"repo": "c/d", "commits": []any{map[string]any{"sha": sha40('c'), "subject": "x"}}})
	if !strings.Contains(e, "Rate limited") || !strings.Contains(e, "retry in about") || hasCJK(e) {
		t.Fatal(e)
	}
}
