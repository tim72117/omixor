package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// ISS-36 C-2c:組織鏈、頻道的「誰」是 actor id;對外輸出仍是 user:<email> / agent:<handle>。環境同 chainEnv

func TestChainEdgesStoredByActorID(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	saved, err := svc.SaveChain(boss, "o1", edges("user:BOSS@x>agent:cto>目標", "agent:cto>agent:pm"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Edges[0].From != "user:boss@x" || saved.Edges[0].To != "agent:cto" || saved.Edges[1].To != "agent:pm" {
		t.Fatalf("輸出的節點寫法不變:%+v", saved.Edges)
	}
	raw, err := st.GetOrgChain("o1")
	if err != nil || len(raw.Edges) != 2 {
		t.Fatal(raw, err)
	}
	bossActor, cto, pm := storetest.User(t, st, "boss@x"), storetest.Agent(t, st, "o1", "cto"), storetest.Agent(t, st, "o1", "pm")
	if raw.Edges[0].From != bossActor.ID || raw.Edges[0].To != cto.ID || raw.Edges[1].From != cto.ID || raw.Edges[1].To != pm.ID {
		t.Fatalf("store 存的是 actor id:%+v", raw.Edges)
	}
	if got, _ := svc.GetChain(alice, "o1"); got.Edges[0].From != "user:boss@x" || got.Edges[0].Label != "目標" {
		t.Fatalf("讀出來節點寫法不變:%+v", got.Edges)
	}
	// 同一個人不同寫法(大小寫、前綴、actor id)是同一個節點:重複
	for _, dup := range []string{"boss@X", "user:Boss@x", "@boss@x", bossActor.ID} {
		_, err := svc.SaveChain(boss, "o1", edges(dup+">agent:cto", "user:boss@x>cto"))
		if code(err) != 400 || !strings.Contains(err.Error(), "重複") {
			t.Fatalf("%s 與 boss@x 是同一人,要擋重複:%v", dup, err)
		}
	}
	// 讀回再存(輸出的節點寫法當輸入)
	if _, err := svc.SaveChain(boss, "o1", saved.Edges); err != nil {
		t.Fatal("讀出再寫回", err)
	}
}

func TestChainNodeInputs(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	cto := storetest.Agent(t, st, "o1", "cto")
	// handle、角色名稱、actor id、@handle 都能當節點
	c, err := svc.SaveChain(boss, "o1", edges("@boss@x>"+cto.ID, "cto>研發長x"))
	if code(err) != 400 {
		t.Fatalf("研發長x 不存在要 400:%v %v", c, err)
	}
	c, err = svc.SaveChain(boss, "o1", edges("boss@x>agent:研發長", "cto>產品經理", "agent:pm>@designer"))
	if err != nil || len(c.Edges) != 3 || c.Edges[0].To != "agent:cto" || c.Edges[1].To != "agent:pm" || c.Edges[2].To != "agent:designer" {
		t.Fatalf("%+v %v", c, err)
	}
	// 未註冊使用者(只有 actor、沒有使用者列)不是組織成員,不能上鏈
	storetest.Legacy(t, st, "o1", "ghost@x")
	if _, err := svc.SaveChain(boss, "o1", edges("user:ghost@x>agent:cto")); code(err) != 400 || !strings.Contains(err.Error(), "不是組織成員") {
		t.Fatal("未註冊使用者不能上鏈", err)
	}
}

func TestChainSurvivesAgentRenameAndPause(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("user:alice@x>agent:cto>目標")); err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "討論", Members: []string{"cto"}})
	if err != nil {
		t.Fatal(err)
	}
	// 角色改名、暫停:邊與頻道成員是 actor id,照常可用
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "技術長", Active: false}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetChain(alice, "o1"); len(got.Edges) != 1 || got.Edges[0].To != "agent:cto" {
		t.Fatalf("改名暫停後鏈還在:%+v", got.Edges)
	}
	if _, err := svc.AssignTask(alice, "o1", service.TaskInput{AgentID: "cto", Title: "x"}); err != nil {
		t.Fatal("鏈上的邊仍有效", err)
	}
	m, err := svc.PostChannelMessage(alice, "o1", c.ID, service.PostInput{Text: "@技術長 你好,@cto 也是"})
	if err != nil || strings.Join(m.Mentions, ",") != "agent:cto" {
		t.Fatalf("用新名稱與 handle 都能 @:%+v %v", m, err)
	}
	if x, _ := svc.GetChannel(alice, "o1", c.ID, ""); strings.Join(x.Members, ",") != "user:alice@x,agent:cto" {
		t.Fatalf("成員:%v", x.Members)
	}
	_ = st
}

