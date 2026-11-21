package service_test

import (
	"errors"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// ISS-36 審查修正(服務層):D9 的相容修正、頻道 as、restore、例行任務署名順序、角色沒有 actor、使用者 email 入口

// 頻道:伺服器管理員的 as 只能是角色;給別的使用者(或找不到)靜默當本人,不會變成別人的署名(D9 + 相容)
func TestChannelServerAdminAsCannotImpersonateUser(t *testing.T) {
	svc, st, _, _, _ := chainEnv(t)
	_ = st.SaveUser(model.User{ID: "root", Email: "root@x", Role: "admin"})
	root := &model.Principal{User: model.User{ID: "root", Email: "root@x", Role: "admin"}}
	if _, err := svc.CreateChannel(root, "o1", service.ChannelInput{Name: "c", Members: []string{"alice@x", "bob@x", "agent:cto"}}); err != nil {
		t.Fatal(err)
	}
	for _, as := range []string{"bob@x", "alice@x", "user:bob@x", "查無此人"} {
		m, err := svc.PostChannelMessage(root, "o1", "CH-1", service.PostInput{Text: "hi " + as, As: as})
		if err != nil || m.Author != "user:root@x" {
			t.Fatalf("as=%q 要署名管理員本人,不是 %q:%+v %v", as, as, m, err)
		}
		m, err = svc.PostChannelMessage(root, "o1", "CH-1", service.PostInput{Text: "hi2 " + as, Author: as})
		if err != nil || m.Author != "user:root@x" {
			t.Fatalf("author=%q 要署名管理員本人:%+v %v", as, m, err)
		}
	}
	// 角色:照舊可以代
	m, err := svc.PostChannelMessage(root, "o1", "CH-1", service.PostInput{Text: "角色", As: "agent:cto"})
	if err != nil || m.Author != "agent:cto" {
		t.Fatalf("管理員可以代角色:%+v %v", m, err)
	}
}

// ResolveAuthor:管理員在相容模式(AuthorIgnore)給的名字解析不到或是別的使用者 → 靜默當本人;角色與系統照給;嚴格模式維持 400 / 403
func TestResolveAuthorServerAdminCompat(t *testing.T) {
	st := storetest.New(t)
	svc, root, _, _, _ := issueEnv(t, st)
	self, err := svc.SelfActor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"alice@x", "boss@x", "mcp", "tim", "不存在的人", "agent:ghost"} {
		ref, err := svc.ResolveAuthor(root, "o1", g, service.AuthorIgnore)
		if err != nil || ref.ID != self.ID {
			t.Fatalf("管理員給 %q 相容模式當本人:%+v %v", g, ref, err)
		}
	}
	if ref, err := svc.ResolveAuthor(root, "o1", "agent:dev", service.AuthorIgnore); err != nil || ref.Kind != model.ActorKindAgent {
		t.Fatalf("角色照給:%+v %v", ref, err)
	}
	if ref, err := svc.ResolveAuthor(root, "o1", model.SystemActorRoutine, service.AuthorIgnore); err != nil || ref.ID != model.SystemActorRoutine {
		t.Fatalf("系統 actor id 照給:%+v %v", ref, err)
	}
	if _, err := svc.ResolveAuthor(root, "o1", "alice@x", service.AuthorReject); service.Code(err) != 403 {
		t.Fatal("嚴格模式別的使用者 403:", err)
	}
	if _, err := svc.ResolveAuthor(root, "o1", "不存在的人", service.AuthorReject); service.Code(err) != 400 {
		t.Fatal("嚴格模式找不到 400:", err)
	}
	// 明確傳 author:"mcp" 的舊用戶端(CreateIssue):管理員當本人
	i, err := svc.CreateIssue(root, "o1", service.IssueInput{Title: "t", Author: "mcp"})
	if err != nil || i.CreatedBy != "root@x" {
		t.Fatalf("author=mcp 管理員當本人:%+v %v", i, err)
	}
}

// restore:只有伺服器管理員;還原模式下留言整份取代的歷史作者走寬鬆解析器;沒有旗標時維持 D9 的擋法
func TestUpdateIssueRestore(t *testing.T) {
	st := storetest.New(t)
	svc, root, boss, alice, _ := issueEnv(t, st)
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	hist := []model.Comment{{ID: "c1", Author: "alice@x", Text: "a"}, {ID: "c2", Author: "離職的人@old.example", Text: "b"}, {ID: "c3", Author: "mcp", Text: "c"}, {ID: "c4", Text: "d"}}
	if _, err := svc.UpdateIssue(root, "o1", i.ID, service.IssuePatch{Comments: &hist}); err == nil {
		t.Fatal("沒有 restore 旗標:新留言代別的使用者要被擋")
	}
	for _, p := range []*model.Principal{boss, alice} {
		if _, err := svc.UpdateIssue(p, "o1", i.ID, service.IssuePatch{Comments: &hist, Restore: true}); service.Code(err) != 403 {
			t.Fatalf("非伺服器管理員不能用 restore:%v", err)
		}
	}
	got, err := svc.UpdateIssue(root, "o1", i.ID, service.IssuePatch{Comments: &hist, Restore: true})
	if err != nil || len(got.Comments) != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	want := []string{"alice@x", "離職的人@old.example", "mcp", "root@x"}
	for k, w := range want {
		if got.Comments[k].Author != w {
			t.Fatalf("留言 %d 作者要是 %q:%+v", k, w, got.Comments)
		}
	}
}

