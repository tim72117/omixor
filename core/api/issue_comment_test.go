package api_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// POST /api/orgs/{org}/issues/{id}/comments:只附加一則;同時多人留言不會互相蓋掉;作者規則同建立 Issue
func TestIssueCommentsEndpoint(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveMember("lab", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := boss.SaveAgent("lab", model.Agent{ID: "ceo", Name: "執行長", Active: true}); err != nil {
		t.Fatal(err)
	}
	alice, bob := e.login("evo-alice"), e.login("evo-bob")
	iss, err := alice.CreateIssue("lab", service.IssueInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := bob.AddIssueComment("lab", strings.ToLower(iss.ID), service.CommentInput{Text: "看到了", Author: "冒名"})
	if err != nil || len(u.Comments) != 1 || u.Comments[0].Author != "bob@example.com" || u.ID != iss.ID {
		t.Fatalf("%+v %v", u, err)
	}
	if u, _ := boss.AddIssueComment("lab", iss.ID, service.CommentInput{Text: "x", Author: "agent:ceo"}); u.Comments[1].Author != "agent:ceo" {
		t.Fatal("伺服器管理員可以指定作者", u.Comments[1])
	}
	if _, err := bob.AddIssueComment("lab", "ISS-99", service.CommentInput{Text: "x"}); status(err) != 404 {
		t.Fatal(err)
	}
	if _, err := bob.AddIssueComment("lab", iss.ID, service.CommentInput{Text: " "}); status(err) != 400 {
		t.Fatal(err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := alice
			if k%2 == 1 {
				c = bob
			}
			_, err := c.AddIssueComment("lab", iss.ID, service.CommentInput{Text: fmt.Sprintf("第 %d 則", k)})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := alice.GetIssue("lab", iss.ID)
	if len(got.Comments) != n+2 {
		t.Fatalf("留言應該 %d 則:%d", n+2, len(got.Comments))
	}

	// 一般成員用 comments 改別人的留言:403
	edited := append([]model.Comment{}, got.Comments...)
	edited[0].Text = "改掉"
	if _, err := alice.UpdateIssue("lab", iss.ID, service.IssuePatch{Comments: &edited}); status(err) != 403 {
		t.Fatal("一般成員不能改已有的留言", err)
	}

	// backend.Remote 也走新端點
	rb := backend.NewRemote(bob)
	if u, err := rb.AddIssueComment("lab", iss.ID, "從 Remote", ""); err != nil || len(u.Comments) != n+3 || u.Comments[n+2].Author != "bob@example.com" {
		t.Fatalf("%v %v", u, err)
	}
}
