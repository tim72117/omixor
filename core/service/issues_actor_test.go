package service_test

import (
	"strings"
	"sync/atomic"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// ISS-36 C-2a:Issue、留言、文件的「誰」欄位是 actor id(store),對外輸出相容字串

// issueActorCounter 數 ActorsByIDs 被呼叫幾次(列表輸出要一次批次查詢,不是每筆各查一次)
type issueActorCounter struct {
	store.Store
	n atomic.Int32
}

func (c *issueActorCounter) ActorsByIDs(ids []string) (map[string]model.ActorRecord, error) {
	c.n.Add(1)
	return c.Store.ActorsByIDs(ids)
}

func agentPrincipal(t *testing.T, st store.Store, org, handle string) *model.Principal {
	t.Helper()
	rec := storetest.Agent(t, st, org, handle)
	return &model.Principal{Agent: &model.AgentIdentity{Org: org, AgentID: handle}, Actor: rec.Ref(), Via: model.ViaAgentCredential}
}

func actorIDOf(t *testing.T, st store.Store, email string) string {
	t.Helper()
	return storetest.User(t, st, email).ID
}

// author 規則:成員與角色憑證只能是自己;伺服器管理員只能代角色與系統,代別的使用者 403
func TestIssueAuthorRules(t *testing.T) {
	st := storetest.New(t)
	svc, root, boss, alice, _ := issueEnv(t, st)
	dev := agentPrincipal(t, st, "o1", "dev")

	// 成員:給了別人的名字靜默忽略,作者是自己;store 裡是 actor id
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Author: "boss@x"})
	if err != nil || i.CreatedBy != "alice@x" {
		t.Fatalf("成員只能是自己:%+v %v", i, err)
	}
	raw, _ := st.GetIssue("o1", i.ID)
	if raw.CreatedBy != actorIDOf(t, st, "alice@x") || !model.ValidActorID(raw.CreatedBy) {
		t.Fatalf("store 的 created_by 要是 actor id:%q", raw.CreatedBy)
	}
	// 角色憑證:只能是自己(角色 actor),給別人的名字忽略
	i2, err := svc.CreateIssue(dev, "o1", service.IssueInput{Title: "t2", Author: "alice@x"})
	if err != nil || i2.CreatedBy != "agent:dev" {
		t.Fatalf("角色憑證只能是自己:%+v %v", i2, err)
	}
	// 嚴格模式:給了不是自己的 → 403(成員、角色憑證都是)
	if _, err := svc.ResolveAuthor(alice, "o1", "boss@x", service.AuthorReject); code(err) != 403 {
		t.Fatal("成員代別人署名要 403:", err)
	}
	if _, err := svc.ResolveAuthor(dev, "o1", "alice@x", service.AuthorReject); code(err) != 403 {
		t.Fatal("角色憑證代別人署名要 403:", err)
	}
	if ref, err := svc.ResolveAuthor(alice, "o1", "ALICE@x", service.AuthorReject); err != nil || ref.Handle != "alice@x" {
		t.Fatal("給自己照給:", ref, err)
	}
	// 伺服器管理員:沒給是自己(不再是 "mcp");可以代角色;別的使用者、找不到的名字(含舊用戶端傳的 "mcp"、app.me):
	// 相容模式(AuthorIgnore)靜默當自己,不會變成別人的署名;嚴格模式(AuthorReject)才是 403 / 400(D9 的相容修正)
	if c, err := svc.CreateIssue(root, "o1", service.IssueInput{Title: "a"}); err != nil || c.CreatedBy != "root@x" {
		t.Fatalf("管理員沒給 author 是自己:%+v %v", c, err)
	}
	if c, err := svc.CreateIssue(root, "o1", service.IssueInput{Title: "b", Author: "agent:dev"}); err != nil || c.CreatedBy != "agent:dev" {
		t.Fatalf("管理員可以代角色:%+v %v", c, err)
	}
	if c, err := svc.CreateIssue(root, "o1", service.IssueInput{Title: "b2", Author: "root@x"}); err != nil || c.CreatedBy != "root@x" {
		t.Fatalf("管理員署名自己照給:%+v %v", c, err)
	}
	for _, who := range []string{"alice@x", "冒名", "mcp", "tim"} {
		if c, err := svc.CreateIssue(root, "o1", service.IssueInput{Title: "c " + who, Author: who}); err != nil || c.CreatedBy != "root@x" {
			t.Fatalf("管理員給 %q 相容模式當自己:%+v %v", who, c, err)
		}
	}
	if _, err := svc.ResolveAuthor(root, "o1", "alice@x", service.AuthorReject); code(err) != 403 {
		t.Fatal("嚴格模式管理員代別的使用者要 403:", err)
	}
	if _, err := svc.ResolveAuthor(root, "o1", "冒名", service.AuthorReject); code(err) != 400 {
		t.Fatal("嚴格模式管理員給不存在的名字要 400:", err)
	}
	// 留言同一套規則
	if u, err := svc.AddIssueComment(root, "o1", i.ID, service.CommentInput{Text: "x", Author: "alice@x"}); err != nil || u.Comments[len(u.Comments)-1].Author != "root@x" {
		t.Fatal("留言管理員給別的使用者要當自己:", u, err)
	}
	if u, err := svc.AddIssueComment(alice, "o1", i.ID, service.CommentInput{Text: "x", Author: "boss@x"}); err != nil || u.Comments[len(u.Comments)-1].Author != "alice@x" {
		t.Fatalf("成員留言作者是自己:%+v %v", u, err)
	}
	_ = boss
}

