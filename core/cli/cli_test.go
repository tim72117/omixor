package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omixor-core/api"
	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// CLI 對真的核心 API(記憶體存放 + 假的 evoleap)跑一輪
func TestCLI(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","name":"老闆","google":true}}`))
	}))
	defer evo.Close()
	st := store.NewMem()
	svc := &service.Service{Store: st, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	srv := httptest.NewServer(api.New(svc).Handler())
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("OMIXOR_CREDENTIALS", filepath.Join(dir, "cred.json"))
	t.Setenv("OMIXOR_SERVER", "")
	// 瀏覽器那段跳過:直接換 token 存成登入資料
	res, err := client.New(srv.URL).LoginWithEvoleap("x", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = saveCred(credentials{Server: srv.URL, Token: res.Token})
	if st, _ := os.Stat(credPath()); st.Mode().Perm() != 0o600 {
		t.Fatal("登入資料的權限要是 600")
	}
	run := func(args ...string) string {
		t.Helper()
		var b bytes.Buffer
		if err := Run(args, &b); err != nil {
			t.Fatalf("%v:%v", args, err)
		}
		return b.String()
	}
	run("orgs", "create", "default", "--name", "evoleap")
	if out := run("orgs"); !strings.Contains(out, "default") || !strings.Contains(out, "admin") {
		t.Fatal(out)
	}
	run("members", "add", "a@example.com", "--role", "member")
	if out := run("members"); !strings.Contains(out, "a@example.com") {
		t.Fatal(out)
	}
	now := model.Time{Time: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
	file := filepath.Join(dir, "issues.json")
	b, _ := json.Marshal([]model.Issue{{ID: "ISS-1", Title: "原本的", Status: "open", Priority: "normal", Labels: []string{"a"}, CreatedBy: "tim", CreatedAt: now, UpdatedAt: now,
		Comments: []model.Comment{{ID: "c1", Author: "tim", Text: "原本的留言", At: now}}}})
	_ = os.WriteFile(file, b, 0o600)
	if out := run("issues", "import", file); !strings.Contains(out, "1 筆") {
		t.Fatal(out)
	}
	// 改壞再還原
	cl, _ := authed()
	bad, closed := "壞掉", "closed"
	_, _ = cl.UpdateIssue("default", "ISS-1", service.IssuePatch{Title: &bad, Status: &closed})
	run("issues", "restore", "ISS-1", "--from", file)
	got, _ := cl.GetIssue("default", "ISS-1")
	if got.Title != "原本的" || got.Status != "open" || got.ClosedAt != nil || len(got.Comments) != 1 {
		t.Fatalf("%+v", got)
	}
	// 刪除:沒加 --yes 只列出
	if out := run("issues", "delete", "--all"); !strings.Contains(out, "--yes") {
		t.Fatal(out)
	}
	run("issues", "delete", "--all", "--yes")
	if out := run("issues", "list"); strings.TrimSpace(out) != "" {
		t.Fatal(out)
	}
	// token 只送回發它的伺服器
	t.Setenv("OMIXOR_SERVER", "https://evil.example")
	if err := Run([]string{"whoami"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "OMIXOR_SERVER") {
		t.Fatal(err)
	}
}
