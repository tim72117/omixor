package service_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// present:actor id 批次查名稱,輸出舊格式字串與 actor 物件
func TestPresentLegacyAndRef(t *testing.T) {
	svc, st, boss := actorEnv(t)
	cto := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	carol, err := st.ResolveLegacy("o1", "carol@x") // 未註冊使用者
	if err != nil {
		t.Fatal(err)
	}
	mcp, _ := st.ResolveLegacy("o1", "mcp") // 沒有本機使用者:sys_legacy_mcp
	ghost, _ := st.ResolveLegacy("o1", "agent:gone")
	odd, _ := st.ResolveLegacy("o1", "冒名")
	blank, _ := st.ResolveLegacy("o1", "")
	if blank != "" {
		t.Fatal("空字串是 NULL")
	}
	const missing = "usr_00000000-0000-7000-8000-000000000000"

	n, err := svc.LoadActors(boss.Actor.ID, cto.ID, carol, "sys_omixor", "sys_routine", "sys_chain", mcp, ghost, odd, "", boss.Actor.ID, missing)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, what string
		f        model.LegacyForm
		want     string
	}{
		{boss.Actor.ID, "註冊使用者(email 取使用者列,保留大小寫)", model.LegacyFree, "Boss@X"},
		{boss.Actor.ID, "節點", model.LegacyNode, "user:Boss@X"},
		{boss.Actor.ID, "稽核 userId", model.LegacyUserID, "u1"},
		{boss.Actor.ID, "assignee 只有角色", model.LegacyAgentID, ""},
		{cto.ID, "角色", model.LegacyFree, "agent:cto"},
		{cto.ID, "角色節點", model.LegacyNode, "agent:cto"},
		{cto.ID, "assignee", model.LegacyAgentID, "cto"},
		{cto.ID, "稽核", model.LegacyUserID, "agent:cto"},
		{carol, "未註冊使用者用 handle", model.LegacyFree, "carol@x"},
		{carol, "未註冊使用者節點", model.LegacyNode, "user:carol@x"},
		{"sys_omixor", "系統", model.LegacyFree, "omixor"},
		{"sys_routine", "例行", model.LegacyFree, "routine"},
		{"sys_chain", "組織鏈", model.LegacyFree, "chain"},
		{mcp, "舊 mcp 署名", model.LegacyFree, "mcp"},
		{ghost, "已刪角色的舊署名原樣", model.LegacyFree, "agent:gone"},
		{ghost, "已刪角色當 assignee", model.LegacyAgentID, "agent:gone"},
		{odd, "對不上的舊值原樣", model.LegacyFree, "冒名"},
		{"", "空", model.LegacyFree, ""},
		{missing, "不存在", model.LegacyFree, ""},
	} {
		if got := n.Legacy(c.id, c.f); got != c.want {
			t.Errorf("%s:Legacy(%q, %v) = %q,要 %q", c.what, c.id, c.f, got, c.want)
		}
	}
	if r := n.Ref(cto.ID); r == nil || r.ID != cto.ID || r.Kind != model.ActorKindAgent || r.Handle != "cto" || r.Name != "研發長" {
		t.Fatalf("Ref:%+v", r)
	}
	if n.Ref("") != nil || n.Ref(missing) != nil {
		t.Fatal("空與不存在的 id 回 nil")
	}
	if z := n.RefOrZero(""); z.ID != "" {
		t.Fatal("RefOrZero", z)
	}
	for _, c := range []struct{ id, kind, who string }{
		{boss.Actor.ID, "person", "Boss@X"}, {cto.ID, "ai", "cto"}, {"sys_omixor", "system", ""}, {mcp, "ai", "mcp"}, {odd, "ai", "冒名"}, {"", "", ""},
	} {
		if k, w := n.EventActor(c.id); k != c.kind || w != c.who {
			t.Errorf("EventActor(%q) = %s/%s,要 %s/%s", c.id, k, w, c.kind, c.who)
		}
	}
	// 零值的 Names 什麼都查不到、不 panic
	var nn *service.Names
	if nn.Ref("x") != nil || nn.Legacy("x", model.LegacyFree) != "" {
		t.Fatal("nil 的 Names 什麼都查不到")
	}
	// 單筆
	l, ref, err := svc.Present(cto.ID, model.LegacyFree)
	if err != nil || l != "agent:cto" || ref == nil || ref.Handle != "cto" {
		t.Fatalf("Present:%q %+v %v", l, ref, err)
	}
	if ids := model.ActorIDs("a", "", "b", "a", "c", "b"); len(ids) != 3 || ids[0] != "a" || ids[2] != "c" {
		t.Fatal("ActorIDs 去重、濾空、保留順序", ids)
	}
}
