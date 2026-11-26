package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// Issue 接受與放回(service/issue_accept.go):接受者成為 assignee、進行中(人類接受者只有 assigneeActor)、冪等、被別人接受 409、
// 已關閉 409、放回的授權、受限專案 404 不洩漏、角色憑證、放回取消角色未領取的任務。

func boardKey(t *testing.T, e *rEnv, p *model.Principal, key string) string {
	t.Helper()
	b, err := e.svc.IssueBoard(p, "o1", model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range b.Columns {
		for _, i := range c.Issues {
			if i.Key == key {
				return c.Key
			}
		}
	}
	t.Fatalf("看板找不到 %s", key)
	return ""
}

func commentsWith(i *model.Issue, sub string) int {
	n := 0
	for _, c := range i.Comments {
		if strings.Contains(c.Text, sub) {
			n++
		}
	}
	return n
}

func TestAcceptReleaseHuman(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	if got := boardKey(t, e, e.boss, key); got != model.BoardTodo {
		t.Fatalf("接受前在 %s", got)
	}
	i, err := e.svc.AcceptIssue(e.alice, "o1", strings.ToLower(key)) // 編號不分大小寫
	if err != nil {
		t.Fatal(err)
	}
	// 人類接受者:assignee 字串(角色 handle)是空的,負責人在 assigneeActor;狀態仍是 open,留一則系統留言
	if i.Assignee != "" || i.AssigneeActor == nil || i.AssigneeActor.Kind != model.ActorKindUser || i.AssigneeActor.Handle != "alice@x" || i.Status != "open" {
		t.Fatalf("接受後:%+v", i)
	}
	if commentsWith(i, "已接受") != 1 || i.Comments[len(i.Comments)-1].Author != "alice@x" {
		t.Fatalf("要留一則接受留言:%+v", i.Comments)
	}
	if got := boardKey(t, e, e.boss, key); got != model.BoardDoing {
		t.Fatalf("接受後看板要在進行中,實際 %s", got)
	}
	// 篩「沒指派」要排除已接受的
	l, err := e.svc.QueryIssues(e.boss, "o1", model.IssueQuery{Assignee: model.IssueFilterNone})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range l {
		if x.Key == key {
			t.Fatalf("已接受的不該出現在「沒指派」:%s", key)
		}
	}
	// 重複接受自己:冪等(不改版本、不重複留言)
	again, err := e.svc.AcceptIssue(e.alice, "o1", key)
	if err != nil || again.Version != i.Version || commentsWith(again, "已接受") != 1 {
		t.Fatalf("重複接受要冪等:%+v %v", again, err)
	}
	// 被別人接受:409(訊息指出是誰);關閉前先試別人與管理員都不能搶
	_, err = e.svc.AcceptIssue(e.bob, "o1", key)
	wantCode(t, "別人已接受", err, 409)
	if err == nil || !strings.Contains(err.Error(), "alice@x") {
		t.Fatalf("訊息要指出接受者:%v", err)
	}
	_, err = e.svc.AcceptIssue(e.boss, "o1", key)
	wantCode(t, "管理員也不能搶", err, 409)
	// 放回:別人 403,接受者本人與管理員可以
	_, err = e.svc.ReleaseIssue(e.bob, "o1", key)
	wantCode(t, "別人不能放回", err, 403)
	got, err := e.svc.ReleaseIssue(e.boss, "o1", key)
	if err != nil || got.Assignee != "" || got.AssigneeActor != nil {
		t.Fatalf("管理員放回:%+v %v", got, err)
	}
	if boardKey(t, e, e.boss, key) != model.BoardTodo {
		t.Fatal("放回後要退回待處理")
	}
	if commentsWith(got, "已放回") != 1 {
		t.Fatalf("要留放回留言:%+v", got.Comments)
	}
	// 沒有人接受時放回:冪等(任何成員都不報錯、不留言)
	n, err := e.svc.ReleaseIssue(e.bob, "o1", key)
	if err != nil || commentsWith(n, "已放回") != 1 {
		t.Fatalf("沒人接受的放回要冪等:%+v %v", n, err)
	}
	// 放回後別人可以接受,接受者本人放回
	if _, err := e.svc.AcceptIssue(e.bob, "o1", key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReleaseIssue(e.bob, "o1", key); err != nil {
		t.Fatalf("接受者本人放回:%v", err)
	}
}

func TestAcceptClosedAndTransfer(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	closed := "closed"
	if _, err := e.svc.UpdateIssue(e.boss, "o1", key, service.IssuePatch{Status: &closed}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.AcceptIssue(e.alice, "o1", key)
	wantCode(t, "已關閉不能接受", err, 409)
	open := "open"
	if _, err := e.svc.UpdateIssue(e.boss, "o1", key, service.IssuePatch{Status: &open}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptIssue(e.alice, "o1", key); err != nil {
		t.Fatal(err)
	}
	// 轉交走 issue_update 的 assignee(角色):人類接受者被取代,角色照舊排任務
	i, err := e.svc.UpdateIssue(e.boss, "o1", key, service.IssuePatch{Assignee: sp("cto")})
	if err != nil || i.Assignee != "cto" || i.AssigneeActor == nil || i.AssigneeActor.Handle != "cto" {
		t.Fatalf("轉交給角色:%+v %v", i, err)
	}
	if ts, _ := e.svc.Store.ListAgentTasks("o1"); len(ts) != 1 {
		t.Fatalf("轉交給角色要排一筆任務:%d", len(ts))
	}
	// 不存在的 Issue
	_, err = e.svc.AcceptIssue(e.alice, "o1", "APP-999")
	wantCode(t, "不存在", err, 404)
}

func TestAcceptByAgent(t *testing.T) {
	e := newREnv(t)
	own, err := e.svc.CreateIssue(e.cto, "o1", service.IssueInput{Title: "cto 開的", Project: sp(e.app.ID)})
	if err != nil {
		t.Fatal(err)
	}
	key := own.Key // 角色只能接受自己的 Issue(這裡是自己建立的)
	i, err := e.svc.AcceptIssue(e.cto, "o1", key)
	if err != nil || i.Assignee != "cto" || i.AssigneeActor == nil || i.AssigneeActor.Kind != model.ActorKindAgent {
		t.Fatalf("角色接受:%+v %v", i, err)
	}
	if ts, _ := e.svc.Store.ListAgentTasks("o1"); len(ts) != 0 {
		t.Fatalf("角色接受自己不該替自己排任務:%d", len(ts))
	}
	if boardKey(t, e, e.boss, key) != model.BoardDoing {
		t.Fatal("角色接受後要在進行中")
	}
	if _, err := e.svc.AcceptIssue(e.cto, "o1", key); err != nil {
		t.Fatalf("角色重複接受要冪等:%v", err)
	}
	_, err = e.svc.AcceptIssue(e.alice, "o1", key)
	wantCode(t, "人不能搶角色接受的", err, 409)
	_, err = e.svc.AcceptIssue(e.pm, "o1", key)
	wantCode(t, "別的角色也不能搶", err, 409)
	_, err = e.svc.ReleaseIssue(e.pm, "o1", key)
	wantCode(t, "別的角色不能放回", err, 403)
	_, err = e.svc.ReleaseIssue(e.alice, "o1", key)
	wantCode(t, "一般成員不能放回角色接受的", err, 403)
	if _, err := e.svc.ReleaseIssue(e.cto, "o1", key); err != nil {
		t.Fatalf("接受的角色自己放回:%v", err)
	}
	// 外部組織的角色憑證:403
	_, err = e.svc.AcceptIssue(e.xa, "o1", key)
	wantCode(t, "外部組織的角色憑證", err, 403)
	// 管理員可以放回角色接受的
	if _, err := e.svc.AcceptIssue(e.cto, "o1", key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReleaseIssue(e.boss, "o1", key); err != nil {
		t.Fatalf("管理員放回:%v", err)
	}
}

func TestAcceptRestrictedProject(t *testing.T) {
	e := newREnv(t)
	key := e.secIssue.Key
	// 非專案成員(人、角色)和外部組織成員:404,訊息和不存在的一樣,不洩漏
	_, missing := e.svc.AcceptIssue(e.alice, "o1", "SEC-999")
	for who, p := range map[string]*model.Principal{"alice": e.alice, "pm": e.pm} {
		_, err := e.svc.AcceptIssue(p, "o1", key)
		wantCode(t, who+" 接受受限專案", err, 404)
		_, err = e.svc.ReleaseIssue(p, "o1", key)
		wantCode(t, who+" 放回受限專案", err, 404)
		if who == "alice" && strings.Replace(missing.Error(), "SEC-999", key, 1) != err.Error() {
			t.Fatalf("404 訊息要和不存在的一樣:%v / %v", err, missing)
		}
	}
	_, err := e.svc.AcceptIssue(e.eve, "o1", key)
	wantCode(t, "外部組織成員", err, 403)
	// 專案成員(人、角色)可以;舊 key 與內部 id 也行
	if _, err := e.svc.AcceptIssue(e.bob, "o1", key); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AcceptIssue(e.cto, "o1", e.secIssue.ID)
	wantCode(t, "已被 bob 接受", err, 409)
	if _, err := e.svc.ReleaseIssue(e.carol, "o1", key); service.Code(err) != 403 {
		t.Fatalf("專案管理員不是組織管理員、也不是接受者,不能放回:%v", err)
	}
	if _, err := e.svc.ReleaseIssue(e.bob, "o1", key); err != nil {
		t.Fatal(err)
	}
	cto := linkedAgent(t, e, "cto", e.secIssue.Key) // 角色要任務連到這張 Issue 才能接受
	if i, err := e.svc.AcceptIssue(cto, "o1", e.secIssue.ID); err != nil || i.Assignee != "cto" {
		t.Fatalf("角色專案成員接受:%+v %v", i, err)
	}
}

func TestReleaseCancelsQueuedRoleTask(t *testing.T) {
	e := newREnv(t)
	i, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "派給角色", Project: sp(e.app.ID), Assignee: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	if ts, _ := e.svc.Store.ListAgentTasks("o1"); len(ts) != 1 || ts[0].Status != "queued" {
		t.Fatalf("指派要排任務:%+v", ts)
	}
	// 指派給角色就是已被角色接受:別人不能接受,自己接受是冪等
	_, err = e.svc.AcceptIssue(e.alice, "o1", i.Key)
	wantCode(t, "已指派給角色", err, 409)
	if _, err := e.svc.AcceptIssue(e.cto, "o1", i.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReleaseIssue(e.boss, "o1", i.Key); err != nil {
		t.Fatal(err)
	}
	ts, _ := e.svc.Store.ListAgentTasks("o1")
	if len(ts) != 1 || ts[0].Status != "canceled" {
		t.Fatalf("放回要取消還沒領取的任務:%+v", ts)
	}
}
