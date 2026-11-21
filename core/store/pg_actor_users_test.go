package store

// 使用者接上 actor(ISS-36 B-2b):SaveUser 自動建立/接回 actor、同步 handle 與名稱、停用、大小寫、未註冊使用者(D3)、
// 啟動遷移補齊(冪等、多批、並行啟動、舊結構轉換)與外鍵

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
)

func saveU(t *testing.T, s Store, id, email, name string) model.User {
	t.Helper()
	u := model.User{ID: id, Email: email, Name: name, Role: "member", CreatedAt: model.Now()}
	chk(t, s.SaveUser(u))
	got, err := s.UserByID(id)
	chk(t, err)
	return *got
}

func userActorCount(t *testing.T, p *PG) int {
	t.Helper()
	return pgCount(t, p, `select count(*) from omixor_actors where kind = 'user'`)
}

func TestUserActorCreatedBySaveUser(t *testing.T) {
	p := actorStore(t)
	u := saveU(t, p, "u_a", "Tim@Example.COM", "Tim")
	if !model.ValidActorIDOf(model.ActorKindUser, u.ActorID) {
		t.Fatalf("要有 usr_ actor:%q", u.ActorID)
	}
	a, err := p.ActorByID(u.ActorID)
	chk(t, err)
	if a.Handle != "tim@example.com" || a.Name != "Tim" || a.Kind != model.ActorKindUser || a.Disabled() {
		t.Fatalf("%+v", a)
	}
	// email 是原樣存的(登入、比對的行為不變),handle 才是小寫
	if u.Email != "Tim@Example.COM" {
		t.Fatal(u.Email)
	}
	// 重複存(登入)不重複建立、id 不變
	for i := 0; i < 3; i++ {
		u2 := u
		u2.LastLoginAt = &model.Time{Time: time.Now()}
		chk(t, p.SaveUser(u2))
	}
	if n := userActorCount(t, p); n != 1 {
		t.Fatalf("使用者 actor 數 %d", n)
	}
	got, err := p.UserByID("u_a")
	chk(t, err)
	if got.ActorID != u.ActorID {
		t.Fatal("actor id 變了")
	}
	byA, err := p.UserByActorID(u.ActorID)
	chk(t, err)
	if byA.ID != "u_a" {
		t.Fatal(byA.ID)
	}
	if byEmail, _ := p.UserByEmail("Tim@Example.COM"); byEmail == nil || byEmail.ActorID != u.ActorID {
		t.Fatal("UserByEmail 要帶 ActorID")
	}
	list, err := p.ListUsers()
	chk(t, err)
	if len(list) != 1 || list[0].ActorID != u.ActorID {
		t.Fatalf("%+v", list)
	}
	// token、成員帶出 actor id
	chk(t, p.CreateOrg(model.Org{ID: "o1", Name: "o1", CreatedAt: model.Now()}))
	chk(t, p.SaveMember(model.Member{Org: "o1", UserID: "u_a", Role: "member", AddedAt: model.Now()}))
	ms, err := p.ListMembers("o1")
	chk(t, err)
	if len(ms) != 1 || ms[0].ActorID != u.ActorID {
		t.Fatalf("%+v", ms)
	}
	chk(t, p.CreateToken(model.Token{ID: "t1", Hash: []byte("h1"), UserID: "u_a", Device: "d", CreatedAt: model.Now()}))
	_, tu, err := p.TokenByHash([]byte("h1"), time.Now())
	chk(t, err)
	if tu.ActorID != u.ActorID {
		t.Fatal("TokenByHash 要帶 ActorID")
	}
	// 不是使用者 actor 的 id 找不到
	_, err = p.UserByActorID(model.SystemActorOmixor)
	wantNotFound(t, "系統 actor 不是使用者", err)
	// 其他使用者各有各的 actor
	u3 := saveU(t, p, "u_b", "b@x.com", "")
	if u3.ActorID == u.ActorID {
		t.Fatal("不同使用者要有不同 actor")
	}
	if a3, _ := p.ActorByID(u3.ActorID); a3.Name != "b@x.com" {
		t.Fatalf("沒有名稱時用 handle:%q", a3.Name)
	}
}

