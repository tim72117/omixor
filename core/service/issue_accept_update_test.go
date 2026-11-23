package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// issue_update 的 assignee="" 對人類接受者 = 放回(同 ReleaseIssue 的權限與語意);patch 沒給 assignee 欄位不算清除。
// assignee 篩選要找得到被人接受的 Issue。

func TestUpdateClearAssigneeIsRelease(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	if _, err := e.svc.AcceptIssue(e.alice, "o1", key); err != nil {
		t.Fatal(err)
	}
	// patch 沒給 assignee:不動負責人
	title := "改標題"
	i, err := e.svc.UpdateIssue(e.bob, "o1", key, service.IssuePatch{Title: &title})
	if err != nil || i.AssigneeActor == nil || i.AssigneeActor.Handle != "alice@x" {
		t.Fatalf("沒給 assignee 不該清掉負責人:%+v %v", i, err)
	}
	// 別人明確給空字串:403,訊息指向 issue_accept release=true,負責人沒變
	empty := ""
	_, err = e.svc.UpdateIssue(e.bob, "o1", key, service.IssuePatch{Assignee: &empty})
	wantCode(t, "別人取消人類接受者", err, 403)
	if err == nil || !strings.Contains(err.Error(), "release=true") {
		t.Fatalf("訊息要指向 issue_accept 的 release=true:%v", err)
	}
	if cur, _ := e.svc.GetIssue(e.boss, "o1", key); cur == nil || cur.AssigneeActor == nil {
		t.Fatalf("403 之後負責人要還在:%+v", cur)
	}
	// 接受者本人:清掉,留放回留言(同 ReleaseIssue)
	got, err := e.svc.UpdateIssue(e.alice, "o1", key, service.IssuePatch{Assignee: &empty})
	if err != nil || got.Assignee != "" || got.AssigneeActor != nil {
		t.Fatalf("接受者本人取消:%+v %v", got, err)
	}
	if cur, _ := e.svc.GetIssue(e.boss, "o1", key); cur == nil || commentsWith(cur, "已放回") != 1 {
		t.Fatalf("要留放回留言:%+v", cur)
	}
	// 沒人接受:冪等(不報錯、不再留言)
	if _, err := e.svc.UpdateIssue(e.bob, "o1", key, service.IssuePatch{Assignee: &empty}); err != nil {
		t.Fatalf("沒人接受時取消要冪等:%v", err)
	}
	if cur, _ := e.svc.GetIssue(e.boss, "o1", key); commentsWith(cur, "已放回") != 1 {
		t.Fatalf("冪等不該再留言:%+v", cur.Comments)
	}
	// 組織管理員可以清掉別人接受的
	if _, err := e.svc.AcceptIssue(e.bob, "o1", key); err != nil {
		t.Fatal(err)
	}
	if got, err = e.svc.UpdateIssue(e.boss, "o1", key, service.IssuePatch{Assignee: &empty}); err != nil || got.AssigneeActor != nil {
		t.Fatalf("管理員取消:%+v %v", got, err)
	}
}

// 角色指派的舊行為不變:一般成員照樣可以用 assignee="" 取消指派給角色
func TestUpdateClearRoleAssigneeUnchanged(t *testing.T) {
	e := newREnv(t)
	i, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "派給角色", Project: sp(e.app.ID), Assignee: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	got, err := e.svc.UpdateIssue(e.alice, "o1", i.Key, service.IssuePatch{Assignee: &empty})
	if err != nil || got.Assignee != "" || got.AssigneeActor != nil {
		t.Fatalf("取消角色指派:%+v %v", got, err)
	}
}

func TestQueryAssigneeFindsHumanAcceptor(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	role, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "派給角色", Project: sp(e.app.ID), Assignee: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	acc, err := e.svc.AcceptIssue(e.alice, "o1", key)
	if err != nil || acc.AssigneeActor == nil {
		t.Fatal(err, acc)
	}
	has := func(a, k string) bool {
		t.Helper()
		l, err := e.svc.QueryIssues(e.boss, "o1", model.IssueQuery{Assignee: a})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range l {
			if x.Key == k {
				return true
			}
		}
		return false
	}
	for _, a := range []string{"alice@x", "ALICE@X ", acc.AssigneeActor.ID} {
		if !has(a, key) {
			t.Fatalf("assignee=%q 要找得到被 alice 接受的 %s", a, key)
		}
	}
	if has(model.IssueFilterNone, key) {
		t.Fatal("(none) 不該找到已被接受的")
	}
	if has("alice@x", role.Key) || !has("cto", role.Key) || has("cto", key) {
		t.Fatal("角色指派的篩選行為不變")
	}
}
