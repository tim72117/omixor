package store

// omixor_actors(ISS-36 B-2a):建表與系統 actor 的啟動遷移(冪等、並行啟動)、CRUD、唯一約束與退役後的名字、守門

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/storetest/testdb"
)

func actorStore(t *testing.T) *PG {
	t.Helper()
	return testPG(t).(*PG)
}

func newUser(t *testing.T, s Store, email string) model.ActorRecord {
	t.Helper()
	a, err := s.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "N " + email, Handle: email})
	chk(t, err)
	return a
}

func newAgent(t *testing.T, s Store, org, handle string, owner string) model.ActorRecord {
	t.Helper()
	a, err := s.CreateActor(model.ActorRecord{Kind: model.ActorKindAgent, Org: org, Name: handle, Handle: handle, Owner: owner})
	chk(t, err)
	return a
}

func wantDup(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("%s:要 ErrDuplicateKey,拿到 %v", what, err)
	}
}

// 系統 actor 在遷移後就在:固定 id、名稱、handle,之後重複啟動不變
func TestActorSystemActorsSeeded(t *testing.T) {
	p := actorStore(t)
	for _, s := range model.SystemActors {
		a, err := p.ActorByID(s.ID)
		chk(t, err)
		if a.Kind != model.ActorKindSystem || a.Handle != s.Handle || a.Name != s.Name || a.Org != "" || a.Owner != "" || a.Disabled() {
			t.Fatalf("%+v", a)
		}
		byH, err := p.ActorByHandle(model.ActorKindSystem, "", s.Handle)
		chk(t, err)
		if byH.ID != s.ID {
			t.Fatal(byH.ID)
		}
	}
	list, err := p.ListActors(ActorFilter{})
	chk(t, err)
	if len(list) != len(model.SystemActors) {
		t.Fatalf("新資料庫只有系統 actor:%d", len(list))
	}
	// 固定 id(資料庫裡已經存在的值,不能改)
	want := []string{"sys_chain", "sys_omixor", "sys_routine"}
	var got []string
	for _, a := range list {
		got = append(got, a.ID)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("系統 actor id 變了:%v", got)
	}
}

// 重複啟動冪等:列數與列內容不變(連 xmin 都不動 = 沒有重寫列);被改壞的系統 actor 啟動時校正回來
func TestActorMigrateIdempotent(t *testing.T) {
	p := fkPG(t, fkSchema(t)) // 要改系統列,用全新的資料庫(不回收)
	snap := func() string {
		var s string
		chk(t, p.pool.QueryRow(context.Background(), `select coalesce(string_agg(format('%s|%s|%s|%s|%s|%s', id, name, handle, created_at, disabled_at, xmin::text), ',' order by id), '') from omixor_actors`).Scan(&s))
		return s
	}
	before := snap()
	for i := 0; i < 3; i++ {
		chk(t, syncSystemActors(context.Background(), p.pool))
	}
	if after := snap(); after != before {
		t.Fatalf("重跑不該動列:\n%s\n%s", before, after)
	}
	// 被改壞(手動 SQL)→ 下次啟動校正;使用者資料不受影響
	u := newUser(t, p, "keep@x.com")
	pgExec(t, p, `update omixor_actors set name = 'bad', disabled_at = now() where id = 'sys_omixor'`)
	chk(t, syncSystemActors(context.Background(), p.pool))
	a, err := p.ActorByID(model.SystemActorOmixor)
	chk(t, err)
	if a.Name != "omixor 系統" || a.Disabled() {
		t.Fatalf("系統 actor 要被校正:%+v", a)
	}
	if _, err := p.ActorByID(u.ID); err != nil {
		t.Fatal("使用者 actor 不受影響", err)
	}
}

// 多個 NewPG 同時對全新(空)資料庫啟動 + 之後再重複啟動:都成功、系統 actor 只有一份、固定 id 不變(建表在 advisory lock 內)
func TestActorConcurrentStartup(t *testing.T) {
	url := testdb.Blank(t)
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
	}
	t.Cleanup(func() {
		for _, p := range pgs {
			p.Close()
		}
	})
	if c := pgCount(t, pgs[0], `select count(*) from omixor_actors`); c != len(model.SystemActors) {
		t.Fatalf("系統 actor 列數 %d", c)
	}
	for _, id := range []string{"sys_omixor", "sys_routine", "sys_chain"} {
		if _, err := pgs[0].ActorByID(id); err != nil {
			t.Fatal(id, err)
		}
	}
	// 再啟動一輪
	again, err := NewPG(url)
	chk(t, err)
	defer again.Close()
	if c := pgCount(t, again, `select count(*) from omixor_actors`); c != len(model.SystemActors) {
		t.Fatalf("重複啟動後列數 %d", c)
	}
}

