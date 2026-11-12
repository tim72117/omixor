package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 訊息頻道測試沿用組織鏈的環境:組織 o1,管理員 boss、成員 alice、bob;角色 cto(研發長)、pm(產品經理)、designer(設計師)

func agentP(id string) *model.Principal {
	return &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: id, TaskID: "T-1"}}
}

func TestChannelCreateAndPermissions(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	if _, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "  "}); code(err) != 400 {
		t.Fatal("名稱空白要 400", err)
	}
	if _, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", Members: []string{"stranger@x"}}); code(err) != 400 {
		t.Fatal("不是組織成員要 400", err)
	}
	if _, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", Members: []string{"agent:nope"}}); code(err) != 400 {
		t.Fatal("角色不存在要 400", err)
	}
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "改版討論", Topic: "首頁", Members: []string{"產品經理", "agent:CTO", "PM"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "CH-1" || strings.Join(c.Members, ",") != "user:alice@x,agent:pm,agent:cto" || c.CreatedBy != "user:alice@x" {
		t.Fatalf("建立者自動是成員、成員換成節點並去重:%+v", c)
	}
	// 角色身分不能建頻道
	if _, err := svc.CreateChannel(agentP("pm"), "o1", service.ChannelInput{Name: "y"}); code(err) != 403 {
		t.Fatal("角色不能建頻道", err)
	}
	// bob 不是成員:看不到、不能發、不能改
	if l, _ := svc.ListChannels(bob, "o1", service.ChannelFilter{}); len(l) != 0 {
		t.Fatal("bob 看不到", l)
	}
	if _, err := svc.GetChannel(bob, "o1", "CH-1", ""); code(err) != 404 {
		t.Fatal("bob 看不到要 404", err)
	}
	if _, err := svc.PostChannelMessage(bob, "o1", "CH-1", service.PostInput{Text: "hi"}); code(err) != 404 {
		t.Fatal("bob 不能發", err)
	}
	// 組織管理員看得到全部、可以管成員,但不是成員不能發
	if l, _ := svc.ListChannels(boss, "o1", service.ChannelFilter{}); len(l) != 1 {
		t.Fatal("管理員看得到全部", l)
	}
	if _, err := svc.PostChannelMessage(boss, "o1", "ch-1", service.PostInput{Text: "hi"}); code(err) != 403 {
		t.Fatal("管理員不是成員不能發", err)
	}
	if _, err := svc.ChangeChannelMembers(boss, "o1", "CH-1", service.ChannelMembersInput{Add: []string{"bob@x"}}); err != nil {
		t.Fatal(err)
	}
	// bob 加進來後可以改主題、加減成員
	if x, err := svc.UpdateChannel(bob, "o1", "CH-1", service.ChannelPatch{Topic: sp("新主題")}); err != nil || x.Topic != "新主題" || x.Name != "改版討論" {
		t.Fatal(x, err)
	}
	// 角色只能看與發自己是成員的頻道,不能改成員或名稱
	pm := agentP("pm")
	if l, _ := svc.ListChannels(pm, "o1", service.ChannelFilter{}); len(l) != 1 {
		t.Fatal("pm 看得到自己是成員的頻道", l)
	}
	if l, _ := svc.ListChannels(agentP("designer"), "o1", service.ChannelFilter{}); len(l) != 0 {
		t.Fatal("designer 不是成員,看不到", l)
	}
	if _, err := svc.PostChannelMessage(agentP("designer"), "o1", "CH-1", service.PostInput{Text: "hi"}); code(err) != 404 {
		t.Fatal("designer 不能發", err)
	}
	if _, err := svc.ChangeChannelMembers(pm, "o1", "CH-1", service.ChannelMembersInput{Add: []string{"designer"}}); code(err) != 403 {
		t.Fatal("角色不能改成員", err)
	}
	if _, err := svc.UpdateChannel(pm, "o1", "CH-1", service.ChannelPatch{Name: sp("x")}); code(err) != 403 {
		t.Fatal("角色不能改名稱", err)
	}
	m, err := svc.PostChannelMessage(pm, "o1", "CH-1", service.PostInput{Text: "收到", As: "agent:cto"})
	if err != nil || m.Author != "agent:pm" {
		t.Fatal("角色身分一律署名自己(as 不算數)", m, err)
	}
	// 一般成員帶 as 不算數;伺服器管理員可以以角色身分發(角色要是成員)
	if m, err := svc.PostChannelMessage(alice, "o1", "CH-1", service.PostInput{Text: "a", As: "agent:pm"}); err != nil || m.Author != "user:alice@x" {
		t.Fatal(m, err)
	}
	root := &model.Principal{User: model.User{ID: "root", Email: "root@x", Role: "admin"}}
	if m, err := svc.PostChannelMessage(root, "o1", "CH-1", service.PostInput{Text: "b", Author: "agent:cto"}); err != nil || m.Author != "agent:cto" {
		t.Fatal(m, err)
	}
	if _, err := svc.PostChannelMessage(root, "o1", "CH-1", service.PostInput{Text: "c", As: "designer"}); code(err) != 404 {
		t.Fatal("以不是成員的角色身分不行", err)
	}
	// 訊息長度
	if _, err := svc.PostChannelMessage(alice, "o1", "CH-1", service.PostInput{Text: " \n "}); code(err) != 400 {
		t.Fatal("空白訊息 400", err)
	}
	if _, err := svc.PostChannelMessage(alice, "o1", "CH-1", service.PostInput{Text: strings.Repeat("字", model.MaxChannelMessage+1)}); code(err) != 400 {
		t.Fatal("太長 400", err)
	}
}

