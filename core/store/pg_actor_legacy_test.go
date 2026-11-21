package store

// 寬鬆解析器(ISS-36 C-1;DOC-13 §1.5):純邏輯用假的查表單元測試(不需要資料庫);omixor_actor_aliases 的建立;PG 版的 ResolveLegacy

import (
	"strings"
	"testing"

	"omixor-core/model"
)

// fakeLookup 記憶體版的查表
type fakeLookup struct {
	actors  map[string]bool
	users   map[string]string // handle → actor id
	userIDs map[string]string
	agents  map[string][2]string // org|id → [current, retired]
	aliases map[string]string
	local   LegacyLocal
}

func newFake() *fakeLookup {
	return &fakeLookup{actors: map[string]bool{"sys_omixor": true, "sys_routine": true, "sys_chain": true},
		users: map[string]string{}, userIDs: map[string]string{}, agents: map[string][2]string{}, aliases: map[string]string{}}
}

func (f *fakeLookup) ActorExists(id string) (bool, error)     { return f.actors[id], nil }
func (f *fakeLookup) UserActor(h string) (string, error)      { return f.users[strings.ToLower(h)], nil }
func (f *fakeLookup) UserActorByID(id string) (string, error) { return f.userIDs[id], nil }
func (f *fakeLookup) AliasActor(a string) (string, error)     { return f.aliases[a], nil }
func (f *fakeLookup) Local() LegacyLocal                      { return f.local }
func (f *fakeLookup) AgentActor(org, id string) (string, string, error) {
	v := f.agents[org+"|"+id]
	return v[0], v[1], nil
}

const (
	uTim   = "usr_00000000-0000-7000-8000-000000000001"
	uAlice = "usr_00000000-0000-7000-8000-000000000002"
	aDev   = "agt_00000000-0000-7000-8000-000000000011"
	aOld   = "agt_00000000-0000-7000-8000-000000000012"
)

func fixtureLookup() *fakeLookup {
	f := newFake()
	f.users["tim@example.com"] = uTim
	f.users["alice@x"] = uAlice
	f.userIDs["u_alice"] = uAlice
	f.agents["o|dev"] = [2]string{aDev, ""}
	f.agents["o|old"] = [2]string{"", aOld}
	f.actors[uTim], f.actors[uAlice], f.actors[aDev], f.actors[aOld] = true, true, true, true
	return f
}

func legacyNode(raw string) string { return model.PrincipalNode(strings.TrimSpace(raw)) }

// DOC-13 §7 的樣態:每種舊值寫法對到哪裡
func TestResolveLegacyPatterns(t *testing.T) {
	sysLegacy := func(raw string) string { return LegacyActorID(legacyNode(raw)) }
	cases := []struct {
		raw, want string // want:actor id;"NULL"、"CREATE-USER:<handle>"、"CREATE-SYS:<id>"
	}{
		// 空值
		{"", "NULL"}, {"   ", "NULL"},
		// email 與 user:<email>(大小寫、空白)
		{"alice@x", uAlice}, {"user:alice@x", uAlice}, {"ALICE@X", uAlice}, {" user:Alice@X ", uAlice},
		{"tim@example.com", uTim}, {"user:tim@example.com", uTim},
		// 沒有這位使用者:建「未註冊使用者」
		{"boss@x", "CREATE-USER:boss@x"}, {"user:root@x", "CREATE-USER:root@x"}, {"Root@X", "CREATE-USER:root@x"},
		// user:<非 email>:沒有本機使用者設定、也沒有同 handle 的使用者 → 建未註冊使用者(同含 @ 的 email)
		{"user:a", "CREATE-USER:a"},
		// 本機別名:沒有設定本機使用者 → sys_legacy
		{"ops-web", sysLegacy("ops-web")}, {"omixor-console", sysLegacy("omixor-console")}, {"user:ops-web", sysLegacy("user:ops-web")},
		{"local:foo", sysLegacy("local:foo")}, {"ops-web-2", sysLegacy("ops-web-2")},
		// mcp:沒有本機使用者 → sys_legacy_mcp
		{"mcp", "sys_legacy_mcp"}, {"local:mcp", "sys_legacy_mcp"},
		// 內建署名
		{"routine", "sys_routine"}, {"chain", "sys_chain"}, {"omixor", "sys_omixor"}, {"system", "sys_omixor"},
		// 角色
		// 怪值
		{"冒名", sysLegacy("冒名")}, {"ops", sysLegacy("ops")}, {"x", sysLegacy("x")}, {"  x  ", sysLegacy("x")}, {"bob", sysLegacy("bob")},
		{"agent:", sysLegacy("agent:")},
		// 組織鏈的串接字串不是人
		{"user:alice@x>agent:cto>目標", sysLegacy("user:alice@x>agent:cto>目標")}, {"user:stranger@x>agent:cto", sysLegacy("user:stranger@x>agent:cto")},
	}
	for _, c := range cases {
		res, err := ResolveLegacy(fixtureLookup(), "o", c.raw, LegacyOpts{})
		chk(t, err)
		if got := resultString(res); got != c.want {
			t.Errorf("%q → %q(%+v),想要 %q", c.raw, got, res, c.want)
		}
	}
}