func TestActorCreateAndGet(t *testing.T) {
	p := actorStore(t)
	u, err := p.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "Tim", Handle: "  @Tim@Example.COM ", Avatar: "https://x/a.png"})
	chk(t, err)
	if !model.ValidActorIDOf(model.ActorKindUser, u.ID) || u.Handle != "tim@example.com" || u.Avatar != "https://x/a.png" || u.CreatedAt.IsZero() || u.Disabled() {
		t.Fatalf("%+v", u)
	}
	got, err := p.ActorByID(u.ID)
	chk(t, err)
	if *got != u {
		t.Fatalf("讀回要相同:%+v vs %+v", got, u)
	}
	// 依 handle:不分大小寫、可帶 @
	for _, h := range []string{"tim@example.com", "TIM@EXAMPLE.COM", "@tim@example.com", " tim@example.com "} {
		g, err := p.ActorByHandle(model.ActorKindUser, "", h)
		if err != nil || g.ID != u.ID {
			t.Fatalf("handle %q:%v", h, err)
		}
	}
	wantNotFound(t, "沒有的 handle", errOf(p.ActorByHandle(model.ActorKindUser, "", "none@x.com")))
	wantNotFound(t, "壞 handle", errOf(p.ActorByHandle(model.ActorKindUser, "", "a b")))
	wantNotFound(t, "沒有的 id", errOf(p.ActorByID("usr_00000000-0000-7000-8000-000000000000")))

	// 指定 id(匯入、遷移)
	id := model.NewUserActorID(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	at := mt("2020-01-02T03:04:05Z")
	c, err := p.CreateActor(model.ActorRecord{ID: id, Kind: model.ActorKindUser, Name: "Old", Handle: "old@x.com", CreatedAt: at})
	chk(t, err)
	if c.ID != id || !c.CreatedAt.Equal(at.Time) {
		t.Fatalf("%+v", c)
	}
	// 角色:owner 與組織
	ag := newAgent(t, p, "o1", "Helper", u.ID)
	if ag.Handle != "helper" || ag.Owner != u.ID || ag.Org != "o1" || !strings.HasPrefix(ag.ID, "agt_") {
		t.Fatalf("%+v", ag)
	}
	g, err := p.ActorByHandle(model.ActorKindAgent, "o1", "@HELPER")
	chk(t, err)
	if g.ID != ag.ID {
		t.Fatal(g.ID)
	}
	wantNotFound(t, "別的組織沒有", errOf(p.ActorByHandle(model.ActorKindAgent, "o2", "helper")))
	wantNotFound(t, "角色一定要給組織", errOf(p.ActorByHandle(model.ActorKindAgent, "", "helper")))
	// 沒有 owner 的角色(遷移時找不到人)
	if a := newAgent(t, p, "o1", "orphan", ""); a.Owner != "" {
		t.Fatal(a.Owner)
	}

	// 批次取
	m, err := p.ActorsByIDs([]string{u.ID, ag.ID, u.ID, "usr_00000000-0000-7000-8000-000000000000", "sys_chain"})
	chk(t, err)
	if len(m) != 3 || m[u.ID].Handle != "tim@example.com" || m[ag.ID].Org != "o1" || m["sys_chain"].Handle != "chain" {
		t.Fatalf("%v", m)
	}
	if m, err := p.ActorsByIDs(nil); err != nil || m == nil || len(m) != 0 {
		t.Fatal("空輸入回空 map", m, err)
	}
}

