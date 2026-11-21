// pg_actor_users.go 使用者接上 actor(ISS-36 階段 B-2b;規格見雲端 omixor DOC-13 §1.2、§1.4、§1.7、§2、§8)。
//
// 決定紀錄的採用(D1、D3、D11):
//   - 每個使用者有一個 usr_ actor,omixor_users.actor_id 指到它(唯一,複合外鍵 (actor_id, kind) → omixor_actors(id, kind) 擋住「指到角色或系統 actor」)。
//   - 第一次正式部署只「轉換」:omixor_users.id 維持 u_<x>(不改寫成 usr_…,tokens、成員、audit 的 user_id 都不動),email、name 欄位也還在
//     (actor 的 handle = email 小寫、name 是複本,SaveUser 每次同步);id 改寫、移除 email/name 與 actor_id 設 NOT NULL 留給下一版。
//     所以 actor_id 欄位是可為 NULL 的:滾動部署時舊 revision 仍會寫沒有 actor_id 的使用者列,下次啟動的補齊與下一次 SaveUser 會補上。
//   - D3:舊資料裡對不上使用者的 email 會是「未註冊使用者」actor(usr_、handle = email、沒有 omixor_users 列;C-1 建立,EnsureUserActorByEmail 也會建);
//     之後那個 email 的人建使用者(SaveUser)、登入時接回同一個 actor(不產生第二個)。
//   - 使用者的登入開關仍是 omixor_users.disabled;actor 的 disabled_at 這一版不用於使用者(DOC-13 §1.1)。

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

const (
	actorUsersLockKey = "omixor-actor-users-migrate"
	actorUsersFK      = "fk_users_actor"
)

// actorBackfillBatch 補齊 actor 時每個交易處理幾位使用者(測試會調小)
var actorBackfillBatch = 2000

// userActorHandle 使用者 actor 的 handle:email 規範化(小寫、去空白);email 不能當 handle(空的、含空白或控制字元)時改用使用者 id
func userActorHandle(email, id string) (string, error) {
	if h, err := model.CleanUserHandle(email); err == nil {
		return h, nil
	}
	return model.CleanUserHandle(id)
}

func userActorName(name, handle string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return handle
}

// ensureUserActorTx 在交易裡取得或建立這位使用者的 actor id,並把 actor 的 name、handle 同步成使用者目前的值。順序:
//  1. u.ActorID 有給 → 必須是存在的使用者 actor(不是就回 ErrBadRef)
//  2. omixor_users 裡這位使用者已經有 actor_id → 用它
//  3. handle(email 小寫)已經有使用者 actor、而且沒被別的使用者佔用 → 接回(D3 的未註冊使用者)
//  4. 都沒有 → 建新的 usr_ actor(同時有人建同一個 handle:on conflict do nothing 後重查,不會有兩個)
//
// email 只差大小寫的兩位使用者(omixor_users.email 的唯一是區分大小寫的)handle 會撞:後來的改用使用者 id 當 handle
func ensureUserActorTx(c context.Context, tx pgx.Tx, u model.User) (string, error) {
	handle, err := userActorHandle(u.Email, u.ID)
	if err != nil {
		return "", fmt.Errorf("使用者 %q 的 actor handle:%w", u.ID, err)
	}
	actorID := u.ActorID
	if actorID != "" {
		var ok bool
		if err := tx.QueryRow(c, `select exists (select 1 from omixor_actors where id = $1 and kind = 'user')`, actorID).Scan(&ok); err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("%w: actor %s 不存在或不是使用者", ErrBadRef, actorID)
		}
	} else {
		var cur string
		err := tx.QueryRow(c, `select coalesce(actor_id, '') from omixor_users where id = $1`, u.ID).Scan(&cur)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		actorID = cur
	}
	if actorID == "" {
		if actorID, err = claimOrCreateUserActor(c, tx, u, handle); err != nil {
			return "", err
		}
		return actorID, nil
	}
	return actorID, syncUserActor(c, tx, actorID, handle, u.Name)
}

