package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/model"
)

// PostgreSQL 的存放處。Issue 的表(ops_issues、ops_issue_comments)和 omixor-web 原本的一樣,正式資料直接接得上。
//
//	omixor_users / omixor_tokens / omixor_audit   使用者、token(只存雜湊)、操作紀錄
//	omixor_orgs / omixor_org_members              組織與成員(角色 admin、member)
//	ops_issues / ops_issue_comments               Issue 與留言(主鍵 (org, id);pos 大的在前)
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
create table if not exists omixor_orgs (
  id text primary key,
  name text not null,
  created_at timestamptz not null default now()
);
create table if not exists omixor_org_members (
  org text not null references omixor_orgs(id),
  user_id text not null references omixor_users(id),
  role text not null check (role in ('admin','member')),
  added_at timestamptz not null default now(),
  primary key (org, user_id)
);
create table if not exists ops_issues (
  org text not null,
  id text not null,
  number integer not null,
  pos bigint not null,
  title text not null,
  body text not null default '',
  status text not null check (status in ('open', 'closed')),
  priority text not null check (priority in ('urgent', 'high', 'normal', 'low')),
  labels text[] not null default '{}',
  source text not null default '',
  link text not null default '',
  created_by text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  closed_at timestamptz,
  primary key (org, id)
);
create index if not exists ops_issues_org_pos on ops_issues (org, pos desc);
create table if not exists ops_issue_comments (
  org text not null,
  issue_id text not null,
  pos integer not null,
  id text not null,
  author text not null,
  text text not null,
  at timestamptz not null,
  primary key (org, issue_id, pos),
  foreign key (org, issue_id) references ops_issues (org, id) on delete cascade on update cascade
);
`

type PG struct{ pool *pgxpool.Pool }

// NewPG 連資料庫並建表(已經有就不動)
func NewPG(url string) (*PG, error) {
	c, cancel := ctx()
	defer cancel()
	pool, err := pgxpool.New(c, url)
	if err != nil {
		return nil, fmt.Errorf("資料庫網址格式不對:%w", err)
	}
	if err := pool.Ping(c); err != nil {
		pool.Close()
		return nil, fmt.Errorf("連不到資料庫:%w", err)
	}
	if _, err := pool.Exec(c, pgSchema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("建表失敗:%w", err)
	}
	return &PG{pool: pool}, nil
}

// NewPGFromPool 用已經連好的連線池(omixor-web 內建模式和它自己的資料共用一個)
func NewPGFromPool(pool *pgxpool.Pool) (*PG, error) {
	c, cancel := ctx()
	defer cancel()
	if _, err := pool.Exec(c, pgSchema); err != nil {
		return nil, fmt.Errorf("建表失敗:%w", err)
	}
	return &PG{pool: pool}, nil
}

func (p *PG) Close() { p.pool.Close() }

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func secs(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func tp(t *time.Time) *model.Time {
	if t == nil {
		return nil
	}
	return &model.Time{Time: t.UTC()}
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---- 使用者、token、操作紀錄 ----

const userCols = `id, coalesce(evoleap_id,''), email, name, role, disabled, created_at, last_login_at`

func scanUser(row pgx.Row) (*model.User, error) {
	var u model.User
	var created time.Time
	var last *time.Time
	if err := row.Scan(&u.ID, &u.EvoleapID, &u.Email, &u.Name, &u.Role, &u.Disabled, &created, &last); err != nil {
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
	_, err := p.pool.Exec(c, `insert into omixor_users (id, evoleap_id, email, name, role, disabled, created_at, last_login_at)
