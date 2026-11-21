package server

// omixor-local 匯入與本機使用者的 actor 測試(ISS-36 C-2):
//   - 匯入的每一個「誰」字串(mcp、omixor-console、user:<x>、email、agent:<id>、已刪角色、怪值)都轉成有效的 actor id,沒有孤兒
//   - 重複匯入冪等(不多建 actor、不重複寫)
//   - 本機使用者別名:啟動時接上 SetMigrationLocalUser,mcp / omixor-console / ops-web / 本機 handle 對到本機使用者的 actor
//   - 本機 Principal 帶 Actor(Via = local),SelfActor 可用,建立 Issue、留言、頻道可用

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// openActorLocal 打開本機守護服務並在結束時還原全域的本機使用者設定
func openActorLocal(t *testing.T, user string) (*Local, string) {
	t.Helper()
	db := testDB(t)
	l, err := OpenLocal(LocalConfig{DataDir: t.TempDir(), DatabaseURL: db, Token: testTok, User: user})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.SetMigrationLocalUser(""); _ = l.Close() })
	return l, db
}

// assertActorColumns 22 欄裡每一個值都是有效格式、存在於 omixor_actors(外鍵之外再驗一次),回傳有資料的欄位數
func assertActorColumns(t *testing.T, db string) int {
	t.Helper()
	c := context.Background()
	conn, err := pgx.Connect(c, db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(c)
	re := regexp.MustCompile(`^(usr_|agt_)[0-9a-f-]{36}$|^sys_[a-z0-9_]+$`)
	filled := 0
	for _, col := range store.ActorColumns {
		var orphan int
		if err := conn.QueryRow(c, `select count(*) from `+col.Table+` t where t.`+col.Target+` is not null
			and not exists (select 1 from omixor_actors a where a.id = t.`+col.Target+`)`).Scan(&orphan); err != nil {
			t.Fatal(col.Key(), err)
		}
		if orphan != 0 {
			t.Fatalf("%s 有 %d 筆孤兒", col.Key(), orphan)
		}
		rows, err := conn.Query(c, `select distinct `+col.Target+` from `+col.Table+` where `+col.Target+` is not null`)
		if err != nil {
			t.Fatal(col.Key(), err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			filled++
			if !re.MatchString(id) || !model.ValidActorID(id) {
				t.Fatalf("%s 的值不是有效的 actor id:%q", col.Key(), id)
			}
		}
		rows.Close()
	}
	return filled
}

func actorCount(t *testing.T, db string) int {
	t.Helper()
	c := context.Background()
	conn, err := pgx.Connect(c, db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(c)
	var n int
	if err := conn.QueryRow(c, `select count(*) from omixor_actors`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func actorHandle(t *testing.T, l *Local, id string) (kind, handle string) {
	t.Helper()
	rec, err := l.Store.ActorByID(id)
	if err != nil {
		t.Fatalf("actor %s:%v", id, err)
	}
	return string(rec.Kind), rec.Handle
}

func TestLocalImportActors(t *testing.T) {
	l, db := openActorLocal(t, "tim")
	at := model.Now()
	tab := "/x"
	weird := "!!<weird> \"值\";,"
	in := AppImport{
		Issues: &[]model.Issue{
			{ID: "ISS-1", Title: "a", CreatedBy: "mcp", CreatedAt: at, UpdatedAt: at, Comments: []model.Comment{
				{ID: "c1", Author: "omixor-console", Text: "x", At: at}, {ID: "c2", Author: "bob@example.com", Text: "y", At: at},
				{ID: "c3", Author: weird, Text: "z", At: at}}},
			{ID: "ISS-2", Title: "b", CreatedBy: "user:alice", Assignee: "dev", CreatedAt: at, UpdatedAt: at,
				Comments: []model.Comment{{ID: "c4", Author: "agent:dev", Text: "w", At: at}, {ID: "c5", Author: "agent:gone", Text: "v", At: at}}},
		},
		Agents: &service.AgentImport{Agents: []model.Agent{{ID: "dev", Name: "工程師", Active: true}},
			Tasks: []model.AgentTask{{ID: "T-1", AgentID: "dev", Title: "做事", Status: "queued", CreatedBy: "user:tim",
				Notes: []model.AgentTaskNote{{ID: "n1", Author: "agent:gone", Text: "舊角色", At: at}, {ID: "n2", Author: "routine", Text: "r", At: at}, {ID: "n3", Author: "ops-web", Text: "o", At: at}}}}},
		CabinetLayout: &[]model.CabinetSection{{ID: "docs", Label: "文件", Tabs: []model.CabinetTab{{Path: "/x", Label: "X"}}}},
		Docs:          &[]model.CabinetDoc{{ID: "DOC-1", Title: "d", Section: "docs", Tab: &tab, Author: "mcp", CreatedAt: at, UpdatedAt: at}, {ID: "DOC-2", Title: "e", Section: "docs", Tab: &tab, Author: "carol@example.com", CreatedAt: at, UpdatedAt: at}},
		Chain:         &model.OrgChain{Edges: []model.ChainEdge{{From: "user:tim", To: "agent:dev"}, {From: "user:alice", To: "agent:dev"}}},
		Channels: &AppChannels{
			Channels: []model.Channel{
				{ID: "CH-1", Name: "一般", Members: []string{"user:tim", "tim", "mcp", "agent:dev", "user:alice", "dave@example.com", "agent:gone", weird}, CreatedBy: "omixor-console", CreatedAt: at, UpdatedAt: at},
				{ID: "CH-2", Name: "空建立者", Members: []string{"user:alice"}, CreatedAt: at, UpdatedAt: at},
			},
			Messages: []model.ChannelMessage{
				{ID: "M-1", ChannelID: "CH-1", Author: "mcp", Text: "a", Mentions: []string{"agent:dev", "user:alice", "dev", "agent:gone"}, At: at},
				{ID: "M-2", ChannelID: "CH-1", Author: "user:alice", Text: "b", Mentions: []string{"user:tim", "tim", weird}, At: at},
				{ID: "M-3", ChannelID: "CH-1", Author: "ops-web", Text: "c", At: at},
				{ID: "M-4", ChannelID: "CH-1", Author: "agent:gone", Text: "d", At: at},
				{ID: "M-5", ChannelID: "CH-1", Author: weird, Text: "e", At: at},
				{ID: "M-6", ChannelID: "CH-1", Author: "erin@example.com", Text: "f", At: at},
				{ID: "M-7", ChannelID: "CH-1", Author: "", Text: "g", At: at},
			},
			Reads: []model.ChannelRead{
				{ChannelID: "CH-1", Member: "user:tim", LastRead: 2, At: at}, {ChannelID: "CH-1", Member: "tim", LastRead: 4, At: at}, // 同一個人兩種寫法
				{ChannelID: "CH-1", Member: "agent:dev", LastRead: 1, At: at}, {ChannelID: "CH-1", Member: "mcp", LastRead: 1, At: at},
				{ChannelID: "CH-1", Member: weird, LastRead: 1, At: at}, {ChannelID: "CH-1", Member: "", LastRead: 1, At: at},
			},
		},
	}
	res, err := l.Import("default", in)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"issues", "agents", "cabinet", "chain", "channels"} {
		if res[k].Status != "done" {
			t.Fatalf("%s:%+v", k, res[k])
		}
	}
	if n := assertActorColumns(t, db); n < 10 {
		t.Fatalf("有資料的欄位太少:%d", n)
	}
	// 本機別名 → 本機使用者 actor
	me := l.actor.ID
	tim, _ := l.Store.UserByEmail("tim")
	if tim.ActorID != me {
		t.Fatalf("本機使用者的 actor %s ≠ %s", me, tim.ActorID)
	}
	chs, _ := l.Store.ListChannels("default")
	byName := map[string]model.Channel{}
	for _, c := range chs {
		byName[c.Name] = c
	}
	g := byName["一般"]
	if g.CreatedBy != me {
		t.Fatalf("omixor-console 建立者要是本機使用者:%s", g.CreatedBy)
	}
	if byName["空建立者"].CreatedBy != "" {
		t.Fatalf("沒有建立者要是空:%q", byName["空建立者"].CreatedBy)
	}
	count := map[string]int{}
	for _, m := range g.Members {
		count[m]++
	}
	for id, n := range count {
		if n != 1 {
			t.Fatalf("成員 %s 重複 %d 次(user:tim / tim / mcp 都是本機使用者)", id, n)
		}
	}
	if count[me] != 1 {
		t.Fatal("本機使用者要在成員裡")
	}
	// 成員:本機使用者、dev、alice、dave(未註冊)、agent:gone 與怪值(sys_legacy)= 6 個不同的 actor
	if len(g.Members) != 6 {
		t.Fatalf("成員數 %d:%v", len(g.Members), g.Members)
	}
	msgs, _, err := l.Store.ListChannelMessages("default", g.ID, model.MessageQuery{Limit: 100})
	if err != nil || len(msgs) != 7 {
		t.Fatalf("%v %d", err, len(msgs))
	}
	byNum := map[int]model.ChannelMessage{}
	for _, m := range msgs {
		byNum[m.Number()] = m
	}
	for _, n := range []int{1, 3, 7} { // mcp、ops-web、空署名 = 本機使用者
		if byNum[n].Author != me {
			t.Fatalf("訊息 %d 作者 %s 要是本機使用者", n, byNum[n].Author)
		}
	}
	if k, h := actorHandle(t, l, byNum[2].Author); k != "user" || h != "alice" {
		t.Fatalf("user:alice 要對到使用者 alice:%s %s", k, h)
	}
	if u, err := l.Store.UserByEmail("alice"); err != nil || u.ActorID != byNum[2].Author {
		t.Fatalf("alice 的 actor 要是使用者列的 actor:%v", err)
	}
	if k, _ := actorHandle(t, l, byNum[4].Author); k != "system" {
		t.Fatalf("已刪角色署名要是 sys_legacy:%s", k)
	}
	if !strings.HasPrefix(byNum[5].Author, "sys_legacy_") {
		t.Fatalf("怪值要是 sys_legacy:%s", byNum[5].Author)
	}
	if k, h := actorHandle(t, l, byNum[6].Author); k != "user" || h != "erin@example.com" {
		t.Fatalf("email 要對到未註冊使用者:%s %s", k, h)
	}
	if len(byNum[1].Mentions) != 4 { // agent:dev、alice、agent:gone、純字串 dev(寬鬆解析器只認 agent: 前綴,純字串是 sys_legacy)
		t.Fatalf("提及要去重:%v", byNum[1].Mentions)
	}
	if len(byNum[2].Mentions) != 2 { // 本機使用者(兩種寫法)、怪值
		t.Fatalf("提及要去重:%v", byNum[2].Mentions)
	}
	// 已讀:user:tim 與 tim 合併取最大(舊 4 → 新 4)
	rd, err := l.Store.ChannelReads("default", me)
	if err != nil || rd[g.ID].LastRead != 4 {
		t.Fatalf("已讀要合併取最大:%v %+v", err, rd[g.ID])
	}

	// 冪等:再匯入一次全部略過,actor 數不變;匯入紀錄遺失後重送回 existing,不新增 actor、不重複寫
	before := actorCount(t, db)
	res, err = l.Import("default", in)
	if err != nil || res["channels"].Status != "skipped" || res["issues"].Status != "skipped" {
		t.Fatalf("%v %+v", err, res)
	}
	if err := writeFileSync(l.importLogPath(), []byte(`{"orgs":{}}`)); err != nil {
		t.Fatal(err)
	}
	res, err = l.Import("default", in)
	if err != nil {
		t.Fatal(err)
	}
	for k, s := range res {
		if s.Status != "existing" {
			t.Fatalf("%s 要是 existing:%+v", k, s)
		}
	}
	if after := actorCount(t, db); after != before {
		t.Fatalf("重複匯入多建了 actor:%d → %d", before, after)
	}
	if again, _ := l.Store.ListChannels("default"); len(again) != len(chs) {
		t.Fatal("重複匯入不能多寫頻道")
	}
	assertActorColumns(t, db)
}

// 組織鏈與頻道裡 App 的人員 id 建出的本機使用者:有 actor、接回「未註冊使用者」actor(同一個 actor)
func TestLocalImportReconnectsUnregisteredUser(t *testing.T) {
	l, db := openActorLocal(t, "tim")
	at := model.Now()
	// 先有一筆署名 carol@example.com 的資料,讓 carol@example.com 成為未註冊使用者 actor
	if res, err := l.Import("default", AppImport{Issues: &[]model.Issue{{ID: "ISS-1", Title: "a", CreatedBy: "carol@example.com", CreatedAt: at, UpdatedAt: at}}}); err != nil || res["issues"].Status != "done" {
		t.Fatalf("%v %+v", err, res)
	}
	un, err := l.Store.ResolveLegacy("default", "carol@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Store.UserByActorID(un); err == nil {
		t.Fatal("此時還沒有使用者列")
	}
	// 頻道成員 user:carol@example.com 要有使用者列,接回同一個 actor
	res, err := l.Import("default", AppImport{Channels: &AppChannels{Channels: []model.Channel{{ID: "CH-1", Name: "c", Members: []string{"user:carol@example.com"}, CreatedBy: "user:carol@example.com", CreatedAt: at, UpdatedAt: at}}}})
	if err != nil || res["channels"].Status != "done" {
		t.Fatalf("%v %+v", err, res)
	}
	u, err := l.Store.UserByEmail("carol@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.ActorID != un {
		t.Fatalf("要接回同一個 actor:%s ≠ %s", u.ActorID, un)
	}
	assertActorColumns(t, db)
}

// 本機使用者別名:啟動時接線,mcp / omixor-console / ops-web / 本機 handle 對到本機使用者 actor
func TestLocalUserAliasWiring(t *testing.T) {
	l, _ := openActorLocal(t, "Tim")
	for _, raw := range []string{"mcp", "omixor-console", "ops-web", "tim", "user:tim", "TIM"} {
		id, err := l.Store.ResolveLegacy("default", raw)
		if err != nil || id != l.actor.ID {
			t.Fatalf("%q → %q(要是本機使用者 %s)%v", raw, id, l.actor.ID, err)
		}
	}
	if id, err := l.Store.ResolveLegacy("default", "someone-else"); err != nil || id == l.actor.ID {
		t.Fatalf("別的字串不能對到本機使用者:%q %v", id, err)
	}
}

// 本機 Principal 帶 Actor(Via = local),建立 Issue、留言、頻道在本機模式可用
func TestLocalPrincipalActor(t *testing.T) {
	l, db := openActorLocal(t, "tim")
	p := l.principal()
	if p.Actor.ID == "" || p.Actor.Kind != model.ActorKindUser || p.Via != model.ViaLocal || p.User.ActorID != p.Actor.ID {
		t.Fatalf("%+v", p)
	}
	self, err := l.Svc.SelfActor(p)
	if err != nil || self.ID != p.Actor.ID {
		t.Fatalf("%v %+v", err, self)
	}
	iss, err := l.Svc.CreateIssue(p, "default", service.IssueInput{Title: "本機 issue"})
	if err != nil {
		t.Fatal(err)
	}
	if iss.CreatedBy != "tim" {
		t.Fatalf("建立者:%q", iss.CreatedBy)
	}
	if _, err := l.Svc.AddIssueComment(p, "default", iss.ID, service.CommentInput{Text: "留言"}); err != nil {
		t.Fatal(err)
	}
	ch, err := l.Svc.CreateChannel(p, "default", service.ChannelInput{Name: "本機頻道"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Svc.PostChannelMessage(p, "default", ch.ID, service.PostInput{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	assertActorColumns(t, db)
}