func TestChannelMembersDedupByActor(t *testing.T) {
	svc, st, boss, alice, bob := chainEnv(t)
	// 同一個人用兩種寫法加入:只留一列
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", Members: []string{"bob@x", "user:BOB@x", "@bob@x", "alice@x", "pm", "agent:PM", "產品經理"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Members, ",") != "user:alice@x,user:bob@x,agent:pm" {
		t.Fatalf("成員:%v", c.Members)
	}
	raw, err := st.GetChannel("o1", c.ID)
	if err != nil || len(raw.Members) != 3 {
		t.Fatalf("store 只有三列:%+v %v", raw, err)
	}
	for _, m := range raw.Members {
		if !model.ValidActorID(m) {
			t.Fatalf("store 存 actor id:%v", raw.Members)
		}
	}
	if raw.CreatedBy != storetest.User(t, st, "alice@x").ID {
		t.Fatal("建立者是 actor id", raw.CreatedBy)
	}
	// 加成員用另一種寫法:已經是成員就不重複;拿掉用不同寫法也拿得掉
	x, err := svc.ChangeChannelMembers(boss, "o1", c.ID, service.ChannelMembersInput{Add: []string{"BOB@x", "designer"}, Remove: []string{"user:Bob@X"}})
	if err != nil || strings.Join(x.Members, ",") != "user:alice@x,agent:pm,agent:designer" {
		t.Fatalf("%+v %v", x, err)
	}
	// 未註冊使用者、不是組織成員的人不能加
	storetest.Legacy(t, st, "o1", "ghost@x")
	for _, who := range []string{"ghost@x", "user:ghost@x", "stranger@x"} {
		if _, err := svc.ChangeChannelMembers(boss, "o1", c.ID, service.ChannelMembersInput{Add: []string{who}}); code(err) != 400 {
			t.Fatalf("%s 不能加進頻道:%v", who, err)
		}
	}
	_ = bob
}