func TestUserActorSyncsHandleAndName(t *testing.T) {
	p := actorStore(t)
	u := saveU(t, p, "u_a", "old@x.com", "Old")
	u.Email, u.Name = "New@X.com", "New"
	chk(t, p.SaveUser(u))
	a, _ := p.ActorByID(u.ActorID)
	if a.Handle != "new@x.com" || a.Name != "New" {
		t.Fatalf("%+v", a)
	}
	if _, err := p.ActorByHandle(model.ActorKindUser, "", "old@x.com"); err == nil {
		t.Fatal("舊 handle 不再對得到")
	}
	if n := userActorCount(t, p); n != 1 {
		t.Fatal(n)
	}
	// 名稱留空不覆蓋(UpdateActorProfile 改過的保留)
	nm := "Profile"
	chk(t, p.UpdateActorProfile(u.ActorID, &nm, nil))
	u.Name = ""
	chk(t, p.SaveUser(u))
	if a, _ := p.ActorByID(u.ActorID); a.Name != "Profile" {
		t.Fatalf("%q", a.Name)
	}
	// 改成的新 email 已經有別的 actor 佔著(未註冊使用者):使用者照常存,actor 的 handle 維持
	ghost, err := p.EnsureUserActorByEmail("taken@x.com", "")
	chk(t, err)
	u.Email = "taken@x.com"
	chk(t, p.SaveUser(u))
	if a, _ := p.ActorByID(u.ActorID); a.Handle != "new@x.com" {
		t.Fatalf("handle 衝突時維持:%q", a.Handle)
	}
	if g, _ := p.ActorByHandle(model.ActorKindUser, "", "taken@x.com"); g.ID != ghost.ID {
		t.Fatal("未註冊使用者 actor 不動")
	}
}

func TestUserActorDisabledKeepsActor(t *testing.T) {
	p := actorStore(t)
	u := saveU(t, p, "u_a", "d@x.com", "D")
	u.Disabled = true
	chk(t, p.SaveUser(u))
	got, _ := p.UserByID("u_a")
	if !got.Disabled || got.ActorID != u.ActorID {
		t.Fatalf("%+v", got)
	}
	if a, _ := p.ActorByID(u.ActorID); a.Disabled() {
		t.Fatal("使用者的登入開關是 omixor_users.disabled,actor 不退役")
	}
	if n := userActorCount(t, p); n != 1 {
		t.Fatal(n)
	}
}

// D3:未註冊使用者 actor,之後同 email 的使用者建立時接回同一個
func TestUserActorUnregisteredClaimed(t *testing.T) {
	p := actorStore(t)
	g1, err := p.EnsureUserActorByEmail("Ghost@X.com", "")
	chk(t, err)
	g2, err := p.EnsureUserActorByEmail("ghost@x.com", "別名")
	chk(t, err)
	if g1.ID != g2.ID || g1.Handle != "ghost@x.com" || g1.Name != "ghost@x.com" {
		t.Fatalf("%+v %+v", g1, g2)
	}
	if _, err := p.UserByActorID(g1.ID); err == nil {
		t.Fatal("還沒有使用者")
	}
	u := saveU(t, p, "u_g", "ghost@x.com", "Ghost")
	if u.ActorID != g1.ID {
		t.Fatalf("要接回同一個 actor:%s vs %s", u.ActorID, g1.ID)
	}
	if a, _ := p.ActorByID(g1.ID); a.Name != "Ghost" {
		t.Fatalf("接回後名稱換成使用者的:%q", a.Name)
	}
	if n := userActorCount(t, p); n != 1 {
		t.Fatal(n)
	}
	// 已註冊的 email:EnsureUserActorByEmail 回同一個
	g3, err := p.EnsureUserActorByEmail("GHOST@x.com", "")
	chk(t, err)
	if g3.ID != u.ActorID {
		t.Fatal("已註冊的回同一個")
	}
	if _, err := p.EnsureUserActorByEmail("   ", ""); err == nil {
		t.Fatal("空 email 要錯")
	}
	// EnsureUserActor:已經有的原樣回
	e, err := p.EnsureUserActor("u_g")
	chk(t, err)
	if e.ID != u.ActorID {
		t.Fatal(e.ID)
	}
	_, err = p.EnsureUserActor("u_none")
	wantNotFound(t, "使用者不存在", err)
}

