package service_test

import (
	"errors"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// memberP 成員 amy 的 Principal(用 token 驗證出來,Actor 已填)
func memberP(t *testing.T, svc *service.Service, st store.Store) *model.Principal {
	t.Helper()
	if err := st.CreateToken(model.Token{ID: "t_amy2", UserID: "u2", CreatedAt: model.Now(), Hash: service.HashToken("omx_amy2")}); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Authenticate("omx_amy2")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// agentP 角色的憑證身分(Principal.Agent 與 Actor)
func credP(t *testing.T, svc *service.Service, boss *model.Principal, handle string) *model.Principal {
	t.Helper()
	ref, err := svc.ResolveActor(boss, "o1", "agent:"+handle)
	if err != nil {
		t.Fatal(err)
	}
	return &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: handle}, Actor: ref, Via: model.ViaAgentCredential}
}

func TestSelfActorAndWhoOrSelf(t *testing.T) {
	svc, st, boss := actorEnv(t)
	amy := memberP(t, svc, st)
	self, err := svc.SelfActor(boss)
	if err != nil || self.ID != boss.Actor.ID {
		t.Fatalf("管理員本人:%+v %v", self, err)
	}
	// Actor 沒填的手工 Principal:由使用者列補
	bare := &model.Principal{User: boss.User}
	bare.User.ActorID = ""
	if got, err := svc.SelfActor(bare); err != nil || got.ID != boss.Actor.ID {
		t.Fatalf("沒有 Actor 的 Principal 要由使用者列補:%+v %v", got, err)
	}
	if _, err := svc.SelfActor(nil); !errors.Is(err, service.ErrActorEmpty) {
		t.Fatal("nil 要 ErrActorEmpty", err)
	}
	if _, err := svc.SelfActor(&model.Principal{}); !errors.Is(err, service.ErrActorEmpty) {
		t.Fatal("空的 Principal 要 ErrActorEmpty", err)
	}
	cto := credP(t, svc, boss, "cto")
	if got, err := svc.SelfActor(cto); err != nil || got.Kind != model.ActorKindAgent || got.Handle != "cto" {
		t.Fatalf("角色憑證本人是角色 actor:%+v %v", got, err)
	}
	// 沒有 Actor 的角色 Principal:由角色表補
	noActor := &model.Principal{Agent: cto.Agent}
	if got, err := svc.SelfActor(noActor); err != nil || got.ID != cto.Actor.ID {
		t.Fatalf("角色要由角色表補 actor:%+v %v", got, err)
	}

	// WhoOrSelf:空 = 本人(種類要符合),給了就解析
	if got, err := svc.WhoOrSelf(amy, "o1", "  ", service.WantUser); err != nil || got.ID != amy.Actor.ID {
		t.Fatalf("空白是本人:%+v %v", got, err)
	}
	if _, err := svc.WhoOrSelf(amy, "o1", "", service.WantAgent); !errors.Is(err, service.ErrActorKind) {
		t.Fatal("本人是使用者,只收角色要 ErrActorKind", err)
	}
	if got, err := svc.WhoOrSelf(amy, "o1", "agent:cto", service.WantAgent); err != nil || got.Handle != "cto" {
		t.Fatalf("角色:%+v %v", got, err)
	}
	if _, err := svc.WhoOrSelf(amy, "o1", "cto", service.WantUser); !errors.Is(err, service.ErrActorKind) {
		t.Fatal("cto 是角色,只收使用者要 ErrActorKind", err)
	}
	if _, err := svc.ResolveWho(amy, "o1", "nobody", service.WantAny); !errors.Is(err, service.ErrActorNotFound) {
		t.Fatal("找不到要 ErrActorNotFound", err)
	}
	if _, err := svc.ResolveWho(amy, "o1", "", service.WantAny); !errors.Is(err, service.ErrActorEmpty) {
		t.Fatal("空字串要 ErrActorEmpty", err)
	}
	if got, err := svc.ResolveWho(amy, "o1", "sys_omixor", service.WantSystem); err != nil || got.Kind != model.ActorKindSystem {
		t.Fatalf("系統 actor 用 id:%+v %v", got, err)
	}
	if !service.IsActorInputError(service.ErrActorKind) || service.IsActorInputError(errors.New("x")) {
		t.Fatal("IsActorInputError")
	}
}