// assignee:輸入角色 handle(也接受 @handle、agent:handle、名稱)→ store 存角色 actor id → 輸出角色 handle;已退役的角色讀回仍是原 handle
func TestIssueAssigneeActorRoundTrip(t *testing.T) {
	st := storetest.New(t)
	svc, _, boss, alice, _ := issueEnv(t, st)
	dev := storetest.Agent(t, st, "o1", "dev")

	for _, in := range []string{"dev", "@dev", "agent:dev", "DEV", "工程師", dev.ID} {
		i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t " + in, Assignee: in})
		if err != nil || i.Assignee != "dev" {
			t.Fatalf("assignee %q 要解析成 dev:%+v %v", in, i, err)
		}
		raw, _ := st.GetIssue("o1", i.ID)
		if raw.Assignee != dev.ID {
			t.Fatalf("store 的 assignee 要是角色的 actor id(%s):%q", dev.ID, raw.Assignee)
		}
	}
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "x", Assignee: "ghost"}); code(err) != 400 {
		t.Fatal("不存在的角色 400:", err)
	}
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "x", Assignee: "alice@x"}); code(err) != 400 {
		t.Fatal("assignee 不能是人(B/C 階段只能角色):", err)
	}
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "改指派"})
	if u, err := svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Assignee: sp("dev")}); err != nil || u.Assignee != "dev" {
		t.Fatalf("%+v %v", u, err)
	}
	if got, _ := svc.GetIssue(alice, "o1", i.ID); got.Assignee != "dev" {
		t.Fatalf("讀回是 handle:%+v", got)
	}
	// 篩選用 handle
	got, err := svc.QueryIssues(alice, "o1", model.IssueQuery{Assignee: "dev"})
	if err != nil || len(got) != 7 {
		t.Fatalf("assignee 篩選要用 handle(6 張建立時指派 + 1 張改指派):%d %v", len(got), err)
	}
	// 取消指派 = NULL
	if u, err := svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Assignee: sp("")}); err != nil || u.Assignee != "" {
		t.Fatalf("%+v %v", u, err)
	}
	if raw, _ := st.GetIssue("o1", i.ID); raw.Assignee != "" {
		t.Fatalf("取消指派 store 是空:%q", raw.Assignee)
	}
	// 角色退役(actor 設 disabled):歷史指派讀回仍顯示原 handle
	if _, err := svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Assignee: sp("dev")}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActorDisabled(dev.ID, true, model.Now().Time); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetIssue(boss, "o1", i.ID); err != nil || got.Assignee != "dev" {
		t.Fatalf("退役角色的指派讀回仍是原 handle:%+v %v", got, err)
	}
}