// resultString 把解析結果攤成一個字串:NULL、actor id、CREATE-USER:<handle>、要建的系統 actor id
func resultString(res LegacyResult) string {
	switch {
	case res.Null:
		return "NULL"
	case res.ActorID != "":
		return res.ActorID
	case res.Create != nil && res.Create.Kind == model.ActorKindUser:
		return "CREATE-USER:" + res.Create.Handle
	case res.Create != nil:
		return res.Create.ID
	}
	return "?"
}

// 角色:現役、已刪(退役)、查無此角色、沒有組織、跨組織同名、純角色 id(assignee)
func TestResolveLegacyAgents(t *testing.T) {
	f := fixtureLookup()
	f.agents["o2|dev"] = [2]string{"agt_00000000-0000-7000-8000-000000000021", ""}
	f.actors["agt_00000000-0000-7000-8000-000000000021"] = true
	for _, c := range []struct {
		org, raw string
		form     IdentityForm
		want     string
	}{
		{"o", "agent:dev", IdentityFree, aDev},
		{"o2", "agent:dev", IdentityFree, "agt_00000000-0000-7000-8000-000000000021"}, // 同 handle 在兩個組織各自解析
		{"o", "dev", IdentityAgentID, aDev},
		{"o", "agent:old", IdentityFree, aOld}, // 已刪 → 退役 actor
		{"o", "old", IdentityAgentID, aOld},
		{"o", "agent:ghost", IdentityFree, LegacyAgentActorID("o", "agent:ghost")}, // 都沒有 → sys_legacy(帶組織)
		{"o2", "agent:ghost", IdentityFree, LegacyAgentActorID("o2", "agent:ghost")}, // 不同組織同名的已刪角色不併
		{"o", "ghost", IdentityAgentID, LegacyAgentActorID("o", "agent:ghost")},    // 純 id 與 agent: 前綴是同一個節點、同一個 actor
		{"o", "Agent:dev", IdentityFree, aDev},                                     // 前綴大小寫不分
		{"", "agent:dev", IdentityFree, LegacyActorID("agent:dev")},      // 沒有組織(server_features)只能對使用者與系統
	} {
		res, err := ResolveLegacy(f, c.org, c.raw, LegacyOpts{Form: c.form})
		chk(t, err)
		got := res.ActorID
		if got == "" && res.Create != nil {
			got = res.Create.ID
		}
		if got != c.want {
			t.Errorf("org=%q %q(%s)→ %q,想要 %q", c.org, c.raw, c.form, got, c.want)
		}
	}
}

// 本機使用者設定:mcp、別名、user:<別名> 都對到本機使用者;本機使用者已經存在或要建;別名記進 omixor_actor_aliases
func TestResolveLegacyLocalUser(t *testing.T) {
	f := fixtureLookup()
	f.local = LegacyLocal{Handle: "tim", Aliases: map[string]bool{"tim-laptop": true}}
	f.users["tim"] = uTim
	for _, raw := range []string{"mcp", "local:mcp", "ops-web", "omixor-console", "tim", "user:tim", "tim-laptop", "local:anything", "user:ops-web"} {
		res, err := ResolveLegacy(f, "o", raw, LegacyOpts{})
		chk(t, err)
		if res.ActorID != uTim || res.Create != nil {
			t.Errorf("%q → %+v,想要本機使用者", raw, res)
		}
		if res.Alias != legacyNode(raw) {
			t.Errorf("%q 的別名要記規範化節點:%q", raw, res.Alias)
		}
	}
	// 本機使用者還沒有 actor → 要建(handle tim)
	delete(f.users, "tim")
	res, err := ResolveLegacy(f, "o", "mcp", LegacyOpts{})
	chk(t, err)
	if res.ActorID != "" || res.Create == nil || res.Create.Kind != model.ActorKindUser || res.Create.Handle != "tim" {
		t.Fatalf("%+v", res)
	}
	// 純名稱:有同 handle 的使用者就是他(表格以外的補充),沒有就 sys_legacy
	f.users["bob"] = uAlice
	if res, _ := ResolveLegacy(f, "o", "bob", LegacyOpts{}); res.ActorID != uAlice {
		t.Fatalf("純名稱對到同 handle 的使用者:%+v", res)
	}
	// 別名表已經有的節點:直接用(重跑、匯入穩定),不看設定
	f2 := newFake()
	f2.aliases["local:foo"] = "sys_legacy_deadbeef"
	if res, _ := ResolveLegacy(f2, "o", "foo", LegacyOpts{}); res.ActorID != "sys_legacy_deadbeef" {
		t.Fatalf("%+v", res)
	}
}