func TestActorCreateRejects(t *testing.T) {
	p := actorStore(t)
	u := newUser(t, p, "u@x.com")
	ag := newAgent(t, p, "o", "a1", u.ID)
	bad := map[string]model.ActorRecord{
		"種類空":               {Name: "x", Handle: "x"},
		"種類不認得":             {Kind: "robot", Name: "x", Handle: "x"},
		"使用者 handle 空":      {Kind: model.ActorKindUser, Name: "x", Handle: "  "},
		"使用者 handle 空白":     {Kind: model.ActorKindUser, Name: "x", Handle: "a b@x.com"},
		"角色 handle 不合法":     {Kind: model.ActorKindAgent, Org: "o", Name: "x", Handle: "Has Space"},
		"角色沒組織":             {Kind: model.ActorKindAgent, Name: "x", Handle: "x"},
		"使用者帶組織":            {Kind: model.ActorKindUser, Org: "o", Name: "x", Handle: "x@x.com"},
		"id 前綴與種類不符":        {ID: ag.ID, Kind: model.ActorKindUser, Name: "x", Handle: "x@x.com"},
		"id 格式壞":            {ID: "u_abc123", Kind: model.ActorKindUser, Name: "x", Handle: "x@x.com"},
		"owner 是角色":         {Kind: model.ActorKindAgent, Org: "o", Name: "x", Handle: "x", Owner: ag.ID},
		"owner 格式壞":         {Kind: model.ActorKindAgent, Org: "o", Name: "x", Handle: "x", Owner: "tim"},
		"使用者帶 owner":        {Kind: model.ActorKindUser, Name: "x", Handle: "x@x.com", Owner: u.ID},
		"系統 id 要自己給":        {Kind: model.ActorKindSystem, Name: "x", Handle: "x"},
		"系統 handle 與 id 不符": {ID: "sys_foo", Kind: model.ActorKindSystem, Name: "x", Handle: "bar"},
	}
	for name, a := range bad {
		if _, err := p.CreateActor(a); err == nil {
			t.Errorf("%s 應該被拒絕", name)
		}
	}
	// owner 不存在 → ErrBadRef
	_, err := p.CreateActor(model.ActorRecord{Kind: model.ActorKindAgent, Org: "o", Name: "x", Handle: "ghost-owner", Owner: model.NewUserActorID(time.Now())})
	if !errors.Is(err, ErrBadRef) {
		t.Fatalf("owner 不存在要 ErrBadRef:%v", err)
	}
	// 沒有留下半筆
	if n := pgCount(t, p, `select count(*) from omixor_actors where handle in ('x','ghost-owner')`); n != 0 {
		t.Fatal("拒絕的不能寫入", n)
	}
	// 系統 actor 可以用固定 id 重建?id 已存在 → 衝突
	_, err = p.CreateActor(model.ActorRecord{ID: "sys_omixor", Kind: model.ActorKindSystem, Name: "x", Handle: "omixor"})
	wantDup(t, "系統 actor 已存在", err)
	// 其他固定的系統 actor(遷移才建的 legacy)可以由程式建
	l, err := p.CreateActor(model.ActorRecord{ID: "sys_legacy_1a2b3c4d", Kind: model.ActorKindSystem, Name: "冒名", Handle: "legacy_1a2b3c4d"})
	chk(t, err)
	if l.ID != "sys_legacy_1a2b3c4d" {
		t.Fatal(l.ID)
	}
}

