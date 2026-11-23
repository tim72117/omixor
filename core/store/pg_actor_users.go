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
