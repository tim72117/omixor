package store

import (
	"testing"

	"omixor-core/model"
)

func TestPGInvalidUTF8Sanitized(t *testing.T) {
	p := cabPG(t)
	at := mt("2026-01-02T03:04:05Z")
	bad := "壞\xff字"
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at,
		Routines: []model.AgentRoutine{{ID: "r", Title: bad, Instructions: bad, Kind: "daily", Priority: "normal", CreatedAt: at}}}))
	a, err := p.GetAgent("o", "dev")
	chk(t, err)
	if a.Routines[0].Title != "壞�字" {
		t.Fatalf("%q", a.Routines[0].Title)
	}
	task, err := p.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "t", Status: "queued", Priority: "normal", CreatedAt: at, UpdatedAt: at,
		Links: []string{bad}, Notes: []model.AgentTaskNote{{ID: "n", Author: whoID(t, p, "o", "a@x"), Text: bad, At: at}}})
	chk(t, err)
	g, err := p.GetAgentTask("o", task.ID)
	chk(t, err)
	if g.Notes[0].Text != "壞�字" || g.Links[0] != "壞�字" {
		t.Fatalf("%+v", g)
	}
	// 再更新一次(備註原樣):不報錯
	chk(t, p.UpdateAgentTask("o", *g))
}

// 標籤、頻道成員、提及寫入時去重;例行任務 id 重複拒絕
func TestPGDedup(t *testing.T) {
	m := testPG(t)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, m.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	d, err := m.CreateDoc("o", model.CabinetDoc{Title: "t", Section: "s", Tags: []string{"a", "a", "b"}, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	g, _ := m.GetDoc("o", d.ID)
	sameJSON(t, "標籤", g.Tags, []string{"a", "b"})
	g.Tags = []string{"c", "c"}
	chk(t, m.UpdateDoc("o", *g))
	g, _ = m.GetDoc("o", d.ID)
	sameJSON(t, "更新標籤", g.Tags, []string{"c"})
	ua, ab, ux := lid(t, m, "o", "user:a"), lid(t, m, "o", "agent:b"), lid(t, m, "o", "user:x")
	ch, err := m.CreateChannel("o", model.Channel{Name: "c", Members: []string{ua, ua, ab}, CreatedBy: ua, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	c2, _ := m.GetChannel("o", ch.ID)
	sameJSON(t, "成員", c2.Members, []string{ua, ab})
	c2.Members = []string{ux, ux}
	chk(t, m.UpdateChannel("o", *c2))
	c2, _ = m.GetChannel("o", ch.ID)
	sameJSON(t, "更新成員", c2.Members, []string{ux})
	msg, err := m.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Author: ua, Text: "t", Mentions: []string{ab, ab}, At: at})
	chk(t, err)
	// 注意:PG 的 CreateChannelMessage 回傳值原樣帶輸入的 Mentions(沒去重);去重發生在寫入表時,讀回才是去重後的
	sameJSON(t, "建立回傳的提及(未去重)", msg.Mentions, []string{ab, ab})
	ms, _, err := m.ListChannelMessages("o", ch.ID, model.MessageQuery{})
	chk(t, err)
	if len(ms) != 1 {
		t.Fatal(ms)
	}
	sameJSON(t, "讀回的提及", ms[0].Mentions, []string{ab})
	dup := model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at, Routines: []model.AgentRoutine{{ID: "r", Title: "a"}, {ID: "r", Title: "b"}}}
	if err := m.SaveAgent("o", dup); err == nil {
		t.Fatal("例行任務 id 重複要拒絕")
	}
}