values ($1,$2,$3,$4,$5,$6,$7,$8)
on conflict (id) do update set evoleap_id = excluded.evoleap_id, email = excluded.email, name = excluded.name,
  role = excluded.role, disabled = excluded.disabled, last_login_at = excluded.last_login_at`,
		u.ID, evo, u.Email, u.Name, u.Role, u.Disabled, u.CreatedAt.Time, last)
	return err
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
  u.id, coalesce(u.evoleap_id,''), u.email, u.name, u.role, u.disabled, u.created_at, u.last_login_at
from omixor_tokens t join omixor_users u on u.id = t.user_id
where t.token_hash = $1 and t.revoked_at is null and (t.expires_at is null or t.expires_at > $2)`, h, now).
		Scan(&t.ID, &t.UserID, &t.Device, &tCreated, &tUsed, &tExp,
			&u.ID, &u.EvoleapID, &u.Email, &u.Name, &u.Role, &u.Disabled, &uCreated, &uLast)
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
	rows, err := p.pool.Query(c, `select t.id, t.user_id, u.email, t.device, t.created_at, t.last_used_at, t.expires_at
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
		if err := rows.Scan(&t.ID, &t.UserID, &t.Email, &t.Device, &created, &used, &exp); err != nil {
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
	_, err := p.pool.Exec(c, `insert into omixor_audit (at, user_id, email, action, org, detail) values ($1,$2,$3,$4,$5,$6::jsonb)`,
		e.At.Time, e.UserID, e.Email, e.Action, e.Org, detail)
	return err
}

func (p *PG) ListAudit(limit int) ([]model.AuditEntry, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select id, at, user_id, email, action, org, coalesce(detail::text,'') from omixor_audit order by id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		var at time.Time
		var d string
		if err := rows.Scan(&e.ID, &at, &e.UserID, &e.Email, &e.Action, &e.Org, &d); err != nil {
			return nil, err
		}
		e.At = model.Time{Time: at.UTC()}
		if d != "" {
			_ = json.Unmarshal([]byte(d), &e.Detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- 組織與成員 ----

func (p *PG) ListOrgs() ([]model.Org, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select id, name, created_at from omixor_orgs order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Org{}
	for rows.Next() {
		var o model.Org
		var created time.Time
		if err := rows.Scan(&o.ID, &o.Name, &created); err != nil {
			return nil, err
		}
		o.CreatedAt = model.Time{Time: created.UTC()}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *PG) GetOrg(id string) (*model.Org, error) {
	c, cancel := ctx()
	defer cancel()
	var o model.Org
	var created time.Time
	if err := p.pool.QueryRow(c, `select id, name, created_at from omixor_orgs where id = $1`, id).Scan(&o.ID, &o.Name, &created); err != nil {
		return nil, notFound(err)
	}
	o.CreatedAt = model.Time{Time: created.UTC()}
	return &o, nil
}

func (p *PG) CreateOrg(o model.Org) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `insert into omixor_orgs (id, name, created_at) values ($1,$2,$3) on conflict (id) do nothing`, o.ID, o.Name, o.CreatedAt.Time)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("組織 %s 已經存在", o.ID)
	}
	return err
}

func (p *PG) MemberRole(org, userID string) (string, error) {
	c, cancel := ctx()
	defer cancel()
	var r string
	err := p.pool.QueryRow(c, `select role from omixor_org_members where org = $1 and user_id = $2`, org, userID).Scan(&r)
	return r, notFound(err)
}

func (p *PG) ListMembers(org string) ([]model.Member, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select m.org, m.user_id, u.email, u.name, m.role, m.added_at
from omixor_org_members m join omixor_users u on u.id = m.user_id where m.org = $1 order by u.email`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Member{}
	for rows.Next() {
		var m model.Member
		var added time.Time
		if err := rows.Scan(&m.Org, &m.UserID, &m.Email, &m.Name, &m.Role, &added); err != nil {
			return nil, err
		}
		m.AddedAt = model.Time{Time: added.UTC()}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *PG) SaveMember(m model.Member) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `insert into omixor_org_members (org, user_id, role, added_at) values ($1,$2,$3,$4)
on conflict (org, user_id) do update set role = excluded.role`, m.Org, m.UserID, m.Role, m.AddedAt.Time)
	return err
}

