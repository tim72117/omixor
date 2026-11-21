package store

import (
	"context"
	"testing"

	"omixor-core/model"
)

func splitPG(t *testing.T) *PG {
	t.Helper()
	p := testPG(t).(*PG)
	for _, org := range []string{"o", "keep"} {
		if err := p.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// lid 舊字串 → actor id(寬鬆解析器;同一個字串永遠同一個 actor)
func lid(t *testing.T, p Store, org, raw string) string {
	t.Helper()
	id, err := p.ResolveLegacy(org, raw)
	chk(t, err)
	return id
}

// lids 一批舊字串 → actor id
func lids(t *testing.T, p Store, org string, raws ...string) []string {
	t.Helper()
	out := make([]string, len(raws))
	for i, r := range raws {
		out[i] = lid(t, p, org, r)
	}
	return out
}

func mkChannel(t *testing.T, p *PG, org string, members []string) model.Channel {
	t.Helper()
	at := mt("2026-01-02T03:04:05Z")
	ch, err := p.CreateChannel(org, model.Channel{Name: "c", Members: members, CreatedBy: lid(t, p, org, "user:a"), CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func mkMessage(t *testing.T, p *PG, org, ch string, mentions []string) model.ChannelMessage {
	t.Helper()
	m, err := p.CreateChannelMessage(org, model.ChannelMessage{ChannelID: ch, Author: lid(t, p, org, "user:a"), Text: "t", Mentions: mentions, At: mt("2026-01-02T03:04:05Z")})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 新表有資料列、順序保持、舊欄位不再寫、split_at 已標記
func TestPGSplitChannelsRoundTripNoLegacyWrite(t *testing.T) {
	p := splitPG(t)
	z, b, a := lid(t, p, "o", "user:z"), lid(t, p, "o", "agent:b"), lid(t, p, "o", "user:a")
	ch := mkChannel(t, p, "o", []string{z, b, a, b}) // 重複的只留第一個
	empty := mkChannel(t, p, "o", nil)
	m := mkMessage(t, p, "o", ch.ID, []string{z, b, z})
	m0 := mkMessage(t, p, "o", ch.ID, nil)
	c := lid(t, p, "o", "user:c")
	chk(t, p.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{{From: b, To: a, Label: "x"}, {From: a, To: c}}}))

	got, err := p.GetChannel("o", ch.ID)
	chk(t, err)
	sameJSON(t, "成員", got.Members, []string{z, b, a})
	e2, err := p.GetChannel("o", empty.ID)
	chk(t, err)
	sameJSON(t, "空成員", e2.Members, []string{})
	list, err := p.ListChannels("o")
	chk(t, err)
	if len(list) != 2 {
		t.Fatal(list)
	}
	sameJSON(t, "列表成員", list[0].Members, []string{z, b, a})
	sameJSON(t, "列表空成員", list[1].Members, []string{})
	msgs, _, err := p.ListChannelMessages("o", ch.ID, model.MessageQuery{})
	chk(t, err)
	if len(msgs) != 2 || msgs[0].ID != m.ID || msgs[1].ID != m0.ID {
		t.Fatalf("%+v", msgs)
	}
	sameJSON(t, "提及", msgs[0].Mentions, []string{z, b})
	sameJSON(t, "沒提及", msgs[1].Mentions, []string{})
	last, err := p.LastChannelMessages("o")
	chk(t, err)
	sameJSON(t, "最後一則", last[ch.ID].Mentions, []string{})
	chain, err := p.GetOrgChain("o")
	chk(t, err)
	sameJSON(t, "邊", chain.Edges, []model.ChainEdge{{From: b, To: a, Label: "x"}, {From: a, To: c}})
	// 整份取代
	q, r := lid(t, p, "o", "user:q"), lid(t, p, "o", "user:r")
	chk(t, p.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{{From: q, To: r}}}))
	chain, err = p.GetOrgChain("o")
	chk(t, err)
	sameJSON(t, "取代後的邊", chain.Edges, []model.ChainEdge{{From: q, To: r}})
	chk(t, p.SaveOrgChain("o", model.OrgChain{}))
	chain, err = p.GetOrgChain("o")
	chk(t, err)
	sameJSON(t, "空的邊", chain.Edges, []model.ChainEdge{})
	// 更新成員
	got.Members = []string{b}
	got.UpdatedAt = mt("2026-02-02T03:04:05Z")
	chk(t, p.UpdateChannel("o", *got))
	got, err = p.GetChannel("o", ch.ID)
	chk(t, err)
	sameJSON(t, "更新後成員", got.Members, []string{b})
	if err := p.UpdateChannel("o", model.Channel{ID: "CH-99"}); err != ErrNotFound {
		t.Fatal("不存在要 ErrNotFound", err)
	}
	// UnreadMentions 用新表
	um, err := p.UnreadMentions("o", b, []string{ch.ID}, 10)
	chk(t, err)
	if len(um) != 1 || um[0].ID != m.ID {
		t.Fatalf("%+v", um)
	}
	sameJSON(t, "未讀提及", um[0].Mentions, []string{z, b})

	if n := count(t, p, `select count(*) from omixor_channel_members where org = 'o'`); n != 1 {
		t.Fatal("成員列數", n)
	}
	if n := count(t, p, `select count(*) from omixor_channel_mentions where org = 'o'`); n != 2 {
		t.Fatal("提及列數", n)
	}
	// 舊欄位維持預設 '[]'、split_at 都標記了
	if n := count(t, p, `select count(*) from omixor_channels where members <> '[]'::jsonb or split_at is null`); n != 0 {
		t.Fatal("頻道舊欄位被寫或沒標記", n)
	}
	if n := count(t, p, `select count(*) from omixor_channel_messages where mentions <> '[]'::jsonb or split_at is null`); n != 0 {
		t.Fatal("訊息舊欄位被寫或沒標記", n)
	}
	if n := count(t, p, `select count(*) from omixor_org_chains where edges <> '[]'::jsonb or split_at is null`); n != 0 {
		t.Fatal("組織鏈舊欄位被寫或沒標記", n)
	}
}

// 啟動遷移:舊 jsonb 展開進新表;壞資料跳過(split_at 保持 NULL);重跑冪等、不覆蓋新表
func TestPGSplitChannelsMigration(t *testing.T) {
	p := splitPG(t)
	for _, q := range []string{
		`insert into omixor_channels (org, id, number, name, members, created_at, updated_at) values
		  ('o','CH-1',1,'ok','["user:a","agent:b"]',now(),now()),
		  ('o','CH-2',2,'notstr','["user:a",5]',now(),now()),
		  ('o','CH-3',3,'dup','["user:a","user:a"]',now(),now()),
		  ('o','CH-4',4,'obj','{"x":1}',now(),now()),
		  ('o','CH-5',5,'empty','[]',now(),now())`,
		`insert into omixor_channel_messages (org, channel_id, id, number, author, text, mentions, at) values
		  ('o','CH-1','M-1',1,'sys_omixor','t','["agent:b","user:a"]',now()),
		  ('o','CH-1','M-2',2,'sys_omixor','t','[]',now()),
		  ('o','CH-1','M-3',3,'sys_omixor','t','[1]',now()),
		  ('o','CH-1','M-4',4,'sys_omixor','t','["x","x"]',now())`,
		`insert into omixor_org_chains (org, edges) values
		  ('o','[{"from":"a","to":"b","label":"l"},{"from":"b","to":"c","label":""}]'),
		  ('keep','[{"from":1}]')`,
	} {
		pgExec(t, p, q)
	}
	chk(t, p.migrateChannelSplit())

	ch, err := p.GetChannel("o", "CH-1")
	chk(t, err)
	ua, ab := lid(t, p, "o", "user:a"), lid(t, p, "o", "agent:b")
	sameJSON(t, "成員(舊字串經過渡橋換成 actor id)", ch.Members, []string{ua, ab})
	// 重複(CH-3)是無損的:去重後展開;元素不是字串(CH-2)、不是陣列(CH-4)展開不了:split_at 保持 NULL,讀取退回舊 jsonb(不是空白)
	c3, err := p.GetChannel("o", "CH-3")
	chk(t, err)
	sameJSON(t, "重複去重", c3.Members, []string{ua})
	for id, want := range map[string][]string{"CH-2": {ua}, "CH-4": {}} {
		c2, err := p.GetChannel("o", id)
		chk(t, err)
		sameJSON(t, id+" 退回舊 jsonb", c2.Members, want)
		if n := count(t, p, `select count(*) from omixor_channels where id = $1 and split_at is null`, id); n != 1 {
			t.Fatal(id, "壞資料 split_at 要保持 NULL")
		}
		if n := count(t, p, `select count(*) from omixor_channel_members where channel_id = $1`, id); n != 0 {
			t.Fatal(id, "壞資料不能寫進新表")
		}
	}
	if n := count(t, p, `select count(*) from omixor_channels where id in ('CH-1','CH-3','CH-5') and split_at is not null`); n != 3 {
		t.Fatal("合格的要標記")
	}
	msgs, _, err := p.ListChannelMessages("o", "CH-1", model.MessageQuery{})
	chk(t, err)
	if len(msgs) != 4 {
		t.Fatal(msgs)
	}
	sameJSON(t, "M-1 提及", msgs[0].Mentions, []string{ab, ua})
	sameJSON(t, "M-2 提及", msgs[1].Mentions, []string{})
	sameJSON(t, "M-3(壞資料)退回舊 jsonb,非字串丟掉", msgs[2].Mentions, []string{})
	sameJSON(t, "M-4 重複去重", msgs[3].Mentions, []string{lid(t, p, "o", "x")})
	if n := count(t, p, `select count(*) from omixor_channel_messages where split_at is null and id in ('M-3','M-4')`); n != 1 {
		t.Fatal("只有壞訊息 M-3 保持 NULL")
	}
	chain, err := p.GetOrgChain("o")
	chk(t, err)
	// 舊的純名稱 a 對到同名的(未註冊)使用者 user:a(成員 user:a 展開時建的);b、c 沒有同名使用者,維持原值
	sameJSON(t, "邊", chain.Edges, []model.ChainEdge{{From: lid(t, p, "o", "a"), To: lid(t, p, "o", "b"), Label: "l"}, {From: lid(t, p, "o", "b"), To: lid(t, p, "o", "c")}})
	if n := count(t, p, `select count(*) from omixor_org_chain_edges where org = 'keep'`); n != 0 {
		t.Fatal("壞的組織鏈不展開")
	}
	if n := count(t, p, `select count(*) from omixor_org_chains where org = 'keep' and split_at is null`); n != 1 {
		t.Fatal("壞的組織鏈 split_at 要保持 NULL")
	}

	// 冪等、不覆蓋:新表被改過(新版寫的),舊 jsonb 不同,重跑不能蓋回去
	pgExec(t, p, `insert into omixor_channel_mentions (org, channel_id, message_id, member, pos) values ('o','CH-1','M-2','sys_omixor',0)`)
	pgExec(t, p, `update omixor_channel_messages set mentions = '["user:zzz"]' where id = 'M-2'`)
	pgExec(t, p, `delete from omixor_channel_members where org = 'o' and channel_id = 'CH-1' and member = (select id from omixor_actors where kind = 'user' and handle = 'a')`)
	pgExec(t, p, `delete from omixor_org_chain_edges where org = 'o' and pos = 1`)
	before := count(t, p, `select count(*) from omixor_channel_members`) + count(t, p, `select count(*) from omixor_channel_mentions`) + count(t, p, `select count(*) from omixor_org_chain_edges`)
	chk(t, p.migrateChannelSplit())
	chk(t, p.migrateChannelSplit())
	after := count(t, p, `select count(*) from omixor_channel_members`) + count(t, p, `select count(*) from omixor_channel_mentions`) + count(t, p, `select count(*) from omixor_org_chain_edges`)
	if before != after {
		t.Fatalf("重跑不該改新表:%d → %d", before, after)
	}
	ch, err = p.GetChannel("o", "CH-1")
	chk(t, err)
	sameJSON(t, "重跑後成員", ch.Members, []string{ab})
	// 修好壞資料後重跑才展開
	pgExec(t, p, `update omixor_channels set members = '["user:z"]' where id = 'CH-2'`)
	chk(t, p.migrateChannelSplit())
	ch, err = p.GetChannel("o", "CH-2")
	chk(t, err)
	sameJSON(t, "修好後成員", ch.Members, []string{lid(t, p, "o", "user:z")})
}

// 刪頻道、刪組織時子表跟著清掉
func TestPGSplitChannelsCascade(t *testing.T) {
	p := splitPG(t)
	ua, ub := lid(t, p, "o", "user:a"), lid(t, p, "o", "user:b")
	ch := mkChannel(t, p, "o", []string{ua})
	mkMessage(t, p, "o", ch.ID, []string{ua})
	k := mkChannel(t, p, "keep", []string{ua})
	mkMessage(t, p, "keep", k.ID, []string{ua})
	chk(t, p.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{{From: ua, To: ub}}}))
	chk(t, p.SaveOrgChain("keep", model.OrgChain{Edges: []model.ChainEdge{{From: ua, To: ub}}}))

	pgExec(t, p, `delete from omixor_channels where org = 'o' and id = $1`, ch.ID)
	if a, b := count(t, p, `select count(*) from omixor_channel_members where org = 'o'`), count(t, p, `select count(*) from omixor_channel_mentions where org = 'o'`); a != 0 || b != 0 {
		t.Fatal("刪頻道要清掉成員與提及", a, b)
	}
	pgExec(t, p, `delete from omixor_org_chains where org = 'o'`)
	if n := count(t, p, `select count(*) from omixor_org_chain_edges where org = 'o'`); n != 0 {
		t.Fatal("刪組織鏈要清掉邊", n)
	}
	chk(t, p.DeleteOrg("keep"))
	for _, tb := range []string{"omixor_channel_members", "omixor_channel_mentions", "omixor_org_chain_edges"} {
		if n := count(t, p, `select count(*) from `+tb); n != 0 {
			t.Fatal("刪組織後還有", tb, n)
		}
	}
	// 外鍵:沒有父列不能插
	if _, err := p.pool.Exec(ctx0(), `insert into omixor_channel_members (org, channel_id, member, pos) values ('o','CH-404','sys_omixor',0)`); err == nil {
		t.Fatal("成員要有外鍵")
	}
	if _, err := p.pool.Exec(ctx0(), `insert into omixor_org_chain_edges (org, pos, from_actor, to_actor) values ('nope',0,'sys_omixor','sys_omixor')`); err == nil {
		t.Fatal("邊要有外鍵")
	}
}

func ctx0() context.Context { return context.Background() }
