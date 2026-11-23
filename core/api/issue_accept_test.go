package api_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// POST /api/orgs/{org}/issues/{id}/accept、/release:接受者是呼叫者;被別人接受 409;放回的授權 403;
// 看板與篩選把已接受的算進行中;經 Remote client 走同一條路
func TestIssueAcceptEndpoint(t *testing.T) {
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
	alice, bob := e.login("evo-alice"), e.login("evo-bob")
	iss, err := alice.CreateIssue("lab", service.IssueInput{Title: "待分析"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := alice.AcceptIssue("lab", strings.ToLower(iss.Key))
	if err != nil || got.Assignee != "" || got.AssigneeActor == nil || got.AssigneeActor.Handle != "alice@example.com" || got.Status != "open" {
		t.Fatalf("接受:%+v %v", got, err)
	}
	if got.Key != iss.Key || len(got.Comments) != 1 {
		t.Fatalf("要留一則系統留言:%+v", got)
	}
	if _, err := bob.AcceptIssue("lab", iss.Key); status(err) != 409 {
		t.Fatalf("別人已接受要 409:%v", err)
	}
	if _, err := bob.ReleaseIssue("lab", iss.Key); status(err) != 403 {
		t.Fatalf("別人不能放回要 403:%v", err)
	}
	if _, err := alice.AcceptIssue("lab", "ISS-99"); status(err) != 404 {
		t.Fatalf("不存在要 404:%v", err)
	}
	b, err := bob.IssueBoard("lab", model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	doing := 0
	for _, c := range b.Columns {
		if c.Key == model.BoardDoing {
			doing = len(c.Issues)
		}
	}
	if doing != 1 {
		t.Fatalf("看板進行中要有 1 張:%d", doing)
	}
	r, err := boss.ReleaseIssue("lab", iss.Key)
	if err != nil || r.AssigneeActor != nil || r.Assignee != "" {
		t.Fatalf("管理員放回:%+v %v", r, err)
	}
	if _, err := bob.AcceptIssue("lab", iss.Key); err != nil {
		t.Fatalf("放回後別人可以接受:%v", err)
	}
}
