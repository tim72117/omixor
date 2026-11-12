package omixor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omixor-web/internal/core"
)

// 假的 evoleap /api/me:token → 使用者
func fakeEvoleap(t *testing.T) *httptest.Server {
	users := map[string]string{
		"evo-boss":   `{"user":{"id":"e1","email":"Boss@Example.com","name":"老闆","google":true,"isAdmin":true}}`,
		"evo-member": `{"user":{"id":"e2","email":"member@example.com","name":"成員","google":true}}`,
		"evo-pw":     `{"user":{"id":"e3","email":"pw@example.com","name":"密碼註冊","google":false}}`,
		"evo-other":  `{"user":{"id":"e4","email":"other@example.com","name":"路人","google":true}}`,
		// e2 在 evoleap 改了 email
		"evo-member-renamed": `{"user":{"id":"e2","email":"other@example.com","name":"成員","google":true}}`,
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			w.WriteHeader(404)
			return
		}
		if u, ok := users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]; ok {
			w.Write([]byte(u))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	t.Cleanup(s.Close)
	return s
}

type harness struct {
	t      *testing.T
	h      http.Handler
	store  *MemStore
	issues *MemIssues
}

func newHarness(t *testing.T) *harness {
	evo := fakeEvoleap(t)
	store := NewMemStore()
	issues := &MemIssues{Orgs: map[string][]core.OpsIssue{}}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": r.Method + " " + r.URL.Path})
	})
	srv := &Server{Store: store, Issues: issues, IssueAPI: api, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: 90 * 24 * time.Hour}
	return &harness{t: t, h: srv.Handler(), store: store, issues: issues}
}

