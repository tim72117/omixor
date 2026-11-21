package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omixor-core/api"
	"omixor-core/client"
	"omixor-core/service"
	"omixor-core/storetest"
)

// omixor decisions:提出(decision 附選項、help)、列表篩選、show、處理、由決定開 Issue、撤回
func TestCLIDecisions(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","name":"老闆","google":true}}`))
	}))
	defer evo.Close()
	svc := &service.Service{Store: storetest.New(t), Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	srv := httptest.NewServer(api.New(svc).Handler())
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("OMIXOR_CONFIG_DIR", dir)
	t.Setenv("OMIXOR_CREDENTIALS", filepath.Join(dir, "cred.json"))
	t.Setenv("OMIXOR_SERVER", "")
	res, err := client.New(srv.URL).LoginWithEvoleap("x", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = saveCred(credentials{Server: srv.URL, Token: res.Token})
	run := func(args ...string) string {
		t.Helper()
		var b bytes.Buffer
		if err := Run(args, &b); err != nil {
			t.Fatalf("%v:%v", args, err)
		}
		return b.String()
	}
	run("orgs", "create", "default", "--name", "evoleap")
	run("issues", "list") // 確認登入可用
	cl, _ := authed(srv.URL)
	iss, err := cl.CreateIssue("default", service.IssueInput{Title: "來源"})
	if err != nil {
		t.Fatal(err)
	}
	if out := run("decisions", "add", "選哪個", "--options", "*方案 A;方案 B", "--issue", iss.Key, "--body", "背景"); !strings.Contains(out, "DEC-1 [decision/open] 選哪個") || !strings.Contains(out, "←"+iss.Key) {
		t.Fatal(out)
	}
	if out := run("decisions", "add", "請備份", "--kind", "help"); !strings.Contains(out, "DEC-2 [help/open]") {
		t.Fatal(out)
	}
	if err := Run([]string{"decisions", "add", "x", "--kind", "help", "--options", "a;b"}, &bytes.Buffer{}); err == nil {
		t.Fatal("help 帶選項要被拒絕")
	}
	if out := run("decisions", "list", "--kind", "help"); !strings.Contains(out, "DEC-2") || strings.Contains(out, "DEC-1") {
		t.Fatal(out)
	}
	if out := run("decisions", "--mine"); !strings.Contains(out, "DEC-1") || !strings.Contains(out, "DEC-2") {
		t.Fatal("待我處理兩種都含", out)
	}
	if out := run("decisions", "show", "DEC-1"); !strings.Contains(out, `"recommended": true`) || !strings.Contains(out, `"id": "o2"`) {
		t.Fatal(out)
	}
	if out := run("decisions", "answer", "DEC-1", "o2", "--note", "穩"); !strings.Contains(out, "decision/decided") {
		t.Fatal(out)
	}
	if out := run("decisions", "answer", "DEC-2", "done", "--evidence", "ok"); !strings.Contains(out, "help/已處理") {
		t.Fatal(out)
	}
	if err := Run([]string{"decisions", "answer", "DEC-2", "done"}, &bytes.Buffer{}); err == nil {
		t.Fatal("重複處理要被拒絕")
	}
	if out := run("decisions", "list", "--status", "decided", "--issue", iss.Key); !strings.Contains(out, "DEC-1") || strings.Contains(out, "DEC-2") {
		t.Fatal(out)
	}
	run("decisions", "reopen", "DEC-2")
	if out := run("decisions", "withdraw", "DEC-2"); !strings.Contains(out, "withdrawn") {
		t.Fatal(out)
	}
	run("decisions", "add", "又一個", "--options", "a;b")
	if out := run("decisions", "to-issue", "DEC-3"); !strings.Contains(out, "已開出") {
		t.Fatal(out)
	}
}