// NOT NULL 欄位的空字串 → sys_legacy_unknown;已經是 actor id 的值對到自己(部分已轉換)
func TestResolveLegacyEmptyAndAlready(t *testing.T) {
	f := fixtureLookup()
	res, err := ResolveLegacy(f, "o", "", LegacyOpts{NotNull: true})
	chk(t, err)
	if res.Null || res.Create == nil || res.Create.ID != "sys_legacy_unknown" {
		t.Fatalf("%+v", res)
	}
	for _, id := range []string{uTim, aDev, "sys_omixor"} {
		res, err := ResolveLegacy(f, "o", id, LegacyOpts{})
		chk(t, err)
		if res.ActorID != id || res.Create != nil {
			t.Fatalf("%s → %+v", id, res)
		}
	}
	// 長得像 actor id、但不存在:當一般舊值(sys_legacy)
	res, err = ResolveLegacy(f, "o", "usr_00000000-0000-7000-8000-0000000000ff", LegacyOpts{})
	chk(t, err)
	if res.Create == nil || !strings.HasPrefix(res.Create.ID, "sys_legacy_") {
		t.Fatalf("%+v", res)
	}
}

// sys_legacy 的 id 穩定:同一個舊值永遠同一個、格式 sys_legacy_<8 碼 hex>、名稱是原值(去頭尾空白)、且符合 actor id 格式
func TestLegacyActorIDStable(t *testing.T) {
	a, b := LegacyActorID("local:冒名"), LegacyActorID("local:冒名")
	if a != b || !strings.HasPrefix(a, "sys_legacy_") || len(a) != len("sys_legacy_")+8 {
		t.Fatal(a, b)
	}
	if !model.ValidActorID(a) {
		t.Fatal("要符合 actor id 格式", a)
	}
	if LegacyActorID("local:x") == LegacyActorID("local:y") {
		t.Fatal("不同舊值不同 actor")
	}
	// 同一個值的不同寫法(空白、大小寫的 email)規範化後同一個
	r1, _ := ResolveLegacy(newFake(), "o", "  冒名 ", LegacyOpts{})
	r2, _ := ResolveLegacy(newFake(), "o", "冒名", LegacyOpts{})
	if r1.Create == nil || r1.Create.ID != r2.Create.ID || r1.Create.Name != "冒名" {
		t.Fatalf("%+v %+v", r1, r2)
	}
}

// 事件時間軸 actor_kind + actor_id
func TestResolveLegacyEvent(t *testing.T) {
	f := fixtureLookup()
	f.local = LegacyLocal{Handle: "tim"}
	f.users["tim"] = uTim
	for _, c := range []struct{ kind, id, want string }{
		{"system", "", "sys_omixor"},
		{"system", "whatever", "sys_omixor"},
		{"ai", "dev", aDev},
		{"ai", "old", aOld},
		{"ai", "mcp", uTim},
		{"ai", "auto-9", LegacyAgentActorID("o", "agent:auto-9")},
		{"person", "alice@x", uAlice},
		{"person", "tim", uTim},
		{"", "alice@x", uAlice},
		{"", "", "NULL"},
		{"ai", "", "NULL"},
		{"person", "", "NULL"},
	} {
		res, err := ResolveLegacyEvent(f, "o", c.kind, c.id, LegacyOpts{Form: IdentityActor})
		chk(t, err)
		got := res.ActorID
		if res.Null {
			got = "NULL"
		} else if got == "" && res.Create != nil {
			got = res.Create.ID
		}
		if got != c.want {
			t.Errorf("%s/%s → %q,想要 %q", c.kind, c.id, got, c.want)
		}
	}
	// ai 但不是角色的自動化 id:名稱 = 原 id
	res, _ := ResolveLegacyEvent(f, "o", "ai", "auto-9", LegacyOpts{})
	if res.Create == nil || res.Create.Name != "auto-9" {
		t.Fatalf("%+v", res)
	}
}