// DB 層約束自己也擋(繞過 Go 檢查的手動 SQL、其他程式)
func TestActorDBConstraints(t *testing.T) {
	p := actorStore(t)
	u := newUser(t, p, "c@x.com")
	cases := map[string]string{
		"id 前綴與 kind 不符":     `insert into omixor_actors (id, kind, name, handle) values ('agt_x', 'user', 'n', 'x@x.com')`,
		"使用者有 org":           `insert into omixor_actors (id, kind, org, name, handle) values ('usr_x', 'user', 'o', 'n', 'x@x.com')`,
		"角色沒 org":            `insert into omixor_actors (id, kind, name, handle) values ('agt_x', 'agent', 'n', 'x')`,
		"系統有 org":            `insert into omixor_actors (id, kind, org, name, handle) values ('sys_x', 'system', 'o', 'n', 'x')`,
		"kind 不認得":           `insert into omixor_actors (id, kind, name, handle) values ('usr_x', 'robot', 'n', 'x')`,
		"handle 空白":          `insert into omixor_actors (id, kind, name, handle) values ('usr_x', 'user', 'n', '  ')`,
		"使用者 handle 有大寫":     `insert into omixor_actors (id, kind, name, handle) values ('usr_x', 'user', 'n', 'X@x.com')`,
		"使用者有 owner":         `insert into omixor_actors (id, kind, name, handle, owner) values ('usr_x', 'user', 'n', 'x@x.com', '` + u.ID + `')`,
		"owner 不存在":          `insert into omixor_actors (id, kind, org, name, handle, owner) values ('agt_x', 'agent', 'o', 'n', 'x', 'usr_nope')`,
		"使用者 handle 大小寫繞過唯一": `insert into omixor_actors (id, kind, name, handle) values ('usr_y', 'user', 'n', 'c@x.com')`,
	}
	for name, q := range cases {
		if _, err := p.pool.Exec(context.Background(), q); err == nil {
			t.Errorf("%s:DB 應該擋下", name)
		}
	}
	// owner 是 restrict:有角色指著的使用者 actor 不能刪
	newAgent(t, p, "o", "owned", u.ID)
	if _, err := p.pool.Exec(context.Background(), `delete from omixor_actors where id = $1`, u.ID); err == nil {
		t.Error("被當 owner 的 actor 不能刪")
	}
}

// 唯一性:使用者 email 全伺服器唯一(不分大小寫)、角色現役 (org, handle) 唯一、系統名稱唯一;不同組織、不同種類同名可以
func TestActorUniqueness(t *testing.T) {
	p := actorStore(t)
	u := newUser(t, p, "dup@x.com")
	_, err := p.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "other", Handle: "DUP@x.com"})
	wantDup(t, "email 大小寫不同也算同一個", err)
	_, err = p.CreateActor(model.ActorRecord{ID: u.ID, Kind: model.ActorKindUser, Name: "other", Handle: "other@x.com"})
	wantDup(t, "id 重複", err)
	// 使用者被停用後仍佔著 email
	chk(t, p.SetActorDisabled(u.ID, true, time.Now()))
	_, err = p.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "other", Handle: "dup@x.com"})
	wantDup(t, "停用的使用者仍佔 email", err)
	if g, err := p.ActorByHandle(model.ActorKindUser, "", "dup@x.com"); err != nil || g.ID != u.ID {
		t.Fatal("停用的使用者仍找得到", err)
	}

	a1 := newAgent(t, p, "o1", "helper", u.ID)
	_, err = p.CreateActor(model.ActorRecord{Kind: model.ActorKindAgent, Org: "o1", Name: "x", Handle: "HELPER"})
	wantDup(t, "同組織現役同名", err)
	b := newAgent(t, p, "o2", "helper", "") // 不同組織可以同名
	if b.ID == a1.ID {
		t.Fatal()
	}
	// 同 handle 的使用者與角色互不衝突(分得開要靠種類)
	chk(t, errOfA(p.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "x", Handle: "helper"})))
	chk(t, errOfA(p.CreateActor(model.ActorRecord{Kind: model.ActorKindAgent, Org: "o1", Name: "x", Handle: "dup-at-x"})))

	// 退役後名字讓出來:同名再建是新的 actor,舊的仍在(ActorByID 找得到),handle 查詢指向新的
	t0 := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	chk(t, p.SetActorDisabled(a1.ID, true, t0))
	old, err := p.ActorByID(a1.ID)
	chk(t, err)
	if !old.Disabled() || !old.DisabledAt.Equal(t0) {
		t.Fatalf("%+v", old)
	}
	wantNotFound(t, "退役的不算現役", errOf(p.ActorByHandle(model.ActorKindAgent, "o1", "helper")))
	a2 := newAgent(t, p, "o1", "helper", u.ID)
	if a2.ID == a1.ID {
		t.Fatal("同名再建是新的 actor")
	}
	if g, err := p.ActorByHandle(model.ActorKindAgent, "o1", "helper"); err != nil || g.ID != a2.ID {
		t.Fatal("handle 指向新的", err)
	}
	// 退役的可以有多個同名
	chk(t, p.SetActorDisabled(a2.ID, true, t0.Add(time.Hour)))
	a3 := newAgent(t, p, "o1", "helper", "")
	chk(t, p.SetActorDisabled(a3.ID, true, t0.Add(2*time.Hour)))
	if n := pgCount(t, p, `select count(*) from omixor_actors where org = 'o1' and handle = 'helper' and disabled_at is not null`); n != 3 {
		t.Fatal(n)
	}
	// 重新啟用:名字被現役的占走就衝突
	a4 := newAgent(t, p, "o1", "helper", "")
	wantDup(t, "啟用時同名現役已存在", p.SetActorDisabled(a2.ID, false, time.Time{}))
	chk(t, p.SetActorDisabled(a4.ID, true, time.Now()))
	chk(t, p.SetActorDisabled(a2.ID, false, time.Time{}))
	if g, err := p.ActorByHandle(model.ActorKindAgent, "o1", "helper"); err != nil || g.ID != a2.ID {
		t.Fatal("重新啟用後又是現役", err)
	}
	// 系統名稱唯一(DB 層;Go 層因為 id 與 handle 綁定先擋)
	if _, err := p.pool.Exec(context.Background(), `insert into omixor_actors (id, kind, name, handle) values ('sys_other', 'system', 'n', 'omixor')`); err == nil {
		t.Fatal("系統名稱要唯一")
	}
}

