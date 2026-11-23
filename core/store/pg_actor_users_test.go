package store

// 使用者接上 actor(ISS-36 B-2b):SaveUser 自動建立/接回 actor、同步 handle 與名稱、停用、大小寫、未註冊使用者(D3)、
// 啟動遷移補齊(冪等、多批、並行啟動、舊結構轉換)與外鍵

import (
	"context"
	"errors"
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

type userSnap struct{ actors, users string }