// RunRoutine:署名失敗時例行任務不能被標成已執行(先解析署名再寫 lastRunAt)
func TestRunRoutineAuthorErrorKeepsLastRunAt(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	past := model.Time{Time: time.Now().Add(-48 * time.Hour).UTC()}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: true,
		Routines: []model.AgentRoutine{{ID: "r1", Title: "日報", Kind: "daily", Enabled: true, Priority: "normal", CreatedAt: past}}}); err != nil {
		t.Fatal(err)
	}
	// 伺服器管理員 Principal 沒有使用者列、沒有 email:署名補不出來
	ghost := &model.Principal{User: model.User{ID: "u_nobody", Role: "admin"}}
	if _, err := svc.RunRoutine(ghost, "o1", "cto", "r1", ""); err == nil {
		t.Fatal("署名補不出來要回錯誤")
	}
	g, err := st.GetAgent("o1", "cto")
	if err != nil {
		t.Fatal(err)
	}
	if g.Routines[0].LastRunAt != nil {
		t.Fatalf("署名失敗,例行任務不能被標成已執行:%+v", g.Routines[0])
	}
	if ts, _ := st.ListAgentTasks("o1"); len(ts) != 0 {
		t.Fatalf("不能排任務:%+v", ts)
	}
	// 正常的呼叫照舊
	if _, err := svc.RunRoutine(boss, "o1", "cto", "r1", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ = st.GetAgent("o1", "cto"); g.Routines[0].LastRunAt == nil {
		t.Fatal("成功後要記 lastRunAt")
	}
}

// 角色沒有 actor_id(id 不符 handle 規則的舊資料):一般寫入路徑回明確錯誤,不建立 sys_legacy 的 ghost
func TestAgentWithoutActorIsExplicitError(t *testing.T) {
	st := storetest.New(t)
	svc, _, boss, _, _ := issueEnv(t, st)
	if err := st.SaveAgent("o1", model.Agent{ID: "Old_Agent", Name: "舊角色", Active: true, CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	if g, err := st.GetAgent("o1", "Old_Agent"); err != nil || g.ActorID != "" {
		t.Skipf("這個資料庫版本替不合規則的角色 id 也建了 actor(%v、%+v),無法重現", err, g)
	}
	before, _ := st.ListActors(store.ActorFilter{Kind: model.ActorKindSystem})
	p := &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "Old_Agent", TaskID: "T-1"}, User: model.User{ID: "agent:Old_Agent"}}
	_, err := svc.ListAgentTasks(p, "o1", service.TaskFilter{ReportTo: "me"})
	if err == nil {
		t.Fatal("角色沒有 actor 要回錯誤")
	}
	if !errors.Is(err, service.ErrAgentNoActor) && !errors.Is(err, service.ErrActorUnavailable) {
		t.Fatalf("要是明確的錯誤:%v", err)
	}
	after, _ := st.ListActors(store.ActorFilter{Kind: model.ActorKindSystem})
	if len(after) != len(before) {
		t.Fatalf("不能長出 sys_legacy ghost:%d → %d", len(before), len(after))
	}
	_ = boss
}

// LDAP:mail 沒有 @(例如被填成保留的本機身分 omixor-console)當作沒有 mail:退回 帳號@網域,仍沒有就不能建立;
// 絕不會建出 email 是 omixor-console 的使用者
func TestLDAPMailWithoutAtIsIgnored(t *testing.T) {
	svc, dir, st := ldapEnv(t)
	dir.users["evil"] = fakeEntry{pw: "pw-e", name: "Evil", mail: "omixor-console"}
	if _, err := svc.LoginWithLDAP("evil", "pw-e", "", "1.1.1.1"); service.Code(err) != 403 {
		t.Fatalf("mail 沒有 @ 又沒有網域:不能建立:%v", err)
	}
	if u, err := st.UserByEmail("omixor-console"); err == nil {
		t.Fatalf("不能建出本機 handle 的使用者:%+v", u)
	}
	svc.LDAPEmailDomain = "corp.test"
	res, err := svc.LoginWithLDAP("evil", "pw-e", "", "1.1.1.1")
	if err != nil || res.User.Email != "evil@corp.test" || res.User.Role != "member" {
		t.Fatalf("退回帳號@網域:%+v %v", res, err)
	}
	for k, m := range []string{"tim", "ops-web", "mcp", "a@", "@b", "a@b@c"} {
		name := "bad" + string(rune('a'+k))
		dir.users[name] = fakeEntry{pw: "p", name: "X", mail: m}
		r, err := svc.LoginWithLDAP(name, "p", "", "1.1.1.1")
		if err != nil || r.User.Email != name+"@corp.test" {
			t.Fatalf("mail %q:%+v %v", m, r, err)
		}
	}
}

// 對外入口(新增使用者、加組織成員)也擋沒有 @ 與保留的本機 handle
func TestUserEmailEntryPoints(t *testing.T) {
	st := storetest.New(t)
	svc, root, boss, _, _ := issueEnv(t, st)
	for _, e := range []string{"omixor-console", "ops-web", "mcp", "tim", "noat", "a@", "@b"} {
		if _, err := svc.SaveUser(root, e, nil, nil); service.Code(err) != 400 {
			t.Fatalf("SaveUser(%q) 要 400:%v", e, err)
		}
		if _, err := svc.SaveMember(boss, "o1", e, "member"); service.Code(err) != 400 {
			t.Fatalf("SaveMember(%q) 要 400:%v", e, err)
		}
	}
	if _, err := svc.SaveUser(root, "ok@x.example", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !service.ReservedUserHandle(" Omixor-Console ") || service.ReservedUserHandle("alice@x") {
		t.Fatal("ReservedUserHandle")
	}
}