func (h *harness) do(method, path, token string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (h *harness) login(evo, device string) (int, map[string]any) {
	return h.do("POST", "/api/auth/evoleap", "", map[string]any{"token": evo, "device": device})
}

func TestBootstrapAndAllowList(t *testing.T) {
	h := newHarness(t)
	// 名單是空的:不是 bootstrap email 的人不能當第一位管理員
	if c, out := h.login("evo-member", "x"); c != 403 || !strings.Contains(out["error"].(string), "沒有 omixor 的權限") {
		t.Fatal(c, out)
	}
	// email 沒經過 Google 驗證的不行;evoleap 登入無效的不行
	if c, _ := h.login("evo-pw", "x"); c != 403 {
		t.Fatal(c)
	}
	if c, _ := h.login("bogus", "x"); c != 401 {
		t.Fatal(c)
	}
	// bootstrap(email 不分大小寫)
	c, out := h.login("evo-boss", "MacBook CLI")
	if c != 200 || out["user"].(map[string]any)["role"] != "admin" || !strings.HasPrefix(out["token"].(string), TokenPrefix) || out["expiresAt"] == nil {
		t.Fatal(c, out)
	}
	admin := out["token"].(string)
	// 第二次登入不會再建一個管理員,同一個人另一台裝置拿到另一個 token
	_, out2 := h.login("evo-boss", "omixor app")
	if out2["token"] == admin {
		t.Fatal("每台裝置要不同的 token")
	}
	if n, _ := h.store.CountUsers(); n != 1 {
		t.Fatal(n)
	}
	// 管理員加入成員(先用 email,第一次登入時對上 evoleap 帳號)
	if c, out := h.do("POST", "/api/admin/users", admin, map[string]any{"email": "Member@example.com"}); c != 200 || out["result"].(map[string]any)["role"] != "member" {
		t.Fatal(c, out)
	}
	c, out = h.login("evo-member", "成員的 Mac")
	if c != 200 {
		t.Fatal(c, out)
	}
	member := out["token"].(string)
	if u, _ := h.store.UserByEmail("member@example.com"); u.EvoleapID != "e2" {
		t.Fatalf("%+v", u)
	}
	// 名單外的人還是不行(bootstrap 只在名單空的時候)
	if c, _ := h.login("evo-other", "x"); c != 403 {
		t.Fatal(c)
	}
	// 成員可以用 Issue,不能用管理功能
	if c, out := h.do("GET", "/api/issues/default", member, nil); c != 200 || out["result"] != "GET /api/issues/default" {
		t.Fatal(c, out)
	}
	if c, _ := h.do("GET", "/api/admin/users", member, nil); c != 403 {
		t.Fatal(c)
	}
	// 沒帶、帶 evoleap 的 token、亂帶:都不行
	for _, tok := range []string{"", "evo-boss", "omx_nope"} {
		if c, _ := h.do("GET", "/api/issues/default", tok, nil); c != 401 {
			t.Fatal(tok, c)
		}
	}
	// 停用成員:舊 token 立刻失效,也不能重新登入
	h.do("POST", "/api/admin/users", admin, map[string]any{"email": "member@example.com", "disabled": true})
	if c, _ := h.do("GET", "/api/issues/default", member, nil); c != 403 {
		t.Fatal(c)
	}
	if c, _ := h.login("evo-member", "x"); c != 403 {
		t.Fatal(c)
	}
	// 管理員不能把自己降級
	if c, _ := h.do("POST", "/api/admin/users", admin, map[string]any{"email": "boss@example.com", "role": "member"}); c != 400 {
		t.Fatal(c)
	}
	// 撤銷:列出 token、撤銷 app 那一個,CLI 那個還能用
	_, toks := h.do("GET", "/api/admin/tokens", admin, nil)
	var appID string
	for _, x := range toks["result"].([]any) {
		m := x.(map[string]any)
		if m["device"] == "omixor app" {
			appID = m["id"].(string)
		}
	}
	if c, _ := h.do("POST", "/api/admin/tokens/"+appID+"/revoke", admin, nil); c != 200 {
		t.Fatal(c)
	}
	if c, _ := h.do("GET", "/api/issues/default", out2["token"].(string), nil); c != 401 {
		t.Fatal("撤銷後要失效", c)
	}
	if c, _ := h.do("GET", "/api/auth/me", admin, nil); c != 200 {
		t.Fatal(c)
	}
	// 自己登出
	h.do("POST", "/api/auth/logout", admin, nil)
	if c, _ := h.do("GET", "/api/auth/me", admin, nil); c != 401 {
		t.Fatal(c)
	}
	// token 只存雜湊
	for _, tk := range h.store.tokens {
		if len(tk.Hash) != 32 {
			t.Fatal("要存雜湊")
		}
	}
}

func TestTokenExpires(t *testing.T) {
	h := newHarness(t)
	_, out := h.login("evo-boss", "x")
	tok := out["token"].(string)
	h.store.nowFunc = func() time.Time { return time.Now().Add(91 * 24 * time.Hour) }
	if c, _ := h.do("GET", "/api/auth/me", tok, nil); c != 401 {
		t.Fatal("90 天後要失效", c)
	}
}

func TestMaintenanceAndAudit(t *testing.T) {
	h := newHarness(t)
	_, out := h.login("evo-boss", "CLI")
	admin := out["token"].(string)
	now := core.Time{Time: time.Date(2026, 11, 10, 1, 2, 3, 0, time.UTC)}
	file := []core.OpsIssue{
		{ID: "ISS-1", Title: "一", Status: "open", Priority: "high", Labels: []string{"a"}, CreatedBy: "tim", CreatedAt: now, UpdatedAt: now,
			Comments: []core.IssueComment{{ID: "c1", Author: "tim", Text: "留言", At: now}}},
		{ID: "ISS-2", Title: "二", Status: "closed", Priority: "low", Labels: []string{}, CreatedBy: "mcp", CreatedAt: now, UpdatedAt: now, Comments: []core.IssueComment{}},
	}
	c, r := h.do("POST", "/api/admin/issues/default/import", admin, map[string]any{"issues": file})
	if c != 200 || r["result"].(map[string]any)["inserted"].(float64) != 2 {
		t.Fatal(c, r)
	}
	// 只在第一次:已經有資料就拒絕,不做差異或覆蓋
	if c, r := h.do("POST", "/api/admin/issues/default/import", admin, map[string]any{"issues": file}); c != 409 || !strings.Contains(r["error"].(string), "只在第一次") {
		t.Fatal(c, r)
	}
	// 其他組織是空的,可以匯入
	if c, _ := h.do("POST", "/api/admin/issues/side/import", admin, map[string]any{"issues": file[:1]}); c != 200 {
		t.Fatal(c)
	}
	// 匯出
	_, r = h.do("GET", "/api/admin/issues/default/export", admin, nil)
	if len(r["result"].([]any)) != 2 {
		t.Fatal(r)
	}
	// 壞資料
	if c, _ := h.do("POST", "/api/admin/issues/default/import", admin, map[string]any{"issues": []map[string]any{{"title": "沒 id"}}}); c != 400 {
		t.Fatal(c)
	}
	if c, _ := h.do("GET", "/api/admin/issues/..%2Fx/export", admin, nil); c != 400 && c != 404 {
		t.Fatal(c)
	}
	// Issue 的寫入也記到操作紀錄
	h.do("POST", "/api/issues/default/ISS-1", admin, map[string]any{"title": "x"})
	_, r = h.do("GET", "/api/admin/audit?limit=50", admin, nil)
	got := mustJSON(r["result"])
	for _, want := range []string{"bootstrap.admin", `"login"`, "issues.import", "issues.export", "issue.update", "boss@example.com"} {
		if !strings.Contains(got, want) {
			t.Fatalf("操作紀錄缺 %s:%s", want, got)
		}
	}
	if strings.Contains(got, admin) {
		t.Fatal("操作紀錄不能有 token")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestLoginRateLimit(t *testing.T) {
	old1, old2 := LoginPerIP, LoginTotal
	LoginPerIP, LoginTotal = 3, 100
	defer func() { LoginPerIP, LoginTotal = old1, old2 }()
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		if c, _ := h.login("bogus", "x"); c != 401 {
			t.Fatal(i, c)
		}
	}
	if c, out := h.login("evo-boss", "x"); c != 429 || !strings.Contains(out["error"].(string), "太頻繁") {
		t.Fatal("超過上限要回 429", c, out)
	}
	// 被擋下的不會去問 evoleap,也不會寫操作紀錄
	if n := len(h.store.audit); n != 0 {
		t.Fatal(n)
	}
}

// 成員不能冒名:作者一律是自己;既有留言的作者照伺服器上的,新留言是自己。管理員(例如 app)照原本的
func TestMemberAuthorPinned(t *testing.T) {
	evo := fakeEvoleap(t)
	store := NewMemStore()
	now := core.Time{Time: time.Now().UTC()}
	issues := &MemIssues{Orgs: map[string][]core.OpsIssue{"default": {{ID: "ISS-1", Title: "一", Comments: []core.IssueComment{{ID: "c1", Author: "tim", Text: "原本的", At: now}}}}}}
	var got []map[string]any
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
	})
	srv := &Server{Store: store, Issues: issues, IssueAPI: api, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	h := &harness{t: t, h: srv.Handler(), store: store, issues: issues}
	_, out := h.login("evo-boss", "app")
	admin := out["token"].(string)
	h.do("POST", "/api/admin/users", admin, map[string]any{"email": "member@example.com"})
	_, out = h.login("evo-member", "x")
	member := out["token"].(string)

	h.do("POST", "/api/issues/default", member, map[string]any{"title": "x", "author": "agent:ceo"})
	h.do("POST", "/api/issues/default/ISS-1", member, map[string]any{"author": "agent:ceo", "comments": []map[string]any{
		{"id": "c1", "author": "冒名", "text": "原本的"}, {"id": "c2", "author": "agent:ceo", "text": "新的"}}})
	h.do("POST", "/api/issues/default", admin, map[string]any{"title": "y", "author": "agent:ceo"})
	if got[0]["author"] != "member@example.com" || got[1]["author"] != "member@example.com" {
		t.Fatal(mustJSON(got))
	}
	cs := got[1]["comments"].([]any)
	if cs[0].(map[string]any)["author"] != "tim" || cs[1].(map[string]any)["author"] != "member@example.com" {
		t.Fatal(mustJSON(cs))
	}
	if got[2]["author"] != "agent:ceo" {
		t.Fatal("管理員照原本的", mustJSON(got[2]))
	}
}

// Issue API 回錯誤時不能記成成功
func TestAuditSkipsFailedIssueWrites(t *testing.T) {
	evo := fakeEvoleap(t)
	store := NewMemStore()
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "要給 title"})
	})
	srv := &Server{Store: store, Issues: &MemIssues{}, IssueAPI: api, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	h := &harness{t: t, h: srv.Handler(), store: store}
	_, out := h.login("evo-boss", "x")
	h.do("POST", "/api/issues/default", out["token"].(string), map[string]any{})
	for _, e := range store.audit {
		if strings.HasPrefix(e.Action, "issue.") {
			t.Fatal("失敗的寫入不能記", e)
		}
	}
}