func TestChannelMentionsAndUnread(t *testing.T) {
	svc, _, _, alice, bob := chainEnv(t)
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "討論", Members: []string{"pm", "cto", "bob@x"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := svc.PostChannelMessage(alice, "o1", c.ID, service.PostInput{Text: "@產品經理請看,@cto 也看一下;@designer 不是成員、@bob@x. 寄信給 x@pm 不算,@alice@x 自己不算"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Mentions, ",") != "agent:pm,agent:cto,user:bob@x" {
		t.Fatalf("@ 解析:%v", m.Mentions)
	}
	if m2, _ := svc.PostChannelMessage(alice, "o1", c.ID, service.PostInput{Text: "@pm-x 與 @ctoo 不算,@user:bob@x 算"}); strings.Join(m2.Mentions, ",") != "user:bob@x" {
		t.Fatalf("邊界:%v", m2.Mentions)
	}
	// 作者自己沒有未讀;bob 有兩則
	if l, _ := svc.ListChannels(alice, "o1", service.ChannelFilter{}); l[0].Unread != 0 || l[0].LastMessageAt == nil {
		t.Fatal("作者沒有未讀", l)
	}
	if l, _ := svc.ListChannels(bob, "o1", service.ChannelFilter{}); l[0].Unread != 2 {
		t.Fatal("bob 兩則未讀", l)
	}
	// 角色的 inbox:提到 pm 的未讀訊息
	pm := agentP("pm")
	x, err := svc.ChannelInbox(pm, "o1", "me")
	if err != nil || len(x.Mentions) != 1 || x.Mentions[0].ID != "M-1" || x.Mentions[0].ChannelName != "討論" || len(x.Channels) != 1 || x.Channels[0].Unread != 2 {
		t.Fatalf("inbox:%+v %v", x, err)
	}
	if _, err := svc.ChannelInbox(pm, "o1", "agent:cto"); code(err) != 403 {
		t.Fatal("角色只能查自己", err)
	}
	// 人可以查角色的 inbox(只看自己看得到的頻道)
	if x, err := svc.ChannelInbox(alice, "o1", "agent:cto"); err != nil || len(x.Mentions) != 1 {
		t.Fatal(x, err)
	}
	// 讀訊息不會自動標已讀;標到 M-1 後剩一則未讀、提到的訊息讀過了
	page, err := svc.ListChannelMessages(pm, "o1", c.ID, service.MessageListInput{})
	if err != nil || len(page.Messages) != 2 || page.HasMore {
		t.Fatal(page, err)
	}
	if r, err := svc.MarkChannelRead(pm, "o1", c.ID, service.ReadInput{UpTo: "M-1"}); err != nil || r.LastRead != 1 || r.Member != "agent:pm" {
		t.Fatal(r, err)
	}
	if x, _ := svc.ChannelInbox(pm, "o1", ""); len(x.Mentions) != 0 || x.Channels[0].Unread != 1 {
		t.Fatalf("已讀後:%+v", x)
	}
	// 已讀不倒退;沒給 upTo = 最新
	if r, _ := svc.MarkChannelRead(pm, "o1", c.ID, service.ReadInput{}); r.LastRead != 2 {
		t.Fatal(r)
	}
	if r, _ := svc.MarkChannelRead(pm, "o1", c.ID, service.ReadInput{UpTo: "M-1"}); r.LastRead != 2 {
		t.Fatal("不倒退", r)
	}
	// 發訊息時作者的已讀跟著往前
	if _, err := svc.PostChannelMessage(bob, "o1", c.ID, service.PostInput{Text: "好"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.ListChannels(bob, "o1", service.ChannelFilter{}); l[0].Unread != 0 {
		t.Fatal("bob 發訊息後前面的都算讀過", l)
	}
	// since、limit、before
	page, _ = svc.ListChannelMessages(alice, "o1", c.ID, service.MessageListInput{Since: "M-1", Limit: 1})
	if len(page.Messages) != 1 || page.Messages[0].ID != "M-2" || !page.HasMore {
		t.Fatal(page)
	}
	page, _ = svc.ListChannelMessages(alice, "o1", c.ID, service.MessageListInput{Limit: 2})
	if len(page.Messages) != 2 || page.Messages[0].ID != "M-2" || page.Messages[1].ID != "M-3" || !page.HasMore {
		t.Fatal("沒給 since 是最新的", page)
	}
	page, _ = svc.ListChannelMessages(alice, "o1", c.ID, service.MessageListInput{Before: "M-2"})
	if len(page.Messages) != 1 || page.Messages[0].ID != "M-1" {
		t.Fatal(page)
	}
	if _, err := svc.ListChannelMessages(alice, "o1", c.ID, service.MessageListInput{Since: "昨天"}); code(err) != 400 {
		t.Fatal(err)
	}
}

func TestChannelIssueBinding(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	if _, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", IssueID: "ISS-9"}); code(err) != 404 {
		t.Fatal("Issue 要存在", err)
	}
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "首頁壞了"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "首頁壞了", IssueID: strings.ToLower(i.ID)})
	if err != nil || c.IssueID != i.ID {
		t.Fatal(c, err)
	}
	// 同一個 Issue:看得到就回傳既有的,看不到 409
	if again, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "又一個", IssueID: i.ID}); err != nil || again.ID != c.ID {
		t.Fatal("回傳既有的", again, err)
	}
	if again, err := svc.CreateChannel(boss, "o1", service.ChannelInput{Name: "又一個", IssueID: i.ID}); err != nil || again.ID != c.ID {
		t.Fatal("管理員看得到,回傳既有的", again, err)
	}
	if _, err := svc.CreateChannel(bob, "o1", service.ChannelInput{Name: "又一個", IssueID: i.ID}); code(err) != 409 || !strings.Contains(err.Error(), c.ID) {
		t.Fatal("看不到 409", err)
	}
	if l, _ := svc.ListChannels(alice, "o1", service.ChannelFilter{IssueID: i.ID}); len(l) != 1 {
		t.Fatal(l)
	}
	if l, _ := svc.ListChannels(alice, "o1", service.ChannelFilter{IssueID: "ISS-99"}); len(l) != 0 {
		t.Fatal(l)
	}
}

func TestChannelPruneOnDelete(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", Members: []string{"pm", "cto", "bob@x"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteAgent(boss, "o1", "pm"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(boss, "o1", "bob@x"); err != nil {
		t.Fatal(err)
	}
	x, _ := svc.GetChannel(alice, "o1", c.ID, "")
	if strings.Join(x.Members, ",") != "user:alice@x,agent:cto" {
		t.Fatal("刪角色、移除成員時從頻道拿掉", x.Members)
	}
}