func TestActorList(t *testing.T) {
	p := actorStore(t)
	u1 := newUser(t, p, "l1@x.com")
	u2 := newUser(t, p, "l2@x.com")
	// created_at 只有秒的解析度,同秒建立時由隨機的 uuidv7 id 決勝;排序斷言要明確給嚴格遞增的 CreatedAt
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	mk := func(org, handle, owner string, n int) model.ActorRecord {
		a, err := p.CreateActor(model.ActorRecord{Kind: model.ActorKindAgent, Org: org, Name: handle, Handle: handle, Owner: owner,
			CreatedAt: model.Time{Time: base.Add(time.Duration(n) * time.Second)}})
		chk(t, err)
		return a
	}
	a1 := mk("o1", "a1", u1.ID, 1)
	a2 := mk("o1", "a2", u1.ID, 2)
	b1 := mk("o2", "b1", u2.ID, 3)
	chk(t, p.SetActorDisabled(a2.ID, true, time.Now()))
	chk(t, p.SetActorDisabled(u2.ID, true, time.Now()))
	ids := func(f ActorFilter) []string {
		l, err := p.ListActors(f)
		chk(t, err)
		var out []string
		for _, a := range l {
			out = append(out, a.Handle)
		}
		return out
	}
	eq := func(what string, got []string, want ...string) {
		t.Helper()
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s:%v,應為 %v", what, got, want)
		}
	}
	eq("預設只列現役", ids(ActorFilter{}), "chain", "omixor", "routine", "l1@x.com", "a1", "b1")
	eq("含退役", ids(ActorFilter{IncludeDisabled: true}), "chain", "omixor", "routine", "l1@x.com", "l2@x.com", "a1", "a2", "b1")
	eq("只看使用者", ids(ActorFilter{Kind: model.ActorKindUser}), "l1@x.com")
	eq("只看系統", ids(ActorFilter{Kind: model.ActorKindSystem}), "chain", "omixor", "routine")
	eq("組織 o1 的角色(現役)", ids(ActorFilter{Org: "o1"}), "a1")
	eq("組織 o1 含退役", ids(ActorFilter{Org: "o1", IncludeDisabled: true}), "a1", "a2")
	eq("組織 + 種類不符", ids(ActorFilter{Org: "o1", Kind: model.ActorKindUser}))
	// 排序:建立時間舊的在前
	l, err := p.ListActors(ActorFilter{Kind: model.ActorKindAgent, IncludeDisabled: true})
	chk(t, err)
	if len(l) != 3 || l[0].ID != a1.ID || l[1].ID != a2.ID || l[2].ID != b1.ID {
		t.Fatalf("%+v", l)
	}
}

