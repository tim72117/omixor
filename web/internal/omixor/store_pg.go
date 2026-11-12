package omixor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-web/internal/core"
)

// PostgreSQL 的存放處(omixor 資料庫,和 ops_issues 同一個庫)
const pgSchema = `
create table if not exists omixor_users (
  id text primary key,
  evoleap_id text unique,
  email text not null unique,
  name text not null default '',
  role text not null check (role in ('admin','member')),
  disabled boolean not null default false,
  created_at timestamptz not null default now(),
  last_login_at timestamptz
);
create table if not exists omixor_tokens (
  id text primary key,
  token_hash bytea not null unique,
  user_id text not null references omixor_users(id),
  device text not null default '',
  created_at timestamptz not null default now(),
  last_used_at timestamptz,
  expires_at timestamptz,
  revoked_at timestamptz
);
create table if not exists omixor_audit (
  id bigserial primary key,
  at timestamptz not null default now(),
  user_id text not null default '',
  email text not null default '',
  action text not null,
  org text not null default '',
  detail jsonb
);
create index if not exists omixor_audit_at on omixor_audit (at desc);
`

type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore 建表(已經有就不動)並回傳存放處;要先 core.OpenIssueDB
func NewPGStore() (*PGStore, error) {
	pool := core.DB()
	if pool == nil {
		return nil, errors.New("沒有連資料庫")
	}
	ctx, cancel := ctx()
	defer cancel()
	if _, err := pool.Exec(ctx, pgSchema); err != nil {
		return nil, err
	}
	return &PGStore{pool: pool}, nil
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

const userCols = `id, coalesce(evoleap_id,''), email, name, role, disabled, created_at, last_login_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.EvoleapID, &u.Email, &u.Name, &u.Role, &u.Disabled, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

func (p *PGStore) CountUsers() (int, error) {
	c, cancel := ctx()
	defer cancel()
	var n int
	err := p.pool.QueryRow(c, `select count(*) from omixor_users`).Scan(&n)
	return n, err
}

func (p *PGStore) ListUsers() ([]User, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+userCols+` from omixor_users order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (p *PGStore) UserByEvoleapID(id string) (*User, error) {
	c, cancel := ctx()
	defer cancel()
	return scanUser(p.pool.QueryRow(c, `select `+userCols+` from omixor_users where evoleap_id = $1`, id))
}

func (p *PGStore) UserByEmail(email string) (*User, error) {
	c, cancel := ctx()
	defer cancel()
	return scanUser(p.pool.QueryRow(c, `select `+userCols+` from omixor_users where email = $1`, email))
}

func (p *PGStore) SaveUser(u User) error {
	c, cancel := ctx()
	defer cancel()
	var evo any
	if u.EvoleapID != "" {
		evo = u.EvoleapID
	}
	_, err := p.pool.Exec(c, `insert into omixor_users (id, evoleap_id, email, name, role, disabled, created_at, last_login_at)
values ($1,$2,$3,$4,$5,$6,$7,$8)
on conflict (id) do update set evoleap_id = excluded.evoleap_id, email = excluded.email, name = excluded.name,
  role = excluded.role, disabled = excluded.disabled, last_login_at = excluded.last_login_at`,
		u.ID, evo, u.Email, u.Name, u.Role, u.Disabled, u.CreatedAt, u.LastLoginAt)
	return err
}

func (p *PGStore) CreateToken(t Token) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `insert into omixor_tokens (id, token_hash, user_id, device, created_at, expires_at) values ($1,$2,$3,$4,$5,$6)`,
		t.ID, t.Hash, t.UserID, t.Device, t.CreatedAt, t.ExpiresAt)
	return err
}

func (p *PGStore) TokenByHash(h []byte) (*Token, *User, error) {
	c, cancel := ctx()
	defer cancel()
	var t Token
	var u User
	err := p.pool.QueryRow(c, `select t.id, t.user_id, t.device, t.created_at, t.last_used_at, t.expires_at,
  u.id, coalesce(u.evoleap_id,''), u.email, u.name, u.role, u.disabled, u.created_at, u.last_login_at
from omixor_tokens t join omixor_users u on u.id = t.user_id
where t.token_hash = $1 and t.revoked_at is null and (t.expires_at is null or t.expires_at > now())`, h).
		Scan(&t.ID, &t.UserID, &t.Device, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt,
			&u.ID, &u.EvoleapID, &u.Email, &u.Name, &u.Role, &u.Disabled, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	t.Email = u.Email
	return &t, &u, nil
}

func (p *PGStore) TouchToken(id string, at time.Time) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `update omixor_tokens set last_used_at = $2 where id = $1`, id, at)
	return err
}

func (p *PGStore) RevokeToken(id string, at time.Time) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_tokens set revoked_at = $2 where id = $1 and revoked_at is null`, id, at)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *PGStore) ListTokens(now time.Time) ([]Token, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select t.id, t.user_id, u.email, t.device, t.created_at, t.last_used_at, t.expires_at
from omixor_tokens t join omixor_users u on u.id = t.user_id
where t.revoked_at is null and (t.expires_at is null or t.expires_at > $1) order by t.created_at desc`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.UserID, &t.Email, &t.Device, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *PGStore) AddAudit(e AuditEntry) error {
	c, cancel := ctx()
	defer cancel()
	var detail any
	if e.Detail != nil {
		b, _ := json.Marshal(e.Detail)
		detail = string(b)
	}
	_, err := p.pool.Exec(c, `insert into omixor_audit (at, user_id, email, action, org, detail) values ($1,$2,$3,$4,$5,$6::jsonb)`,
		e.At, e.UserID, e.Email, e.Action, e.Org, detail)
	return err
}

func (p *PGStore) ListAudit(limit int) ([]AuditEntry, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select id, at, user_id, email, action, org, coalesce(detail::text,'') from omixor_audit order by id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var d string
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.Email, &e.Action, &e.Org, &d); err != nil {
			return nil, err
		}
		if d != "" {
			_ = json.Unmarshal([]byte(d), &e.Detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PGIssues 正式的 Issue 讀取與匯入(core 的 ops_issues 表)
type PGIssues struct{}

func (PGIssues) Read(org string) ([]core.OpsIssue, error) { return core.PGReadIssues(org) }
func (PGIssues) Import(org string, list []core.OpsIssue, overwrite bool) (core.IssueImportResult, error) {
	return core.PGImportIssues(org, list, overwrite)
}
