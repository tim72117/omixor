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

// omixor issues accept|release <Issue>
func TestCLIIssueAccept(t *testing.T) {
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
	iss, err := cl.CreateIssue("default", service.IssueInput{Title: "待分析"})
	if err != nil {
		t.Fatal(err)
	}
	if out := run("issues", "accept", iss.Key); !strings.Contains(out, "已接受 "+iss.Key) || !strings.Contains(out, "負責:") {
		t.Fatal(out)
	}
	if i, _ := cl.GetIssue("default", iss.Key); i.AssigneeActor == nil {
		t.Fatalf("接受後要有負責人:%+v", i)
	}
	if out := run("issues", "release", iss.Key); !strings.Contains(out, "已放回 "+iss.Key) || strings.Contains(out, "負責:") {
		t.Fatal(out)
	}
	if err := Run([]string{"issues", "accept"}, &bytes.Buffer{}); err == nil {
		t.Fatal("沒給編號要報錯")
	}
}
