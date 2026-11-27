package mcp

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"omixor-web/internal/core"

	"omixor-core/model"
	"omixor-core/store"
	"omixor-core/storetest"
)

// iaStore console 自己的核心存放處與目前組織(測試直接查核心用)
func iaStore(t *testing.T, a *core.App) (*store.PG, string) {
	t.Helper()
	svc := core.AgentService()
	if svc == nil {
		t.Fatal("沒有核心")
	}
	st, ok := svc.Store.(*store.PG)
	if !ok {
		t.Fatalf("核心的存放處:%T", svc.Store)
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return st, a.CurrentOrgID()
}

// web /mcp 的留言與別處(直接寫核心)同時新增 N 則,最後剛好 N 則;append_body 也不互相蓋掉
func TestWebMCPConcurrentCommentsWithCore(t *testing.T) {
	a := iaApp(t)
	st, org := iaStore(t, a)
	iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "並發"})
	other := storetest.Legacy(t, st, org, "x") // 直接寫 Store 的留言作者要是 actor id

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
			// 別處(App、core/api)直接寫進核心:留言與內文
			if _, err := st.AddIssueComment(org, "ISS-1", model.Comment{ID: fmt.Sprintf("x-%d", k), Author: other, Text: fmt.Sprintf("other-%d", k), At: model.Now()}); err != nil {
				t.Error(err)
			}
		}(k)
	}
	wg.Wait()
	got, err := st.GetIssue(org, "ISS-1")
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
