package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omixor-core/api"
	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 假的 evoleap /api/me
func fakeEvoleap(t *testing.T) *httptest.Server {
	users := map[string]string{
		"evo-boss":   `{"user":{"id":"e1","email":"Boss@Example.com","name":"老闆","google":true}}`,
		"evo-alice":  `{"user":{"id":"e2","email":"alice@example.com","name":"Alice","google":true}}`,
		"evo-bob":    `{"user":{"id":"e3","email":"bob@example.com","name":"Bob","google":true}}`,
		"evo-pw":     `{"user":{"id":"e4","email":"pw@example.com","google":false}}`,
		"evo-random": `{"user":{"id":"e5","email":"random@example.com","google":true}}`,
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]; ok {
			w.Write([]byte(u))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	t.Cleanup(s.Close)
	return s
}

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Mem
	svc *service.Service
}

func newEnv(t *testing.T) *env {
	st := store.NewMem()
	svc := &service.Service{Store: st, Evoleap: fakeEvoleap(t).URL, Bootstrap: "boss@example.com", TTL: 90 * 24 * time.Hour, LoginPerIP: 1000, LoginTotal: 1000}
	srv := httptest.NewServer(api.New(svc).Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, st: st, svc: svc}
}

func (e *env) login(evo string) *client.Client {
	e.t.Helper()
	c := client.New(e.srv.URL)
	r, err := c.LoginWithEvoleap(evo, "test")
	if err != nil {
		e.t.Fatalf("%s 登入:%v", evo, err)
	}
	tok := r.Token
	c.Token = func() string { return tok }
	return c
}

func status(err error) int { return client.Status(err) }

func TestOrgsMembersIssues(t *testing.T) {
	e := newEnv(t)
	// 第一位登入的是 bootstrap email:伺服器管理員
	boss := e.login("evo-boss")
	me, _ := boss.Me()
	if me.User.Role != "admin" || me.User.Email != "boss@example.com" {
		t.Fatalf("%+v", me.User)
	}
	// 名單外的人不能登入;email 沒驗證的不行
	if _, err := client.New(e.srv.URL).LoginWithEvoleap("evo-alice", "x"); status(err) != 403 {
		t.Fatal(err)
	}
	if _, err := client.New(e.srv.URL).LoginWithEvoleap("evo-pw", "x"); status(err) != 403 {
		t.Fatal(err)
	}

	// 建立組織(伺服器管理員),把 alice 加成組織管理員、bob 加成成員(email 先加,第一次登入時對上)
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.CreateOrg("lab", "重複"); status(err) != 409 {
		t.Fatal("重複的組織要 409", err)
	}
	if _, err := boss.CreateOrg("../x", ""); status(err) != 400 {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "Alice@example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	if _, err := alice.SaveMember("lab", "bob@example.com", "member"); err != nil {
		t.Fatal("組織管理員可以加人", err)
	}
	bob := e.login("evo-bob")
	if orgs, _ := bob.ListOrgs(); len(orgs) != 1 || orgs[0].ID != "lab" || orgs[0].MyRole != "member" {
		t.Fatalf("%+v", orgs)
	}
	if _, err := bob.SaveMember("lab", "x@example.com", "member"); status(err) != 403 {
		t.Fatal("成員不能加人", err)
	}
	if _, err := alice.CreateOrg("x", ""); status(err) != 403 {
		t.Fatal("只有伺服器管理員能建組織", err)
	}

	// Issue:成員可以讀寫;作者一律是自己;伺服器管理員可以指定作者
	i, err := bob.CreateIssue("lab", service.IssueInput{Title: "首頁壞了", Labels: []string{" a ", "A", "b"}, Author: "agent:ceo"})
	if err != nil || i.ID != "ISS-1" || i.CreatedBy != "bob@example.com" || strings.Join(i.Labels, ",") != "a,b" || i.Priority != "normal" {
		t.Fatalf("%+v %v", i, err)
	}
	j, _ := boss.CreateIssue("lab", service.IssueInput{Title: "第二筆", Author: "agent:ceo", Priority: "high"})
	if j.ID != "ISS-2" || j.CreatedBy != "agent:ceo" {
		t.Fatalf("%+v", j)
	}
	if _, err := bob.CreateIssue("lab", service.IssueInput{Title: "  "}); status(err) != 400 {
		t.Fatal(err)
	}
	if _, err := bob.CreateIssue("lab", service.IssueInput{Title: "x", Priority: "huge"}); status(err) != 400 {
		t.Fatal(err)
	}
	closed := "closed"
	cs := []model.Comment{{Text: "看到了", Author: "冒名"}}
	u, err := bob.UpdateIssue("lab", "iss-1", service.IssuePatch{Status: &closed, Comments: &cs})
	if err != nil || u.Status != "closed" || u.ClosedAt == nil || len(u.Comments) != 1 || u.Comments[0].Author != "bob@example.com" || u.Comments[0].ID == "" {
		t.Fatalf("%+v %v", u, err)
	}
	// 既有留言的作者不能被改
	cs2 := append([]model.Comment{}, u.Comments...)
	cs2[0].Author = "boss@example.com"
	u2, _ := alice.UpdateIssue("lab", "ISS-1", service.IssuePatch{Comments: &cs2})
	if u2.Comments[0].Author != "bob@example.com" {
		t.Fatal(u2.Comments[0])
	}
	if list, _ := bob.ListIssues("lab"); len(list) != 2 || list[0].ID != "ISS-2" {
		t.Fatalf("新的在前:%+v", list)
	}
	// 沒有改到東西不更新時間
	before, _ := bob.GetIssue("lab", "ISS-2")
	same := "第二筆"
	after, _ := bob.UpdateIssue("lab", "ISS-2", service.IssuePatch{Title: &same})
	if !after.UpdatedAt.Equal(before.UpdatedAt.Time) {
		t.Fatal("沒改到東西不能更新時間")
	}
	if err := bob.DeleteIssue("lab", "ISS-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.GetIssue("lab", "ISS-2"); status(err) != 404 {
		t.Fatal(err)
	}

	// 組織隔離:不是成員就看不到;組織不存在 404
	random := e.login2(boss, "evo-random")
	if _, err := random.ListIssues("lab"); status(err) != 403 {
		t.Fatal("不是成員要 403", err)
	}
	if _, err := bob.ListIssues("nope"); status(err) != 404 {
		t.Fatal(err)
	}
	// 移除成員後就不能用
	if err := alice.RemoveMember("lab", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.ListIssues("lab"); status(err) != 403 {
		t.Fatal(err)
	}

	// 舊路徑還能用(現有的 app 與 CLI)
	var list []model.Issue
	if err := alice.Do("GET", "/api/issues/lab", nil, &list); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}

	// 匯出、第一次匯入(組織管理員;已經有資料就拒絕)
	exp, err := alice.ExportIssues("lab")
	if err != nil || len(exp) != 1 {
		t.Fatal(err)
	}
	if _, err := alice.ImportIssues("lab", exp); status(err) != 409 {
		t.Fatal("已經有資料要拒絕", err)
	}
	if _, err := boss.CreateOrg("lab2", ""); err != nil {
		t.Fatal(err)
	}
	r, err := boss.ImportIssues("lab2", exp)
	if err != nil || r.Inserted != 1 {
		t.Fatal(r, err)
	}
	got, _ := boss.ListIssues("lab2")
	if got[0].ID != "ISS-1" || got[0].CreatedBy != "bob@example.com" || !got[0].CreatedAt.Equal(exp[0].CreatedAt.Time) {
		t.Fatalf("匯入要保留 id、作者與時間:%+v", got[0])
	}
	if _, err := alice.ExportIssues("lab2"); status(err) != 403 {
		t.Fatal("不是 lab2 的成員", err)
	}

	// 操作紀錄
	audit, _ := boss.ListAudit(200)
	all := ""
	for _, a := range audit {
		all += a.Action + " "
	}
	for _, want := range []string{"bootstrap.admin", "org.create", "member.save", "issue.create", "issue.update", "issue.delete", "member.remove", "issues.import", "login.denied"} {
		if !strings.Contains(all, want) {
			t.Fatalf("操作紀錄缺 %s:%s", want, all)
		}
	}
	if _, err := alice.ListAudit(10); status(err) != 403 {
		t.Fatal("只有伺服器管理員", err)
	}
}

