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
//	omixor_attachments                            Issue 的圖片附件(主鍵 (org, id),id 是內容雜湊)
//	omixor_agents                                 虛擬角色(主鍵 (org, id);docAccess、routines 是 jsonb)
//	omixor_agent_tasks                            角色任務(主鍵 (org, id) = T-<n>;links、notes 是 jsonb)
//	omixor_agent_credentials                      任務綁定的角色憑證(只存 SHA-256 雜湊與到期時間;token_hash 是主鍵)
//	ops_issues.assignee                           指派給的角色 id(空字串 = 未指派)
//	omixor_cabinet_layouts                        文件櫃結構(主鍵 org;layout 是 jsonb,區塊與頁籤)
//	omixor_docs                                   文件櫃的文件(主鍵 (org, id) = DOC-<n>;pos 大的在前;tags 是 jsonb;tab 可以是 null)
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
alter table omixor_orgs add column if not exists symbol text not null default '';
alter table omixor_orgs add column if not exists color text not null default '';
alter table omixor_orgs add column if not exists note text not null default '';
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
alter table ops_issues add column if not exists assignee text not null default '';
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
create table if not exists omixor_attachments (
  org text not null,
  id text not null,
  data bytea not null,
  created_at timestamptz not null default now(),
  primary key (org, id)
);
create table if not exists omixor_agents (
  org text not null,
  id text not null,
  name text not null,
  role text not null default '',
  instructions text not null default '',
  active boolean not null default true,
  symbol text not null default '',
  color text not null default '',
  doc_access jsonb not null default '{}',
  routines jsonb not null default '[]',
  created_at timestamptz not null default now(),
  primary key (org, id)
);
create table if not exists omixor_agent_tasks (
  org text not null,
  id text not null,
  number integer not null,
  agent_id text not null,
  title text not null,
  instructions text not null default '',
  priority text not null default 'normal',
  status text not null check (status in ('queued','inProgress','blocked','done','canceled')),
  result text not null default '',
  links jsonb not null default '[]',
  notes jsonb not null default '[]',
  routine_id text,
  created_by text not null default '',
  created_at timestamptz not null,
  updated_at timestamptz not null,
  claimed_at timestamptz,
  completed_at timestamptz,
  primary key (org, id)
);
alter table omixor_agent_tasks add column if not exists locked_until timestamptz;
alter table omixor_agent_tasks add column if not exists claimed_by text not null default '';
create index if not exists omixor_agent_tasks_agent on omixor_agent_tasks (org, agent_id, status);
create table if not exists omixor_agent_credentials (
  token_hash bytea primary key,
  org text not null,
  agent_id text not null,
  task_id text not null,
  issued_at timestamptz not null,
  expires_at timestamptz not null
);
create unique index if not exists omixor_agent_credentials_task on omixor_agent_credentials (org, task_id);
create table if not exists omixor_cabinet_layouts (
  org text primary key,
  layout jsonb not null default '[]',
  updated_at timestamptz not null default now()
);
create table if not exists omixor_docs (
  org text not null,
  id text not null,
  number integer not null,
  pos bigint not null,
  title text not null,
  body text not null default '',
  section text not null,
  tab text,
  tags jsonb not null default '[]',
  author text not null default '',
  created_at timestamptz not null,
  updated_at timestamptz not null,
  primary key (org, id)
);
create index if not exists omixor_docs_org_pos on omixor_docs (org, pos desc);
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
	rows, err := p.pool.Query(c, `select id, name, symbol, color, note, created_at from omixor_orgs order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Org{}
	for rows.Next() {
		var o model.Org
		var created time.Time
		if err := rows.Scan(&o.ID, &o.Name, &o.Symbol, &o.Color, &o.Note, &created); err != nil {
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
	if err := p.pool.QueryRow(c, `select id, name, symbol, color, note, created_at from omixor_orgs where id = $1`, id).Scan(&o.ID, &o.Name, &o.Symbol, &o.Color, &o.Note, &created); err != nil {
		return nil, notFound(err)
	}
	o.CreatedAt = model.Time{Time: created.UTC()}
	return &o, nil
}

func (p *PG) CreateOrg(o model.Org) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `insert into omixor_orgs (id, name, symbol, color, note, created_at) values ($1,$2,$3,$4,$5,$6) on conflict (id) do nothing`, o.ID, o.Name, o.Symbol, o.Color, o.Note, o.CreatedAt.Time)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("組織 %s 已經存在", o.ID)
	}
	return err
}

func (p *PG) UpdateOrg(o model.Org) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_orgs set name = $2, symbol = $3, color = $4, note = $5 where id = $1`, o.ID, o.Name, o.Symbol, o.Color, o.Note)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *PG) DeleteOrg(id string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	for _, q := range []string{
		`delete from ops_issues where org = $1`, // 留言跟著刪(on delete cascade)
		`delete from omixor_attachments where org = $1`,
		`delete from omixor_agent_credentials where org = $1`,
		`delete from omixor_agent_tasks where org = $1`,
		`delete from omixor_agents where org = $1`,
		`delete from omixor_docs where org = $1`,
		`delete from omixor_cabinet_layouts where org = $1`,
		`delete from omixor_org_members where org = $1`,
	} {
		if _, err := tx.Exec(c, q, id); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(c, `delete from omixor_orgs where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(c)
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
	rows, err := p.pool.Query(c, `select o.id, o.name, o.symbol, o.color, o.note, o.created_at, m.role
from omixor_org_members m join omixor_orgs o on o.id = m.org where m.user_id = $1 order by o.created_at, o.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Org{}
	for rows.Next() {
		var o model.Org
		var created time.Time
		if err := rows.Scan(&o.ID, &o.Name, &o.Symbol, &o.Color, &o.Note, &created, &o.MyRole); err != nil {
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
		created_at, updated_at, closed_at, assignee from ops_issues where `+where+` order by pos desc, number desc`, args...)
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
			&created, &updated, &closed, &i.Assignee); err != nil {
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
			created_by, created_at, updated_at, closed_at, assignee) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			org, i.ID, i.Number(), pos, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link,
			i.CreatedBy, secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed, i.Assignee); err != nil {
			return err
		}
	} else {
		tag, err := tx.Exec(c, `update ops_issues set title = $3, body = $4, status = $5, priority = $6, labels = $7, source = $8,
			link = $9, created_by = $10, created_at = $11, updated_at = $12, closed_at = $13, assignee = $14 where org = $1 and id = $2`,
			org, i.ID, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link, i.CreatedBy,
			secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed, i.Assignee)
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

func (p *PG) PutAttachment(org, id string, data []byte) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `insert into omixor_attachments (org, id, data) values ($1, $2, $3) on conflict (org, id) do nothing`, org, id, data)
	return err
}

func (p *PG) GetAttachment(org, id string) ([]byte, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	err := p.pool.QueryRow(c, `select data from omixor_attachments where org = $1 and id = $2`, org, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// ---- 虛擬角色、任務與憑證 ----

func jb(v any) []byte { b, _ := json.Marshal(v); return b }

const agentCols = `id, name, role, instructions, active, symbol, color, doc_access, routines, created_at`

func (p *PG) ListAgents(org string) ([]model.Agent, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+agentCols+` from omixor_agents where org = $1 order by created_at, id`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Agent{}
	for rows.Next() {
		g, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func scanAgent(row pgx.Row) (*model.Agent, error) {
	var g model.Agent
	var acc, rts []byte
	var created time.Time
	if err := row.Scan(&g.ID, &g.Name, &g.Role, &g.Instructions, &g.Active, &g.Symbol, &g.Color, &acc, &rts, &created); err != nil {
		return nil, err
	}
	g.CreatedAt = model.Time{Time: created.UTC()}
	_ = json.Unmarshal(acc, &g.DocAccess)
	_ = json.Unmarshal(rts, &g.Routines)
	if g.DocAccess == nil {
		g.DocAccess = map[string]string{}
	}
	if g.Routines == nil {
		g.Routines = []model.AgentRoutine{}
	}
	return &g, nil
}

func (p *PG) GetAgent(org, id string) (*model.Agent, error) {
	c, cancel := ctx()
	defer cancel()
	g, err := scanAgent(p.pool.QueryRow(c, `select `+agentCols+` from omixor_agents where org = $1 and id = $2`, org, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (p *PG) SaveAgent(org string, a model.Agent) error {
	c, cancel := ctx()
	defer cancel()
	if a.DocAccess == nil {
		a.DocAccess = map[string]string{}
	}
	if a.Routines == nil {
		a.Routines = []model.AgentRoutine{}
	}
	_, err := p.pool.Exec(c, `insert into omixor_agents (org, id, name, role, instructions, active, symbol, color, doc_access, routines, created_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (org, id) do update set name = $3, role = $4, instructions = $5, active = $6, symbol = $7, color = $8,
		doc_access = $9, routines = $10`,
		org, a.ID, a.Name, a.Role, a.Instructions, a.Active, a.Symbol, a.Color, jb(a.DocAccess), jb(a.Routines), secs(a.CreatedAt.Time))
	return err
}

func (p *PG) DeleteAgent(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_agents where org = $1 and id = $2`, org, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

const taskCols = `id, agent_id, title, instructions, priority, status, result, links, notes, routine_id, created_by,
	created_at, updated_at, claimed_at, completed_at, locked_until, claimed_by`

func scanTask(row pgx.Row) (*model.AgentTask, error) {
	var t model.AgentTask
	var links, notes []byte
	var created, updated time.Time
	var claimed, completed, locked *time.Time
	if err := row.Scan(&t.ID, &t.AgentID, &t.Title, &t.Instructions, &t.Priority, &t.Status, &t.Result, &links, &notes,
		&t.RoutineID, &t.CreatedBy, &created, &updated, &claimed, &completed, &locked, &t.ClaimedBy); err != nil {
		return nil, err
	}
	t.CreatedAt, t.UpdatedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}
	t.ClaimedAt, t.CompletedAt, t.LockedUntil = tp(claimed), tp(completed), tp(locked)
	_ = json.Unmarshal(links, &t.Links)
	_ = json.Unmarshal(notes, &t.Notes)
	if t.Links == nil {
		t.Links = []string{}
	}
	if t.Notes == nil {
		t.Notes = []model.AgentTaskNote{}
	}
	return &t, nil
}

func (p *PG) ListAgentTasks(org string) ([]model.AgentTask, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+taskCols+` from omixor_agent_tasks where org = $1 order by number desc`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AgentTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (p *PG) GetAgentTask(org, id string) (*model.AgentTask, error) {
	c, cancel := ctx()
	defer cancel()
	t, err := scanTask(p.pool.QueryRow(c, `select `+taskCols+` from omixor_agent_tasks where org = $1 and id = $2`, org, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func optSecs(t *model.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	x := secs(t.Time)
	return &x
}

func linksOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func notesOrEmpty(l []model.AgentTaskNote) []model.AgentTaskNote {
	if l == nil {
		return []model.AgentTaskNote{}
	}
	return l
}

func (p *PG) CreateAgentTask(org string, t model.AgentTask) (model.AgentTask, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return t, err
	}
	defer tx.Rollback(c)
	// 同一個組織同時只建一筆,號碼才不會撞
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-agent-tasks:' || $1))`, org); err != nil {
		return t, err
	}
	var num int
	if err := tx.QueryRow(c, `select coalesce(max(number),0) from omixor_agent_tasks where org = $1`, org).Scan(&num); err != nil {
		return t, err
	}
	t.ID = fmt.Sprintf("T-%d", num+1)
	_, err = tx.Exec(c, `insert into omixor_agent_tasks (org, id, number, agent_id, title, instructions, priority, status, result, links, notes,
		routine_id, created_by, created_at, updated_at, claimed_at, completed_at, locked_until, claimed_by) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		org, t.ID, num+1, t.AgentID, t.Title, t.Instructions, t.Priority, t.Status, t.Result, jb(linksOrEmpty(t.Links)), jb(notesOrEmpty(t.Notes)),
		t.RoutineID, t.CreatedBy, secs(t.CreatedAt.Time), secs(t.UpdatedAt.Time), optSecs(t.ClaimedAt), optSecs(t.CompletedAt), optSecs(t.LockedUntil), t.ClaimedBy)
	if err != nil {
		return t, err
	}
	return t, tx.Commit(c)
}

const updateTaskSQL = `update omixor_agent_tasks set agent_id = $3, title = $4, instructions = $5, priority = $6, status = $7,
		result = $8, links = $9, notes = $10, routine_id = $11, created_by = $12, created_at = $13, updated_at = $14, claimed_at = $15,
		completed_at = $16, locked_until = $17, claimed_by = $18 where org = $1 and id = $2`

func taskArgs(org string, t model.AgentTask) []any {
	return []any{org, t.ID, t.AgentID, t.Title, t.Instructions, t.Priority, t.Status, t.Result, jb(linksOrEmpty(t.Links)), jb(notesOrEmpty(t.Notes)),
		t.RoutineID, t.CreatedBy, secs(t.CreatedAt.Time), secs(t.UpdatedAt.Time), optSecs(t.ClaimedAt), optSecs(t.CompletedAt), optSecs(t.LockedUntil), t.ClaimedBy}
}

func (p *PG) UpdateAgentTask(org string, t model.AgentTask) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, updateTaskSQL, taskArgs(org, t)...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ClaimAgentTask 條件式更新:只有任務「不是進行中、或沒有鎖、或鎖已到期」才寫入;
// 影響 0 列時再查一次,任務還在就是被別的程式搶先領取(ErrLocked),不在就是 ErrNotFound
func (p *PG) ClaimAgentTask(org string, t model.AgentTask, now time.Time) error {
	c, cancel := ctx()
	defer cancel()
	args := append(taskArgs(org, t), now)
	tag, err := p.pool.Exec(c, updateTaskSQL+` and (status <> 'inProgress' or locked_until is null or locked_until <= $19)`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var one int
		err := p.pool.QueryRow(c, `select 1 from omixor_agent_tasks where org = $1 and id = $2`, org, t.ID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return ErrLocked
	}
	return nil
}

func (p *PG) ExtendAgentCredential(org, taskID string, expires time.Time) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `update omixor_agent_credentials set expires_at = $3 where org = $1 and task_id = $2`, org, taskID, secs(expires))
	return err
}

func (p *PG) SaveAgentCredential(cr model.AgentCredential) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `delete from omixor_agent_credentials where org = $1 and task_id = $2`, cr.Org, cr.TaskID); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `insert into omixor_agent_credentials (token_hash, org, agent_id, task_id, issued_at, expires_at)
		values ($1,$2,$3,$4,$5,$6)`, cr.Hash, cr.Org, cr.AgentID, cr.TaskID, secs(cr.IssuedAt.Time), secs(cr.ExpiresAt.Time)); err != nil {
		return err
	}
	return tx.Commit(c)
}

func (p *PG) AgentCredentialByHash(h []byte) (*model.AgentCredential, error) {
	c, cancel := ctx()
	defer cancel()
	var cr model.AgentCredential
	var issued, exp time.Time
	err := p.pool.QueryRow(c, `select token_hash, org, agent_id, task_id, issued_at, expires_at from omixor_agent_credentials
		where token_hash = $1`, h).Scan(&cr.Hash, &cr.Org, &cr.AgentID, &cr.TaskID, &issued, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	cr.IssuedAt, cr.ExpiresAt = model.Time{Time: issued.UTC()}, model.Time{Time: exp.UTC()}
	return &cr, nil
}

func (p *PG) DeleteAgentCredentials(org, taskID, agentID string) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `delete from omixor_agent_credentials where org = $1 and (($2 <> '' and task_id = $2) or ($3 <> '' and agent_id = $3))`,
		org, taskID, agentID)
	return err
}

func (p *PG) PruneAgentCredentials(now time.Time) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `delete from omixor_agent_credentials where expires_at <= $1`, now)
	return err
}

// ---- 文件櫃 ----

func (p *PG) GetCabinetLayout(org string) ([]model.CabinetSection, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	if err := p.pool.QueryRow(c, `select layout from omixor_cabinet_layouts where org = $1`, org).Scan(&b); err != nil {
		return nil, notFound(err)
	}
	l := []model.CabinetSection{}
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	return l, nil
}

func (p *PG) SaveCabinetLayout(org string, layout []model.CabinetSection) error {
	c, cancel := ctx()
	defer cancel()
	if layout == nil {
		layout = []model.CabinetSection{}
	}
	_, err := p.pool.Exec(c, `insert into omixor_cabinet_layouts (org, layout, updated_at) values ($1, $2, now())
		on conflict (org) do update set layout = $2, updated_at = now()`, org, jb(layout))
	return err
}

const docCols = `id, title, body, section, tab, tags, author, created_at, updated_at`

func scanDoc(row pgx.Row) (*model.CabinetDoc, error) {
	var d model.CabinetDoc
	var tags []byte
	var created, updated time.Time
	if err := row.Scan(&d.ID, &d.Title, &d.Body, &d.Section, &d.Tab, &tags, &d.Author, &created, &updated); err != nil {
		return nil, err
	}
	d.CreatedAt, d.UpdatedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}
	_ = json.Unmarshal(tags, &d.Tags)
	if d.Tags == nil {
		d.Tags = []string{}
	}
	return &d, nil
}

func tagsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (p *PG) ListDocs(org string) ([]model.CabinetDoc, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+docCols+` from omixor_docs where org = $1 order by pos desc`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CabinetDoc{}
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (p *PG) GetDoc(org, id string) (*model.CabinetDoc, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := scanDoc(p.pool.QueryRow(c, `select `+docCols+` from omixor_docs where org = $1 and id = $2`, org, id))
	if err != nil {
		return nil, notFound(err)
	}
	return d, nil
}

func insertDoc(c context.Context, tx pgx.Tx, org string, d model.CabinetDoc, pos int64) error {
	_, err := tx.Exec(c, `insert into omixor_docs (org, id, number, pos, title, body, section, tab, tags, author, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		org, d.ID, d.Number(), pos, d.Title, d.Body, d.Section, d.Tab, jb(tagsOrEmpty(d.Tags)), d.Author, secs(d.CreatedAt.Time), secs(d.UpdatedAt.Time))
	return err
}

func (p *PG) CreateDoc(org string, d model.CabinetDoc) (model.CabinetDoc, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(c)
	// 同一個組織同時只建一份,號碼才不會撞
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-docs:' || $1))`, org); err != nil {
		return d, err
	}
	var num int
	var pos int64
	if err := tx.QueryRow(c, `select coalesce(max(number),0), coalesce(max(pos),0) from omixor_docs where org = $1`, org).Scan(&num, &pos); err != nil {
		return d, err
	}
	d.ID = fmt.Sprintf("%s%d", model.DocPrefix, num+1)
	if err := insertDoc(c, tx, org, d, pos+1); err != nil {
		return d, err
	}
	return d, tx.Commit(c)
}

func (p *PG) UpdateDoc(org string, d model.CabinetDoc) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_docs set title = $3, body = $4, section = $5, tab = $6, tags = $7, author = $8, updated_at = $9
		where org = $1 and id = $2`, org, d.ID, d.Title, d.Body, d.Section, d.Tab, jb(tagsOrEmpty(d.Tags)), d.Author, secs(d.UpdatedAt.Time))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) DeleteDoc(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_docs where org = $1 and id = $2`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) ImportDocs(org string, list []model.CabinetDoc) (int, error) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-docs:' || $1))`, org); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from omixor_docs where org = $1`, org).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 %d 份文件", org, n)
	}
	for k, d := range list {
		if err := insertDoc(c, tx, org, d, int64(len(list)-k)); err != nil {
			return 0, fmt.Errorf("%s:%w", d.ID, err)
		}
	}
	return len(list), tx.Commit(c)
}
