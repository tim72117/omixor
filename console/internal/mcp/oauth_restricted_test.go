package mcp

import (
	"strings"
	"testing"

	"omixor-web/internal/core"

	"omixor-core/model"
	"omixor-core/service"
)

// OAuth 入口的專有工具照核心的受限專案規則看授權者本人:專案成員、組織管理員(不必在管理員帳號名單)看得到,其他人看不到
func TestOAuthConsoleToolsRestrictedByCoreRule(t *testing.T) {
	r := newHostRig(t)
	user := func(account string) (*model.Principal, string) {
		u, err := r.svc.Store.UserByEvoleapID(service.LDAPIDPrefix + account)
		if err != nil {
			t.Fatal(err)
		}
		p := &model.Principal{User: *u, Via: model.ViaLogin}
		ref, err := r.svc.SelfActor(p)
		if err != nil {
			t.Fatal(err)
		}
		p.Actor = ref
		return p, ref.ID
	}
	chief, _ := user("alice")
	_, bobActor := user("bob")
	project, err := r.svc.CreateProject(chief, r.system, service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := r.svc.CreateIssue(chief, r.system, service.IssueInput{Title: "機密票", Project: &project.ID})
	if err != nil {
		t.Fatal(err)
	}
	r.app.Mu.Lock()
	r.app.Log(core.Person("alice"), "flag", "建立 "+issue.Key+"「機密票」", "development")
	r.app.Mu.Unlock()

	args := map[string]any{"org": r.system, "collection": "activity", "limit": 200}
	sees := func(account string) bool {
		return strings.Contains(r.expectOK(r.token(account, "read"), "list", args), "機密票")
	}
	if sees("bob") {
		t.Error("非專案成員不該看到")
	}
	if !sees("olga") {
		t.Error("組織管理員(不在管理員名單)要看得到")
	}
	if _, err := r.svc.SetProjectMember(chief, r.system, project.ID, service.ProjectMemberInput{Actor: bobActor, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if !sees("bob") {
		t.Error("專案成員要看得到")
	}
	if err := r.svc.RemoveProjectMember(chief, r.system, project.ID, bobActor); err != nil {
		t.Fatal(err)
	}
	if sees("bob") {
		t.Error("移出專案後立即看不到")
	}
}
