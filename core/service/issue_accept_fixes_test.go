package service_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 接受 / 放回的重試用完仍版本衝突要回 409 並帶 current、currentVersion;
// issue_update 的 assignee="" 只有目前負責人是人類接受者才查呼叫者身分與放回權限。

// alwaysConflictStore 包一層:UpdateIssue 一律回版本衝突(模擬別的實例一直搶先改),其他方法沿用內層
type alwaysConflictStore struct {
	store.Store
	calls atomic.Int32
}

func (c *alwaysConflictStore) UpdateIssue(org string, i model.Issue) error {
	c.calls.Add(1)
	return &store.VersionConflictError{Current: 77}
}

func checkConflict409(t *testing.T, what string, err error, wantID string) {
	t.Helper()
	wantCode(t, what, err, 409)
	se, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("%s:要是 *service.Error(和 UpdateIssue 的版本衝突同一種):%T %v", what, err, err)
	}
	if se.Data["currentVersion"] != int64(77) {
		t.Fatalf("%s:Data.currentVersion 要是衝突回報的版本:%+v", what, se.Data)
	}
	cur, _ := se.Data["current"].(*model.Issue)
	if cur == nil || cur.ID != wantID {
		t.Fatalf("%s:Data.current 要帶目前的 Issue:%+v", what, se.Data)
	}
}

func TestAcceptRetryExhaustedConflict(t *testing.T) {
	e := newREnv(t)
	wrap := &alwaysConflictStore{Store: e.svc.Store}
	e.svc.Store = wrap
	_, err := e.svc.AcceptIssue(e.alice, "o1", e.appIssue.Key)
	checkConflict409(t, "接受重試用完", err, e.appIssue.ID)
	if wrap.calls.Load() < 2 {
		t.Fatalf("版本衝突要重讀重試,UpdateIssue 只被呼叫 %d 次", wrap.calls.Load())
	}
	// 沒改到:仍沒有負責人
	if cur, _ := e.svc.GetIssue(e.boss, "o1", e.appIssue.Key); cur == nil || cur.AssigneeActor != nil {
		t.Fatalf("衝突不能改到負責人:%+v", cur)
	}
}

func TestReleaseRetryExhaustedConflict(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	if _, err := e.svc.AcceptIssue(e.alice, "o1", key); err != nil { // 先用真的 store 接受,放回才會走到寫入
		t.Fatal(err)
	}
	wrap := &alwaysConflictStore{Store: e.svc.Store}
	e.svc.Store = wrap
	_, err := e.svc.ReleaseIssue(e.alice, "o1", key)
	checkConflict409(t, "放回重試用完", err, e.appIssue.ID)
	if wrap.calls.Load() < 2 {
		t.Fatalf("版本衝突要重讀重試,UpdateIssue 只被呼叫 %d 次", wrap.calls.Load())
	}
	if cur, _ := e.svc.GetIssue(e.boss, "o1", key); cur == nil || cur.AssigneeActor == nil {
		t.Fatalf("衝突不能清掉負責人:%+v", cur)
	}
}

// 負責人是角色(非人類接受者):任何組織成員用 assignee="" 清掉都成功,不查放回權限;
// 角色憑證不能清掉指派(ISS-7),403 帶機器碼、負責人不變
func TestUpdateClearRoleAssigneeHumansOnly(t *testing.T) {
	e := newREnv(t)
	empty := ""
	assigned := func(who string) *model.Issue {
		t.Helper()
		i, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "派給角色 " + who, Project: sp(e.app.ID), Assignee: "cto"})
		if err != nil || i.Assignee != "cto" {
			t.Fatalf("前置:要指派給 cto:%+v %v", i, err)
		}
		return i
	}
	for who, p := range map[string]*model.Principal{"alice": e.alice, "bob": e.bob} {
		i := assigned(who)
		got, err := e.svc.UpdateIssue(p, "o1", i.Key, service.IssuePatch{Assignee: &empty})
		if err != nil {
			t.Fatalf("%s 清掉角色指派要成功(不是 403):%v", who, err)
		}
		if got.Assignee != "" || got.AssigneeActor != nil {
			t.Fatalf("%s 清掉後負責人要清空:%+v", who, got)
		}
		cur, err := e.svc.GetIssue(e.boss, "o1", i.Key)
		if err != nil || cur.Assignee != "" || cur.AssigneeActor != nil {
			t.Fatalf("%s 清掉後讀回負責人要清空:%+v %v", who, cur, err)
		}
		if commentsWith(cur, "已放回") != 0 { // 角色指派不是接受,不留放回留言
			t.Fatalf("清掉角色指派不該留放回留言:%+v", cur.Comments)
		}
	}
	i := assigned("pm")
	_, err := e.svc.UpdateIssue(e.pm, "o1", i.Key, service.IssuePatch{Assignee: &empty})
	wantCode(t, "角色憑證清掉指派", err, 403)
	var se *service.Error
	if !errors.As(err, &se) || se.Data["code"] != service.AgentIssueForbiddenCode {
		t.Fatalf("要帶機器碼:%v", err)
	}
	if cur, _ := e.svc.GetIssue(e.boss, "o1", i.Key); cur == nil || cur.Assignee != "cto" {
		t.Fatalf("403 之後負責人要還在:%+v", cur)
	}
}

// Issue 沒有負責人時送 assignee="":成功、不報錯、不留放回留言(任何成員)
func TestUpdateClearAssigneeWhenUnassigned(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	empty := ""
	for who, p := range map[string]*model.Principal{"alice": e.alice, "bob": e.bob, "pm": e.pm} {
		got, err := e.svc.UpdateIssue(p, "o1", key, service.IssuePatch{Assignee: &empty})
		if err != nil {
			t.Fatalf("%s:沒有負責人時 assignee=\"\" 要成功:%v", who, err)
		}
		if got.Assignee != "" || got.AssigneeActor != nil {
			t.Fatalf("%s:仍應沒有負責人:%+v", who, got)
		}
	}
	if cur, _ := e.svc.GetIssue(e.boss, "o1", key); cur == nil || commentsWith(cur, "已放回") != 0 {
		t.Fatalf("沒有負責人時不該留放回留言:%+v", cur)
	}
}

// 人類接受者:角色憑證(不是本人、不是組織管理員)清掉 403;負責人仍在
func TestUpdateClearHumanAcceptorByAgentDenied(t *testing.T) {
	e := newREnv(t)
	key := e.appIssue.Key
	if _, err := e.svc.AcceptIssue(e.alice, "o1", key); err != nil {
		t.Fatal(err)
	}
	empty := ""
	_, err := e.svc.UpdateIssue(e.pm, "o1", key, service.IssuePatch{Assignee: &empty})
	wantCode(t, "角色憑證清掉人類接受者", err, 403)
	if cur, _ := e.svc.GetIssue(e.boss, "o1", key); cur == nil || cur.AssigneeActor == nil {
		t.Fatalf("403 之後負責人要還在:%+v", cur)
	}
}
