package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opsweb/internal/core"
)

// 假的 Issue 伺服器(omixor-issues 的 /api/issues/<組織>):記住收到的 token,資料放記憶體
type fakeIssueServer struct {
	mu     sync.Mutex
	issues map[string][]core.OpsIssue
	tokens []string
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
	list := f.issues[org]
	switch {
	case r.Method == "GET":
		if list == nil {
			list = []core.OpsIssue{}
		}
		reply(list)
	case len(parts) == 1:
		now := core.Time{Time: time.Now().UTC().Truncate(time.Second)}
		i := core.OpsIssue{ID: "ISS-" + string(rune('0'+len(list)+1)), Title: in["title"].(string), Status: "open", Priority: "normal",
			Labels: []string{}, Comments: []core.IssueComment{}, CreatedBy: in["author"].(string), CreatedAt: now, UpdatedAt: now}
		f.issues[org] = append([]core.OpsIssue{i}, list...)
		reply(i)
	case len(parts) == 3 && parts[2] == "delete":
		kept := []core.OpsIssue{}
		for _, i := range list {
			if i.ID != parts[1] {
				kept = append(kept, i)
			}
		}
		f.issues[org] = kept
		reply(true)
	default:
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
	f := &fakeIssueServer{issues: map[string][]core.OpsIssue{}}
	srv := httptest.NewServer(f)
	t.Setenv("OPS_ISSUE_SERVER", srv.URL)
	t.Setenv("OPS_ISSUE_SERVER_TOKEN", "tok-123")
	a := iaApp(t)
	d := iaRun(t, a, core.MCPActor, "ops_issue_create", map[string]any{"title": "遠端的第一筆"})
	if d["id"] != "ISS-1" {
		t.Fatal(d)
	}
	// 別處(例如 Swift app)直接在伺服器上新增:查詢時看得到
	f.mu.Lock()
	f.issues["default"] = append([]core.OpsIssue{{ID: "ISS-2", Title: "別處新增", Status: "open", Priority: "high", Labels: []string{}, Comments: []core.IssueComment{}}}, f.issues["default"]...)
	f.mu.Unlock()
	l := iaRun(t, a, core.MCPActor, "ops_issues", map[string]any{})
	if l["total"].(float64) != 2 {
		t.Fatal(l)
	}
	d = iaRun(t, a, core.MCPActor, "ops_issue_update", map[string]any{"id": "ISS-2", "status": "closed", "comment": "處理好了"})
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
	iaErrPrefix(t, a, "ops_issue_create", map[string]any{"title": "x"}, "Issue 沒有存起來:連不到 omixor server")
	iaErrPrefix(t, a, "ops_issue_update", map[string]any{"id": "ISS-1", "title": "y"}, "Issue 沒有改成:連不到 omixor server")
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
	f := &fakeIssueServer{issues: map[string][]core.OpsIssue{}}
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
	t.Setenv("OPS_ISSUE_SERVER", srv.URL)
	t.Setenv("OPS_ISSUE_SERVER_TOKEN", "")
	core.Vault = core.NewMemoryVault()
	a := iaApp(t)
	// 沒登入 evoleap:錯誤訊息要說清楚
	iaErrPrefix(t, a, "ops_issue_create", map[string]any{"title": "x"}, "Issue 沒有存起來:還沒登入 evoleap 連線")
	core.ConnSetBearer(a, "evoleap", "evo-tok") // 自己會拿鎖
	if d := iaRun(t, a, core.MCPActor, "ops_issue_create", map[string]any{"title": "一"}); d["id"] != "ISS-1" {
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
	if d := iaRun(t, a, core.MCPActor, "ops_issue_create", map[string]any{"title": "二"}); d["id"] != "ISS-2" {
		t.Fatal(d)
	}
	if exchanges != 2 {
		t.Fatal("被撤銷後要重新換一次:", exchanges)
	}
}
