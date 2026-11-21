package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-web/internal/core"

	"omixor-core/api"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// web 的 /mcp:comment 走核心的 POST …/comments(只附加一則),append_body 送 appendBody,修改不帶整份留言
func TestWebMCPCommentAndAppendBodyAppendOnly(t *testing.T) {
	f := &fakeIssueServer{issues: map[string][]model.Issue{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OMIXOR_SERVER_TOKEN", "tok-123")
	a := iaApp(t)
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
		t.Fatal(err)
	}
	iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "只附加"})
	f.mu.Lock()
	f.issues["default"][0].Body = "原本"
	f.patches = nil
	f.mu.Unlock()

	d := iaRun(t, a, core.MCPActor, "issue_update", map[string]any{"id": "ISS-1", "comment": "第一則"})
	if len(d["comments"].([]any)) != 1 {
		t.Fatal(d)
	}
	f.mu.Lock()
	if f.commentPosts != 1 || len(f.patches) != 0 {
		t.Fatalf("只留言要走 …/comments、不能送修改:posts=%d patches=%v", f.commentPosts, f.patches)
	}
	f.mu.Unlock()

	iaRun(t, a, core.MCPActor, "issue_update", map[string]any{"id": "ISS-1", "append_body": "補充", "status": "closed", "comment": "第二則"})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commentPosts != 2 || len(f.patches) != 1 {
		t.Fatalf("posts=%d patches=%v", f.commentPosts, f.patches)
	}
	p := f.patches[0]
	if p["appendBody"] != "補充" || p["status"] != "closed" {
		t.Fatalf("要送 appendBody 與 status:%v", p)
	}
	for _, k := range []string{"comments", "body", "title", "labels", "priority", "source", "link"} {
		if _, ok := p[k]; ok {
			t.Fatalf("不該送 %s:%v", k, p)
		}
	}
}

// 遠端模式接上真的核心:web /mcp 的留言與別處(直接打核心)同時新增 N 則,最後剛好 N 則;append_body 也不互相蓋掉
func TestWebMCPConcurrentCommentsWithCore(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer evo-tok" {
			w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","name":"老闆","google":true}}`))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	defer evo.Close()
	st := storetest.New(t)
	svc := &service.Service{Store: st, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	srv := httptest.NewServer(api.New(svc).Handler())
	defer srv.Close()
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OPS_ISSUE_SERVER", "") // 舊名稱也清掉,不受開發機環境影響
	t.Setenv("OMIXOR_SERVER_TOKEN", "")
	t.Setenv("OPS_ISSUE_SERVER_TOKEN", "")
	core.Vault = core.NewMemoryVault()
	a := iaApp(t)
	core.ConnSetBearer(a, "evoleap", "evo-tok")
	_ = st.CreateOrg(modelOrg("default"))
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
		t.Fatal(err)
	}
	iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "並發"})
	other := storetest.Legacy(t, st, "default", "x") // 直接寫 Store 的留言作者要是 actor id

	const n = 30
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			if k%2 == 0 {
				if _, err := Run(a, "issue_update", map[string]any{"id": "ISS-1", "comment": fmt.Sprintf("web-%d", k), "append_body": fmt.Sprintf("p%d", k)}, core.MCPActor); err != nil {
					t.Error(err)
				}
				return
			}
			// 別處(App、另一個 omixor-web)直接寫進核心:留言與內文
			if _, err := st.AddIssueComment("default", "ISS-1", model.Comment{ID: fmt.Sprintf("x-%d", k), Author: other, Text: fmt.Sprintf("other-%d", k), At: model.Now()}); err != nil {
				t.Error(err)
			}
		}(k)
	}
	wg.Wait()
	got, err := st.GetIssue("default", "ISS-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Comments) != n {
		t.Fatalf("留言要 %d 則,得到 %d 則", n, len(got.Comments))
	}
	for k := 0; k < n; k += 2 {
		if !containsLine(got.Body, fmt.Sprintf("p%d", k)) {
			t.Fatalf("內文少了 p%d:%q", k, got.Body)
		}
	}
}

func containsLine(body, s string) bool { return slices.Contains(strings.Split(body, "\n\n"), s) }