// 列表輸出:整批一次查詢,不是每筆各查一次(Issue 建立者、指派、留言作者都在同一次批次裡)
func TestIssueListPresentsInOneBatch(t *testing.T) {
	pg := storetest.New(t)
	cs := &issueActorCounter{Store: pg}
	svc, _, boss, alice, bob := issueEnv(t, cs)
	for k := range 6 {
		who := []*model.Principal{alice, bob, boss}[k%3]
		i, err := svc.CreateIssue(who, "o1", service.IssueInput{Title: "t", Assignee: "dev"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddIssueComment(who, "o1", i.ID, service.CommentInput{Text: "c"}); err != nil {
			t.Fatal(err)
		}
	}
	before := cs.n.Load()
	list, err := svc.ListIssues(alice, "o1")
	if err != nil || len(list) != 6 {
		t.Fatalf("%d %v", len(list), err)
	}
	if n := cs.n.Load() - before; n != 1 {
		t.Fatalf("列表 6 張 Issue 要只發一次批次查詢,實際 %d 次", n)
	}
	for _, i := range list {
		if !strings.Contains(i.CreatedBy, "@x") || i.Assignee != "dev" || len(i.Comments) != 1 || !strings.Contains(i.Comments[0].Author, "@x") {
			t.Fatalf("輸出要是相容字串:%+v", i)
		}
	}
}

// UpdateIssue 帶 Comments 整份改寫:既有留言的作者照伺服器上的;新留言作者經署名規則;相容字串(讀出再寫回)可以接回 actor id
func TestIssueCommentsRewriteKeepsAuthors(t *testing.T) {
	st := storetest.New(t)
	svc, root, _, alice, bob := issueEnv(t, st)
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	if _, err := svc.AddIssueComment(alice, "o1", i.ID, service.CommentInput{Text: "甲"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddIssueComment(root, "o1", i.ID, service.CommentInput{Text: "乙", Author: "agent:dev"}); err != nil {
		t.Fatal(err)
	}
	cur, _ := svc.GetIssue(alice, "o1", i.ID)
	if cur.Comments[0].Author != "alice@x" || cur.Comments[1].Author != "agent:dev" {
		t.Fatalf("%+v", cur.Comments)
	}
	// 管理員整份改寫:讀出的相容字串寫回,作者不變;改了既有留言的作者字串無效;新留言可代角色
	given := append([]model.Comment{}, cur.Comments...)
	given[0].Author = "bob@x"
	given[1].Text = "乙改"
	given = append(given, model.Comment{Text: "丙", Author: "agent:dev"}, model.Comment{Text: "丁"})
	u, err := svc.UpdateIssue(root, "o1", i.ID, service.IssuePatch{Comments: &given})
	if err != nil || len(u.Comments) != 4 {
		t.Fatalf("%+v %v", u, err)
	}
	want := []string{"alice@x", "agent:dev", "agent:dev", "root@x"}
	for k, w := range want {
		if u.Comments[k].Author != w {
			t.Fatalf("第 %d 則作者應該是 %s:%+v", k, w, u.Comments)
		}
	}
	raw, _ := st.GetIssue("o1", i.ID)
	for _, c := range raw.Comments {
		if !model.ValidActorID(c.Author) {
			t.Fatalf("store 的留言作者要是 actor id:%q", c.Author)
		}
	}
	if raw.Comments[0].Author != actorIDOf(t, st, "alice@x") {
		t.Fatalf("既有留言作者不變:%q", raw.Comments[0].Author)
	}
	// 管理員在新留言代別的使用者 → 403
	bad := append([]model.Comment{}, u.Comments...)
	bad = append(bad, model.Comment{Text: "冒", Author: "bob@x"})
	if _, err := svc.UpdateIssue(root, "o1", i.ID, service.IssuePatch{Comments: &bad}); code(err) != 403 {
		t.Fatal("新留言代別的使用者要 403:", err)
	}
	// 一般成員:只能新增,新增的作者是自己(給別人的名字無效)
	mine := append([]model.Comment{}, u.Comments...)
	mine = append(mine, model.Comment{Text: "戊", Author: "alice@x"})
	u2, err := svc.UpdateIssue(bob, "o1", i.ID, service.IssuePatch{Comments: &mine})
	if err != nil || u2.Comments[4].Author != "bob@x" {
		t.Fatalf("成員新增的留言作者是自己:%+v %v", u2, err)
	}
}

// 匯入:舊字串走寬鬆解析器(缺的 actor 會建、對不上的退成 sys_legacy_*,讀回仍是原值;沒給的建立者當 mcp)
func TestIssueImportLegacyAuthors(t *testing.T) {
	st := storetest.New(t)
	svc, _, boss, _, _ := issueEnv(t, st)
	list := []model.Issue{
		{ID: "ISS-1", Title: "一", CreatedBy: "tim", Assignee: "dev", Comments: []model.Comment{{Author: "alice@x", Text: "a"}, {Author: "agent:ghost", Text: "b"}, {Text: "c"}}},
		{ID: "ISS-2", Title: "二", CreatedBy: "agent:dev", Assignee: "gone"},
		{ID: "ISS-3", Title: "三"},
		{ID: "ISS-4", Title: "四", CreatedBy: "boss@x", DeletedBy: "alice@x"}, // 沒有 deletedAt:deletedBy 不帶
	}
	if _, err := svc.ImportIssues(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ExportIssues(boss, "o1")
	if err != nil || len(got) != 4 {
		t.Fatalf("%d %v", len(got), err)
	}
	by := map[string]model.Issue{}
	for _, i := range got {
		by[i.Title] = i
	}
	if i := by["一"]; i.CreatedBy != "tim" || i.Assignee != "dev" || i.Comments[0].Author != "alice@x" || i.Comments[1].Author != "agent:ghost" || i.Comments[2].Author != "mcp" {
		t.Fatalf("%+v", i)
	}
	if i := by["二"]; i.CreatedBy != "agent:dev" || i.Assignee != "agent:gone" {
		t.Fatalf("對不上的角色 id 退成 sys_legacy_*,讀回節點寫法:%+v", i)
	}
	if i := by["三"]; i.CreatedBy != "mcp" {
		t.Fatalf("沒給的建立者當 mcp:%+v", i)
	}
	if i := by["四"]; i.CreatedBy != "boss@x" || i.DeletedBy != "" {
		t.Fatalf("%+v", i)
	}
	// store 裡一律是 actor id
	raw, _ := st.ListIssues("o1")
	for _, i := range raw {
		if !model.ValidActorID(i.CreatedBy) {
			t.Fatalf("created_by 要是 actor id:%+v", i)
		}
		for _, c := range i.Comments {
			if !model.ValidActorID(c.Author) {
				t.Fatalf("留言作者要是 actor id:%+v", c)
			}
		}
	}
}

// 垃圾桶:deleted_by 是刪除者的 actor id,輸出相容字串;還原清成 NULL
func TestIssueTrashDeletedByActor(t *testing.T) {
	st := storetest.New(t)
	svc, root, boss, alice, _ := issueEnv(t, st)
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	if err := svc.DeleteIssue(alice, "o1", i.ID); err != nil {
		t.Fatal(err)
	}
	trash, err := svc.ListDeletedIssues(boss, "o1")
	if err != nil || len(trash) != 1 || trash[0].DeletedBy != "alice@x" || trash[0].CreatedBy != "alice@x" {
		t.Fatalf("%+v %v", trash, err)
	}
	rawTrash, _ := st.ListDeletedIssues("o1")
	if rawTrash[0].DeletedBy != actorIDOf(t, st, "alice@x") {
		t.Fatalf("store 的 deleted_by 要是 actor id:%q", rawTrash[0].DeletedBy)
	}
	back, err := svc.RestoreIssue(boss, "o1", i.ID)
	if err != nil || back.DeletedBy != "" || back.DeletedAt != nil || back.CreatedBy != "alice@x" {
		t.Fatalf("%+v %v", back, err)
	}
	if raw, _ := st.GetIssue("o1", i.ID); raw.DeletedBy != "" {
		t.Fatalf("還原後 deleted_by 是 NULL:%q", raw.DeletedBy)
	}
	// 管理員刪(署名是它自己)
	if err := svc.DeleteIssue(root, "o1", i.ID); err != nil {
		t.Fatal(err)
	}
	trash, _ = svc.ListDeletedIssues(boss, "o1")
	if len(trash) != 1 || trash[0].DeletedBy != "root@x" {
		t.Fatalf("%+v", trash)
	}
}

// 文件作者:store 是 actor id(沒有時 NULL),輸出相容字串;匯入的舊字串走寬鬆解析器
func TestDocAuthorActor(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	st := svc.Store
	d, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "甲", Section: "docs", Author: "boss@x"})
	if err != nil || d.Author != "alice@x" {
		t.Fatalf("成員署名是自己:%+v %v", d, err)
	}
	raw, _ := st.GetDoc("o1", d.ID)
	if raw.Author != actorIDOf(t, st, "alice@x") {
		t.Fatalf("store 的作者要是 actor id:%q", raw.Author)
	}
	ag := agentPrincipal(t, st, "o1", "writer")
	if d2, err := svc.CreateDoc(ag, "o1", service.DocInput{Title: "乙", Section: "docs", Author: "alice@x"}); err != nil || d2.Author != "agent:writer" {
		t.Fatalf("角色憑證署名是角色:%+v %v", d2, err)
	}
	// 修改不改作者;列表輸出相容字串
	if u, err := svc.UpdateDoc(boss, "o1", d.ID, service.DocPatch{Title: sp("甲改")}); err != nil || u.Author != "alice@x" {
		t.Fatalf("%+v %v", u, err)
	}
	c, _ := svc.GetCabinet(boss, "o1", "")
	for _, x := range c.Docs {
		if x.Author != "alice@x" && x.Author != "agent:writer" {
			t.Fatalf("%+v", x)
		}
	}
	// 匯入:舊字串
	st2 := storetest.New(t)
	svc2 := &service.Service{Store: st2}
	_ = st2.CreateOrg(model.Org{ID: "o1", Name: "o", CreatedAt: model.Now()})
	_ = st2.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: model.Now()})
	_ = st2.SaveMember(model.Member{Org: "o1", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: model.Now()})
	b2 := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	layout := []model.CabinetSection{{ID: "docs", Label: "文件", Path: "/docs", Tabs: []model.CabinetTab{}}}
	docs := []model.CabinetDoc{{ID: "DOC-2", Title: "新", Section: "docs", Author: "tim"}, {ID: "DOC-1", Title: "舊", Section: "docs", Author: "boss@x"}, {ID: "DOC-3", Title: "無", Section: "docs"}}
	if _, err := svc2.ImportCabinet(b2, "o1", service.CabinetImport{Layout: layout, Docs: docs}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc2.GetCabinet(b2, "o1", "")
	by := map[string]string{}
	for _, x := range got.Docs {
		by[x.ID] = x.Author
	}
	if by["DOC-2"] != "tim" || by["DOC-1"] != "boss@x" || by["DOC-3"] != "" {
		t.Fatalf("匯入的作者:%+v", by)
	}
	rawDocs, _ := st2.ListDocs("o1")
	for _, x := range rawDocs {
		if x.ID != "DOC-3" && !model.ValidActorID(x.Author) {
			t.Fatalf("store 的作者要是 actor id:%+v", x)
		}
		if x.ID == "DOC-3" && x.Author != "" {
			t.Fatalf("沒給作者是 NULL:%+v", x)
		}
	}
}
