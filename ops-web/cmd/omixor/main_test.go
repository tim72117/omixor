package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opsweb/internal/core"
	"opsweb/internal/omixor"
)

// CLI 對真的 omixor.Server(記憶體存放處 + 假的 evoleap)跑一輪:登入後的使用者管理、匯入試算、匯入、比對、匯出、登出
func TestCLIAgainstServer(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer evo-boss" {
			w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","google":true}}`))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	defer evo.Close()
	mem := &omixor.MemIssues{}
	srv := &omixor.Server{Store: omixor.NewMemStore(), Issues: mem, IssueAPI: http.NotFoundHandler(),
		Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	dir := t.TempDir()
	t.Setenv("OMIXOR_CREDENTIALS", filepath.Join(dir, "cred.json"))
	t.Setenv("OMIXOR_SERVER", ts.URL)
	// 瀏覽器那段在測試裡跳過:直接換 token,存成 CLI 的登入資料
	raw, err := call(ts.URL, "", "POST", "/api/auth/evoleap", map[string]any{"token": "evo-boss", "device": "test"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(raw, &out)
	if err := saveCred(credentials{Server: ts.URL, Token: out.Token}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(credPath()); st.Mode().Perm() != 0o600 {
		t.Fatalf("登入資料的權限要是 600:%v", st.Mode().Perm())
	}

	users([]string{"add", "a@example.com", "--role", "member"})
	var list []omixor.User
	_ = json.Unmarshal(authed("GET", "/api/admin/users", nil), &list)
	if len(list) != 2 {
		t.Fatal(list)
	}

	now := core.Time{Time: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
	file := filepath.Join(dir, "issues.json")
	b, _ := json.Marshal([]core.OpsIssue{{ID: "ISS-1", Title: "一", Status: "open", Priority: "normal", Labels: []string{}, Comments: []core.IssueComment{}, CreatedAt: now, UpdatedAt: now}})
	_ = os.WriteFile(file, b, 0o600)
	issues2 := func(args ...string) { issues(args) }
	issues2("import", file)
	if len(mem.Orgs["default"]) != 1 {
		t.Fatal(mem.Orgs)
	}
	// 第二次匯入:伺服器已經有資料,拒絕
	if _, err := call(ts.URL, out.Token, "POST", "/api/admin/issues/default/import", map[string]any{"issues": []any{map[string]any{"id": "ISS-9", "title": "x"}}}); err == nil || !strings.Contains(err.Error(), "只在第一次") {
		t.Fatal(err)
	}
	exp := filepath.Join(dir, "export.json")
	issues2("export", "-o", exp)
	if b, _ := os.ReadFile(exp); !strings.Contains(string(b), `"ISS-1"`) {
		t.Fatal(string(b))
	}
	// 登出後 token 失效
	_, err = call(ts.URL, out.Token, "POST", "/api/auth/logout", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(ts.URL, out.Token, "GET", "/api/auth/me", nil); err == nil {
		t.Fatal("登出後要失效")
	}
}

func TestReorderFlags(t *testing.T) {
	got := strings.Join(reorder([]string{"a@x.com", "--role", "admin", "file", "--org", "x"}), " ")
	if got != "--role admin --org x a@x.com file" {
		t.Fatal(got)
	}
}
