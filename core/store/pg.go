// pg.go PG 結構與連線、建表語句 pgSchema(含資料表說明)和跨領域共用的輔助函式

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/model"
)

// PostgreSQL 的存放處。Issue 的表(ops_issues、ops_issue_comments)和 omixor-web 原本的一樣,正式資料直接接得上。
//
//	omixor_users / omixor_tokens / omixor_audit   使用者、token(只存雜湊)、操作紀錄
//	omixor_orgs / omixor_org_members              組織與成員(角色 admin、member)
//	ops_issues / ops_issue_comments               Issue 與留言(主鍵 (org, id),id 是 UUIDv7;pos 大的在前;key 是顯示編號 ISS-<n> / <專案代號>-<n>,
//	                                              (org, upper(key)) 唯一;number 是 key 在所屬範圍(project,'' = 組織層級)內的號碼;project_ref 產生欄位外鍵指向專案(restrict);
//	                                              version 樂觀鎖版本號;deleted_at 不是 NULL = 在垃圾桶(軟刪除);見 pg_issue.go、pg_issue_keys.go)
//	omixor_issue_keys                             Issue 目前與過去用過的 key(主鍵 (org, key),key 一律存大寫;issue_id 外鍵 cascade)
//	omixor_attachments                            Issue 的圖片附件(主鍵 (org, id),id 是內容雜湊)
//	omixor_agents                                 虛擬角色(主鍵 (org, id);docAccess、routines 在 omixor_agent_doc_access、omixor_agent_routines;舊 jsonb 欄位與 split_at 已不再寫,下一版 drop;見 pg_split_docs_agents.go)
//	omixor_agent_tasks                            角色任務(主鍵 (org, id) = T-<n>;備註、連結在子表 omixor_agent_task_notes / _links,見 pg_split_incidents_tasks.go;舊的 links、notes jsonb 欄位不再寫)
//	omixor_agent_credentials                      任務綁定的角色憑證(只存 SHA-256 雜湊與到期時間;token_hash 是主鍵)
//	ops_issues.assignee                           指派給的角色 id(空字串 = 未指派)
//	ops_issues.repo                               Issue 所屬的程式碼倉庫(owner/name 或 group/sub/project,可帶 github:/gitlab:;空字串 = 未標記)
//	omixor_cabinet_layouts                        文件櫃結構的「存過」標記與版本號(主鍵 (org, project);project 空字串 = 未分專案;version 是樂觀鎖的版本號,每存一次 +1;
//	                                              layout(jsonb)與 split_at 是舊格式遺留,已不再寫,下一版 drop;見 pg_cabinet.go)
//	omixor_cabinet_sections                       文件櫃的區塊(主鍵 (org, project, id);pos 是順序;文件的 section 欄位以字串指向 id,不加外鍵)
//	omixor_cabinet_tabs                           文件櫃的頁籤(主鍵 (org, project, path),網址在同一份結構內不重複;section_id 是所屬區塊,pos 是區塊內順序;
//	                                              code、grp 可以是 null;文件的 tab 欄位以字串指向 path)
//	omixor_docs                                   文件櫃的文件(主鍵 (org, id) = DOC-<n>,在組織內唯一;project 是所屬專案,空字串 = 未分專案;
//	                                              pos 大的在前;tags 在 omixor_doc_tags(舊 jsonb 欄位已不再寫,下一版 drop);tab 可以是 null)
//	omixor_projects                               專案(主鍵 (org, id) = PRJ-<n>;依 number 排序;key 是專案代號,(org, upper(key)) where key <> '' 唯一;方法在 pg_project.go)
//	omixor_project_seq                            專案最後發出去的號碼(主鍵 org;刪掉的號碼不再使用)
//	omixor_seq                                    Issue、任務、文件、頻道、事件、頻道訊息(scope msg:<頻道 id>)最後發出去的號碼(主鍵 (org, scope);見 pg_seq.go)
//	omixor_org_chains                             組織鏈(主鍵 org;邊在 omixor_org_chain_edges;edges(jsonb)、split_at 是舊格式遺留,不再寫,下一版 drop;見 pg_split_channels.go)
//	omixor_channels                               訊息頻道(主鍵 (org, id) = CH-<n>;成員在 omixor_channel_members(舊 members jsonb 已不再寫,見 pg_split_channels.go);issue_id 沒綁是 NULL,同組織的 issue_id 不重複)
//	omixor_channel_messages                       頻道訊息(主鍵 (org, channel_id, id) = M-<n>,每個頻道各自編號;@ 的人在 omixor_channel_mentions(舊 mentions jsonb 已不再寫))
//	omixor_channel_reads                          已讀(主鍵 (org, channel_id, member);last_read 是讀到的訊息號碼)
//	omixor_incidents                              事件(主鍵 (org, id) = INC-<n>;pos 大的在前;純量欄位在本表、服務/後續工作/告警/時間軸在 omixor_incident_* 子表,見 pg_split_incidents_tasks.go;舊的 data jsonb 不再寫;方法在 pg_incident.go)
//	omixor_org_integrations                       組織設定文件(選用模組的設定,例如 GitHub、GitLab 模組的 github、gitlab)與外部 MCP 登記(主鍵 (org, kind),kind 是 github、gitlab、mcp;設定存在 data jsonb,方法在 pg_integration.go、pg_mcp.go)
//	omixor_org_system                             組織的系統設定(功能開關;主鍵 org;有改過的開關存在 features jsonb,方法在 pg_system.go)
//	omixor_server_features                        系統層的功能設定(整台伺服器一列,主鍵 id 固定是 'server';有改過的開關存在 features jsonb,方法在 pg_system.go)
//
// 「誰」欄位(22 個:created_by、author、assignee、member、from_actor/to_actor、commander、updated_by、事件的 actor、稽核的 actor…)在這份建表語句裡已經是
// 「最終結構」:存 actor id(text,可否 NULL 見 identity_fields.go:ActorColumns)。它們的外鍵(references omixor_actors(id))與完成標記由啟動遷移的最後一步
// (pg_actor_columns.go)補上 —— 全新資料庫在同一次啟動內補完(空表,瞬間),舊結構的資料庫由同一個引擎原地轉換成一樣的結構。欄位契約見 ACTOR_CONTRACT.md。
//
// 這份建表語句只有 create table if not exists(表已存在時不碰表鎖);後來加的欄位與索引由 bootstrapSchema(pg_schema_boot.go)先查 catalog、缺才補(帶 lock_timeout 與重試)。
//
// 外鍵與刪除規則(角色→任務 restrict、頻道→Issue set null、各表的 org cascade 等)不在這份建表語句裡,而是啟動時由 migrateForeignKeys(pg_fk.go)
// 用 not valid 補上再 validate;頻道的 issue_id 沒綁是 NULL、文件與區塊的 project 另有產生欄位 project_ref,細節見 pg_fk.go 檔頭
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
  actor text,
  email text not null default '',
  source text not null default '',
  action text not null,
  org text not null default '',
  detail jsonb
);
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
create table if not exists omixor_issue_keys (
  org text not null,
  key text not null,
  issue_id text not null,
  created_at timestamptz not null default now(),
  primary key (org, key),
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
  created_by text,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  claimed_at timestamptz,
  completed_at timestamptz,
  primary key (org, id)
);
create table if not exists omixor_agent_credentials (
  token_hash bytea primary key,
  org text not null,
  agent_id text not null,
  task_id text not null,
  issued_at timestamptz not null,
  expires_at timestamptz not null
);
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
  author text,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  primary key (org, id)
);
create table if not exists omixor_doc_tags (
  org text not null,
  doc_id text not null,
  tag text not null,
  pos int not null,
  primary key (org, doc_id, tag),
  foreign key (org, doc_id) references omixor_docs (org, id) on delete cascade on update cascade
);
create table if not exists omixor_agent_doc_access (
  org text not null,
  agent_id text not null,
  access_key text not null,
  level text not null,
  primary key (org, agent_id, access_key),
  foreign key (org, agent_id) references omixor_agents (org, id) on delete cascade on update cascade
);
create table if not exists omixor_agent_routines (
  org text not null,
  agent_id text not null,
  id text not null,
  pos int not null,
  title text not null default '',
  instructions text not null default '',
  kind text not null default '',
  enabled boolean not null default false,
  every_hours int not null default 0,
  hour int not null default 0,
  minute int not null default 0,
  weekday int not null default 0,
  priority text not null default '',
  created_at timestamptz,
  last_run_at timestamptz,
  primary key (org, agent_id, id),
  foreign key (org, agent_id) references omixor_agents (org, id) on delete cascade on update cascade
);
create table if not exists omixor_cabinet_sections (
  org text not null,
  project text not null default '',
  id text not null,
  pos int not null,
  label text not null,
  path text not null default '',
  primary key (org, project, id)
);
create table if not exists omixor_cabinet_tabs (
  org text not null,
  project text not null default '',
  section_id text not null,
  path text not null,
  pos int not null,
  code text,
  grp text,
  label text not null,
  primary key (org, project, path)
);
create table if not exists omixor_projects (
  org text not null,
  id text not null,
  number integer not null,
  name text not null,
  description text not null default '',
  created_at timestamptz not null,
  updated_at timestamptz not null,
  primary key (org, id)
);
create table if not exists omixor_project_seq (
  org text primary key,
  last integer not null
);
create table if not exists omixor_org_chains (
  org text primary key,
  edges jsonb not null default '[]',
  updated_at timestamptz not null default now()
);
create table if not exists omixor_channels (
  org text not null,
  id text not null,
  number integer not null,
  name text not null,
  topic text not null default '',
  issue_id text,
  members jsonb not null default '[]',
  created_by text,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  primary key (org, id)
);
create table if not exists omixor_channel_messages (
  org text not null,
  channel_id text not null,
  id text not null,
  number integer not null,
  author text not null,
  text text not null,
  mentions jsonb not null default '[]',
  at timestamptz not null,
  primary key (org, channel_id, id)
);
create table if not exists omixor_channel_reads (
  org text not null,
  channel_id text not null,
  member text not null,
  last_read integer not null default 0,
  at timestamptz not null,
  primary key (org, channel_id, member)
);
create table if not exists omixor_channel_members (
  org text not null,
  channel_id text not null,
  member text not null,
  pos integer not null,
  primary key (org, channel_id, member),
  foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade on update cascade
);
create table if not exists omixor_channel_mentions (
  org text not null,
  channel_id text not null,
  message_id text not null,
  member text not null,
  pos integer not null,
  primary key (org, channel_id, message_id, member),
  foreign key (org, channel_id, message_id) references omixor_channel_messages (org, channel_id, id) on delete cascade on update cascade
);
create table if not exists omixor_org_chain_edges (
  org text not null,
  pos integer not null,
  from_actor text not null,
  to_actor text not null,
  label text not null default '',
  primary key (org, pos),
  foreign key (org) references omixor_org_chains (org) on delete cascade on update cascade
);
create table if not exists omixor_incidents (
  org text not null,
  id text not null,
  number integer not null,
  pos bigint not null,
  status text not null,
  started_at timestamptz not null,
  data jsonb not null,
  primary key (org, id)
);
create table if not exists omixor_org_integrations (
  org text not null,
  kind text not null,
  data jsonb not null,
  updated_at timestamptz not null,
  updated_by text,
  primary key (org, kind)
);
create table if not exists omixor_org_system (
  org text primary key,
  features jsonb not null,
  updated_at timestamptz not null,
  updated_by text
);
create table if not exists omixor_server_features (
  id text primary key check (id = 'server'),
  features jsonb not null,
  updated_at timestamptz not null,
  updated_by text
);
`

// pgConn PG 用的資料庫連線:連線池(一般)或批次裡的交易(Batch)。交易的 Begin 是 savepoint,
// 所以方法裡自己開的交易在批次裡就是巢狀交易,批次失敗時一起還原
type pgConn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type PG struct {
	pool pgConn
	root *pgxpool.Pool // 連線池本身(Close 用;批次裡的 PG 是 nil)
	// 背景工作(外鍵 validate,見 pg_fk.go):Close 時取消並等它結束
	bg *pgBackground
	// 啟動流程(NewPG)還沒跑完以前,外鍵 validate 先記著、不開跑(免得和後面的遷移 DDL 互等死鎖);ready 之後才在背景跑
	fkPending []fkSpec
	ready     bool
	// 還有沒有未展開(split_at 為 NULL)的列;見 pg_split_migrate.go
	mig *migState
	// 啟動遷移期間的整體預算(見 pg_migrate_guard.go 的 migNotes)
	note migNotes
}

// txConn 批次裡的交易:BeginTx 在交易裡只能開 savepoint(隔離等級沿用批次的)
type txConn struct{ pgx.Tx }

func (t txConn) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) { return t.Tx.Begin(ctx) }

// Batch 批次寫入(全有或全無):fn 用 tx 做的寫入在同一個資料庫交易裡,fn 成功才一起 commit,
// 回傳錯誤(或 panic)就整批還原。fn 裡只能用 tx(用 p 的寫入不在這個交易裡)。批次以外的讀寫不受影響
// (讀不到批次沒 commit 的資料;同一個組織的 Issue、任務編號用 advisory lock,會等到批次結束)
func (p *PG) Batch(fn func(tx *PG) error) error {
	if p.root == nil { // 已經在批次裡:直接用同一個交易
		return fn(p)
	}
	c, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tx, err := p.root.Begin(c)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(c) }() // commit 之後是 no-op
	if err := fn(&PG{pool: txConn{tx}, mig: p.mig}); err != nil {
		return err
	}
	return tx.Commit(c)
}

// newPG 用連線池組出 *PG(NewPG、NewPGFromPool、AttachMigratedPG 共用這一處;PG 日後加欄位只改這裡)
func newPG(pool *pgxpool.Pool) *PG {
	return &PG{pool: pool, root: pool, mig: &migState{}}
}

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
	if err := bootstrapSchema(c, pool); err != nil {
		pool.Close()
		return nil, err
	}
	p := newPG(pool)
	if err := p.startupMigrations(); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

// NewPGFromPool 用已經連好的連線池(omixor-web 內建模式和它自己的資料共用一個)
func NewPGFromPool(pool *pgxpool.Pool) (*PG, error) {
	c, cancel := ctx()
	defer cancel()
	if err := bootstrapSchema(c, pool); err != nil {
		return nil, err
	}
	p := newPG(pool)
	if err := p.startupMigrations(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *PG) Close() {
	p.stopBackground()
	if p.root != nil {
		p.root.Close()
	}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func secs(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// utcTime 資料庫讀出的時間 → model.Time(UTC)
func utcTime(t time.Time) model.Time { return model.Time{Time: t.UTC()} }

// fromNull 可為 NULL 的時間 → model.Time(NULL 是零值)
func fromNull(t *time.Time) model.Time {
	if t == nil {
		return model.Time{}
	}
	return utcTime(*t)
}

// tp 可為 NULL 的時間 → *model.Time(NULL 是 nil)
func tp(t *time.Time) *model.Time {
	if t == nil {
		return nil
	}
	x := utcTime(*t)
	return &x
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func jb(v any) []byte { b, _ := json.Marshal(v); return b }

func optSecs(t *model.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	x := secs(t.Time)
	return &x
}

// nullSecs 零值時間存 NULL(其餘截到秒、UTC)
func nullSecs(t model.Time) *time.Time { return optSecs(&t) }

func tagsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