// 署名規則:呼叫者本人預設;管理員代署名只能代角色與系統(D9);角色憑證與一般成員只能是自己
func TestResolveAuthorRules(t *testing.T) {
	svc, st, boss := actorEnv(t)
	amy := memberP(t, svc, st)
	cto := credP(t, svc, boss, "cto")

	ok := func(who string, p *model.Principal, given string, mode service.AuthorMode, wantKind model.ActorKind, wantHandle string) {
		t.Helper()
		got, err := svc.ResolveAuthor(p, "o1", given, mode)
		if err != nil || got.Kind != wantKind || got.Handle != wantHandle {
			t.Fatalf("%s given=%q:%+v %v(要 %s %s)", who, given, got, err, wantKind, wantHandle)
		}
	}
	bad := func(who string, p *model.Principal, given string, mode service.AuthorMode, want error) {
		t.Helper()
		if _, err := svc.ResolveAuthor(p, "o1", given, mode); !errors.Is(err, want) {
			t.Fatalf("%s given=%q 要 %v,拿到 %v", who, given, want, err)
		}
	}
	// 管理員
	ok("管理員", boss, "", service.AuthorIgnore, model.ActorKindUser, "boss@x")
	ok("管理員", boss, "boss@x", service.AuthorReject, model.ActorKindUser, "boss@x")
	ok("管理員", boss, "agent:cto", service.AuthorReject, model.ActorKindAgent, "cto")
	ok("管理員", boss, "pm", service.AuthorIgnore, model.ActorKindAgent, "pm")
	ok("管理員", boss, "sys_omixor", service.AuthorReject, model.ActorKindSystem, "omixor")
	// 不能代別的使用者:嚴格模式 403 / 400;相容模式(AuthorIgnore,D9 的相容修正)靜默當本人,不會變成別人的署名
	ok("管理員", boss, "amy@x", service.AuthorIgnore, model.ActorKindUser, "boss@x")
	ok("管理員", boss, "冒名", service.AuthorIgnore, model.ActorKindUser, "boss@x")
	ok("管理員", boss, "mcp", service.AuthorIgnore, model.ActorKindUser, "boss@x")
	bad("管理員", boss, "amy@x", service.AuthorReject, service.ErrActorImpersonate)
	bad("管理員", boss, "冒名", service.AuthorReject, service.ErrActorNotFound) // 不能再傳自由字串
	// 一般成員:Ignore 靜默當本人;Reject 給了別人 403;給自己照給
	ok("成員", amy, "", service.AuthorReject, model.ActorKindUser, "amy@x")
	ok("成員", amy, "冒名", service.AuthorIgnore, model.ActorKindUser, "amy@x")
	ok("成員", amy, "agent:cto", service.AuthorIgnore, model.ActorKindUser, "amy@x")
	ok("成員", amy, "amy@x", service.AuthorReject, model.ActorKindUser, "amy@x")
	bad("成員", amy, "agent:cto", service.AuthorReject, service.ErrActorImpersonate)
	bad("成員", amy, "boss@x", service.AuthorReject, service.ErrActorImpersonate)
	// 角色憑證:只能是自己
	ok("角色", cto, "", service.AuthorReject, model.ActorKindAgent, "cto")
	ok("角色", cto, "agent:cto", service.AuthorReject, model.ActorKindAgent, "cto")
	ok("角色", cto, "agent:pm", service.AuthorIgnore, model.ActorKindAgent, "cto")
	bad("角色", cto, "agent:pm", service.AuthorReject, service.ErrActorImpersonate)
	bad("角色", cto, "boss@x", service.AuthorReject, service.ErrActorImpersonate)
	if e := service.ErrActorImpersonate; e.Code != 403 {
		t.Fatal("代署名要 403", e.Code)
	}
}
