package mcp_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// countingStore 記下留言走哪一條路
type countingStore struct {
	store.Store
	adds, fullWrites atomic.Int32
}

func (c *countingStore) AddIssueComment(org, id string, cm model.Comment) (*model.Issue, error) {
	c.adds.Add(1)
	return c.Store.AddIssueComment(org, id, cm)
}

func (c *countingStore) UpdateIssue(org string, i model.Issue) error {
	if i.Comments != nil {
		c.fullWrites.Add(1)
	}
	return c.Store.UpdateIssue(org, i)
}

// noCommentBackend 模擬還沒實作 AddIssueComment 的 Backend(例如 omixor-web 的 appBackend):留言要退回舊的做法
type noCommentBackend struct{ *backend.Local }

func (noCommentBackend) AddIssueComment(string, string, string, string) (*model.Issue, error) {
	return backend.Unsupported{}.AddIssueComment("", "", "", "")
}

func issueToolsEnv(t *testing.T) (*countingStore, *service.Service, *model.Principal) {
	cs := &countingStore{Store: storetest.New(t)}
	svc := &service.Service{Store: cs}
	now := model.Now()
	_ = cs.SaveUser(model.User{ID: "u1", Email: "amy@x", Role: "member", CreatedAt: now})
	_ = cs.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = cs.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "amy@x", Role: "member", AddedAt: now})
	return cs, svc, &model.Principal{User: model.User{ID: "u1", Email: "amy@x", Role: "member"}}
}

// issue_update 的 comment 走 AddIssueComment、append_body 由核心以最新內文附加;同時多個呼叫不會互相蓋掉
func TestIssueUpdateCommentAndAppendBody(t *testing.T) {
	cs, svc, amy := issueToolsEnv(t)
	h := mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: amy}, "lab", nil
	}})
	iss, err := svc.CreateIssue(amy, "lab", service.IssueInput{Title: "t", Body: "開頭"})
	if err != nil {
		t.Fatal(err)
	}
	ok(t, h, "", "issue_update", map[string]any{"id": strings.ToLower(iss.ID), "comment": "第一則", "append_body": "補充"})
	got, _ := svc.GetIssue(amy, "lab", iss.ID)
	if len(got.Comments) != 1 || got.Comments[0].Author != "amy@x" || got.Comments[0].Text != "第一則" || got.Body != "開頭\n\n補充" {
		t.Fatalf("%+v", got)
	}

	const n = 15
	var wg sync.WaitGroup
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok(t, h, "", "issue_update", map[string]any{"id": iss.ID, "comment": fmt.Sprintf("同時 %d", k), "append_body": fmt.Sprintf("段 %d", k)})
		}()
	}
	wg.Wait()
	got, _ = svc.GetIssue(amy, "lab", iss.ID)
	if len(got.Comments) != n+1 {
		t.Fatalf("留言應該 %d 則:%d", n+1, len(got.Comments))
	}
	for k := range n {
		if !strings.Contains(got.Body+"\n", fmt.Sprintf("段 %d\n", k)) {
			t.Fatalf("段 %d 不見:%q", k, got.Body)
		}
	}
	if cs.adds.Load() != n+1 || cs.fullWrites.Load() != 0 {
		t.Fatalf("留言應該都走 AddIssueComment:adds=%d fullWrites=%d", cs.adds.Load(), cs.fullWrites.Load())
	}
}

// Backend 沒有 AddIssueComment 時退回舊做法(讀整份 → 加一則 → 整份送回),留言仍然加得上
func TestIssueUpdateCommentFallback(t *testing.T) {
	_, svc, amy := issueToolsEnv(t)
	h := mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return noCommentBackend{&backend.Local{Svc: svc, P: amy}}, "lab", nil
	}})
	iss, _ := svc.CreateIssue(amy, "lab", service.IssueInput{Title: "t", Body: "開頭"})
	ok(t, h, "", "issue_update", map[string]any{"id": iss.ID, "comment": "舊路徑", "append_body": "補充"})
	got, _ := svc.GetIssue(amy, "lab", iss.ID)
	if len(got.Comments) != 1 || got.Comments[0].Text != "舊路徑" || got.Comments[0].Author != "amy@x" || got.Body != "開頭\n\n補充" {
		t.Fatalf("%+v", got)
	}
}
