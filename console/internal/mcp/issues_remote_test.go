package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-web/internal/core"

	"omixor-core/model"
)

// 假的 Issue 伺服器(omixor-issues 的 /api/issues/<組織>):記住收到的 token,資料放記憶體
type fakeIssueServer struct {
	mu     sync.Mutex
	issues map[string][]model.Issue
	tokens []string
	// patches 收到的修改(PATCH/POST …/issues/{id})內容;commentPosts 只附加留言的次數
	patches      []map[string]any
	commentPosts int
}

func (f *fakeIssueServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))
	// 新路徑 /api/orgs/{org}/issues[/…] 與舊路徑 /api/issues/{org}[/…]
	path := r.URL.Path
	if rest, ok := strings.CutPrefix(path, "/api/orgs/"); ok {
		org, tail, _ := strings.Cut(rest, "/")
		tail = strings.TrimPrefix(strings.TrimPrefix(tail, "issues"), "/")
		path = "/api/issues/" + org
		if tail != "" {
			path += "/" + tail
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/issues/"), "/")
	org := parts[0]
	var in map[string]any
	_ = json.NewDecoder(r.Body).Decode(&in)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"result": v}) }
	if r.URL.Path == "/api/orgs" {
		reply([]map[string]any{{"id": "default", "name": "default", "createdAt": "2026-01-01T00:00:00Z", "myRole": "admin"}})
		return
	}
	if r.URL.Path == "/api/auth/me" {
		reply(map[string]any{"user": map[string]any{"id": "u1", "email": "me@example.com", "name": "我", "role": "admin"}, "token": map[string]any{}})
		return
	}
	list := f.issues[org]
	switch {
	case r.Method == "GET":
		if list == nil {
			list = []model.Issue{}
		}
		reply(list)
	case len(parts) == 1:
		now := model.Time{Time: time.Now().UTC().Truncate(time.Second)}
		i := model.Issue{ID: "ISS-" + string(rune('0'+len(list)+1)), Title: in["title"].(string), Status: "open", Priority: "normal",
			Labels: []string{}, Comments: []model.Comment{}, CreatedBy: in["author"].(string), CreatedAt: now, UpdatedAt: now}
		f.issues[org] = append([]model.Issue{i}, list...)
		reply(i)
	case len(parts) == 3 && parts[2] == "comments" && r.Method == "POST": // 只附加一則留言
		f.commentPosts++
		for k := range list {
			if list[k].ID == parts[1] {
				who, _ := in["author"].(string)
				list[k].Comments = append(list[k].Comments, model.Comment{ID: "C" + string(rune('0'+len(list[k].Comments)+1)),
					Author: who, Text: in["text"].(string), At: model.Time{Time: time.Now().UTC()}})
				reply(list[k])
				return
			}
		}
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "找不到 Issue " + parts[1]})
	case len(parts) == 3 && parts[2] == "delete":
		kept := []model.Issue{}
		for _, i := range list {
			if i.ID != parts[1] {
				kept = append(kept, i)
			}
		}
		f.issues[org] = kept
		reply(true)
	default:
		f.patches = append(f.patches, in)
		for k := range list {
			if list[k].ID == parts[1] {
				b, _ := json.Marshal(in)
				id, created := list[k].ID, list[k].CreatedAt
				_ = json.Unmarshal(b, &list[k])
				list[k].ID, list[k].CreatedAt = id, created
				reply(list[k])
				return
			}
		}
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "找不到 Issue " + parts[1]})
	}
}

func TestIssueServerMode(t *testing.T) {
	f := &fakeIssueServer{issues: map[string][]model.Issue{}}
	srv := httptest.NewServer(f)
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OMIXOR_SERVER_TOKEN", "tok-123")
	a := iaApp(t)
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
		t.Fatal(err)
	}
	d := iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "遠端的第一筆"})
	if d["id"] != "ISS-1" {
		t.Fatal(d)
	}
	// 別處(例如 Swift app)直接在伺服器上新增:查詢時看得到
	f.mu.Lock()
	f.issues["default"] = append([]model.Issue{{ID: "ISS-2", Title: "別處新增", Status: "open", Priority: "high", Labels: []string{}, Comments: []model.Comment{}}}, f.issues["default"]...)
	f.mu.Unlock()
	l := iaRun(t, a, core.MCPActor, "issues", map[string]any{})
	if l["total"].(float64) != 2 {
		t.Fatal(l)
	}
	d = iaRun(t, a, core.MCPActor, "issue_update", map[string]any{"id": "ISS-2", "status": "closed", "comment": "處理好了"})
	if d["status"] != "closed" || len(d["comments"].([]any)) != 1 {
		t.Fatal(d)
	}
	f.mu.Lock()
	if f.issues["default"][0].Status != "closed" || f.tokens[0] != "Bearer tok-123" {
		t.Fatalf("%+v %v", f.issues["default"][0], f.tokens[0])
	}
	f.mu.Unlock()
	// 伺服器關掉:建立與修改要回錯誤,不能假裝成功
	srv.Close()
	iaErrPrefix(t, a, "issue_create", map[string]any{"title": "x"}, "Issue 沒有存起來:連不到 omixor server")
	iaErrPrefix(t, a, "issue_update", map[string]any{"id": "ISS-1", "title": "y"}, "Issue 沒有改成:連不到 omixor server")
}

func iaErrPrefix(t *testing.T, a *core.App, name string, args map[string]any, want string) {
	t.Helper()
	_, err := Run(a, name, args, core.MCPActor)
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("%s:想要「%s…」,得到 %v", name, want, err)
	}
}

// omixor 自己的授權:沒有 omixor token 時用 evoleap 連線換一個;被撤銷(401)時自動再換(同 Swift)
func TestIssueServerOmixorToken(t *testing.T) {
	f := &fakeIssueServer{issues: map[string][]model.Issue{}}
	var mu sync.Mutex
	valid := map[string]bool{}
	exchanges := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.URL.Path == "/api/auth/evoleap" {
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["token"] != "evo-tok" {
				mu.Unlock()
				w.WriteHeader(401)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "evoleap 的登入無效或已過期"})
				return
			}
			exchanges++
			tok := "omx_" + string(rune('0'+exchanges))
			valid[tok] = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"token": tok})
			return
		}
		ok := valid[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		mu.Unlock()
		if !ok {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "omixor 的登入無效、已過期或已撤銷,請重新登入"})
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OMIXOR_SERVER_TOKEN", "")
	core.Vault = core.NewMemoryVault()
	a := iaApp(t)
	// 沒登入 evoleap:錯誤訊息要說清楚
	iaErrPrefix(t, a, "issue_create", map[string]any{"title": "x"}, "Issue 沒有存起來:還沒登入 omixor server")
	core.ConnSetBearer(a, "evoleap", "evo-tok")                 // 自己會拿鎖
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil { // 用 evoleap 連線換 token、讀組織清單
		t.Fatal(err)
	}
	if d := iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "一"}); d["id"] != "ISS-1" {
		t.Fatal(d)
	}
	for _, tok := range f.tokens {
		if tok != "Bearer omx_1" {
			t.Fatal("Issue API 只帶 omixor 的 token:", tok)
		}
	}
	// 伺服器撤銷:自動換新的
	mu.Lock()
	valid = map[string]bool{}
	mu.Unlock()
	if d := iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "二"}); d["id"] != "ISS-2" {
		t.Fatal(d)
	}
	if exchanges != 2 {
		t.Fatal("被撤銷後要重新換一次:", exchanges)
	}
}