func (p *PG) RemoveMember(org, userID string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_org_members where org = $1 and user_id = $2`, org, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *PG) OrgsOfUser(userID string) ([]model.Org, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select o.id, o.name, o.created_at, m.role
from omixor_org_members m join omixor_orgs o on o.id = m.org where m.user_id = $1 order by o.created_at, o.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Org{}
	for rows.Next() {
		var o model.Org
		var created time.Time
		if err := rows.Scan(&o.ID, &o.Name, &created, &o.MyRole); err != nil {
			return nil, err
		}
		o.CreatedAt = model.Time{Time: created.UTC()}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---- Issue ----

func (p *PG) ListIssues(org string) ([]model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	return listIssues(c, p.pool, org, "")
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func listIssues(c context.Context, q querier, org, onlyID string) ([]model.Issue, error) {
	where, args := `org = $1`, []any{org}
	if onlyID != "" {
		where, args = `org = $1 and lower(id) = lower($2)`, []any{org, onlyID}
	}
	rows, err := q.Query(c, `select id, title, body, status, priority, labels, source, link, created_by,
		created_at, updated_at, closed_at from ops_issues where `+where+` order by pos desc, number desc`, args...)
	if err != nil {
		return nil, err
	}
	list := []model.Issue{}
	idx := map[string]int{}
	for rows.Next() {
		var i model.Issue
		var created, updated time.Time
		var closed *time.Time
		if err := rows.Scan(&i.ID, &i.Title, &i.Body, &i.Status, &i.Priority, &i.Labels, &i.Source, &i.Link, &i.CreatedBy,
			&created, &updated, &closed); err != nil {
			rows.Close()
			return nil, err
		}
		i.CreatedAt, i.UpdatedAt, i.ClosedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}, tp(closed)
		if i.Labels == nil {
			i.Labels = []string{}
		}
		i.Comments = []model.Comment{}
		idx[i.ID] = len(list)
		list = append(list, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cw, cargs := `org = $1`, []any{org}
	if onlyID != "" && len(list) == 1 {
		cw, cargs = `org = $1 and issue_id = $2`, []any{org, list[0].ID}
	}
	crows, err := q.Query(c, `select issue_id, id, author, text, at from ops_issue_comments where `+cw+` order by issue_id, pos`, cargs...)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var iid string
		var cm model.Comment
		var at time.Time
		if err := crows.Scan(&iid, &cm.ID, &cm.Author, &cm.Text, &at); err != nil {
			return nil, err
		}
		cm.At = model.Time{Time: at.UTC()}
		if k, ok := idx[iid]; ok {
			list[k].Comments = append(list[k].Comments, cm)
		}
	}
	return list, crows.Err()
}

func (p *PG) GetIssue(org, id string) (*model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	l, err := listIssues(c, p.pool, org, id)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func writeIssue(c context.Context, tx pgx.Tx, org string, i model.Issue, pos int64, insert bool) error {
	var closed *time.Time
	if i.ClosedAt != nil && !i.ClosedAt.IsZero() {
		t := secs(i.ClosedAt.Time)
		closed = &t
	}
	labels := i.Labels
	if labels == nil {
		labels = []string{}
	}
	if insert {
		if _, err := tx.Exec(c, `insert into ops_issues (org, id, number, pos, title, body, status, priority, labels, source, link,
			created_by, created_at, updated_at, closed_at) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			org, i.ID, i.Number(), pos, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link,
			i.CreatedBy, secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed); err != nil {
			return err
		}
	} else {
		tag, err := tx.Exec(c, `update ops_issues set title = $3, body = $4, status = $5, priority = $6, labels = $7, source = $8,
			link = $9, created_by = $10, created_at = $11, updated_at = $12, closed_at = $13 where org = $1 and id = $2`,
			org, i.ID, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link, i.CreatedBy,
			secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(c, `delete from ops_issue_comments where org = $1 and issue_id = $2`, org, i.ID); err != nil {
			return err
		}
	}
	for k, cm := range i.Comments {
		if _, err := tx.Exec(c, `insert into ops_issue_comments (org, issue_id, pos, id, author, text, at) values ($1,$2,$3,$4,$5,$6,$7)`,
			org, i.ID, k, cm.ID, cm.Author, cm.Text, secs(cm.At.Time)); err != nil {
			return err
		}
	}
	return nil
}

func (p *PG) CreateIssue(org string, i model.Issue) (model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return i, err
	}
	defer tx.Rollback(c)
	// 同一個組織同時只建一筆,號碼才不會撞
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-issues:' || $1))`, org); err != nil {
		return i, err
	}
	var num int
	var pos int64
	if err := tx.QueryRow(c, `select coalesce(max(number),0), coalesce(max(pos),0) from ops_issues where org = $1`, org).Scan(&num, &pos); err != nil {
		return i, err
	}
	i.ID = fmt.Sprintf("ISS-%d", num+1)
	if err := writeIssue(c, tx, org, i, pos+1, true); err != nil {
		return i, err
	}
	return i, tx.Commit(c)
}

func (p *PG) UpdateIssue(org string, i model.Issue) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := writeIssue(c, tx, org, i, 0, false); err != nil {
		return err
	}
	return tx.Commit(c)
}

func (p *PG) DeleteIssue(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from ops_issues where org = $1 and id = $2`, org, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *PG) ImportIssues(org string, list []model.Issue) (int, error) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-issues:' || $1))`, org); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from ops_issues where org = $1`, org).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 %d 筆 Issue", org, n)
	}
	for k, i := range list {
		if err := writeIssue(c, tx, org, i, int64(len(list)-k), true); err != nil {
			return 0, fmt.Errorf("%s:%w", i.ID, err)
		}
	}
	return len(list), tx.Commit(c)
}

func (p *PG) IssueOrgs() ([]string, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select distinct org from ops_issues where org not like 'deleted-orgs/%' order by org`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