func TestChannelMentionByHandleAndName(t *testing.T) {
	svc, st, _, alice, bob := chainEnv(t)
	c, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "討論", Members: []string{"pm", "cto", "bob@x"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := svc.PostChannelMessage(alice, "o1", c.ID, service.PostInput{Text: "@PM 看一下,@研發長 也是,@bob@x 呢;@designer 不是成員"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Mentions, ",") != "agent:pm,agent:cto,user:bob@x" {
		t.Fatalf("輸出的提及:%v", m.Mentions)
	}
	list, _, err := st.ListChannelMessages("o1", c.ID, model.MessageQuery{})
	if err != nil || len(list) != 1 || len(list[0].Mentions) != 3 {
		t.Fatal(list, err)
	}
	pm, cto, bobA := storetest.Agent(t, st, "o1", "pm"), storetest.Agent(t, st, "o1", "cto"), storetest.User(t, st, "bob@x")
	if list[0].Mentions[0] != pm.ID || list[0].Mentions[1] != cto.ID || list[0].Mentions[2] != bobA.ID || list[0].Author != storetest.User(t, st, "alice@x").ID {
		t.Fatalf("store 存 actor id:%+v", list[0])
	}
	// bob 的 inbox:輸出的節點寫法不變
	if x, err := svc.ChannelInbox(bob, "o1", ""); err != nil || x.Node != "user:bob@x" || len(x.Mentions) != 1 || x.Mentions[0].Author != "user:alice@x" {
		t.Fatalf("inbox:%+v %v", x, err)
	}
}

func TestChannelReadPositionByActor(t *testing.T) {
	svc, st, _, alice, bob := chainEnv(t)
	c, _ := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", Members: []string{"bob@x"}})
	for i := 0; i < 3; i++ {
		if _, err := svc.PostChannelMessage(alice, "o1", c.ID, service.PostInput{Text: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := svc.MarkChannelRead(bob, "o1", c.ID, service.ReadInput{UpTo: "M-2"})
	if err != nil || r.Member != "user:bob@x" || r.LastRead != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	bobA := storetest.User(t, st, "bob@x")
	reads, err := st.ChannelReads("o1", bobA.ID)
	if err != nil || reads[c.ID].LastRead != 2 || reads[c.ID].Member != bobA.ID {
		t.Fatalf("store 以 actor id 存已讀:%+v %v", reads, err)
	}
	// 作者發訊息時自己的已讀跟著到最新
	aliceA := storetest.User(t, st, "alice@x")
	if reads, _ := st.ChannelReads("o1", aliceA.ID); reads[c.ID].LastRead != 3 {
		t.Fatalf("作者已讀:%+v", reads)
	}
	if n, _ := st.UnreadCounts("o1", bobA.ID, []string{c.ID}); n[c.ID] != 1 {
		t.Fatal("bob 還有一則未讀", n)
	}
}

func TestRemoveMemberPrunesByActor(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("user:alice@x>agent:cto", "agent:cto>agent:pm")); err != nil {
		t.Fatal(err)
	}
	c, _ := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "x", Members: []string{"bob@x", "pm"}})
	if err := svc.RemoveMember(boss, "o1", "ALICE@x"); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.GetOrgChain("o1")
	if len(raw.Edges) != 1 {
		t.Fatalf("alice 的邊拿掉:%+v", raw.Edges)
	}
	aliceA := storetest.User(t, st, "alice@x")
	ch, _ := st.GetChannel("o1", c.ID)
	for _, m := range ch.Members {
		if m == aliceA.ID {
			t.Fatal("alice 從頻道拿掉", ch.Members)
		}
	}
	if len(ch.Members) != 2 {
		t.Fatal(ch.Members)
	}
	// 舊的訊息作者(alice 是 actor,仍在)輸出照舊
	if _, err := svc.PostChannelMessage(boss, "o1", c.ID, service.PostInput{Text: "x"}); code(err) != 403 {
		t.Fatal(err)
	}
}

// 樣板套用:建立者(套用的人)是頻道建立者、鏈上的 creator 節點,store 存的都是他的 actor id
func TestTemplateCreatorIsActor(t *testing.T) {
	svc, st, parent, _ := templateEnv(t)
	serverOn(t, svc, "familyAssistant")
	if _, err := svc.SaveSystemSettings(parent, "home", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyOrgTemplate(parent, "home", "family", service.TemplateApplyInput{}); err != nil {
		t.Fatal(err)
	}
	me := storetest.User(t, st, "parent@x")
	chs, err := st.ListChannels("home")
	if err != nil || len(chs) == 0 {
		t.Fatal(chs, err)
	}
	for _, c := range chs {
		if c.CreatedBy != me.ID || !c.HasMember(me.ID) {
			t.Fatalf("建立者與成員是套用的人的 actor id:%+v", c)
		}
	}
	raw, err := st.GetOrgChain("home")
	if err != nil || len(raw.Edges) == 0 {
		t.Fatal(raw, err)
	}
	hasMe := false
	for _, e := range raw.Edges {
		if !model.ValidActorID(e.From) || !model.ValidActorID(e.To) {
			t.Fatalf("邊的端點是 actor id:%+v", e)
		}
		hasMe = hasMe || e.From == me.ID
	}
	if !hasMe {
		t.Fatal("鏈上有 creator 的邊", raw.Edges)
	}
	if got, _ := svc.GetChain(parent, "home"); !strings.HasPrefix(got.Edges[0].From, "user:parent@x") && !strings.HasPrefix(got.Edges[0].From, "agent:") {
		t.Fatal("輸出節點寫法", got.Edges)
	}
}
