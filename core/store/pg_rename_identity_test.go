package store

import (
	"testing"

	"omixor-core/model"
)

// RenameIdentity:舊身分(ops-web)在任務、頻道、訊息、已讀、Issue、組織鏈裡都換成新身分;其他值不動;重跑不再改
func TestPGRenameIdentity(t *testing.T) {
	s := testPG(t, testPGURL(t)).(*PG)
	const org = "o"
	at := mt("2026-01-02T03:04:05Z")
	chk(t, s.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: at}))

	task, err := s.CreateAgentTask(org, model.AgentTask{AgentID: "dev", Title: "t", Status: "inProgress", Priority: "normal",
		ClaimedBy: "ops-web", CreatedBy: "ops-web", CreatedAt: at, UpdatedAt: at, Links: []string{},
		Notes: []model.AgentTaskNote{{At: at, Author: "ops-web", ID: "n1", Text: "ops-web 留的(內文不改)"}, {At: at, Author: "a@x", ID: "n2", Text: "x"}}})
	chk(t, err)
	other, err := s.CreateAgentTask(org, model.AgentTask{AgentID: "dev", Title: "u", Status: "queued", Priority: "normal",
		ClaimedBy: "ops-web-2", CreatedBy: "a@x", CreatedAt: at, UpdatedAt: at, Links: []string{}, Notes: []model.AgentTaskNote{}})
	chk(t, err)
	ch, err := s.CreateChannel(org, model.Channel{Name: "c", Members: []string{"user:ops-web", "agent:dev", "user:a@x"}, CreatedBy: "user:ops-web", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	msg, err := s.CreateChannelMessage(org, model.ChannelMessage{ChannelID: ch.ID, Author: "user:ops-web", Text: "hi", Mentions: []string{"agent:dev", "user:ops-web"}, At: at})
	chk(t, err)
	chk(t, s.SaveChannelRead(org, model.ChannelRead{ChannelID: ch.ID, Member: "user:ops-web", LastRead: 1, At: at}))
	chk(t, s.SaveOrgChain(org, model.OrgChain{Edges: []model.ChainEdge{{From: "user:ops-web", To: "agent:dev"}}}))
	iss := newTestIssueBy(t, s, org, "ops-web")

	n, err := s.RenameIdentity("ops-web", "omixor-console")
	chk(t, err)
	if n == 0 {
		t.Fatal("應該有改到東西")
	}

	got, err := s.GetAgentTask(org, task.ID)
	chk(t, err)
	if got.ClaimedBy != "omixor-console" || got.CreatedBy != "omixor-console" || got.Notes[0].Author != "omixor-console" ||
		got.Notes[0].Text != "ops-web 留的(內文不改)" || got.Notes[1].Author != "a@x" {
		t.Fatalf("任務沒改對:%+v", got)
	}
	o2, err := s.GetAgentTask(org, other.ID)
	chk(t, err)
	if o2.ClaimedBy != "ops-web-2" || o2.CreatedBy != "a@x" {
		t.Fatalf("不相干的值不能動:%+v", o2)
	}
	c2, err := s.GetChannel(org, ch.ID)
	chk(t, err)
	sameJSON(t, "頻道成員", c2.Members, []string{"user:omixor-console", "agent:dev", "user:a@x"})
	if c2.CreatedBy != "user:omixor-console" {
		t.Fatal("頻道建立者", c2.CreatedBy)
	}
	msgs, _, err := s.ListChannelMessages(org, ch.ID, model.MessageQuery{})
	chk(t, err)
	if len(msgs) != 1 || msgs[0].ID != msg.ID || msgs[0].Author != "user:omixor-console" {
		t.Fatalf("訊息作者:%+v", msgs)
	}
	sameJSON(t, "提及", msgs[0].Mentions, []string{"agent:dev", "user:omixor-console"})
	reads, err := s.ChannelReads(org, "user:omixor-console")
	chk(t, err)
	if reads[ch.ID].LastRead != 1 {
		t.Fatalf("已讀要搬到新身分:%+v", reads)
	}
	chain, err := s.GetOrgChain(org)
	chk(t, err)
	if chain.Edges[0].From != "user:omixor-console" {
		t.Fatal("組織鏈", chain.Edges)
	}
	i2, err := s.GetIssue(org, iss.ID)
	chk(t, err)
	if i2.CreatedBy != "omixor-console" {
		t.Fatal("Issue 建立者", i2.CreatedBy)
	}

	// 冪等:再跑一次不改任何東西
	n, err = s.RenameIdentity("ops-web", "omixor-console")
	chk(t, err)
	if n != 0 {
		t.Fatal("重跑不該再改", n)
	}
	if _, err := s.RenameIdentity("ops-web", "ops-web"); err == nil {
		t.Fatal("新舊相同要錯")
	}
}

func newTestIssueBy(t *testing.T, s Store, org, by string) model.Issue {
	t.Helper()
	i, err := s.CreateIssue(org, model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: by,
		CreatedAt: mt("2026-01-02T03:04:05Z"), UpdatedAt: mt("2026-01-02T03:04:05Z"), Labels: []string{}, Comments: []model.Comment{}})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
