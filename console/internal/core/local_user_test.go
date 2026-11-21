package core

import (
	"errors"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// ---- ISS-36 C-5:console 本機使用者 actor ----

// 本機核心的 Principal 帶本機使用者的 actor(Kind=user、Via=local),SelfActor 成功;使用者是伺服器管理員
func TestLocalPrincipalHasUserActor(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	l := localCore()
	if l == nil {
		t.Fatal("沒有本機核心")
	}
	p := l.P
	if p.Actor.Kind != model.ActorKindUser || !model.ValidActorID(p.Actor.ID) || p.Via != model.ViaLocal {
		t.Fatalf("Principal:%+v", p)
	}
	if p.User.Email != LocalIdentity || !p.ServerAdmin() || p.User.ActorID != p.Actor.ID {
		t.Fatalf("使用者:%+v", p.User)
	}
	ref, err := l.Svc.SelfActor(&model.Principal{User: model.User{ID: p.User.ID}}) // 只有使用者 id 也補得出 actor
	if err != nil || ref.ID != p.Actor.ID {
		t.Fatalf("SelfActor:%+v %v", ref, err)
	}
}

// 本機模式建 Issue、留言、頻道、文件、事件、稽核都可用,署名是本機使用者(輸出相容字串 omixor-console;存的是它的 actor id)
func TestLocalModeAuthorsAreLocalUser(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	l := localCore()
	if l == nil {
		t.Fatal("沒有本機核心")
	}
	const org = "c5"
	ensureLocalOrg(l, org)
	b := authorBackend{l}
	me := l.P.Actor.ID

	// 傳人員 id(tim、mcp、本機身分)當署名:不傳給核心,署名還是本機使用者
	for _, who := range []string{"", "tim", MCPAuthor, LocalIdentity, "ops-web"} {
		i, err := b.CreateIssue(org, service.IssueInput{Title: "標題 " + who, Author: who})
		if err != nil || i.CreatedBy != LocalIdentity {
			t.Fatalf("建立(%q):%+v %v", who, i, err)
		}
		raw, err := l.Svc.Store.GetIssue(org, i.ID)
		if err != nil || raw.CreatedBy != me {
			t.Fatalf("存的建立者要是本機使用者 actor:%+v %v", raw, err)
		}
		c, err := b.AddIssueComment(org, i.ID, "留言", who)
		if err != nil || len(c.Comments) != 1 || c.Comments[0].Author != LocalIdentity {
			t.Fatalf("留言(%q):%+v %v", who, c, err)
		}
	}

	ch, err := b.CreateChannel(org, service.ChannelInput{Name: "本機頻道", Members: []string{"user:tim", "mcp"}, As: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if ch.CreatedBy != "user:"+LocalIdentity || len(ch.Members) != 1 {
		t.Fatalf("頻道:%+v", ch)
	}
	m, err := b.PostChannelMessage(org, ch.ID, service.PostInput{Text: "嗨", As: "tim"})
	if err != nil || m.Author != "user:"+LocalIdentity {
		t.Fatalf("訊息:%+v %v", m, err)
	}
	if in, err := b.ChannelInbox(org, "user:tim"); err != nil || in == nil {
		t.Fatalf("收件匣:%v %v", in, err)
	}

	d, err := b.CreateDoc(org, service.DocInput{Title: "文件", Section: "docs", Author: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Author != LocalIdentity {
		t.Fatalf("文件:%+v", d)
	}

	inc, err := b.DeclareIncident(org, service.IncidentInput{Title: "事件", Sev: 2, Impact: "x", Commander: "tim",
		Actor: &model.IncidentActor{Kind: "person", ID: "tim"}, Author: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if inc.Commander != LocalIdentity || inc.CreatedBy != LocalIdentity {
		t.Fatalf("事件:%+v", inc)
	}
	note := "註記"
	if _, err := b.UpdateIncident(org, inc.ID, service.IncidentPatch{Note: &note, Actor: &model.IncidentActor{Kind: "person", ID: "tim"}}); err != nil {
		t.Fatal(err)
	}

	// 稽核:操作者是本機使用者的 actor、來源是 local、email 快照是 omixor-console
	if _, err := b.CreateOrg(model.Org{ID: "c5b", Name: "b"}); err != nil {
		t.Fatal(err)
	}
	entries, err := l.Svc.Store.ListAudit(50)
	if err != nil || len(entries) == 0 {
		t.Fatalf("稽核:%v %v", entries, err)
	}
	found := false
	for _, e := range entries {
		if e.Actor == me && e.Source == model.ViaLocal && e.Email == LocalIdentity {
			found = true
		}
	}
	if !found {
		t.Fatalf("稽核沒有本機使用者的紀錄:%+v", entries)
	}
}

// 重複啟動冪等:不會多出使用者或 actor;遷移先建的「未註冊使用者」actor(舊資料的 mcp 對到本機使用者)會被接回同一個
func TestEnsureLocalPrincipalIdempotentAndAdopts(t *testing.T) {
	ConfigureLocalIdentity()
	st := storetest.New(t)
	// 模擬舊資料的 mcp 署名:寬鬆解析器對到本機使用者,先建了 handle = omixor-console 的未註冊使用者 actor
	oldID, err := st.ResolveLegacy("default", "mcp")
	if err != nil || !model.ValidActorIDOf(model.ActorKindUser, oldID) {
		t.Fatalf("mcp 要對到本機使用者 actor:%q %v", oldID, err)
	}
	p1, err := ensureLocalPrincipal(st)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Actor.ID != oldID {
		t.Fatalf("要接回遷移建的 actor:%s ≠ %s", p1.Actor.ID, oldID)
	}
	p2, err := ensureLocalPrincipal(st)
	if err != nil || p2.Actor.ID != oldID || p2.User.ID != p1.User.ID {
		t.Fatalf("重複啟動:%+v %v", p2, err)
	}
	a, err := st.ActorByHandle(model.ActorKindUser, "", LocalIdentity)
	if err != nil || a.ID != oldID {
		t.Fatalf("handle 對到的 actor:%+v %v", a, err)
	}
	// 管理員被停用或降級:啟動時補回
	u := p1.User
	u.Role, u.Disabled = "member", true
	if err := st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	p3, err := ensureLocalPrincipal(st)
	if err != nil || !p3.ServerAdmin() || p3.Actor.ID != oldID {
		t.Fatalf("補回管理員:%+v %v", p3, err)
	}
}

// 舊資料的本機別名(mcp、omixor-console、tim、ops-web 與前綴寫法)都對到本機使用者 actor,沒有 sys_legacy 孤兒
func TestLegacyLocalAliasesResolveToLocalUser(t *testing.T) {
	ConfigureLocalIdentity() // 在建 store(NewPG,啟動遷移)之前
	st := storetest.New(t)
	p, err := ensureLocalPrincipal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"mcp", "omixor-console", "tim", "ops-web", "user:tim", "user:ops-web", "local:mcp", "local:omixor-console"} {
		id, err := st.ResolveLegacy("default", raw)
		if err != nil || id != p.Actor.ID {
			t.Errorf("%q → %q(要 %s):%v", raw, id, p.Actor.ID, err)
		}
	}
	if _, err := st.ActorByID("sys_legacy_mcp"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不該有孤兒 sys_legacy_mcp:%v", err)
	}
}

// 沒設本機使用者時(雲端),mcp 對到 sys_legacy_mcp;console 的 ConfigureLocalIdentity 要在 NewPG 之前呼叫才對到本機使用者
func TestConfigureLocalIdentityOrder(t *testing.T) {
	store.SetMigrationLocalUser("")
	t.Cleanup(ConfigureLocalIdentity)
	st := storetest.New(t)
	id, err := st.ResolveLegacy("default", "mcp")
	if err != nil || !model.ValidActorIDOf(model.ActorKindSystem, id) {
		t.Fatalf("沒設本機使用者:mcp 要對到系統 actor:%q %v", id, err)
	}
	ConfigureLocalIdentity()
	st2 := storetest.New(t)
	id2, err := st2.ResolveLegacy("default", "mcp")
	if err != nil || !model.ValidActorIDOf(model.ActorKindUser, id2) {
		t.Fatalf("設了之後:mcp 要對到本機使用者:%q %v", id2, err)
	}
}

// ---- 署名輸入的轉換:不把人員 id 傳給核心(本機與遠端共用 authorBackend) ----

func TestCoreAuthor(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "tim": "", "mcp": "", "omixor-console": "", "ops-web": "", "bob@x.com": "", " tim ": "",
		"agent:pm": "agent:pm", "Agent:PM": "Agent:PM",
		// 系統署名換成核心認得的系統 actor id(核心的嚴格解析器只收 id)
		RoutineAuthor: model.SystemActorRoutine, model.ChainAuthor: model.SystemActorChain, model.SystemAuthor: model.SystemActorOmixor, "system": model.SystemActorOmixor,
	} {
		if got := CoreAuthor(in); got != want {
			t.Errorf("CoreAuthor(%q) = %q,要 %q", in, got, want)
		}
	}
	if coreNode("user:tim") != "user:"+LocalIdentity || coreNode("mcp") != "user:"+LocalIdentity || coreNode("agent:pm") != "agent:pm" || coreNode("bob@x.com") != "bob@x.com" {
		t.Fatal("coreNode")
	}
}

// 系統署名(routine、chain、omixor、system)經 CoreAuthor 之後,本機核心的 Issue、留言、事件都收得下;存的是系統 actor id
func TestLocalCoreAcceptsSystemAuthors(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	l := localCore()
	if l == nil {
		t.Fatal("沒有本機核心")
	}
	const org = "sysauthors"
	ensureLocalOrg(l, org)
	b := authorBackend{l}
	for author, want := range map[string]string{RoutineAuthor: model.SystemActorRoutine, model.ChainAuthor: model.SystemActorChain, model.SystemAuthor: model.SystemActorOmixor, "system": model.SystemActorOmixor} {
		i, err := b.CreateIssue(org, service.IssueInput{Title: "系統 " + author, Author: author})
		if err != nil {
			t.Fatalf("建立 Issue(%q):%v", author, err)
		}
		raw, err := l.Svc.Store.GetIssue(org, i.ID)
		if err != nil || raw.CreatedBy != want {
			t.Fatalf("(%q)存的建立者要是 %s:%+v %v", author, want, raw, err)
		}
		if _, err := b.AddIssueComment(org, i.ID, "留言", author); err != nil {
			t.Fatalf("留言(%q):%v", author, err)
		}
		if _, err := b.DeclareIncident(org, service.IncidentInput{Title: "事件 " + author, Sev: 3, Author: author}); err != nil {
			t.Fatalf("事件(%q):%v", author, err)
		}
		// 人或角色欄位(指揮官、頻道 as)不傳系統署名:空 = 本人
		if corePerson(author) != "" {
			t.Fatalf("corePerson(%q) 要空字串", author)
		}
	}
	if corePerson("agent:pm") != "agent:pm" || corePerson("tim") != "" {
		t.Fatal("corePerson")
	}
}

// ensureLocalPrincipal 只採用本機建立的那一列(固定 evoleap_id);email 是 omixor-console 卻綁了別的登入身分(例如 LDAP)的列
// 不採用、不升管理員、不解除停用
func TestEnsureLocalPrincipalOnlyAdoptsLocalRow(t *testing.T) {
	ConfigureLocalIdentity()
	st := storetest.New(t)
	foreign := model.User{ID: "u_ldapfake", EvoleapID: "ldap:evil", Email: LocalIdentity, Name: "evil", Role: "member", Disabled: true, CreatedAt: model.Now()}
	if err := st.SaveUser(foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureLocalPrincipal(st); err == nil {
		t.Fatal("email 是 omixor-console 卻綁了 LDAP 身分的列,不能當本機使用者")
	}
	got, err := st.UserByEmail(LocalIdentity)
	if err != nil || got.Role != "member" || !got.Disabled || got.EvoleapID != "ldap:evil" {
		t.Fatalf("外部身分的列不能被升級或解除停用:%+v %v", got, err)
	}
}

// 舊版建立的本機使用者(沒有固定 evoleap_id 的標記、沒綁外部身分)接手並補上標記;之後以標記找,不再依 email
func TestEnsureLocalPrincipalAdoptsLegacyLocalRow(t *testing.T) {
	ConfigureLocalIdentity()
	st := storetest.New(t)
	old := model.User{ID: "u_oldlocal", Email: LocalIdentity, Name: LocalIdentity, Role: "admin", CreatedAt: model.Now()}
	if err := st.SaveUser(old); err != nil {
		t.Fatal(err)
	}
	p, err := ensureLocalPrincipal(st)
	if err != nil || p.User.ID != "u_oldlocal" || p.User.EvoleapID != localUserKey {
		t.Fatalf("接手舊的本機列並補標記:%+v %v", p, err)
	}
	byKey, err := st.UserByEvoleapID(localUserKey)
	if err != nil || byKey.ID != "u_oldlocal" {
		t.Fatalf("以標記找得到:%+v %v", byKey, err)
	}
}

// 記下收到的署名的假核心
type recBackend struct {
	backend.Unsupported
	issue   service.IssueInput
	patch   service.IssuePatch
	comment string
	post    service.PostInput
	chIn    service.ChannelInput
	edges   []model.ChainEdge
	who     string
	inc     service.IncidentInput
}

func (r *recBackend) CreateIssue(_ string, in service.IssueInput) (*model.Issue, error) {
	r.issue = in
	return &model.Issue{}, nil
}
func (r *recBackend) UpdateIssue(_, _ string, p service.IssuePatch) (*model.Issue, error) {
	r.patch = p
	return &model.Issue{}, nil
}
func (r *recBackend) AddIssueComment(_, _, _, a string) (*model.Issue, error) {
	r.comment = a
	return &model.Issue{}, nil
}
func (r *recBackend) PostChannelMessage(_, _ string, in service.PostInput) (*model.ChannelMessage, error) {
	r.post = in
	return &model.ChannelMessage{}, nil
}
func (r *recBackend) CreateChannel(_ string, in service.ChannelInput) (*model.ChannelSummary, error) {
	r.chIn = in
	return &model.ChannelSummary{}, nil
}
func (r *recBackend) SaveChain(_ string, e []model.ChainEdge) (*model.OrgChain, error) {
	r.edges = e
	return &model.OrgChain{}, nil
}
func (r *recBackend) ChannelInbox(_, who string) (*service.ChannelInbox, error) {
	r.who = who
	return &service.ChannelInbox{}, nil
}
func (r *recBackend) DeclareIncident(_ string, in service.IncidentInput) (*model.Incident, error) {
	r.inc = in
	return &model.Incident{}, nil
}

// 遠端模式(backend.Remote 也包在 authorBackend 裡)不傳人員 id 的署名;角色署名照傳
func TestAuthorBackendDoesNotSendPersonIDs(t *testing.T) {
	r := &recBackend{}
	b := authorBackend{r}
	_, _ = b.CreateIssue("o", service.IssueInput{Title: "t", Author: "tim"})
	if r.issue.Author != "" {
		t.Fatalf("建立 Issue 不該傳人員署名:%q", r.issue.Author)
	}
	_, _ = b.CreateIssue("o", service.IssueInput{Title: "t", Author: "agent:pm"})
	if r.issue.Author != "agent:pm" {
		t.Fatalf("角色署名要保留:%q", r.issue.Author)
	}
	cs := []model.Comment{{ID: "c1", Author: "tim"}, {ID: "c2", Author: "agent:pm"}, {ID: "c3", Author: "bob@x.com"}}
	_, _ = b.UpdateIssue("o", "i", service.IssuePatch{Author: "mcp", Comments: &cs})
	if r.patch.Author != "" || (*r.patch.Comments)[0].Author != "user:"+LocalIdentity || (*r.patch.Comments)[1].Author != "agent:pm" || (*r.patch.Comments)[2].Author != "bob@x.com" {
		t.Fatalf("整份取代的留言作者:%+v", r.patch)
	}
	if cs[0].Author != "tim" {
		t.Fatal("不可改到呼叫端的切片")
	}
	_, _ = b.AddIssueComment("o", "i", "x", "tim")
	if r.comment != "" {
		t.Fatalf("留言署名:%q", r.comment)
	}
	_, _ = b.PostChannelMessage("o", "c", service.PostInput{Text: "x", As: "tim", Author: "mcp"})
	if r.post.As != "" || r.post.Author != "" {
		t.Fatalf("訊息署名:%+v", r.post)
	}
	_, _ = b.CreateChannel("o", service.ChannelInput{Name: "n", As: "tim", Members: []string{"user:tim", "agent:pm", "bob@x.com"}})
	if r.chIn.As != "" || len(r.chIn.Members) != 3 || r.chIn.Members[0] != "user:"+LocalIdentity || r.chIn.Members[1] != "agent:pm" || r.chIn.Members[2] != "bob@x.com" {
		t.Fatalf("頻道:%+v", r.chIn)
	}
	_, _ = b.SaveChain("o", []model.ChainEdge{{From: "user:tim", To: "agent:pm"}})
	if r.edges[0].From != "user:"+LocalIdentity || r.edges[0].To != "agent:pm" {
		t.Fatalf("組織鏈:%+v", r.edges)
	}
	_, _ = b.ChannelInbox("o", "tim")
	if r.who != "user:"+LocalIdentity {
		t.Fatalf("收件匣:%q", r.who)
	}
	_, _ = b.DeclareIncident("o", service.IncidentInput{Commander: "tim", Author: "tim", Actor: &model.IncidentActor{Kind: "person", ID: "tim"}})
	if r.inc.Commander != "" || r.inc.Author != "" || r.inc.Actor != nil {
		t.Fatalf("事件:%+v", r.inc)
	}
	_, _ = b.DeclareIncident("o", service.IncidentInput{Actor: &model.IncidentActor{Kind: "ai", ID: "pm"}})
	if r.inc.Actor == nil || r.inc.Actor.ID != "pm" {
		t.Fatalf("角色事件執行者要保留:%+v", r.inc)
	}
}