// 稽核的 user_id:u_<x> 對 omixor_users.actor_id、agent:<id> 對該組織角色
func TestResolveLegacyUserID(t *testing.T) {
	f := fixtureLookup()
	for _, c := range []struct{ org, raw, want string }{
		{"o", "u_alice", uAlice}, {"o", "agent:dev", aDev}, {"", "u_alice", uAlice},
		{"o", "u_gone", LegacyActorID("local:u_gone")}, {"o", "", "NULL"},
	} {
		res, err := ResolveLegacy(f, c.org, c.raw, LegacyOpts{Form: IdentityUserID})
		chk(t, err)
		got := res.ActorID
		if res.Null {
			got = "NULL"
		} else if got == "" && res.Create != nil {
			got = res.Create.ID
		}
		if got != c.want {
			t.Errorf("%q → %q,想要 %q", c.raw, got, c.want)
		}
	}
}

// omixor_actor_aliases:啟動遷移建立(空表)、冪等、外鍵指向 omixor_actors
func TestActorAliasesTable(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	if n := pgCount(t, p, `select count(*) from information_schema.columns where table_name = 'omixor_actor_aliases' and column_name in ('alias','actor_id','created_at')`); n != 3 {
		t.Fatal("欄位", n)
	}
	if n := pgCount(t, p, `select count(*) from omixor_actor_aliases`); n != 0 {
		t.Fatal("建表時是空的", n)
	}
	chk(t, p.migrateActorAliases())
	chk(t, p.migrateActorAliases())
	if _, err := p.pool.Exec(bg(), `insert into omixor_actor_aliases (alias, actor_id) values ('x', 'sys_nope')`); err == nil {
		t.Fatal("actor_id 要有外鍵")
	}
	pgExec(t, p, `insert into omixor_actor_aliases (alias, actor_id) values ('local:x', 'sys_omixor')`)
	if _, err := p.pool.Exec(bg(), `insert into omixor_actor_aliases (alias, actor_id) values ('local:x', 'sys_chain')`); err == nil {
		t.Fatal("alias 是主鍵")
	}
	// 表被丟掉:下次啟動遷移補回
	pgExec(t, p, `drop table omixor_actor_aliases`)
	chk(t, p.migrateActorAliases())
	if n := pgCount(t, p, `select count(*) from omixor_actor_aliases`); n != 0 {
		t.Fatal(n)
	}
}

// PG 版的 ResolveLegacy(匯入共用):建缺的 actor、記別名、冪等
func TestPGResolveLegacy(t *testing.T) {
	SetMigrationLocalUser("")
	p := fkPG(t, testPGBlank(t))
	a1, err := p.ResolveLegacy("o", "冒名")
	chk(t, err)
	a2, err := p.ResolveLegacy("o", " 冒名 ")
	chk(t, err)
	if a1 != a2 || a1 != LegacyActorID("local:冒名") {
		t.Fatal(a1, a2)
	}
	if n := pgCount(t, p, `select count(*) from omixor_actors where id = $1 and kind = 'system' and name = '冒名'`, a1); n != 1 {
		t.Fatal("名稱保留原值", n)
	}
	if n := pgCount(t, p, `select count(*) from omixor_actor_aliases where alias = 'local:冒名' and actor_id = $1`, a1); n != 1 {
		t.Fatal("要記別名")
	}
	if id, err := p.ResolveLegacy("o", ""); err != nil || id != "" {
		t.Fatal("空值回空", id, err)
	}
	u1, err := p.ResolveLegacy("o", "user:New@X")
	chk(t, err)
	u2, err := p.ResolveLegacy("o", "new@x")
	chk(t, err)
	if u1 != u2 || !model.ValidActorIDOf(model.ActorKindUser, u1) {
		t.Fatal("未註冊使用者:同一個 email 同一個 actor", u1, u2)
	}
	if s, err := p.ResolveLegacy("", "routine"); err != nil || s != "sys_routine" {
		t.Fatal(s, err)
	}
}
