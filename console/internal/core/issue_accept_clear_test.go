package core

import (
	"testing"

	"omixor-core/model"
)

// 取消人類接受者:assignee 字串本來就是空的,只有 assigneeActor;清掉 assigneeActor 要送 assignee="" 給核心(核心走放回的權限)
func TestIssuePatchForClearsHumanAcceptor(t *testing.T) {
	human := &model.ActorView{ID: "usr_1", Kind: model.ActorKindUser, Handle: "amy@x", Name: "Amy"}
	before := model.Issue{ID: "i1", Title: "t", AssigneeActor: human}
	cleared := before
	cleared.AssigneeActor = nil
	p, changed := issuePatchFor(cleared, before, "tim")
	if !changed || p.Assignee == nil || *p.Assignee != "" {
		t.Fatalf("要送 assignee=\"\":%+v changed=%v", p, changed)
	}
	// 沒動負責人(只改標題):不送 assignee
	renamed := before
	renamed.Title = "新標題"
	if p, changed := issuePatchFor(renamed, before, "tim"); !changed || p.Assignee != nil {
		t.Fatalf("沒動負責人不該送 assignee:%+v", p)
	}
}
