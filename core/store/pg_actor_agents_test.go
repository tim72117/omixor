package store

// 角色與 actor 的連動(ISS-36 B-2c):SaveAgent/DeleteAgent/ImportAgents 同步 actor、交易回滾、同名重建、owner、啟動遷移(舊資料、冪等、並行)

import (
	"context"
	"errors"
	"testing"
	"time"

	"omixor-core/model"
)

// wantActorID 讀回的角色要帶 agt_ 開頭的 actor id
func wantActorID(t *testing.T, g model.Agent) {
	t.Helper()
	if k, ok := model.ActorKindOfID(g.ActorID); !ok || k != model.ActorKindAgent {
		t.Fatalf("角色 %s 的 ActorID %q 不是 agt_ actor id", g.ID, g.ActorID)
	}
}

// mkOrgs 建組織(角色對組織有外鍵)
func mkOrgs(t *testing.T, p *PG, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := p.CreateOrg(model.Org{ID: id, Name: id, CreatedAt: model.Now()}); err != nil && !errors.Is(err, ErrDuplicateKey) {
			t.Fatal(err)
		}
	}
}

func agentActor(t *testing.T, p *PG, org, id string) model.ActorRecord {
	t.Helper()
	g, err := p.GetAgent(org, id)
	chk(t, err)
	wantActorID(t, *g)
	a, err := p.ActorByID(g.ActorID)
	chk(t, err)
	return *a
}