// claimOrCreateUserActor 步驟 3、4
func claimOrCreateUserActor(c context.Context, tx pgx.Tx, u model.User, handle string) (string, error) {
	created := u.CreatedAt.Time
	if created.IsZero() {
		created = time.Now()
	}
	retried := false
	for attempt := 0; attempt < 4; attempt++ {
		var id string
		err := tx.QueryRow(c, `select a.id from omixor_actors a where a.kind = 'user' and lower(a.handle) = $1
  and not exists (select 1 from omixor_users x where x.actor_id = a.id and x.id <> $2)`, handle, u.ID).Scan(&id)
		if err == nil {
			// 接回未註冊使用者(或這位使用者自己的):顯示名稱換成使用者的
			if n := strings.TrimSpace(u.Name); n != "" {
				if _, err := tx.Exec(c, `update omixor_actors set name = $2 where id = $1 and name <> $2`, id, n); err != nil {
					return "", err
				}
			}
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		// 這個 handle 被別的使用者的 actor 佔了(email 只差大小寫):改用使用者 id 當 handle,再試
		var taken bool
		if err := tx.QueryRow(c, `select exists (select 1 from omixor_actors where kind = 'user' and lower(handle) = $1)`, handle).Scan(&taken); err != nil {
			return "", err
		}
		if taken && !retried {
			// 兩個查詢之間,同一位使用者的另一個連線(多實例同時登入)剛提交了這個 handle 的 actor:重查一次(READ COMMITTED 看得到),
			// 還是被別人佔用才改用使用者 id 當 handle,免得同一位使用者建出兩個 actor
			retried = true
			continue
		}
		if taken {
			alt, err := model.CleanUserHandle(u.ID)
			if err != nil || alt == handle {
				return "", fmt.Errorf("%w: 使用者 handle %q 已被另一位使用者佔用", ErrDuplicateKey, handle)
			}
			handle = alt
			continue
		}
		newID := model.NewUserActorID(created)
		var got string
		err = tx.QueryRow(c, `insert into omixor_actors (id, kind, name, handle, created_at) values ($1, 'user', $2, $3, $4)
on conflict do nothing returning id`, newID, userActorName(u.Name, handle), handle, secs(created)).Scan(&got)
		if err == nil {
			return got, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		// 同時有人建了同一個 handle:下一輪重查
	}
	return "", fmt.Errorf("%w: 建立使用者 actor 時 handle %q 一直衝突", ErrDuplicateKey, handle)
}

// syncUserActor 把 actor 的 name、handle 同步成使用者目前的值:name 空的不覆蓋(保留 UpdateActorProfile 改過的);
// handle 改成的新 email 已經被別的 actor 佔了(例如那個 email 的未註冊使用者)就保留原 handle(寫 log,之後由 C-1 的對照處理)
func syncUserActor(c context.Context, tx pgx.Tx, actorID, handle, name string) error {
	if n := strings.TrimSpace(name); n != "" {
		if _, err := tx.Exec(c, `update omixor_actors set name = $2 where id = $1 and name <> $2`, actorID, n); err != nil {
			return err
		}
	}
	ct, err := tx.Exec(c, `update omixor_actors set handle = $2 where id = $1 and handle <> $2
  and not exists (select 1 from omixor_actors o where o.kind = 'user' and lower(o.handle) = $2 and o.id <> $1)`, actorID, handle)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		var cur string
		if err := tx.QueryRow(c, `select handle from omixor_actors where id = $1`, actorID).Scan(&cur); err == nil && cur != handle {
			warnf("使用者 actor %s 的 handle 維持 %q(email 已改成 %q,但這個 handle 已有別的 actor)", actorID, cur, handle)
		}
	}
	return nil
}

// EnsureUserActor 取得或補建一位既有使用者的 actor(使用者列沒有 actor_id 時用:滾動部署時舊 revision 寫入的、補齊還沒做完的)。
// 已經有就直接回傳,不改任何東西;使用者不存在回 ErrNotFound
func (p *PG) EnsureUserActor(userID string) (*model.ActorRecord, error) {
	c, cancel := ctx()
	defer cancel()
	u, err := p.UserByID(userID)
	if err != nil {
		return nil, err
	}
	if u.ActorID == "" {
		tx, err := p.pool.Begin(c)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(c)
		id, err := ensureUserActorTx(c, tx, *u)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(c, `update omixor_users set actor_id = $2 where id = $1 and actor_id is null`, userID, id); err != nil {
			return nil, err
		}
		if err := tx.Commit(c); err != nil {
			return nil, err
		}
		u.ActorID = id
	}
	return p.ActorByID(u.ActorID)
}

// EnsureUserActorByEmail 依 email 取得使用者 actor,沒有就建「未註冊使用者」actor(usr_、handle = email 小寫、name 空的用 email、沒有 omixor_users 列;
// DOC-13 D3)。之後那個 email 的人 SaveUser 時接回同一個 actor。email 不能當 handle 回一般錯誤。回傳的是既有或新建的那一筆(含已經是註冊使用者的)
func (p *PG) EnsureUserActorByEmail(email, name string) (*model.ActorRecord, error) {
	h, err := model.CleanUserHandle(email)
	if err != nil {
		return nil, err
	}
	if a, err := p.ActorByHandle(model.ActorKindUser, "", h); err == nil {
		return a, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	a, err := p.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: userActorName(name, h), Handle: h})
	if errors.Is(err, ErrDuplicateKey) { // 同時有人建了:取現有的
		return p.ActorByHandle(model.ActorKindUser, "", h)
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UserByActorID 依 actor id 找使用者(這個 actor 是未註冊使用者、角色、系統 actor、或不存在都回 ErrNotFound)
func (p *PG) UserByActorID(actorID string) (*model.User, error) {
	return p.userBy("actor_id", actorID)
}

// ---- 啟動遷移 ----

// migrateActorUsers 啟動遷移的一步(B-2b):omixor_users 加 actor_id、kind 欄位與唯一索引,替每個還沒有 actor 的使用者補建(或接回)usr_ actor,
// 補齊後加複合外鍵(not valid 再 validate)。放在 migrateActors 之後(要有 omixor_actors)、migrateForeignKeys 之前。
//   - 冪等:欄位、索引、外鍵都先查在不在,在就完全不下 DDL;補齊只處理 actor_id 為 NULL 的列,全部做完後重跑什麼都不寫。
//   - 多實例:session 層級 advisory lock(omixor-actor-users-migrate)排隊;等不到鎖視為失敗(使用者登入需要這一步的欄位,不能略過)。
//   - 不重寫表:加欄位是只改目錄(可為 NULL 的欄位與常數預設);補齊是每批 2000 位使用者一個交易的 update(列鎖),批間檢查啟動預算,
//     用完就停、下次啟動接著做(未補的使用者在下一次 SaveUser 也會補)。外鍵 add 與欄位 alter 都有 lock_timeout 與重試。
//   - 不改 omixor_users.id、不動 tokens/成員/audit 的 user_id(D11)。
func (p *PG) migrateActorUsers() error {
	p.note.setSkip(skipUserActors, "")
	c, cancel := p.migCtx(10 * time.Minute)
	defer cancel()
	unlock, ok, err := p.sessionLock(c, actorUsersLockKey, "使用者 actor 遷移")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w(%s)", errLockWait, actorUsersLockKey)
	}
	defer unlock()

	hasCol, err := p.columnExists(c, "omixor_users", "actor_id")
	if err != nil {
		return err
	}
	hasKind, err := p.columnExists(c, "omixor_users", "kind")
	if err != nil {
		return err
	}
	if !hasCol || !hasKind {
		err := retryLock(c, 6, time.Second, func() error {
			tx, err := p.pool.Begin(c)
			if err != nil {
				return err
			}
			defer tx.Rollback(c)
			if _, err := tx.Exec(c, `set local lock_timeout = '3s'`); err != nil {
				return err
			}
			if !hasCol {
				if _, err := tx.Exec(c, `alter table omixor_users add column if not exists actor_id text`); err != nil {
					return err
				}
			}
			if !hasKind {
				if _, err := tx.Exec(c, `alter table omixor_users add column if not exists kind text not null default 'user' check (kind = 'user')`); err != nil {
					return err
				}
			}
			return tx.Commit(c)
		})
		if err != nil {
			return fmt.Errorf("omixor_users 加 actor_id 欄位:%w", err)
		}
	}
	if _, err := p.pool.Exec(c, `create unique index if not exists omixor_users_actor_id on omixor_users (actor_id)`); err != nil {
		return fmt.Errorf("omixor_users_actor_id 索引:%w", err)
	}

	n, done, err := p.backfillUserActors(c)
	if err != nil {
		return err
	}
	if n > 0 {
		fmt.Printf("[遷移] 替 %d 位使用者補上 actor\n", n)
	}
	if !done {
		warnf("使用者 actor 補齊還沒做完(啟動預算用完),外鍵先不加,下次啟動接著做")
		p.note.setSkip(skipUserActors, "啟動遷移整體預算已用完,使用者 actor 補齊沒做完")
		return nil
	}
	return p.addUserActorFK(c)
}

// addUserActorFK 加複合外鍵 (actor_id, kind) → omixor_actors(id, kind):not valid 先加(新寫入立刻受檢查),再 validate
func (p *PG) addUserActorFK(c context.Context) error {
	exists, validated, err := fkExists(c, p.pool, "omixor_users", actorUsersFK)
	if err != nil {
		return err
	}
	if !exists {
		err := retryLock(c, 6, time.Second, func() error {
			tx, err := p.pool.Begin(c)
			if err != nil {
				return err
			}
			defer tx.Rollback(c)
			if _, err := tx.Exec(c, `set local lock_timeout = '3s'`); err != nil {
				return err
			}
			if _, err := tx.Exec(c, `alter table omixor_users add constraint `+actorUsersFK+
				` foreign key (actor_id, kind) references omixor_actors (id, kind) on delete restrict not valid`); err != nil {
				return err
			}
			return tx.Commit(c)
		})
		if err != nil {
			return fmt.Errorf("加外鍵 %s:%w", actorUsersFK, err)
		}
		validated = false
	}
	if !validated {
		if _, err := p.pool.Exec(c, `alter table omixor_users validate constraint `+actorUsersFK); err != nil {
			return fmt.Errorf("驗證外鍵 %s:%w", actorUsersFK, err)
		}
	}
	return nil
}

// backfillUserActors 補齊所有 actor_id 為 NULL 的使用者。回傳補了幾位、是不是全部補完(預算用完 = false)
func (p *PG) backfillUserActors(c context.Context) (n int, done bool, err error) {
	for {
		if p.budgetExpired() {
			return n, false, nil
		}
		m, err := p.backfillUserActorsBatch(c)
		if err != nil {
			return n, false, err
		}
		n += m
		if m == 0 || m < actorBackfillBatch {
			return n, true, nil
		}
	}
}

func (p *PG) backfillUserActorsBatch(c context.Context) (int, error) {
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select id, email, name, created_at from omixor_users where actor_id is null order by created_at, id limit $1 for update skip locked`, actorBackfillBatch)
	if err != nil {
		return 0, err
	}
	var us []model.User
	for rows.Next() {
		var u model.User
		var created time.Time
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &created); err != nil {
			rows.Close()
			return 0, err
		}
		u.CreatedAt = utcTime(created)
		us = append(us, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(us) == 0 {
		return 0, nil
	}
	// 整批用集合運算(2 萬位逐位查要 80 秒以上,整批約 1 秒):
	//  1. 算每位的 handle(同批內重複的後面改用使用者 id);
	//  2. 一次查出這些 handle 已有的、沒被別的使用者佔用的 actor(未註冊使用者)→ 接回;
	//  3. 其餘一次 insert(on conflict do nothing);沒插進去的(handle 被已綁定的 actor 佔了、或同時有人建)走單一位的規則(claimOrCreateUserActor);
	//  4. 一次 update 綁定。
	type item struct {
		u      model.User
		handle string
		actor  string
	}
	items := make([]*item, 0, len(us))
	seen := map[string]bool{}
	for _, u := range us {
		handle, err := userActorHandle(u.Email, u.ID)
		if err != nil {
			warnf("使用者 %s 的 email %q 不能當 handle,也不能用 id:%v;略過", u.ID, u.Email, err)
			continue
		}
		if seen[handle] {
			alt, err := model.CleanUserHandle(u.ID)
			if err != nil || seen[alt] {
				warnf("使用者 %s 的 handle %q 重複,也不能用 id:略過", u.ID, handle)
				continue
			}
			handle = alt
		}
		if handle != strings.ToLower(strings.TrimSpace(u.Email)) {
			warnf("使用者 %s 的 email %q 不能當 handle(或與別人重複),改用 %q", u.ID, u.Email, handle)
		}
		seen[handle] = true
		items = append(items, &item{u: u, handle: handle})
	}
	handles := make([]string, len(items))
	for k, it := range items {
		handles[k] = it.handle
	}
	byHandle := map[string]string{}
	rows, err = tx.Query(c, `select lower(a.handle), a.id from omixor_actors a where a.kind = 'user' and lower(a.handle) = any($1)
  and not exists (select 1 from omixor_users x where x.actor_id = a.id)`, handles)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var h, id string
		if err := rows.Scan(&h, &id); err != nil {
			rows.Close()
			return 0, err
		}
		byHandle[h] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var claimIDs, claimNames []string
	var newIDs, newNames, newHandles []string
	var newCreated []time.Time
	for _, it := range items {
		if id, ok := byHandle[it.handle]; ok {
			it.actor = id
			if n := strings.TrimSpace(it.u.Name); n != "" {
				claimIDs, claimNames = append(claimIDs, id), append(claimNames, n)
			}
			continue
		}
		newIDs = append(newIDs, model.NewUserActorID(it.u.CreatedAt.Time))
		newNames = append(newNames, userActorName(it.u.Name, it.handle))
		newHandles = append(newHandles, it.handle)
		newCreated = append(newCreated, secs(it.u.CreatedAt.Time))
	}
	if len(claimIDs) > 0 {
		if _, err := tx.Exec(c, `update omixor_actors a set name = x.n from unnest($1::text[], $2::text[]) x(id, n) where a.id = x.id and a.name <> x.n`, claimIDs, claimNames); err != nil {
			return 0, err
		}
	}
	if len(newIDs) > 0 {
		rows, err = tx.Query(c, `insert into omixor_actors (id, kind, name, handle, created_at)
select x.id, 'user', x.n, x.h, x.c from unnest($1::text[], $2::text[], $3::text[], $4::timestamptz[]) x(id, n, h, c)
on conflict do nothing returning handle, id`, newIDs, newNames, newHandles, newCreated)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var h, id string
			if err := rows.Scan(&h, &id); err != nil {
				rows.Close()
				return 0, err
			}
			byHandle[h] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	var uids, aids []string
	for _, it := range items {
		if it.actor == "" {
			it.actor = byHandle[it.handle]
		}
		if it.actor == "" { // 沒插進去:單一位的規則
			id, err := claimOrCreateUserActor(c, tx, it.u, it.handle)
			if err != nil {
				return 0, fmt.Errorf("使用者 %s:%w", it.u.ID, err)
			}
			it.actor = id
		}
		uids, aids = append(uids, it.u.ID), append(aids, it.actor)
	}
	if len(uids) > 0 {
		if _, err := tx.Exec(c, `update omixor_users u set actor_id = x.a from unnest($1::text[], $2::text[]) x(id, a) where u.id = x.id and u.actor_id is null`, uids, aids); err != nil {
			return 0, fmt.Errorf("綁定使用者 actor:%w", err)
		}
	}
	if err := tx.Commit(c); err != nil {
		return 0, err
	}
	bound := len(uids)
	if len(us) == actorBackfillBatch && bound < len(us) {
		return 0, nil // 這一批有略過的:停下來,不要原地一直重選同一批
	}
	return bound, nil
}
