package api_test

import (
	"strings"
	"testing"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// 訊息頻道的 API:建立、成員權限、訊息與 @、未讀與已讀、綁 Issue、角色憑證只能碰自己的頻道
func TestChannelAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveUser(m, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := boss.SaveMember("o1", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	alice, bob := e.login("evo-alice"), e.login("evo-bob")
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "designer", Name: "設計師"}} {
		g.Active = true
		if _, err := boss.SaveAgent("o1", g); err != nil {
			t.Fatal(err)
		}
	}
	issue, err := alice.CreateIssue("o1", service.IssueInput{Title: "首頁壞了"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := alice.CreateChannel("o1", service.ChannelInput{Name: "首頁", IssueID: issue.ID, Members: []string{"研發長"}})
	if err != nil || c.ID != "CH-1" || len(c.Members) != 2 {
		t.Fatal(c, err)
	}
	if again, err := alice.CreateChannel("o1", service.ChannelInput{Name: "x", IssueID: issue.ID}); err != nil || again.ID != c.ID {
		t.Fatal("同一個 Issue 回傳既有的", again, err)
	}
	if _, err := bob.CreateChannel("o1", service.ChannelInput{Name: "x", IssueID: issue.ID}); status(err) != 409 {
		t.Fatal("看不到既有的 409", err)
	}
	if _, err := alice.PostChannelMessage("o1", c.ID, service.PostInput{Text: "  "}); status(err) != 400 {
		t.Fatal(err)
	}
	m, err := alice.PostChannelMessage("o1", c.ID, service.PostInput{Text: "@研發長 看一下,@bob@example.com 不是成員"})
	if err != nil || strings.Join(m.Mentions, ",") != "agent:cto" {
		t.Fatal(m, err)
	}
	if _, err := bob.ListChannelMessages("o1", c.ID, service.MessageListInput{}); status(err) != 404 {
		t.Fatal("bob 不是成員", err)
	}
	if _, err := alice.ChangeChannelMembers("o1", c.ID, service.ChannelMembersInput{Add: []string{"bob@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if l, err := bob.ListChannels("o1", service.ChannelFilter{}); err != nil || len(l) != 1 || l[0].Unread != 1 || l[0].LastMessageAt == nil {
		t.Fatal(l, err)
	}
	if r, err := bob.MarkChannelRead("o1", c.ID, service.ReadInput{}); err != nil || r.LastRead != 1 {
		t.Fatal(r, err)
	}
	if l, _ := bob.ListChannels("o1", service.ChannelFilter{IssueID: issue.ID}); len(l) != 1 || l[0].Unread != 0 {
		t.Fatal(l)
	}
	if x, err := alice.UpdateChannel("o1", c.ID, service.ChannelPatch{Topic: sp("修好首頁")}); err != nil || x.Topic != "修好首頁" {
		t.Fatal(x, err)
	}

	// 角色憑證:只看與發自己是成員的頻道,不能建、不能改成員
	tk, err := boss.AssignTask("o1", service.TaskInput{AgentID: "cto", Title: "看首頁"})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := bob.ClaimTask("o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := client.New(e.srv.URL)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	x, err := role.ChannelInbox("o1", "me")
	if err != nil || len(x.Mentions) != 1 || x.Mentions[0].ChannelName != "首頁" || x.Channels[0].Unread != 1 {
		t.Fatal(x, err)
	}
	if rm, err := role.PostChannelMessage("o1", c.ID, service.PostInput{Text: "收到 @alice@example.com"}); err != nil || rm.Author != "agent:cto" || rm.Mentions[0] != "user:alice@example.com" {
		t.Fatal(rm, err)
	}
	if _, err := role.CreateChannel("o1", service.ChannelInput{Name: "x"}); status(err) != 403 {
		t.Fatal(err)
	}
	if _, err := role.ChangeChannelMembers("o1", c.ID, service.ChannelMembersInput{Add: []string{"designer"}}); status(err) != 403 {
		t.Fatal(err)
	}
	other, _ := alice.CreateChannel("o1", service.ChannelInput{Name: "私下"})
	if _, err := role.ListChannelMessages("o1", other.ID, service.MessageListInput{}); status(err) != 404 {
		t.Fatal("角色看不到自己不是成員的頻道", err)
	}
	if l, _ := role.ListChannels("o1", service.ChannelFilter{}); len(l) != 1 {
		t.Fatal(l)
	}
	page, err := alice.ListChannelMessages("o1", c.ID, service.MessageListInput{Since: "M-1"})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Author != "agent:cto" {
		t.Fatal(page, err)
	}
	// 伺服器管理員以角色身分(author 欄位)發訊息
	if bm, err := boss.PostChannelMessage("o1", c.ID, service.PostInput{Text: "代發", Author: "agent:cto"}); err != nil || bm.Author != "agent:cto" {
		t.Fatal(bm, err)
	}
}