// login2 管理員先把人加進名單,再登入
func (e *env) login2(admin *client.Client, evo string) *client.Client {
	email := map[string]string{"evo-random": "random@example.com"}[evo]
	if _, err := admin.SaveUser(email, nil, nil); err != nil {
		e.t.Fatal(err)
	}
	return e.login(evo)
}

func TestTokensLogoutAndRateLimit(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	other := e.login("evo-boss")
	toks, _ := boss.ListTokens()
	if len(toks) != 2 {
		t.Fatal(toks)
	}
	if err := other.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Me(); status(err) != 401 {
		t.Fatal("登出後要失效", err)
	}
	if _, err := boss.Me(); err != nil {
		t.Fatal("另一台不受影響", err)
	}
	e.svc.LoginPerIP = 2
	c := client.New(e.srv.URL)
	c.LoginWithEvoleap("bad", "x")
	c.LoginWithEvoleap("bad", "x")
	if _, err := c.LoginWithEvoleap("evo-boss", "x"); status(err) != 429 {
		t.Fatal("超過上限要 429", err)
	}
	// token 只存雜湊;到期
	e.svc.Now = func() time.Time { return time.Now().Add(91 * 24 * time.Hour) }
	if _, err := boss.Me(); status(err) != 401 {
		t.Fatal("90 天後要失效", err)
	}
}

func TestEnsureOrgsForIssues(t *testing.T) {
	e := newEnv(t)
	_, _ = e.st.ImportIssues("default", []model.Issue{{ID: "ISS-1", Title: "舊的", Status: "open", Priority: "normal"}})
	if err := e.svc.EnsureOrgsForIssues(); err != nil {
		t.Fatal(err)
	}
	o, err := e.st.GetOrg("default")
	if err != nil || o.Name != "evoleap" {
		t.Fatal(o, err)
	}
	boss := e.login("evo-boss")
	if l, _ := boss.ListIssues("default"); len(l) != 1 {
		t.Fatal(l)
	}
}
