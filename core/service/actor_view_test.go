package service_test

import (
	"sync/atomic"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// ISS-36 C-3:輸出同時帶相容字串與 actor 物件(ActorView),整批一次查詢

type viewCounter struct {
	store.Store
	n atomic.Int32
}

func (c *viewCounter) ActorsByIDs(ids []string) (map[string]model.ActorRecord, error) {
	c.n.Add(1)
	return c.Store.ActorsByIDs(ids)
}

func wantView(t *testing.T, what string, v *model.ActorView, kind model.ActorKind, handle, status string) {
	t.Helper()
	if v == nil {
		t.Fatalf("%s 沒有 actor 物件", what)
	}
	if v.Kind != kind || v.Handle != handle || v.Status != status || !model.ValidActorIDOf(kind, v.ID) {
		t.Fatalf("%s:%+v(要 %s %s %s)", what, *v, kind, handle, status)
	}
	if (kind == model.ActorKindUser) != (v.Email != "") {
		t.Fatalf("%s:只有使用者有 email:%+v", what, *v)
	}
}

func TestActorViewsInOutputs(t *testing.T) {
	cs := &viewCounter{Store: issueStores(t)["pg"]}
	svc, _, boss, alice, _ := issueEnv(t, cs)

	// Issue:建立者、指派、留言作者都有物件,相容字串不變
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Assignee: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	i, err = svc.AddIssueComment(boss, "o1", i.ID, service.CommentInput{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if i.CreatedBy != "alice@x" || i.Assignee != "dev" || i.Comments[0].Author != "boss@x" {
		t.Fatalf("相容字串要照舊:%+v", i)
	}
	wantView(t, "createdByActor", i.CreatedByActor, model.ActorKindUser, "alice@x", model.ActorStatusActive)
	wantView(t, "assigneeActor", i.AssigneeActor, model.ActorKindAgent, "dev", model.ActorStatusActive)
	wantView(t, "comment.authorActor", i.Comments[0].AuthorActor, model.ActorKindUser, "boss@x", model.ActorStatusActive)
	if i.DeletedByActor != nil {
		t.Fatal("沒有刪除者不該有物件")
	}

	// 列表:很多筆也只查一次 actor 表
	for k := 0; k < 4; k++ {
		if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	cs.n.Store(0)
	l, err := svc.ListIssues(alice, "o1")
	if err != nil || len(l) != 5 {
		t.Fatal(len(l), err)
	}
	if cs.n.Load() != 1 {
		t.Fatalf("ActorsByIDs 呼叫 %d 次,要 1 次", cs.n.Load())
	}
	for _, x := range l {
		wantView(t, "list createdByActor", x.CreatedByActor, model.ActorKindUser, "alice@x", model.ActorStatusActive)
	}

	// 成員清單帶 actorRef
	ms, err := svc.ListMembers(boss, "o1")
	if err != nil || len(ms) != 3 {
		t.Fatal(ms, err)
	}
	for _, m := range ms {
		wantView(t, "member.actorRef", m.ActorRef, model.ActorKindUser, m.Email, model.ActorStatusActive)
	}

	// 頻道:成員、建立者、訊息作者與提及
	ch, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "c", Members: []string{"agent:dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.MemberActors) != len(ch.Members) || len(ch.Members) != 2 {
		t.Fatalf("memberActors 要和 members 同數量同順序:%+v", ch.Channel)
	}
	for k, a := range ch.MemberActors {
		if model.ActorKind(a.Kind) == model.ActorKindAgent && ch.Members[k] != "agent:dev" {
			t.Fatalf("順序不一致:%v %+v", ch.Members, ch.MemberActors)
		}
	}
	wantView(t, "channel.createdByActor", ch.CreatedByActor, model.ActorKindUser, "alice@x", model.ActorStatusActive)
	m, err := svc.PostChannelMessage(alice, "o1", ch.ID, service.PostInput{Text: "@dev 看一下"})
	if err != nil {
		t.Fatal(err)
	}
	wantView(t, "message.authorActor", m.AuthorActor, model.ActorKindUser, "alice@x", model.ActorStatusActive)
	if len(m.MentionActors) != len(m.Mentions) || len(m.Mentions) != 1 || m.MentionActors[0].Handle != "dev" {
		t.Fatalf("mentionActors:%+v %+v", m.Mentions, m.MentionActors)
	}

	// 角色暫停 → suspended;使用者停用 → disabled
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "dev", Name: "工程師", Active: false}); err != nil {
		t.Fatal(err)
	}
	g, err := svc.GetIssue(alice, "o1", i.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantView(t, "suspended assignee", g.AssigneeActor, model.ActorKindAgent, "dev", model.ActorStatusSuspended)

	// 組織鏈
	c, err := svc.SaveChain(boss, "o1", []model.ChainEdge{{From: "user:boss@x", To: "agent:dev"}})
	if err != nil {
		t.Fatal(err)
	}
	wantView(t, "edge.fromActor", c.Edges[0].FromActor, model.ActorKindUser, "boss@x", model.ActorStatusActive)
	wantView(t, "edge.toActor", c.Edges[0].ToActor, model.ActorKindAgent, "dev", model.ActorStatusSuspended)

	// 事件:指揮官、宣告者、時間軸
	inc, err := svc.DeclareIncident(boss, "o1", service.IncidentInput{Title: "inc", Sev: 2, Impact: "x"})
	if err != nil {
		t.Fatal(err)
	}
	wantView(t, "incident.commanderActor", inc.CommanderActor, model.ActorKindUser, "boss@x", model.ActorStatusActive)
	wantView(t, "incident.createdByActor", inc.CreatedByActor, model.ActorKindUser, "boss@x", model.ActorStatusActive)
	if len(inc.Timeline) == 0 {
		t.Fatal("時間軸是空的")
	}
	for _, e := range inc.Timeline {
		if e.ActorRef == nil || e.Actor.ID == "" && e.Actor.Kind != "system" {
			t.Fatalf("時間軸要有 actorRef 與相容 actor:%+v", e)
		}
	}
}