func TestActorUpdate(t *testing.T) {
	p := actorStore(t)
	u := newUser(t, p, "up@x.com")
	other := newUser(t, p, "other@x.com")
	ag := newAgent(t, p, "o", "bot", u.ID)
	str := func(s string) *string { return &s }

	chk(t, p.UpdateActorProfile(u.ID, str("新名字"), str("https://x/b.png")))
	g, _ := p.ActorByID(u.ID)
	if g.Name != "新名字" || g.Avatar != "https://x/b.png" {
		t.Fatalf("%+v", g)
	}
	chk(t, p.UpdateActorProfile(u.ID, str("只改名"), nil))
	g, _ = p.ActorByID(u.ID)
	if g.Name != "只改名" || g.Avatar != "https://x/b.png" {
		t.Fatalf("nil 不改:%+v", g)
	}
	chk(t, p.UpdateActorProfile(u.ID, nil, str("")))
	g, _ = p.ActorByID(u.ID)
	if g.Name != "只改名" || g.Avatar != "" {
		t.Fatalf("空字串清掉頭像:%+v", g)
	}
	wantNotFound(t, "更新不存在的", p.UpdateActorProfile("usr_00000000-0000-7000-8000-000000000000", str("x"), nil))
	wantNotFound(t, "id 壞", p.UpdateActorProfile("nope", str("x"), nil))

	// handle
	chk(t, p.UpdateActorHandle(u.ID, " New@X.com "))
	g, _ = p.ActorByID(u.ID)
	if g.Handle != "new@x.com" {
		t.Fatal(g.Handle)
	}
	wantNotFound(t, "舊 email 找不到", errOf(p.ActorByHandle(model.ActorKindUser, "", "up@x.com")))
	wantDup(t, "改成別人的 email", p.UpdateActorHandle(u.ID, "OTHER@x.com"))
	chk(t, p.UpdateActorHandle(u.ID, "new@x.com")) // 改成自己現在的
	if err := p.UpdateActorHandle(u.ID, "a b"); err == nil {
		t.Fatal("壞 handle")
	}
	if err := p.UpdateActorHandle(ag.ID, "Not Valid"); err == nil {
		t.Fatal("角色 handle 要合法")
	}
	chk(t, p.UpdateActorHandle(ag.ID, "Renamed"))
	g, _ = p.ActorByID(ag.ID)
	if g.Handle != "renamed" || g.Org != "o" || g.Owner != u.ID {
		t.Fatalf("%+v", g)
	}
	wantNotFound(t, "改不存在的 handle", p.UpdateActorHandle(model.NewUserActorID(time.Now()), "zz@x.com"))
	_ = other

	// 停用/啟用:已停用的保留原時間;啟用清掉;不存在
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	chk(t, p.SetActorDisabled(ag.ID, true, t1))
	chk(t, p.SetActorDisabled(ag.ID, true, t1.Add(time.Hour)))
	g, _ = p.ActorByID(ag.ID)
	if !g.Disabled() || !g.DisabledAt.Equal(t1) {
		t.Fatalf("已停用的保留原時間:%+v", g.DisabledAt)
	}
	chk(t, p.SetActorDisabled(ag.ID, false, time.Time{}))
	chk(t, p.SetActorDisabled(ag.ID, false, time.Time{})) // 本來就啟用
	g, _ = p.ActorByID(ag.ID)
	if g.Disabled() {
		t.Fatal("啟用")
	}
	chk(t, p.SetActorDisabled(ag.ID, true, time.Time{})) // 零值 = 現在
	g, _ = p.ActorByID(ag.ID)
	if !g.Disabled() || time.Since(g.DisabledAt.Time) > time.Minute {
		t.Fatal("零值時間 = 現在", g.DisabledAt)
	}
	wantNotFound(t, "停用不存在的", p.SetActorDisabled(model.NewAgentActorID(time.Now()), true, time.Now()))
	wantNotFound(t, "停用 id 壞", p.SetActorDisabled("helper", true, time.Now()))
}

// 系統 actor 不能改
func TestActorSystemImmutable(t *testing.T) {
	p := actorStore(t)
	n := "x"
	for _, id := range []string{"sys_omixor", "sys_routine", "sys_chain", "sys_legacy_unknown"} {
		for what, err := range map[string]error{
			"停用":       p.SetActorDisabled(id, true, time.Now()),
			"啟用":       p.SetActorDisabled(id, false, time.Time{}),
			"改名":       p.UpdateActorProfile(id, &n, nil),
			"改 handle": p.UpdateActorHandle(id, "zzz"),
		} {
			if !errors.Is(err, ErrSystemActor) {
				t.Errorf("%s %s:要 ErrSystemActor,拿到 %v", id, what, err)
			}
		}
	}
	if a, _ := p.ActorByID("sys_omixor"); a.Name != "omixor 系統" || a.Disabled() {
		t.Fatalf("%+v", a)
	}
}