// 新增、修改、停用、啟用、刪除、同名重建
func TestAgentActorLifecycle(t *testing.T) {
	p := actorStore(t)
	mkOrgs(t, p, "o", "o2")
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	a := agentActor(t, p, "o", "dev")
	if a.Kind != model.ActorKindAgent || a.Org != "o" || a.Handle != "dev" || a.Name != "Dev" || a.Owner != "" || a.Disabled() {
		t.Fatalf("%+v", a)
	}
	if byH, err := p.ActorByHandle(model.ActorKindAgent, "o", "dev"); err != nil || byH.ID != a.ID {
		t.Fatal(byH, err)
	}
	// 同一個角色:ID 的對應不變;改名跟著
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Developer", Active: true, CreatedAt: model.Now(), ActorID: "agt_ignored"}))
	b := agentActor(t, p, "o", "dev")
	if b.ID != a.ID || b.Name != "Developer" {
		t.Fatalf("%+v", b)
	}
	// 停用 → actor 停用;啟用 → 清掉
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Developer", Active: false, CreatedAt: model.Now()}))
	if c := agentActor(t, p, "o", "dev"); c.ID != a.ID || !c.Disabled() {
		t.Fatalf("停用:%+v", c)
	}
	wantNotFound(t, "停用的角色不佔 handle 查找", errOf(p.ActorByHandle(model.ActorKindAgent, "o", "dev")))
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Developer", Active: true, CreatedAt: model.Now()}))
	if c := agentActor(t, p, "o", "dev"); c.Disabled() {
		t.Fatalf("啟用:%+v", c)
	}
	// 刪除 = 退役(列保留)
	chk(t, p.DeleteAgent("o", "dev"))
	old, err := p.ActorByID(a.ID)
	chk(t, err)
	if !old.Disabled() || old.Handle != "dev" {
		t.Fatalf("刪除後 actor 要退役:%+v", old)
	}
	if _, err := p.GetAgent("o", "dev"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := p.DeleteAgent("o", "dev"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// 同名重建 = 新的 actor
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev2", Active: true, CreatedAt: model.Now()}))
	n := agentActor(t, p, "o", "dev")
	if n.ID == a.ID || n.Name != "Dev2" || n.Disabled() {
		t.Fatalf("重建:%+v", n)
	}
	if byH, err := p.ActorByHandle(model.ActorKindAgent, "o", "dev"); err != nil || byH.ID != n.ID {
		t.Fatal(byH, err)
	}
	// 不同組織同名互不影響
	chk(t, p.SaveAgent("o2", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	if agentActor(t, p, "o2", "dev").ID == n.ID {
		t.Fatal("不同組織要不同 actor")
	}
	// ListAgents 也帶 actor id
	l, err := p.ListAgents("o")
	chk(t, err)
	if len(l) != 1 || l[0].ActorID != n.ID {
		t.Fatalf("%+v", l)
	}
	// 一個角色一個 actor:全部現役角色 actor 的 handle 不重複(唯一索引),角色列的 actor_id 唯一
	if c := pgCount(t, p, `select count(*) from (select actor_id from omixor_agents group by actor_id having count(*) > 1) x`); c != 0 {
		t.Fatal(c)
	}
}

// id 不符 handle 規則的角色沒有 actor,其餘功能照舊(不讓 SaveAgent 退步)
func TestAgentActorInvalidHandleSkipped(t *testing.T) {
	p := actorStore(t)
	mkOrgs(t, p, "o", "o2")
	chk(t, p.SaveAgent("o", model.Agent{ID: "Old_Agent", Name: "舊", Active: true, CreatedAt: model.Now()}))
	g, err := p.GetAgent("o", "Old_Agent")
	chk(t, err)
	if g.ActorID != "" {
		t.Fatalf("%q", g.ActorID)
	}
	chk(t, p.DeleteAgent("o", "Old_Agent"))
}

// SaveAgent 失敗(子表寫入失敗)→ actor 一起回滾;Atomic 回滾、刪除回滾也一樣
func TestAgentActorRollback(t *testing.T) {
	p := actorStore(t)
	mkOrgs(t, p, "o")
	dup := []model.AgentRoutine{{ID: "r", Title: "a", Kind: "daily"}, {ID: "r", Title: "b", Kind: "daily"}}
	if err := p.SaveAgent("o", model.Agent{ID: "bad", Name: "Bad", Active: true, CreatedAt: model.Now(), Routines: dup}); err == nil {
		t.Fatal("要失敗")
	}
	if n := pgCount(t, p, `select count(*) from omixor_actors where kind = 'agent'`); n != 0 {
		t.Fatalf("失敗後不能留下 actor:%d", n)
	}
	if n := pgCount(t, p, `select count(*) from omixor_agents`); n != 0 {
		t.Fatal(n)
	}
	// 已存在的角色改名失敗 → actor 名稱不變
	chk(t, p.SaveAgent("o", model.Agent{ID: "ok", Name: "Ok", Active: true, CreatedAt: model.Now()}))
	if err := p.SaveAgent("o", model.Agent{ID: "ok", Name: "Changed", Active: false, CreatedAt: model.Now(), Routines: dup}); err == nil {
		t.Fatal("要失敗")
	}
	if a := agentActor(t, p, "o", "ok"); a.Name != "Ok" || a.Disabled() {
		t.Fatalf("%+v", a)
	}
	boom := errors.New("boom")
	if err := p.Atomic(func(tx Store) error {
		if err := tx.SaveAgent("o", model.Agent{ID: "tx1", Name: "T", Active: true, CreatedAt: model.Now()}); err != nil {
			return err
		}
		if err := tx.DeleteAgent("o", "ok"); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := p.GetAgent("o", "tx1"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_actors where kind = 'agent' and handle = 'tx1'`); n != 0 {
		t.Fatal("Atomic 回滾後不能有 tx1 的 actor")
	}
	if a := agentActor(t, p, "o", "ok"); a.Disabled() {
		t.Fatal("刪除被回滾,actor 要還是現役")
	}
	// 還被任務參照 → DeleteAgent 回 ErrInUse,actor 不退役
	_, err := p.CreateAgentTask("o", model.AgentTask{AgentID: "ok", Title: "t", Status: "queued", Priority: "normal", CreatedAt: model.Now(), UpdatedAt: model.Now()})
	chk(t, err)
	if err := p.DeleteAgent("o", "ok"); !errors.Is(err, ErrInUse) {
		t.Fatalf("要 ErrInUse,拿到 %v", err)
	}
	if a := agentActor(t, p, "o", "ok"); a.Disabled() {
		t.Fatal("刪除失敗,actor 要還是現役")
	}
}

// DeleteOrg 把該組織的角色 actor 退役(列保留);同 id 組織重建、角色重建是新的 actor
func TestAgentActorDeleteOrg(t *testing.T) {
	p := actorStore(t)
	chk(t, p.CreateOrg(model.Org{ID: "g", Name: "g", CreatedAt: model.Now()}))
	chk(t, p.SaveAgent("g", model.Agent{ID: "a", Name: "A", Active: true, CreatedAt: model.Now()}))
	old := agentActor(t, p, "g", "a")
	chk(t, p.DeleteOrg("g"))
	got, err := p.ActorByID(old.ID)
	chk(t, err)
	if !got.Disabled() {
		t.Fatal("要退役")
	}
	chk(t, p.CreateOrg(model.Org{ID: "g", Name: "g", CreatedAt: model.Now()}))
	chk(t, p.SaveAgent("g", model.Agent{ID: "a", Name: "A", Active: true, CreatedAt: model.Now()}))
	if agentActor(t, p, "g", "a").ID == old.ID {
		t.Fatal("要新的 actor")
	}
}

// ImportAgents 為每個角色建 actor;匯入失敗整個回滾(含 actor)
func TestAgentActorImport(t *testing.T) {
	p := actorStore(t)
	chk(t, p.CreateOrg(model.Org{ID: "imp", Name: "i", CreatedAt: model.Now()}))
	ag := []model.Agent{{ID: "a", Name: "A", Active: true, CreatedAt: model.Now()}, {ID: "b", Name: "B", CreatedAt: model.Now()}}
	chk(t, p.ImportAgents("imp", ag, nil))
	if a := agentActor(t, p, "imp", "a"); a.Disabled() || a.Name != "A" {
		t.Fatalf("%+v", a)
	}
	if b := agentActor(t, p, "imp", "b"); !b.Disabled() {
		t.Fatalf("停用的角色匯入後 actor 要停用:%+v", b)
	}
	chk(t, p.CreateOrg(model.Org{ID: "imp2", Name: "i", CreatedAt: model.Now()}))
	bad := []model.Agent{{ID: "c", Name: "C", Active: true, CreatedAt: model.Now()}, {ID: "c", Name: "C2", Active: true, CreatedAt: model.Now()}}
	if err := p.ImportAgents("imp2", bad, nil); err == nil {
		t.Fatal("要失敗")
	}
	if n := pgCount(t, p, `select count(*) from omixor_actors where org = 'imp2'`); n != 0 {
		t.Fatalf("匯入失敗不能留下 actor:%d", n)
	}
}

// 複合外鍵:角色與 actor 的 org、handle 對不上就被擋;actor 不能被改成對不上(UpdateActorHandle 失敗);actor_id 唯一
func TestAgentActorConstraints(t *testing.T) {
	p := actorStore(t)
	mkOrgs(t, p, "o", "o2")
	chk(t, p.SaveAgent("o", model.Agent{ID: "a", Name: "A", Active: true, CreatedAt: model.Now()}))
	chk(t, p.SaveAgent("o", model.Agent{ID: "b", Name: "B", Active: true, CreatedAt: model.Now()}))
	a, b := agentActor(t, p, "o", "a"), agentActor(t, p, "o", "b")
	if err := p.UpdateActorHandle(a.ID, "renamed"); err == nil {
		t.Fatal("角色 actor 的 handle 被角色參照,不能單獨改")
	}
	if _, err := p.pool.Exec(ctxBG(), `update omixor_agents set actor_id = $1 where id = 'b'`, a.ID); err == nil {
		t.Fatal("actor_id 唯一、且 handle 要對得上")
	}
	if _, err := p.pool.Exec(ctxBG(), `update omixor_agents set id = 'z' where id = 'b'`); err == nil {
		t.Fatal("角色 id 不能單獨改")
	}
	if _, err := p.pool.Exec(ctxBG(), `update omixor_agents set actor_id = 'agt_nope' where id = 'b'`); err == nil {
		t.Fatal("actor 不存在要被擋")
	}
	if _, err := p.pool.Exec(ctxBG(), `delete from omixor_actors where id = $1`, b.ID); err == nil {
		t.Fatal("被角色參照的 actor 不能刪")
	}
}

// owner:SaveAgentOwned、SetActorOwner、預設 owner(組織第一位管理員)與補 owner
func TestAgentActorOwner(t *testing.T) {
	p := actorStore(t)
	mkOrgs(t, p, "o", "nomember")
	u1, u2 := newUser(t, p, "first@x.com"), newUser(t, p, "second@x.com")
	// 沒有管理員 → owner 空
	chk(t, p.SaveAgent("o", model.Agent{ID: "a0", Name: "A0", Active: true, CreatedAt: model.Now()}))
	if a := agentActor(t, p, "o", "a0"); a.Owner != "" {
		t.Fatalf("%+v", a)
	}
	// 明確指定
	chk(t, p.SaveAgentOwned("o", model.Agent{ID: "a1", Name: "A1", Active: true, CreatedAt: model.Now()}, u2.ID))
	if a := agentActor(t, p, "o", "a1"); a.Owner != u2.ID {
		t.Fatalf("%+v", a)
	}
	// 已存在的角色不改 owner
	chk(t, p.SaveAgentOwned("o", model.Agent{ID: "a1", Name: "A1b", Active: true, CreatedAt: model.Now()}, u1.ID))
	if a := agentActor(t, p, "o", "a1"); a.Owner != u2.ID || a.Name != "A1b" {
		t.Fatalf("%+v", a)
	}
	if err := p.SaveAgentOwned("o", model.Agent{ID: "a2", Name: "A2", Active: true, CreatedAt: model.Now()}, "u_notactor"); err == nil {
		t.Fatal("owner 要是使用者 actor id")
	}
	if err := p.SaveAgentOwned("o", model.Agent{ID: "a2", Name: "A2", Active: true, CreatedAt: model.Now()}, "usr_00000000-0000-7000-8000-000000000000"); !errors.Is(err, ErrBadRef) {
		t.Fatalf("owner 不存在要 ErrBadRef,拿到 %v", err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_actors where handle = 'a2'`); n != 0 {
		t.Fatal("失敗要回滾")
	}
	// SetActorOwner
	a0 := agentActor(t, p, "o", "a0")
	chk(t, p.SetActorOwner(a0.ID, u1.ID))
	if agentActor(t, p, "o", "a0").Owner != u1.ID {
		t.Fatal("owner")
	}
	chk(t, p.SetActorOwner(a0.ID, ""))
	if agentActor(t, p, "o", "a0").Owner != "" {
		t.Fatal("清除")
	}
	if err := p.SetActorOwner(u1.ID, u2.ID); err == nil {
		t.Fatal("使用者沒有 owner")
	}
	if err := p.SetActorOwner("sys_omixor", u2.ID); !errors.Is(err, ErrSystemActor) {
		t.Fatal(err)
	}
	if err := p.SetActorOwner("agt_00000000-0000-7000-8000-000000000000", u2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := p.SetActorOwner(a0.ID, "usr_00000000-0000-7000-8000-000000000000"); !errors.Is(err, ErrBadRef) {
		t.Fatal(err)
	}
	// 預設 owner:omixor_users.actor_id(B-2b)。測試直接寫 omixor_users / omixor_org_members 模擬
	now := time.Now()
	for i, u := range []model.ActorRecord{u2, u1} {
		pgExec(t, p, `insert into omixor_users (id, email, name, role, created_at, actor_id) values ($1,$2,'n','admin',$3,$4)`, "u_"+u.Handle, u.Handle, now.Add(time.Duration(i)*time.Hour), u.ID)
	}
	pgExec(t, p, `insert into omixor_org_members (org, user_id, role, added_at) values ('o',$1,'member',$3), ('o',$2,'admin',$4)`, "u_"+u1.Handle, "u_"+u2.Handle, now, now.Add(time.Hour))
	chk(t, p.SaveAgent("o", model.Agent{ID: "a3", Name: "A3", Active: true, CreatedAt: model.Now()}))
	if a := agentActor(t, p, "o", "a3"); a.Owner != u2.ID {
		t.Fatalf("預設 owner 要是組織第一位管理員成員(u2):%+v", a)
	}
	// 組織沒有成員 → 最早的啟用中伺服器管理員(u2 的 created_at 最早)
	chk(t, p.SaveAgent("nomember", model.Agent{ID: "a4", Name: "A4", Active: true, CreatedAt: model.Now()}))
	if a := agentActor(t, p, "nomember", "a4"); a.Owner != u2.ID {
		t.Fatalf("%+v", a)
	}
	// 補 owner:a0 現在是空的(上面清掉)
	n, err := p.BackfillAgentOwners()
	chk(t, err)
	if n != 1 || agentActor(t, p, "o", "a0").Owner != u2.ID {
		t.Fatalf("補了 %d 個", n)
	}
	if n, err := p.BackfillAgentOwners(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func ctxBG() context.Context { return context.Background() }
