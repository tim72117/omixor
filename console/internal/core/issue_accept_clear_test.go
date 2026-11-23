package core

import (
	"testing"

	"omixor-core/model"
	"omixor-core/version"
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

// 遠端功能偵測:查過而且沒宣告 = 擋(友善訊息);沒查過或有宣告 = 不擋
func TestRemoteLacks(t *testing.T) {
	const srv = "https://old.example/"
	if remoteLacks(srv, version.FeatureIssueAccept) {
		t.Fatal("沒查過不擋")
	}
	remoteVerMu.Lock()
	remoteVerServer, remoteVerFeats = "https://old.example", nil
	remoteVerMu.Unlock()
	if !remoteLacks(srv, version.FeatureIssueAccept) {
		t.Fatal("舊伺服器(沒有 features)要擋")
	}
	remoteVerMu.Lock()
	remoteVerFeats = []string{version.FeatureIssueAccept}
	remoteVerMu.Unlock()
	if remoteLacks(srv, version.FeatureIssueAccept) {
		t.Fatal("有宣告不擋")
	}
	remoteVerMu.Lock()
	remoteVerServer, remoteVerFeats = "", nil
	remoteVerMu.Unlock()
}