// evoleap 上改了 email,而新 email 已經是名單上另一個人:保留原本的 email,照樣能登入(不會因為 email 重複失敗)
func TestEmailConflictKeepsOldEmail(t *testing.T) {
	h := newHarness(t)
	_, out := h.login("evo-boss", "x")
	admin := out["token"].(string)
	h.do("POST", "/api/admin/users", admin, map[string]any{"email": "member@example.com"})
	if c, _ := h.login("evo-member", "x"); c != 200 {
		t.Fatal(c)
	}
	h.do("POST", "/api/admin/users", admin, map[string]any{"email": "other@example.com"}) // 待連結的另一個人
	if c, out := h.login("evo-member-renamed", "x"); c != 200 {
		t.Fatal(c, out)
	}
	u, _ := h.store.UserByEvoleapID("e2")
	if u.Email != "member@example.com" {
		t.Fatal("要保留原本的 email:", u.Email)
	}
	if !strings.Contains(mustJSON(h.store.audit), "email.conflict") {
		t.Fatal("要記到操作紀錄")
	}
	// 沒衝突時照 evoleap 更新
	h2 := newHarness(t)
	_, out = h2.login("evo-boss", "x")
	h2.do("POST", "/api/admin/users", out["token"].(string), map[string]any{"email": "member@example.com"})
	h2.login("evo-member", "x")
	h2.login("evo-member-renamed", "x")
	if u, _ := h2.store.UserByEvoleapID("e2"); u.Email != "other@example.com" {
		t.Fatal(u.Email)
	}
}
