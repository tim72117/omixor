package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// actorEnv 一個組織 o1、一位管理員 boss(有 token)、一位成員 amy、角色 cto、pm、同名衝突用的 dup(角色與使用者都叫 dup)
func actorEnv(t *testing.T) (*service.Service, *store.PG, *model.Principal) {
	t.Helper()
	st := storetest.New(t)
	svc := &service.Service{Store: st, TTL: time.Hour, LoginPerIP: 1000, LoginTotal: 1000}
	now := model.Now()
	if err := st.CreateOrg(model.Org{ID: "o1", Name: "組織一", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ id, email, role string }{{"u1", "Boss@X", "admin"}, {"u2", "amy@x", "member"}, {"u3", "dup", "member"}} {
		if err := st.SaveUser(model.User{ID: u.id, Email: u.email, Name: strings.ToUpper(u.id), Role: u.role, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveMember(model.Member{Org: "o1", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateToken(model.Token{ID: "t_boss", UserID: "u1", Device: "Mac", CreatedAt: now, Hash: service.HashToken("omx_boss")}); err != nil {
		t.Fatal(err)
	}
	boss, err := svc.Authenticate("omx_boss")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "pm", Name: "產品經理"}, {ID: "dup", Name: "同名角色"}, {ID: "old", Name: "退役角色"}} {
		g.Active = true
		if _, err := svc.SaveAgent(boss, "o1", g); err != nil {
			t.Fatal(err)
		}
	}
	return svc, st, boss
}

func actorOf(t *testing.T, st *store.PG, kind model.ActorKind, org, handle string) model.ActorRecord {
	t.Helper()
	a, err := st.ActorByHandle(kind, org, handle)
	if err != nil {
		t.Fatalf("找不到 %s %s:%v", kind, handle, err)
	}
	return *a
}

// Authenticate 填 Actor 與 Via;停用的使用者仍是 403
func TestAuthenticateFillsActor(t *testing.T) {
	svc, st, boss := actorEnv(t)
	want := actorOf(t, st, model.ActorKindUser, "", "boss@x")
	if boss.Actor.ID != want.ID || boss.Actor.Kind != model.ActorKindUser || boss.Actor.Handle != "boss@x" || boss.Actor.Name != "U1" || boss.Via != model.ViaLogin {
		t.Fatalf("Actor:%+v via=%q", boss.Actor, boss.Via)
	}
	if boss.User.ActorID != want.ID {
		t.Fatalf("User.ActorID:%q", boss.User.ActorID)
	}
	// 停用:403,不給 actor
	_ = st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", Disabled: true, CreatedAt: model.Now()})
	_ = st.CreateToken(model.Token{ID: "t_amy", UserID: "u2", CreatedAt: model.Now(), Hash: service.HashToken("omx_amy")})
	if p, err := svc.Authenticate("omx_amy"); code(err) != 403 || p != nil {
		t.Fatalf("停用的使用者要 403:%v", err)
	}
	if _, err := svc.Authenticate("omx_nope"); code(err) != 401 {
		t.Fatal("無效 token 要 401", err)
	}
}

// 角色憑證:Actor 是角色 actor;角色停用後憑證失效
func TestAuthenticateAgentActor(t *testing.T) {
	svc, st, boss := actorEnv(t)
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	ap, err := svc.AuthenticateAgent("cto", res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	want := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	if ap.Actor.ID != want.ID || ap.Actor.Kind != model.ActorKindAgent || ap.Actor.Handle != "cto" || ap.Actor.Name != "研發長" || ap.Via != model.ViaAgentCredential {
		t.Fatalf("角色 Actor:%+v via=%q", ap.Actor, ap.Via)
	}
	if ap.Agent == nil || ap.Agent.AgentID != "cto" || ap.User.Email != "agent:cto" {
		t.Fatalf("暫時保留的偽裝 User 與 Agent 欄位:%+v", ap)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAgent("cto", res.Credential.Secret, "ip"); code(err) != 401 {
		t.Fatal("停用的角色憑證要 401", err)
	}
}

// 建立使用者(管理員加人、加成員)後 ActorID 都存在;email 已是未註冊使用者 actor 的接回同一個
func TestUserCreationHasActor(t *testing.T) {
	svc, st, boss := actorEnv(t)
	cases := []struct {
		name   string
		create func() (*model.User, error)
	}{
		{"SaveUser", func() (*model.User, error) { return svc.SaveUser(boss, "new1@x", nil, nil) }},
		{"SaveMember 順便建使用者", func() (*model.User, error) {
			if _, err := svc.SaveMember(boss, "o1", "new2@x", "member"); err != nil {
				return nil, err
			}
			return st.UserByEmail("new2@x")
		}},
	}
	for _, c := range cases {
		u, err := c.create()
		if err != nil {
			t.Fatalf("%s:%v", c.name, err)
		}
		if !model.ValidActorIDOf(model.ActorKindUser, u.ActorID) {
			t.Errorf("%s:傳回的 ActorID %q 不對", c.name, u.ActorID)
		}
		got, err := st.UserByID(u.ID)
		if err != nil || got.ActorID != u.ActorID {
			t.Errorf("%s:資料庫的 ActorID 和傳回的 %q 不同(%v)", c.name, u.ActorID, err)
		}
	}
	// 未註冊使用者 actor:之後那個 email 的人建使用者 → 接回同一個
	pre, err := st.EnsureUserActorByEmail("late@x", "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := svc.SaveUser(boss, "Late@X", nil, nil)
	if err != nil || u.ActorID != pre.ID {
		t.Fatalf("要接回未註冊使用者的 actor %s:%+v %v", pre.ID, u, err)
	}
	_ = st.CreateToken(model.Token{ID: "t_late", UserID: u.ID, CreatedAt: model.Now(), Hash: service.HashToken("omx_late")})
	if p, err := svc.Authenticate("omx_late"); err != nil || p.Actor.ID != pre.ID {
		t.Fatalf("%+v %v", p, err)
	}
}

// LDAP 登入建立的使用者有 ActorID,第二次登入不變;事先存在的未註冊使用者 actor 接回
func TestLDAPLoginHasActor(t *testing.T) {
	svc, _, st := ldapEnv(t)
	pre, err := st.EnsureUserActorByEmail("carol@corp.test", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ user, pw, wantActor string }{{"amy", "pw-amy", ""}, {"carol", "pw-c", pre.ID}} {
		r, err := svc.LoginWithLDAP(c.user, c.pw, "Mac", "1.1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		if !model.ValidActorIDOf(model.ActorKindUser, r.User.ActorID) || (c.wantActor != "" && r.User.ActorID != c.wantActor) {
			t.Fatalf("%s:ActorID %q", c.user, r.User.ActorID)
		}
		p, err := svc.Authenticate(r.Token)
		if err != nil || p.Actor.ID != r.User.ActorID || p.Via != model.ViaLogin {
			t.Fatalf("%s:%+v %v", c.user, p, err)
		}
		r2, err := svc.LoginWithLDAP(c.user, c.pw, "Mac", "1.1.1.1")
		if err != nil || r2.User.ActorID != r.User.ActorID {
			t.Fatalf("%s:第二次登入 actor 不能變:%+v %v", c.user, r2, err)
		}
	}
}

// 服務層建立角色:owner = 建立者的使用者 actor;沒有 actor 的 Principal 用 store 預設;已存在的角色不改 owner;BackfillAgentOwners 不動已有 owner 的
func TestSaveAgentOwner(t *testing.T) {
	svc, st, boss := actorEnv(t)
	for _, h := range []string{"cto", "pm"} {
		if a := actorOf(t, st, model.ActorKindAgent, "o1", h); a.Owner != boss.Actor.ID {
			t.Fatalf("%s 的 owner 要是建立者 %s,實際 %q", h, boss.Actor.ID, a.Owner)
		}
	}
	// 另一位組織管理員建立:owner 是他自己,不是預設(第一位管理員)
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u9", Email: "second@x", Role: "member", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "o1", UserID: "u9", Email: "second@x", Role: "admin", AddedAt: now})
	_ = st.CreateToken(model.Token{ID: "t_2", UserID: "u9", CreatedAt: now, Hash: service.HashToken("omx_second")})
	second, err := svc.Authenticate("omx_second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveAgent(second, "o1", model.Agent{ID: "qa", Name: "測試", Active: true}); err != nil {
		t.Fatal(err)
	}
	if a := actorOf(t, st, model.ActorKindAgent, "o1", "qa"); a.Owner != second.Actor.ID {
		t.Fatalf("owner:%q", a.Owner)
	}
	// 手工 Principal(沒有 Actor、User.ActorID 空):沿用預設 = 該組織第一位管理員(boss)
	manual := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveAgent(manual, "o1", model.Agent{ID: "ops", Name: "維運", Active: true}); err != nil {
		t.Fatal(err)
	}
	if a := actorOf(t, st, model.ActorKindAgent, "o1", "ops"); a.Owner != boss.Actor.ID {
		t.Fatalf("預設 owner 要是第一位管理員:%q", a.Owner)
	}
	// 修改既有角色不改 owner
	if _, err := svc.SaveAgent(second, "o1", model.Agent{ID: "cto", Name: "研發長2", Active: true}); err != nil {
		t.Fatal(err)
	}
	if a := actorOf(t, st, model.ActorKindAgent, "o1", "cto"); a.Owner != boss.Actor.ID || a.Name != "研發長2" {
		t.Fatalf("修改不改 owner:%+v", a)
	}
	if n, err := st.BackfillAgentOwners(); err != nil || n != 0 {
		t.Fatalf("Backfill 不該動已有 owner 的:%d %v", n, err)
	}
	if a := actorOf(t, st, model.ActorKindAgent, "o1", "qa"); a.Owner != second.Actor.ID {
		t.Fatal("Backfill 後 owner 被改了")
	}
}

// ResolveActor 表驅動
func TestResolveActor(t *testing.T) {
	svc, st, boss := actorEnv(t)
	cto := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	amy := actorOf(t, st, model.ActorKindUser, "", "amy@x")
	dupAgent := actorOf(t, st, model.ActorKindAgent, "o1", "dup")
	dupUser := actorOf(t, st, model.ActorKindUser, "", "dup")
	bossA := actorOf(t, st, model.ActorKindUser, "", "boss@x")
	// 先記下 old 的 actor id,再暫停它(暫停 ≠ 退役)
	old := actorOf(t, st, model.ActorKindAgent, "o1", "old")
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "old", Name: "退役角色", Active: false}); err != nil {
		t.Fatal(err)
	}
	// 另一個組織有同名角色 cto:o1 的解析不能指到它
	_ = st.CreateOrg(model.Org{ID: "o2", Name: "組織二", CreatedAt: model.Now()})
	if err := st.SaveAgent("o2", model.Agent{ID: "cto", Name: "別組織研發長", Active: true, CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	other := actorOf(t, st, model.ActorKindAgent, "o2", "cto")
	nf, amb := service.ErrActorNotFound, service.ErrActorAmbiguous

	cases := []struct {
		name, org, raw string
		want           string // actor id
		err            error
	}{
		{"actor id(角色)", "o1", cto.ID, cto.ID, nil},
		{"actor id 大寫", "o1", strings.ToUpper(cto.ID), cto.ID, nil},
		{"actor id(使用者)", "o1", amy.ID, amy.ID, nil},
		{"actor id(系統)", "o1", model.SystemActorOmixor, model.SystemActorOmixor, nil},
		{"別組織的角色 id", "o1", other.ID, "", nf},
		{"不存在的 actor id", "o1", "usr_00000000-0000-7000-8000-000000000000", "", nf},
		{"@handle(角色)", "o1", "@cto", cto.ID, nil},
		{"handle(角色)大小寫與空白", "o1", " CTO ", cto.ID, nil},
		{"agent: 前綴", "o1", "agent:CTO", cto.ID, nil},
		{"agent: 只找本組織", "o2", "agent:cto", other.ID, nil},
		{"email", "o1", "AMY@x", amy.ID, nil},
		{"@email", "o1", "@amy@x", amy.ID, nil},
		{"user: 前綴 email", "o1", "user:amy@X", amy.ID, nil},
		{"user: 前綴 handle", "o1", "user:dup", dupUser.ID, nil},
		{"同名要前綴", "o1", "dup", "", amb},
		{"同名 @ 也要前綴", "o1", "@dup", "", amb},
		{"同名 agent: 前綴", "o1", "agent:dup", dupAgent.ID, nil},
		{"o2 沒有同名角色,只剩使用者", "o2", "dup", dupUser.ID, nil},
		{"找不到(不建立)", "o1", "nobody", "", nf},
		{"找不到的 email", "o1", "ghost@x", "", nf},
		{"agent: 找不到(email 不是角色)", "o1", "agent:amy@x", "", nf},
		{"user: 找不到(角色名不是使用者)", "o1", "user:cto", "", nf},
		{"暫停的角色仍解析得到", "o1", "old", old.ID, nil},
		{"暫停的角色用 id", "o1", old.ID, old.ID, nil},
		{"空字串", "o1", "  ", "", service.ErrActorEmpty},
		{"只有 @", "o1", "@", "", nf},
		{"沒有組織只能找使用者", "", "cto", "", nf},
		{"另一位使用者 email", "o1", "boss@x", bossA.ID, nil},
	}
	before, _ := st.ListActors(store.ActorFilter{IncludeDisabled: true})
	for _, c := range cases {
		got, err := svc.ResolveActor(boss, c.org, c.raw)
		if c.err != nil {
			if !errors.Is(err, c.err) || service.Code(err) != 400 {
				t.Errorf("%s(%q):要 %v(400),實際 %+v %v", c.name, c.raw, c.err, got, err)
			}
			continue
		}
		if err != nil || got.ID != c.want || got.Handle == "" || got.Kind == "" {
			t.Errorf("%s(%q):要 %s,實際 %+v %v", c.name, c.raw, c.want, got, err)
		}
	}
	after, _ := st.ListActors(store.ActorFilter{IncludeDisabled: true})
	if len(after) != len(before) {
		t.Errorf("解析不能建立 actor:%d → %d", len(before), len(after))
	}
	// 停用的使用者仍解析得到(它佔著名字)
	_ = st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", Disabled: true, CreatedAt: model.Now()})
	if got, err := svc.ResolveActor(boss, "o1", "amy@x"); err != nil || got.ID != amy.ID {
		t.Errorf("停用的使用者:%+v %v", got, err)
	}
	// 錯誤訊息固定,不夾帶輸入
	_, err := svc.ResolveActor(boss, "o1", "SECRET-NAME")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("訊息不能夾帶輸入:%v", err)
	}
}

// ---- ISS-36 B-3 審查修正 ----

// scopeEnv 在 actorEnv 之外:另一個組織 o2、只在 o2 的使用者 zed(有 token)、amy(o1 成員)的 token
func scopeEnv(t *testing.T) (svc *service.Service, st *store.PG, boss, amy, zed *model.Principal) {
	t.Helper()
	svc, st, boss = actorEnv(t)
	now := model.Now()
	if err := st.CreateOrg(model.Org{ID: "o2", Name: "組織二", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUser(model.User{ID: "u4", Email: "zed@x", Name: "Z", Role: "member", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMember(model.Member{Org: "o2", UserID: "u4", Email: "zed@x", Role: "member", AddedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, user string }{{"t_amy", "u2"}, {"t_zed", "u4"}} {
		if err := st.CreateToken(model.Token{ID: c.id, UserID: c.user, CreatedAt: now, Hash: service.HashToken("omx_" + c.id)}); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if amy, err = svc.Authenticate("omx_t_amy"); err != nil {
		t.Fatal(err)
	}
	if zed, err = svc.Authenticate("omx_t_zed"); err != nil {
		t.Fatal(err)
	}
	return
}

// 授權範圍:非成員解析 = 與不存在完全相同的錯誤;本人、同組織成員、伺服器管理員、角色憑證、p 為 nil、沒有組織
func TestResolveActorScope(t *testing.T) {
	svc, st, boss, amy, zed := scopeEnv(t)
	amyA := actorOf(t, st, model.ActorKindUser, "", "amy@x")
	bossA := actorOf(t, st, model.ActorKindUser, "", "boss@x")
	zedA := actorOf(t, st, model.ActorKindUser, "", "zed@x")
	cto := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	pm := actorOf(t, st, model.ActorKindAgent, "o1", "pm")
	ctoCred := &model.Principal{User: model.User{ID: "agent:cto", Email: "agent:cto"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto"}, Actor: cto.Ref()}
	sys := model.SystemActorOmixor
	nf := service.ErrActorNotFound

	cases := []struct {
		name string
		p    *model.Principal
		org  string
		raw  string
		want string // actor id;空 = 找不到
	}{
		{"非成員用 email 找別組織的人", zed, "o1", "amy@x", ""},
		{"非成員用 usr_ id 找別組織的人", zed, "o1", amyA.ID, ""},
		{"非成員用 user: 前綴", zed, "o1", "user:amy@x", ""},
		{"非成員找該組織的角色", zed, "o1", "cto", ""},
		{"非成員找該組織的角色 id", zed, "o1", cto.ID, ""},
		{"非成員 agent: 前綴", zed, "o1", "agent:cto", ""},
		{"非成員在自己的組織找 o1 的人", zed, "o2", "amy@x", ""},
		{"本人(email)", zed, "o1", "zed@x", zedA.ID},
		{"本人(id)", zed, "o1", zedA.ID, zedA.ID},
		{"本人、沒有組織", zed, "", "zed@x", zedA.ID},
		{"本人、沒有組織(id)", zed, "", zedA.ID, zedA.ID},
		{"沒有組織:別人找不到", amy, "", "boss@x", ""},
		{"沒有組織:別人 id 找不到", amy, "", bossA.ID, ""},
		{"同組織成員互相找得到", amy, "o1", "boss@x", bossA.ID},
		{"同組織成員找角色", amy, "o1", "cto", cto.ID},
		{"成員找不是這個組織成員的人", amy, "o1", "zed@x", ""},
		{"成員用別的組織 org 找人", amy, "o2", "zed@x", ""},
		{"伺服器管理員可跨組織(找 o2 的成員)", boss, "o2", "zed@x", zedA.ID},
		{"伺服器管理員可跨組織(沒有組織)", boss, "", "zed@x", zedA.ID},
		{"伺服器管理員用 id", boss, "o1", zedA.ID, zedA.ID},
		{"角色憑證找本組織成員", ctoCred, "o1", "amy@x", amyA.ID},
		{"角色憑證找別組織的人", ctoCred, "o2", "zed@x", ""},
		{"角色憑證找別組織成員 id(org 給 o1)", ctoCred, "o1", zedA.ID, ""},
		{"角色憑證找本組織角色", ctoCred, "o1", "pm", pm.ID},
		{"角色憑證在別組織找角色", ctoCred, "o2", "cto", ""},
		{"p 是 nil:使用者找不到", nil, "o1", "amy@x", ""},
		{"p 是 nil:使用者 id 找不到", nil, "o1", amyA.ID, ""},
		{"p 是 nil:角色找不到", nil, "o1", "cto", ""},
		{"p 是 nil:角色 id 找不到", nil, "o1", cto.ID, ""},
		{"p 是 nil:系統 actor 可以", nil, "o1", sys, sys},
		{"一般使用者:系統 actor 可以", zed, "", sys, sys},
	}
	for _, c := range cases {
		got, err := svc.ResolveActor(c.p, c.org, c.raw)
		if c.want == "" {
			// 找不到:錯誤要和不存在的輸入「同一個」錯誤、同狀態碼、同文字
			if got.ID != "" || !errors.Is(err, nf) || err.Error() != nf.Msg || service.Code(err) != 400 {
				t.Errorf("%s(%q):要找不到,實際 %+v %v", c.name, c.raw, got, err)
			}
			continue
		}
		if err != nil || got.ID != c.want {
			t.Errorf("%s(%q):要 %s,實際 %+v %v", c.name, c.raw, c.want, got, err)
		}
	}
	// 非成員對「存在的 email」與「不存在的 email」的回應完全一致(不能探測存在性)
	_, e1 := svc.ResolveActor(zed, "o1", "amy@x")
	_, e2 := svc.ResolveActor(zed, "o1", "nobody-here@x")
	if e1 == nil || e2 == nil || e1 != e2 || e1.Error() != e2.Error() || service.Code(e1) != service.Code(e2) {
		t.Errorf("存在與不存在的回應要完全一致:%v / %v", e1, e2)
	}
}

// 所有錯誤種類的訊息都不夾帶輸入
func TestResolveActorErrorsDoNotEchoInput(t *testing.T) {
	svc, st, boss, _, zed := scopeEnv(t)
	cto := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	amyA := actorOf(t, st, model.ActorKindUser, "", "amy@x")
	ghost, err := st.EnsureUserActorByEmail("ghost-xyz@x", "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		p        *model.Principal
		org, raw string
	}{
		{boss, "o1", "   "},                                      // ErrActorEmpty
		{boss, "o1", "SECRET-NAME"},                              // 找不到(名稱)
		{boss, "o1", "SECRET-user@x"},                            // 找不到(email)
		{boss, "o1", "agent:SECRET-AGENT"},                       // 找不到(agent:)
		{boss, "o1", "user:SECRET-USER"},                         // 找不到(user:)
		{boss, "o1", "@SECRET-AT"},                               // 找不到(@)
		{boss, "o1", "usr_00000000-0000-7000-8000-5ec2e7000000"}, // 找不到(id)
		{boss, "o1", ghost.ID},                                   // 未註冊使用者
		{boss, "o1", "ghost-xyz@x"},                              // 未註冊使用者(email)
		{boss, "o1", "dup"},                                      // 不明確
		{zed, "o1", "amy@x"},                                     // 沒權限
		{zed, "o1", amyA.ID},                                     // 沒權限(id)
		{zed, "o1", cto.ID},                                      // 沒權限(角色 id)
		{nil, "o1", "amy@x"},                                     // p 為 nil
	}
	for _, c := range cases {
		_, err := svc.ResolveActor(c.p, c.org, c.raw)
		if err == nil {
			t.Errorf("%q:要有錯誤", c.raw)
			continue
		}
		var se *service.Error
		if !errors.As(err, &se) || service.Code(err) != 400 {
			t.Errorf("%q:要是固定的 400 錯誤:%v", c.raw, err)
			continue
		}
		msg := strings.ToLower(err.Error())
		for _, leak := range []string{"secret", "ghost", "amy@x", "usr_", "agt_", "5ec2e7", "cto", strings.ToLower(strings.TrimSpace(c.raw))} {
			if leak != "" && strings.Contains(msg, leak) {
				t.Errorf("%q:訊息夾帶了 %q:%s", c.raw, leak, err)
			}
		}
	}
}

// 「未註冊使用者」actor(沒有 omixor_users 列)解析不到;之後那個 email 註冊(SaveUser)就能解析,actor id 不變
func TestResolveActorUnregisteredUser(t *testing.T) {
	svc, st, boss := actorEnv(t)
	pre, err := st.EnsureUserActorByEmail("later@x", "")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.ListActors(store.ActorFilter{IncludeDisabled: true})
	for _, raw := range []string{"later@x", "@LATER@x", "user:later@x", pre.ID} {
		if _, err := svc.ResolveActor(boss, "o1", raw); !errors.Is(err, service.ErrActorNotFound) {
			t.Errorf("未註冊使用者 %q 要找不到:%v", raw, err)
		}
	}
	// 即使是伺服器管理員、org 為空也一樣
	if _, err := svc.ResolveActor(boss, "", pre.ID); !errors.Is(err, service.ErrActorNotFound) {
		t.Errorf("伺服器管理員也不能解析未註冊使用者:%v", err)
	}
	if after, _ := st.ListActors(store.ActorFilter{IncludeDisabled: true}); len(after) != len(before) {
		t.Errorf("解析不能建立 actor:%d → %d", len(before), len(after))
	}
	u, err := svc.SaveUser(boss, "Later@X", nil, nil)
	if err != nil || u.ActorID != pre.ID {
		t.Fatalf("註冊要接回同一個 actor:%+v %v", u, err)
	}
	for _, raw := range []string{"later@x", pre.ID} {
		got, err := svc.ResolveActor(boss, "o1", raw)
		if err != nil || got.ID != pre.ID {
			t.Errorf("註冊後 %q 要解析得到同一個 actor %s:%+v %v", raw, pre.ID, got, err)
		}
	}
}

// 暫停(Active=false)與退役(角色已刪)分開:暫停仍解析得到、ActorRef 不變;退役解析不到;刪除後以同 id 重建 = 新 actor,舊 id 解析不到
func TestResolveActorSuspendedVsRetired(t *testing.T) {
	svc, st, boss := actorEnv(t)
	cto := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	want := cto.Ref()
	// 暫停
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: false}); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.ActorByID(cto.ID); a == nil || !a.Disabled() {
		t.Fatalf("前提:暫停後 actor 的 disabled_at 要有值:%+v", a)
	}
	for _, raw := range []string{"cto", "@cto", "agent:cto", cto.ID} {
		got, err := svc.ResolveActor(boss, "o1", raw)
		if err != nil || got != want {
			t.Errorf("暫停的角色 %q 要解析得到且 ActorRef 不變 %+v:%+v %v", raw, want, got, err)
		}
	}
	// 退役:角色已刪
	if err := svc.DeleteAgent(boss, "o1", "cto"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"cto", "agent:cto", cto.ID} {
		if _, err := svc.ResolveActor(boss, "o1", raw); !errors.Is(err, service.ErrActorNotFound) {
			t.Errorf("退役的角色 %q 要找不到:%v", raw, err)
		}
	}
	// 同 id 重建:新 actor;舊 actor id 仍然找不到
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "新研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	fresh := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	if fresh.ID == cto.ID {
		t.Fatal("前提:重建要是新的 actor")
	}
	if got, err := svc.ResolveActor(boss, "o1", "cto"); err != nil || got.ID != fresh.ID || got.Name != "新研發長" {
		t.Errorf("重建後用 handle 要得到新的:%+v %v", got, err)
	}
	if got, err := svc.ResolveActor(boss, "o1", fresh.ID); err != nil || got.ID != fresh.ID {
		t.Errorf("重建後新 id:%+v %v", got, err)
	}
	if _, err := svc.ResolveActor(boss, "o1", cto.ID); !errors.Is(err, service.ErrActorNotFound) {
		t.Errorf("重建後舊的 actor id 要找不到:%v", err)
	}
}

// flakyStore 讓指定的讀取失敗(訊息含 boom-secret,用來確認不會漏到對外的錯誤)
type flakyStore struct {
	store.Store
	failUserByID, failActorByID, failEnsure bool
}

var errBoom = errors.New("boom-secret: connection to 10.0.0.1 refused")

func (f *flakyStore) UserByID(id string) (*model.User, error) {
	if f.failUserByID {
		return nil, errBoom
	}
	return f.Store.UserByID(id)
}

func (f *flakyStore) ActorByID(id string) (*model.ActorRecord, error) {
	if f.failActorByID {
		return nil, errBoom
	}
	return f.Store.ActorByID(id)
}

func (f *flakyStore) EnsureUserActor(id string) (*model.ActorRecord, error) {
	if f.failEnsure {
		return nil, errBoom
	}
	return f.Store.EnsureUserActor(id)
}

// sqlEnv 同 storetest.New,另外給一條可以直接下 SQL 的連線(造出沒有 actor_id 的使用者列)
func sqlEnv(t *testing.T) (*store.PG, *pgx.Conn) {
	t.Helper()
	u := storetest.URL(t)
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	st, err := store.AttachMigratedPG(u + sep + "pool_max_conns=4")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	conn, err := pgx.Connect(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return st, conn
}

// Authenticate 補建分支:使用者列沒有 actor_id(滾動部署時舊版寫的)→ 接回同一個 actor、並寫回使用者列;
// 補建失敗回固定的 ErrActorUnavailable(不洩漏細節),修好之後下一次請求就恢復
func TestAuthenticateBackfillsActor(t *testing.T) {
	st, conn := sqlEnv(t)
	now := model.Now()
	if err := st.SaveUser(model.User{ID: "u1", Email: "old@x", Name: "Old", Role: "member", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateToken(model.Token{ID: "t1", UserID: "u1", CreatedAt: now, Hash: service.HashToken("omx_old")}); err != nil {
		t.Fatal(err)
	}
	orig := actorOf(t, st, model.ActorKindUser, "", "old@x")
	if _, err := conn.Exec(context.Background(), `update omixor_users set actor_id = null where id = 'u1'`); err != nil {
		t.Fatalf("造出沒有 actor_id 的使用者列:%v", err)
	}
	if u, _ := st.UserByID("u1"); u == nil || u.ActorID != "" {
		t.Fatalf("前提:使用者列要沒有 actor_id:%+v", u)
	}

	// 補建失敗:固定句子,不含細節
	fs := &flakyStore{Store: st, failEnsure: true}
	svc := &service.Service{Store: fs, TTL: time.Hour}
	p, err := svc.Authenticate("omx_old")
	if p != nil || !errors.Is(err, service.ErrActorUnavailable) || service.Code(err) != 500 || strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "10.0.0.1") {
		t.Fatalf("補建失敗要回固定的 ErrActorUnavailable:%+v %v", p, err)
	}
	// 修好:補建成功,接回原本那個 actor,寫回使用者列
	fs.failEnsure = false
	p, err = svc.Authenticate("omx_old")
	if err != nil || p.Actor.ID != orig.ID || p.Actor.Handle != "old@x" {
		t.Fatalf("補建要接回同一個 actor %s:%+v %v", orig.ID, p, err)
	}
	if u, _ := st.UserByID("u1"); u == nil || u.ActorID != orig.ID {
		t.Fatalf("補建後使用者列要有 actor_id:%+v", u)
	}
}

// 角色驗證讀 actor 失敗:AuthenticateAgent、claimedAgent(經 WorkerReport)都是同一個固定的哨兵錯誤
func TestAgentActorReadFailureIsSentinel(t *testing.T) {
	svc, st, boss := actorEnv(t)
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	fsvc := &service.Service{Store: &flakyStore{Store: st, failActorByID: true}, TTL: time.Hour, LoginPerIP: 1000, LoginTotal: 1000}
	_, err = fsvc.AuthenticateAgent("cto", res.Credential.Secret, "ip")
	if err != service.ErrAuthInternal || service.Code(err) != 500 || strings.Contains(err.Error(), "boom") {
		t.Fatalf("AuthenticateAgent:%v", err)
	}
	_, err = fsvc.WorkerReport(boss, "o1", tk.ID, res.Credential.Code, service.TaskReport{})
	if err != service.ErrAuthInternal {
		t.Fatalf("claimedAgent:%v", err)
	}
}

// saveUser 已存之後重讀失敗:不回錯誤,ActorID 由 EnsureUserActor 補;連它也失敗仍回「已存」、ActorID 留空,下次 Authenticate 補建
func TestSaveUserRereadFailure(t *testing.T) {
	_, st, boss := actorEnv(t)
	fs := &flakyStore{Store: st, failUserByID: true}
	svc := &service.Service{Store: fs, TTL: time.Hour}
	u, err := svc.SaveUser(boss, "reread@x", nil, nil)
	if err != nil || !model.ValidActorIDOf(model.ActorKindUser, u.ActorID) {
		t.Fatalf("重讀失敗要補到 ActorID:%+v %v", u, err)
	}
	if got, err := st.UserByEmail("reread@x"); err != nil || got.ActorID != u.ActorID {
		t.Fatalf("資料要已存、actor 一致:%+v %v", got, err)
	}
	fs.failEnsure = true
	u2, err := svc.SaveUser(boss, "reread2@x", nil, nil)
	if err != nil || u2 == nil || u2.Email != "reread2@x" {
		t.Fatalf("兩條都失敗仍要回已存的結果:%+v %v", u2, err)
	}
	if u2.ActorID != "" {
		t.Fatalf("兩條都失敗時 ActorID 留空:%q", u2.ActorID)
	}
	got, err := st.UserByEmail("reread2@x")
	if err != nil || !model.ValidActorIDOf(model.ActorKindUser, got.ActorID) {
		t.Fatalf("SaveUser 本身已在同一個交易建立 actor:%+v %v", got, err)
	}
}

// GET /actors 的服務層:範圍、狀態、篩選、不洩漏
func TestListOrgActors(t *testing.T) {
	svc, st, boss := actorEnv(t)
	now := model.Now()
	// 另一個組織 o2 與只在 o2 的成員、角色;未註冊使用者 actor;o1 的暫停與退役角色
	if err := st.CreateOrg(model.Org{ID: "o2", Name: "組織二", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUser(model.User{ID: "u9", Email: "zed@x", Name: "Zed", Role: "member", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMember(model.Member{Org: "o2", UserID: "u9", Email: "zed@x", Role: "member", AddedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveAgent(boss, "o2", model.Agent{ID: "spy", Name: "他組織角色", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureUserActorByEmail("ghost@x", "鬼"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "pm", Name: "產品經理", Active: false}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteAgent(boss, "o1", "old"); err != nil {
		t.Fatal(err)
	}

	byHandle := func(l []service.ActorListItem) map[string]service.ActorListItem {
		m := map[string]service.ActorListItem{}
		for _, it := range l {
			m[string(it.Kind)+":"+it.Handle] = it
		}
		return m
	}
	l, err := svc.ListOrgActors(boss, "o1", service.ActorListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	m := byHandle(l)
	for _, k := range []string{"user:boss@x", "user:amy@x", "user:dup", "agent:cto", "agent:pm", "agent:dup", "system:omixor"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺 %s:%+v", k, l)
		}
	}
	for _, k := range []string{"user:zed@x", "user:ghost@x", "agent:spy", "agent:old"} {
		if _, ok := m[k]; ok {
			t.Errorf("不該出現 %s", k)
		}
	}
	if it := m["user:amy@x"]; it.Email != "amy@x" || it.Status != service.ActorStatusActive || !model.ValidActorID(it.ID) {
		t.Errorf("amy:%+v", it)
	}
	if m["agent:pm"].Status != service.ActorStatusSuspended || m["agent:cto"].Status != service.ActorStatusActive {
		t.Errorf("角色狀態:%+v %+v", m["agent:pm"], m["agent:cto"])
	}
	if it := m["system:omixor"]; it.Email != "" || it.Status != service.ActorStatusActive {
		t.Errorf("system:%+v", it)
	}
	// 退役要明確要求
	if l, _ := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Status: "retired"}); len(l) != 1 || l[0].Handle != "old" || l[0].Status != service.ActorStatusRetired {
		t.Errorf("retired:%+v", l)
	}
	if l, _ := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Status: "all", Kind: "agent"}); len(l) != 4 {
		t.Errorf("all agent:%+v", l)
	}
	// 篩選
	if l, _ := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Kind: "agent", Q: "@C"}); len(l) != 1 || l[0].Handle != "cto" {
		t.Errorf("q:%+v", l)
	}
	if l, _ := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Kind: "user", Q: "am"}); len(l) != 1 || l[0].Handle != "amy@x" {
		t.Errorf("user q:%+v", l)
	}
	if l, _ := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Q: "產品"}); len(l) != 1 || l[0].Handle != "pm" {
		t.Errorf("name q:%+v", l)
	}
	if _, err := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Kind: "x"}); err == nil {
		t.Error("kind 不合法要錯")
	}
	if _, err := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Status: "x"}); err == nil {
		t.Error("status 不合法要錯")
	}
	// 停用的使用者
	if err := st.SaveUser(model.User{ID: "u2", Email: "amy@x", Name: "U2", Role: "member", Disabled: true, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.ListOrgActors(boss, "o1", service.ActorListFilter{Status: "disabled"}); len(l) != 1 || l[0].Handle != "amy@x" {
		t.Errorf("disabled:%+v", l)
	}
	// 非成員、角色憑證不能列;o2 成員看不到 o1
	zed := &model.Principal{User: model.User{ID: "u9", Email: "zed@x", Role: "member"}}
	if _, err := svc.ListOrgActors(zed, "o1", service.ActorListFilter{}); err == nil {
		t.Error("非成員不能列")
	}
	zl, err := svc.ListOrgActors(zed, "o2", service.ActorListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range zl {
		if it.Handle == "cto" || it.Handle == "amy@x" {
			t.Errorf("o2 不該看到 o1 的 %+v", it)
		}
	}
	if _, err := svc.ListOrgActors(&model.Principal{User: model.User{ID: "agent:cto"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto"}}, "o1", service.ActorListFilter{}); err == nil {
		t.Error("角色憑證不能列")
	}
}