func TestUserActorEmailCaseCollision(t *testing.T) {
	p := actorStore(t)
	a := saveU(t, p, "u_1", "A@x.com", "")
	b := saveU(t, p, "u_2", "a@x.com", "") // omixor_users.email 的唯一區分大小寫,這種資料可能存在
	if a.ActorID == b.ActorID {
		t.Fatal("兩位使用者不能共用 actor")
	}
	ab, _ := p.ActorByID(b.ActorID)
	if ab.Handle != "u_2" {
		t.Fatalf("後來的改用使用者 id 當 handle:%q", ab.Handle)
	}
	// 再存一次不變
	chk(t, p.SaveUser(b))
	if x, _ := p.ActorByID(b.ActorID); x.Handle != "u_2" {
		t.Fatal(x.Handle)
	}
}

func TestUserActorBadActorID(t *testing.T) {
	p := actorStore(t)
	ag := newAgent(t, p, "o1", "helper", "")
	u := model.User{ID: "u_a", Email: "a@x.com", Role: "member", CreatedAt: model.Now(), ActorID: ag.ID}
	if err := p.SaveUser(u); !errors.Is(err, ErrBadRef) {
		t.Fatalf("角色 actor 不能當使用者的 actor:%v", err)
	}
	u.ActorID = "usr_00000000-0000-7000-8000-000000000000"
	if err := p.SaveUser(u); !errors.Is(err, ErrBadRef) {
		t.Fatalf("不存在的 actor:%v", err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_users`); n != 0 {
		t.Fatal("失敗的 SaveUser 不留半套", n)
	}
	// 外鍵:直接寫 SQL 指到角色 actor 也被擋(複合外鍵 (actor_id, kind))
	good := saveU(t, p, "u_ok", "ok@x.com", "")
	if _, err := p.pool.Exec(context.Background(), `update omixor_users set actor_id = $1 where id = 'u_ok'`, ag.ID); err == nil {
		t.Fatal("外鍵要擋住指到角色 actor")
	}
	// 同一個 actor 不能給兩位使用者(唯一索引)
	saveU(t, p, "u_b", "b@x.com", "")
	if _, err := p.pool.Exec(context.Background(), `update omixor_users set actor_id = $1 where id = 'u_b'`, good.ActorID); err == nil {
		t.Fatal("actor_id 要唯一")
	}
}

// 同一位使用者同時登入(多實例):只有一個 actor,沒有錯誤
func TestUserActorConcurrentSave(t *testing.T) {
	p := actorStore(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = p.SaveUser(model.User{ID: "u_c", Email: "c@x.com", Role: "member", CreatedAt: model.Now()})
		}()
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("%d:%v", i, e)
		}
	}
	if n := userActorCount(t, p); n != 1 {
		t.Fatal(n)
	}
}

// ---- 啟動遷移 ----

// legacyUsers 把一個已遷移的資料庫還原成「HEAD 之前的結構」:拿掉 actor_id、kind、索引、外鍵,刪光使用者 actor,再用舊方式(沒有 actor 欄位)寫入使用者、token、成員
func legacyUsers(t *testing.T, p *PG, n int, withExtra bool) {
	t.Helper()
	pgExec(t, p, `alter table omixor_users drop constraint if exists fk_users_actor`)
	pgExec(t, p, `alter table omixor_users drop column if exists actor_id`)
	pgExec(t, p, `alter table omixor_users drop column if exists kind`)
	pgExec(t, p, `delete from omixor_actors where kind = 'user'`)
	pgExec(t, p, `insert into omixor_orgs (id, name) values ('o1','o1') on conflict do nothing`)
	pgExec(t, p, `insert into omixor_users (id, email, name, role, created_at)
		select 'u_' || g, case when g % 7 = 0 then upper('user' || g || '@x.com') else 'user' || g || '@x.com' end, 'N' || g, 'member', now() - (g || ' seconds')::interval from generate_series(1, $1::int) g`, n)
	pgExec(t, p, `insert into omixor_tokens (id, token_hash, user_id, created_at) select 't' || g, int4send(g), 'u_' || g, now() from generate_series(1, least($1::int, 50)) g`, n)
	pgExec(t, p, `insert into omixor_org_members (org, user_id, role) select 'o1', 'u_' || g, 'member' from generate_series(1, least($1::int, 50)) g`, n)
	if withExtra {
		pgExec(t, p, `insert into omixor_users (id, email, name, role) values ('u_ghost', 'ghost@x.com', 'Ghost', 'member')`)
		pgExec(t, p, `insert into omixor_actors (id, kind, name, handle) values ('usr_11111111-1111-7111-8111-111111111111', 'user', 'ghost@x.com', 'ghost@x.com')`)
		pgExec(t, p, `insert into omixor_users (id, email, name, role) values ('u_weird', 'a b', 'W', 'member')`) // email 不能當 handle
		pgExec(t, p, `insert into omixor_users (id, email, name, role) values ('u_up1', 'Dup@x.com', '', 'member'), ('u_up2', 'dup@x.com', '', 'member')`)
	}
}

type userSnap struct{ actors, users string }

func snapUsers(t *testing.T, p *PG) userSnap {
	t.Helper()
	var a, u string
	chk(t, p.pool.QueryRow(context.Background(), `select coalesce(string_agg(format('%s|%s|%s|%s|%s', id, name, handle, created_at, xmin::text), ',' order by id), '') from omixor_actors`).Scan(&a))
	chk(t, p.pool.QueryRow(context.Background(), `select coalesce(string_agg(format('%s|%s|%s', id, actor_id, xmin::text), ',' order by id), '') from omixor_users`).Scan(&u))
	return userSnap{a, u}
}

func checkUsersConverted(t *testing.T, p *PG, wantUsers int) {
	t.Helper()
	if n := pgCount(t, p, `select count(*) from omixor_users`); n != wantUsers {
		t.Fatal("使用者數", n)
	}
	if n := pgCount(t, p, `select count(*) from omixor_users where actor_id is null`); n != 0 {
		t.Fatal("還有沒 actor 的使用者", n)
	}
	// 每位使用者各自一個 actor、都是 usr_、沒有孤兒
	if n := pgCount(t, p, `select count(distinct actor_id) from omixor_users`); n != wantUsers {
		t.Fatal("actor 不唯一", n)
	}
	if n := pgCount(t, p, `select count(*) from omixor_users u where not exists (select 1 from omixor_actors a where a.id = u.actor_id and a.kind = 'user')`); n != 0 {
		t.Fatal("孤兒", n)
	}
	// 使用者 id 沒被改寫(D11)
	if n := pgCount(t, p, `select count(*) from omixor_users where id not like 'u\_%'`); n != 0 {
		t.Fatal("使用者 id 不能改寫", n)
	}
	exists, validated, err := fkExists(context.Background(), p.pool, "omixor_users", actorUsersFK)
	chk(t, err)
	if !exists || !validated {
		t.Fatalf("外鍵 exists=%v validated=%v", exists, validated)
	}
}

func TestUserActorMigrationFromLegacy(t *testing.T) {
	old := actorBackfillBatch
	actorBackfillBatch = 7 // 多批
	t.Cleanup(func() { actorBackfillBatch = old })
	p := fkPG(t, fkSchema(t))
	legacyUsers(t, p, 40, true)
	chk(t, p.migrateActorUsers())
	checkUsersConverted(t, p, 44)

	// 既有使用者對應:handle = email 小寫、名稱
	u7, err := p.UserByID("u_7")
	chk(t, err)
	a7, _ := p.ActorByID(u7.ActorID)
	if u7.Email != "USER7@X.COM" || a7.Handle != "user7@x.com" || a7.Name != "N7" {
		t.Fatalf("%+v %+v", u7, a7)
	}
	// 接回未註冊使用者(沒有第二個),名稱換成使用者的
	g, _ := p.UserByID("u_ghost")
	if g.ActorID != "usr_11111111-1111-7111-8111-111111111111" {
		t.Fatalf("要接回既有 actor:%s", g.ActorID)
	}
	if ga, _ := p.ActorByID(g.ActorID); ga.Name != "Ghost" {
		t.Fatal(ga.Name)
	}
	// email 不能當 handle → 用使用者 id
	w, _ := p.UserByID("u_weird")
	if wa, _ := p.ActorByID(w.ActorID); wa.Handle != "u_weird" {
		t.Fatalf("%q", wa.Handle)
	}
	// 只差大小寫的 email:兩位各自一個 actor
	d1, _ := p.UserByID("u_up1")
	d2, _ := p.UserByID("u_up2")
	if d1.ActorID == d2.ActorID {
		t.Fatal("不能共用")
	}
	// 遷移前產生的 token 仍可登入、成員資格不變(使用者 id 沒變)
	_, tu, err := p.TokenByHash([]byte{0, 0, 0, 3}, time.Now())
	chk(t, err)
	if tu.ID != "u_3" || tu.ActorID == "" {
		t.Fatalf("%+v", tu)
	}
	if r, err := p.MemberRole("o1", "u_3"); err != nil || r != "member" {
		t.Fatal(r, err)
	}
	// 冪等:重跑什麼都不動(連 xmin 都不變)
	before := snapUsers(t, p)
	for i := 0; i < 3; i++ {
		chk(t, p.migrateActorUsers())
	}
	if after := snapUsers(t, p); after != before {
		t.Fatal("重跑不該動資料")
	}
	// 之後 SaveUser(登入)不新增 actor
	n0 := userActorCount(t, p)
	chk(t, p.SaveUser(*u7))
	if userActorCount(t, p) != n0 {
		t.Fatal("登入不該新增 actor")
	}
}

// 部分已轉換(一半有 actor_id)再跑、舊 revision 寫入的沒有 actor_id 的列(滾動部署)補得起來
func TestUserActorMigrationPartialAndRollingDeploy(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	for i := 0; i < 6; i++ {
		saveU(t, p, fmt.Sprintf("u_%d", i), fmt.Sprintf("p%d@x.com", i), "")
	}
	ids, _ := p.ListUsers()
	keep := map[string]string{}
	for i, u := range ids {
		if i%2 == 0 {
			keep[u.ID] = u.ActorID
		}
	}
	pgExec(t, p, `alter table omixor_users drop constraint fk_users_actor`)
	pgExec(t, p, `update omixor_users set actor_id = null where id = any($1)`, []string{"u_1", "u_3", "u_5"})
	// 舊 revision 這時才寫入的使用者(沒有 actor_id、也沒有 actor)
	pgExec(t, p, `insert into omixor_users (id, email, name, role) values ('u_late', 'late@x.com', '', 'member')`)
	// 在補齊之前 EnsureUserActor 就能補單一位
	e, err := p.EnsureUserActor("u_late")
	chk(t, err)
	if lu, _ := p.UserByID("u_late"); lu.ActorID != e.ID {
		t.Fatal("EnsureUserActor 要把 actor_id 寫回")
	}
	chk(t, p.migrateActorUsers())
	checkUsersConverted(t, p, 7)
	for id, aid := range keep {
		if u, _ := p.UserByID(id); u.ActorID != aid {
			t.Fatalf("已有的 actor 不能換:%s", id)
		}
	}
	// 沒有外鍵檢查之外的情況:舊 revision 又寫入沒 actor 的列,下次啟動補上
	pgExec(t, p, `insert into omixor_users (id, email, name, role) values ('u_late2', 'late2@x.com', '', 'member')`)
	chk(t, p.migrateActorUsers())
	checkUsersConverted(t, p, 8)
}

// 多個實例同時啟動在舊結構的資料庫上:都成功、每位使用者恰好一個 actor
func TestUserActorMigrationConcurrentStartup(t *testing.T) {
	url := fkSchema(t)
	first := fkPG(t, url)
	legacyUsers(t, first, 60, true)
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	pgs := make([]*PG, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pgs[i], errs[i] = NewPG(url)
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("實例 %d 啟動失敗:%v", i, errs[i])
		}
		pgs[i].WaitFKValidate()
		defer pgs[i].Close()
	}
	checkUsersConverted(t, first, 64)
	// 再重複啟動:不變
	before := snapUsers(t, first)
	again, err := NewPG(url)
	chk(t, err)
	again.WaitFKValidate()
	defer again.Close()
	if after := snapUsers(t, first); after != before {
		t.Fatal("重複啟動不該動資料")
	}
}

// 2 萬位使用者的補齊耗時(預設不跑:OMIXOR_TEST_BIG=1 才跑)
func TestUserActorMigration20kUsers(t *testing.T) {
	if os.Getenv("OMIXOR_TEST_BIG") == "" {
		t.Skip("設 OMIXOR_TEST_BIG=1 才跑")
	}
	p := fkPG(t, fkSchema(t))
	legacyUsers(t, p, 20000, false)
	start := time.Now()
	chk(t, p.migrateActorUsers())
	t.Logf("20000 位使用者補齊 actor、加外鍵、validate:%v", time.Since(start))
	checkUsersConverted(t, p, 20000)
	start = time.Now()
	chk(t, p.migrateActorUsers())
	t.Logf("重跑(已完成):%v", time.Since(start))
}
