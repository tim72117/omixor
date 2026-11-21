// pg_user.go 使用者、token(只存雜湊)與操作紀錄的 PG 實作

package store

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 使用者、token、操作紀錄 ----

const userCols = `id, coalesce(evoleap_id,''), email, name, role, disabled, created_at, last_login_at, coalesce(actor_id,'')`

func scanUser(row pgx.Row) (*model.User, error) {
	var u model.User
	var created time.Time
	var last *time.Time
	if err := row.Scan(&u.ID, &u.EvoleapID, &u.Email, &u.Name, &u.Role, &u.Disabled, &created, &last, &u.ActorID); err != nil {
		return nil, notFound(err)
	}
	u.CreatedAt, u.LastLoginAt = model.Time{Time: created.UTC()}, tp(last)
	return &u, nil
}

func (p *PG) CountUsers() (int, error) {
	c, cancel := ctx()
	defer cancel()
	var n int
	err := p.pool.QueryRow(c, `select count(*) from omixor_users`).Scan(&n)
	return n, err
}

func (p *PG) ListUsers() ([]model.User, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+userCols+` from omixor_users order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (p *PG) userBy(col, v string) (*model.User, error) {
	c, cancel := ctx()
	defer cancel()
	return scanUser(p.pool.QueryRow(c, `select `+userCols+` from omixor_users where `+col+` = $1`, v))
}

func (p *PG) UserByID(id string) (*model.User, error)        { return p.userBy("id", id) }
func (p *PG) UserByEvoleapID(id string) (*model.User, error) { return p.userBy("evoleap_id", id) }
func (p *PG) UserByEmail(e string) (*model.User, error)      { return p.userBy("email", e) }

func (p *PG) SaveUser(u model.User) error {
	c, cancel := ctx()
	defer cancel()
	var evo any
	if u.EvoleapID != "" {
		evo = u.EvoleapID
	}
	var last *time.Time
	if u.LastLoginAt != nil {
		t := u.LastLoginAt.Time
		last = &t
	}
	// 使用者與 actor 同一個交易:不會有「有 actor 沒使用者」以外的半套狀態,也不會有使用者存了卻沒有 actor
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	actorID, err := ensureUserActorTx(c, tx, u)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(c, `insert into omixor_users (id, evoleap_id, email, name, role, disabled, created_at, last_login_at, actor_id)
values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
on conflict (id) do update set evoleap_id = excluded.evoleap_id, email = excluded.email, name = excluded.name,
  role = excluded.role, disabled = excluded.disabled, last_login_at = excluded.last_login_at, actor_id = excluded.actor_id`,
		u.ID, evo, u.Email, u.Name, u.Role, u.Disabled, u.CreatedAt.Time, last, actorID); err != nil {
		return err
	}
	return tx.Commit(c)
}

func (p *PG) CreateToken(t model.Token) error {
	c, cancel := ctx()
	defer cancel()
	var exp *time.Time
	if t.ExpiresAt != nil {
		e := t.ExpiresAt.Time
		exp = &e
	}
	_, err := p.pool.Exec(c, `insert into omixor_tokens (id, token_hash, user_id, device, created_at, expires_at) values ($1,$2,$3,$4,$5,$6)`,
		t.ID, t.Hash, t.UserID, t.Device, t.CreatedAt.Time, exp)
	return err
}

func (p *PG) TokenByHash(h []byte, now time.Time) (*model.Token, *model.User, error) {
	c, cancel := ctx()
	defer cancel()
	var t model.Token
	var tCreated time.Time
	var tUsed, tExp *time.Time
	var u model.User
	var uCreated time.Time
	var uLast *time.Time
	err := p.pool.QueryRow(c, `select t.id, t.user_id, t.device, t.created_at, t.last_used_at, t.expires_at,
  u.id, coalesce(u.evoleap_id,''), u.email, u.name, u.role, u.disabled, u.created_at, u.last_login_at, coalesce(u.actor_id,'')
from omixor_tokens t join omixor_users u on u.id = t.user_id
where t.token_hash = $1 and t.revoked_at is null and (t.expires_at is null or t.expires_at > $2)`, h, now).
		Scan(&t.ID, &t.UserID, &t.Device, &tCreated, &tUsed, &tExp,
			&u.ID, &u.EvoleapID, &u.Email, &u.Name, &u.Role, &u.Disabled, &uCreated, &uLast, &u.ActorID)
	if err != nil {
		return nil, nil, notFound(err)
	}
	t.CreatedAt, t.LastUsedAt, t.ExpiresAt, t.Email = model.Time{Time: tCreated.UTC()}, tp(tUsed), tp(tExp), u.Email
	u.CreatedAt, u.LastLoginAt = model.Time{Time: uCreated.UTC()}, tp(uLast)
	return &t, &u, nil
}

func (p *PG) TouchToken(id string, at time.Time) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `update omixor_tokens set last_used_at = $2 where id = $1`, id, at)
	return err
}

func (p *PG) RevokeToken(id string, at time.Time) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_tokens set revoked_at = $2 where id = $1 and revoked_at is null`, id, at)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *PG) ListTokens(now time.Time) ([]model.Token, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select t.id, t.user_id, u.email, t.device, t.created_at, t.last_used_at, t.expires_at, t.token_hash
from omixor_tokens t join omixor_users u on u.id = t.user_id
where t.revoked_at is null and (t.expires_at is null or t.expires_at > $1) order by t.created_at desc`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Token{}
	for rows.Next() {
		var t model.Token
		var created time.Time
		var used, exp *time.Time
		if err := rows.Scan(&t.ID, &t.UserID, &t.Email, &t.Device, &created, &used, &exp, &t.Hash); err != nil {
			return nil, err
		}
		t.CreatedAt, t.LastUsedAt, t.ExpiresAt = model.Time{Time: created.UTC()}, tp(used), tp(exp)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *PG) AddAudit(e model.AuditEntry) error {
	c, cancel := ctx()
	defer cancel()
	var detail any
	if e.Detail != nil {
		b, _ := json.Marshal(e.Detail)
		detail = string(b)
	}
	_, err := p.pool.Exec(c, `insert into omixor_audit (at, actor, email, source, action, org, detail) values ($1,$2,$3,$4,$5,$6,$7::jsonb)`,
		e.At.Time, IDOrNil(e.Actor), e.Email, e.Source, e.Action, e.Org, detail)
	return fkErr(err)
}

func (p *PG) ListAudit(limit int) ([]model.AuditEntry, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select id, at, actor, email, source, action, org, coalesce(detail::text,'') from omixor_audit order by id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		var at time.Time
		var d string
		var actor NullID
		if err := rows.Scan(&e.ID, &at, &actor, &e.Email, &e.Source, &e.Action, &e.Org, &d); err != nil {
			return nil, err
		}
		e.Actor = actor.String()
		e.At = model.Time{Time: at.UTC()}
		if d != "" {
			_ = json.Unmarshal([]byte(d), &e.Detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
