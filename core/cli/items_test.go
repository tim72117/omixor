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

// omixor items:新增(依賴、檔案、驗收)、列表(預設未完成)、領取、回報(完成帶證據、卡住帶原因與等待)、解除卡住、提升、取消、刪除
func TestCLIItems(t *testing.T) {
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
	cl, _ := authed(srv.URL)
	iss, err := cl.CreateIssue("default", service.IssueInput{Title: "大構想"})
	if err != nil {
		t.Fatal(err)
	}
	k := iss.Key
	if out := run("items", "add", k, "第一階段", "--files", "a.go,b.go", "--acceptance", "測試過"); !strings.Contains(out, k+".1 [todo] 第一階段") {
		t.Fatal(out)
	}
	if out := run("items", "add", k, "第二階段", "--depends", "1"); !strings.Contains(out, k+".2 [todo]") || !strings.Contains(out, "←"+k+".1") {
		t.Fatal(out)
	}
	if err := Run([]string{"items", "claim", k + ".2"}, &bytes.Buffer{}); err == nil {
		t.Fatal("前置未完成不能領")
	}
	if err := Run([]string{"items", "claim", "ISS-1"}, &bytes.Buffer{}); err == nil {
		t.Fatal("子項編號要有序號")
	}
	if out := run("items", "claim", k+".1", "--note", "session A"); !strings.Contains(out, "[doing]") || !strings.Contains(out, "領取:") {
		t.Fatal(out)
	}
	if out := run("items", "show", k+".1"); !strings.Contains(out, `"acceptance": "測試過"`) || !strings.Contains(out, `"a.go"`) {
		t.Fatal(out)
	}
	if out := run("items", "report", k+".1", "done", "--evidence", "go test 通過", "--note", "沒驗證 Swift"); !strings.Contains(out, "[done]") {
		t.Fatal(out)
	}
	if out := run("items", "show", k+".1"); !strings.Contains(out, "go test 通過") || !strings.Contains(out, "沒驗證 Swift") {
		t.Fatal("證據字串要包成物件:", out)
	}
	if out := run("items", "list", k); strings.Contains(out, ".1 ") || !strings.Contains(out, k+".2") {
		t.Fatal("預設只列未完成:", out)
	}
	if out := run("items", "list", k, "--include-done"); !strings.Contains(out, k+".1 [done]") {
		t.Fatal(out)
	}
	run("items", "claim", k+".2")
	if out := run("items", "block", k+".2", "要人選", "--wait-kind", "user_decision", "--wait-for", "A 或 B"); !strings.Contains(out, "卡住:要人選") || !strings.Contains(out, "等待 user_decision:A 或 B") {
		t.Fatal(out)
	}
	if out := run("items", "unblock", k+".2"); !strings.Contains(out, "[todo]") {
		t.Fatal(out)
	}
	if out := run("items", "add", k, "太大", "--assignee", "boss@example.com"); !strings.Contains(out, k+".3") || !strings.Contains(out, "負責:boss@example.com") {
		t.Fatal(out)
	}
	if out := run("items", "update", k+".3", "改名", "--acceptance", "新驗收"); !strings.Contains(out, "改名") {
		t.Fatal(out)
	}
	if out := run("items", "promote", k+".3", "--priority", "high"); !strings.Contains(out, "已提升為") {
		t.Fatal(out)
	}
	if out := run("items", "cancel", k+".2"); !strings.Contains(out, "[canceled]") {
		t.Fatal(out)
	}
	if out := run("items", "delete", k+".2"); !strings.Contains(out, "已刪除") {
		t.Fatal(out)
	}
	if err := Run([]string{"items", "show", k + ".2"}, &bytes.Buffer{}); err == nil {
		t.Fatal("刪掉的要 404")
	}
}