// 在 Atomic 裡建立,失敗整批還原(和其他方法一樣走同一個連線)
func TestActorInAtomic(t *testing.T) {
	p := actorStore(t)
	boom := errors.New("boom")
	err := p.Atomic(func(tx Store) error {
		if _, err := tx.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "x", Handle: "atomic@x.com"}); err != nil {
			return err
		}
		if _, err := tx.ActorByHandle(model.ActorKindUser, "", "atomic@x.com"); err != nil {
			t.Error("同一個交易內讀得到", err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	wantNotFound(t, "還原", errOf(p.ActorByHandle(model.ActorKindUser, "", "atomic@x.com")))
}

// 刪組織不連帶動 actor(角色 actor 沒有組織外鍵;之後的包把 DeleteOrg 改成退役)
func TestActorNotTiedToOrgs(t *testing.T) {
	p := actorStore(t)
	chk(t, p.CreateOrg(model.Org{ID: "gone", Name: "g", CreatedAt: model.Now()}))
	a := newAgent(t, p, "gone", "ghost", "")
	chk(t, p.DeleteOrg("gone"))
	if _, err := p.ActorByID(a.ID); err != nil {
		t.Fatal("DeleteOrg 這一批不動 actor:", err)
	}
	// 沒有外鍵指向不存在的組織也能建(組織可以先於 actor 消失)
	newAgent(t, p, "never-existed", "x", "")
}

// 守門:omixor_actors 的結構(欄位、約束、索引)是 DOC-13 §1.1 的樣子;誰改了要同時改這裡與規格
func TestActorSchemaGuard(t *testing.T) {
	p := actorStore(t)
	cols := map[string]string{}
	rows, err := p.pool.Query(context.Background(), `select column_name, data_type || ':' || is_nullable from information_schema.columns where table_name = 'omixor_actors'`)
	chk(t, err)
	for rows.Next() {
		var c, d string
		chk(t, rows.Scan(&c, &d))
		cols[c] = d
	}
	rows.Close()
	want := map[string]string{
		"id": "text:NO", "kind": "text:NO", "org": "text:YES", "name": "text:NO", "handle": "text:NO", "avatar": "text:NO",
		"owner": "text:YES", "disabled_at": "timestamp with time zone:YES", "created_at": "timestamp with time zone:NO",
	}
	if len(cols) != len(want) {
		t.Fatalf("欄位 %v", cols)
	}
	for c, d := range want {
		if cols[c] != d {
			t.Errorf("欄位 %s = %q,應為 %q", c, cols[c], d)
		}
	}
	for _, ix := range []string{"omixor_actors_user_handle", "omixor_actors_system_handle", "omixor_actors_agent_handle", "omixor_actors_agent_org"} {
		if n := pgCount(t, p, `select count(*) from pg_indexes where indexname = $1 and tablename = 'omixor_actors'`, ix); n != 1 {
			t.Errorf("缺索引 %s", ix)
		}
	}
	// 角色現役 (org, handle) 唯一是部分索引(退役不佔名字)
	var def string
	chk(t, p.pool.QueryRow(context.Background(), `select indexdef from pg_indexes where indexname = 'omixor_actors_agent_handle'`).Scan(&def))
	if !strings.Contains(def, "UNIQUE") || !strings.Contains(def, "disabled_at IS NULL") || !strings.Contains(def, "(org, handle)") {
		t.Errorf("角色 handle 索引:%s", def)
	}
	// owner 自我參照、restrict;id + kind 的唯一約束留給之後的複合外鍵
	if n := pgCount(t, p, `select count(*) from pg_constraint where contype = 'f' and conrelid = 'omixor_actors'::regclass and confrelid = 'omixor_actors'::regclass and confdeltype = 'r'`); n != 1 {
		t.Errorf("owner 外鍵:%d", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_constraint where contype = 'u' and conrelid = 'omixor_actors'::regclass`); n != 1 {
		t.Errorf("unique (id, kind):%d", n)
	}
}

func errOf(_ *model.ActorRecord, err error) error { return err }

func errOfA(_ model.ActorRecord, err error) error { return err }
