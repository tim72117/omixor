package store

// 寬鬆解析器(ResolveLegacy)的 PG 版回歸測試:sys_legacy 雜湊碰撞、不同組織的已刪角色、前綴大小寫、同 email 大小寫多帳號

import (
	"strings"
	"testing"

	"omixor-core/model"
)

// actorFx 舊值樣態的固定資料:使用者 a@x、tim@example.com、本機使用者 tim;組織 o 的角色 dev(現役)、old(已刪、退役)
type actorFx struct {
	p                         *PG
	userA, userTim, userLocal string
	agentDev, agentOld        string
}

func newActorFx(t *testing.T) *actorFx {
	t.Helper()
	SetMigrationLocalUser("tim") // console 本機模式的 a.Me;別名 omixor-console、ops-web 內建
	t.Cleanup(func() { SetMigrationLocalUser("") })
	p := fkPG(t, testPGBlank(t))
	fx := &actorFx{p: p}
	mkOrgs(t, p, "o")
	for _, u := range [][3]string{{"u_a", "a@x", "A"}, {"u_tim", "tim@example.com", "Tim"}, {"u_local", "tim", "本機 tim"}} {
		saveU(t, p, u[0], u[1], u[2])
	}
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	chk(t, p.SaveAgent("o", model.Agent{ID: "old", Name: "Old", Active: true, CreatedAt: model.Now()}))
	chk(t, p.DeleteAgent("o", "old"))
	fx.userA = userActorOf(t, p, "u_a")
	fx.userTim = userActorOf(t, p, "u_tim")
	fx.userLocal = userActorOf(t, p, "u_local")
	g, err := p.GetAgent("o", "dev")
	chk(t, err)
	fx.agentDev = g.ActorID
	chk(t, p.pool.QueryRow(bg(), `select id from omixor_actors where kind = 'agent' and org = 'o' and handle = 'old'`).Scan(&fx.agentOld))
	return fx
}

func userActorOf(t *testing.T, p *PG, userID string) string {
	t.Helper()
	u, err := p.UserByID(userID)
	chk(t, err)
	if u.ActorID == "" {
		t.Fatal("使用者沒有 actor", userID)
	}
	return u.ActorID
}

// D:sys_legacy 雜湊碰撞(同雜湊、原值不同)不能靜默併成同一個 actor:改用較長的雜湊,而且重跑穩定
func TestResolveLegacyHashCollision(t *testing.T) {
	fx := newActorFx(t)
	const raw = "冒名甲"
	nodeA := "local:" + raw
	short := LegacyActorID(nodeA)
	// 模擬另一個原值 other 先佔用了同一個 8 碼雜湊的 actor
	pgExec(t, fx.p, `insert into omixor_actors (id, kind, name, handle) values ($1, 'system', 'other', $2)`, short, strings.TrimPrefix(short, "sys_"))
	pgExec(t, fx.p, `insert into omixor_actor_aliases (alias, actor_id) values ('local:other', $1)`, short)
	buf := captureLog(t)
	got, err := fx.p.ResolveLegacy("o", raw)
	chk(t, err)
	if got == short || !strings.HasPrefix(got, "sys_legacy_") || len(got) != len("sys_legacy_")+16 {
		t.Fatalf("碰撞時要改用 16 碼雜湊:%s(短的是 %s)", got, short)
	}
	if !strings.Contains(buf.String(), "碰撞") {
		t.Fatalf("要記錄碰撞:%s", buf.String())
	}
	again, err := fx.p.ResolveLegacy("o", raw)
	chk(t, err)
	if again != got {
		t.Fatal("同一個原值永遠同一個 actor", again, got)
	}
	other, err := fx.p.ResolveLegacy("o", "other")
	chk(t, err)
	if other != short {
		t.Fatal("先佔用的原值仍然對到原本的 actor", other, short)
	}
}

// D:不同組織同名的已刪角色(agent:foo)不併成同一個 sys_legacy actor;沒有組織的列仍是「同一原值同一 actor」
func TestResolveLegacyGhostAgentPerOrg(t *testing.T) {
	fx := newActorFx(t)
	mkOrgs(t, fx.p, "o2")
	a, err := fx.p.ResolveLegacy("o", "agent:foo")
	chk(t, err)
	b, err := fx.p.ResolveLegacy("o2", "agent:foo")
	chk(t, err)
	c, err := fx.p.ResolveLegacy("o", "agent:foo")
	chk(t, err)
	if a == b || a != c {
		t.Fatalf("不同組織 → 不同 actor、同組織穩定:%s %s %s", a, b, c)
	}
	n1, err := fx.p.ResolveLegacy("", "agent:foo")
	chk(t, err)
	n2, err := fx.p.ResolveLegacy("", "agent:foo")
	chk(t, err)
	if n1 != n2 || n1 == a || n1 == b {
		t.Fatalf("沒有組織:同原值同 actor:%s %s", n1, n2)
	}
	if a != LegacyAgentActorID("o", "agent:foo") {
		t.Fatal("LegacyAgentActorID 與解析結果一致")
	}
}

// D:前綴大小寫(Agent:dev、USER:a@x、Local:foo)先正規化,與小寫前綴對到同一個 actor
func TestResolveLegacyPrefixCase(t *testing.T) {
	fx := newActorFx(t)
	for _, c := range []struct{ raw, want string }{
		{"Agent:dev", fx.agentDev}, {"AGENT:dev", fx.agentDev}, {"USER:a@x", fx.userA}, {"User:A@X", fx.userA}, {"Local:foo", fx.userLocal},
	} {
		got, err := fx.p.ResolveLegacy("o", c.raw)
		chk(t, err)
		if got != c.want {
			t.Errorf("%q → %s,想要 %s", c.raw, got, c.want)
		}
	}
	g1, err := fx.p.ResolveLegacy("o", "Agent:ghost")
	chk(t, err)
	g2, err := fx.p.ResolveLegacy("o", "agent:ghost")
	chk(t, err)
	if g1 != g2 {
		t.Fatal("Agent:ghost 與 agent:ghost 是同一個節點", g1, g2)
	}
}

// D:同 email 只差大小寫的多帳號:確定性地選最早建立的,而且寫 log
func TestUserActorCaseDuplicateDeterministic(t *testing.T) {
	fx := newActorFx(t)
	const a1, a2 = "usr_00000000-0000-7000-8000-0000000000a1", "usr_00000000-0000-7000-8000-0000000000a2"
	pgExec(t, fx.p, `insert into omixor_actors (id, kind, name, handle) values ($1,'user','n','dup1@x.com'), ($2,'user','n','dup2@x.com')`, a1, a2)
	// 較晚建立的帳號先插入(避免剛好靠插入順序過關)
	pgExec(t, fx.p, `insert into omixor_users (id, email, name, role, created_at, actor_id) values ('u_dup2','dup@x.com','n','admin','2024-02-01',$1)`, a2)
	pgExec(t, fx.p, `insert into omixor_users (id, email, name, role, created_at, actor_id) values ('u_dup1','DUP@x.com','n','admin','2024-01-01',$1)`, a1)
	buf := captureLog(t)
	for i := 0; i < 3; i++ {
		lk := fx.p.newLegacyLookup(bg())
		got, err := lk.UserActor("dup@x.com")
		chk(t, err)
		if got != a1 {
			t.Fatalf("要選最早建立的帳號:%s", got)
		}
	}
	if !strings.Contains(buf.String(), "dup@x.com") {
		t.Fatalf("同 email 多帳號要寫 log:%s", buf.String())
	}
}
