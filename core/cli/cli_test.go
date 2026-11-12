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
	t.Setenv("OMIXOR_CONFIG_DIR", dir)
	t.Setenv("OMIXOR_CREDENTIALS", filepath.Join(dir, "cred.json"))
	t.Setenv("OMIXOR_SERVER", "")
	// 瀏覽器那段跳過:直接換 token 存成登入資料
	res, err := client.New(srv.URL).LoginWithEvoleap("x", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = saveCred(credentials{Server: srv.URL, Token: res.Token})
	if st, _ := os.Stat(credPath(srv.URL)); st.Mode().Perm() != 0o600 {
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
	cl, _ := authed(srv.URL)
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
	// token 只送回發它的伺服器:指到別台時當作沒登入那台(不會把這台的 token 送過去)
	t.Setenv("OMIXOR_SERVER", "https://evil.example")
	if err := Run([]string{"whoami"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "還沒登入 https://evil.example") {
		t.Fatal(err)
	}
}

// 登入資料依伺服器分檔;舊版 credentials.json 只給它記的那台沿用(複製過去,舊的不刪)
func TestCredentialsPerServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMIXOR_CONFIG_DIR", dir)
	t.Setenv("OMIXOR_CREDENTIALS", "")
	t.Setenv("OMIXOR_SERVER", "")
	prod, local := "https://prod.example.com", "http://127.0.0.1:8080"
	legacy, _ := json.Marshal(credentials{Server: prod + "/", Token: "omx_prod"})
	_ = os.WriteFile(filepath.Join(dir, "credentials.json"), legacy, 0o600)

	if got := pickServer(""); got != prod {
		t.Fatal("沒登入過新格式時用舊版記的那台", got)
	}
	if c := loadCred(prod); c.Token != "omx_prod" {
		t.Fatal("舊版的登入資料要沿用", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials", "prod.example.com.json")); err != nil {
		t.Fatal("要複製成新的檔案", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.json")); err != nil {
		t.Fatal("舊的不刪", err)
	}
	if c := loadCred(local); c.Token != "" {
		t.Fatal("舊版的 token 不能給別台", c)
	}
	if _, err := authed(local); err == nil || !strings.Contains(err.Error(), "--server "+local) {
		t.Fatal(err)
	}
	// 登入另一台:分開存,不會蓋掉
	_ = saveCred(credentials{Server: local, Token: "omx_local"})
	_ = setCurrentServer(local)
	if loadCred(local).Token != "omx_local" || loadCred(prod).Token != "omx_prod" {
		t.Fatal("兩台的登入要分開")
	}
	if st, _ := os.Stat(filepath.Join(dir, "credentials", "127.0.0.1_8080.json")); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatal("檔名用 host_port,權限 600")
	}
	if got := pickServer(""); got != local {
		t.Fatal("沒指定就用上次登入的", got)
	}
	if got := pickServer(prod); got != prod {
		t.Fatal("--server 優先", got)
	}
	t.Setenv("OMIXOR_SERVER", prod+"/")
	if got := pickServer(""); got != prod {
		t.Fatal("OMIXOR_SERVER 優先於上次登入的", got)
	}
}
