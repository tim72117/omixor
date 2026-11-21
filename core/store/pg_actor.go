// pg_actor.go actor(使用者、角色、系統內建署名的共同身分)表 omixor_actors 的建表、固定的系統 actor 與 CRUD(ISS-36 階段 B-2a;
// 規格見雲端 omixor DOC-13 §1.1、§1.6、§2)。
//
// 這一批只有這張表本身:omixor_users、omixor_agents、omixor_org_members、omixor_tokens 與所有「誰」的欄位都還沒改,
// 也還沒有外鍵指到這張表(owner 的自我參照除外);之後的工作包(B-2b 使用者、B-2c 角色、C-1 欄位遷移)再接上。

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/model"
)

// omixor_actors 欄位(DOC-13 §1.1):
//
//	id          usr_<uuidv7> / agt_<uuidv7> / sys_<名稱>;check 只驗前綴與 kind 一致,不驗 UUID 格式(格式由 model.ActorKindOfID 在寫入前驗)
//	org         只有 agent 有;不掛 omixor_orgs 外鍵:刪組織時 actor 只退役(disabled_at)、不刪列,稽核等紀錄仍指得到
//	name        顯示名稱
//	handle      使用者 = email 小寫(本機模式可以是 tim 這種非 email)、角色 = 角色 id、系統 = 名稱
//	owner       agent 的負責人(kind = user 的 actor);DB 層可為 NULL(遷移時可能找不到人),新建由 service 強制必填
//	disabled_at 退役、停用時間;角色現役的 (org, handle) 唯一,退役後名字讓出來(同名再建是新的 actor)
//
// 約束比 DOC-13 §1.1 的 DDL 多三條(都只擋不可能的資料,不影響其他表):handle 不能是空白、使用者 handle 一律小寫(唯一索引才不會被大小寫繞過)、
// owner 只有角色可以有。
//
// 唯一性:使用者 handle 全伺服器唯一(不分大小寫,停用的使用者仍佔名字)、系統名稱唯一、角色 (org, handle) 現役時唯一。
const pgActorSchema = `
create table if not exists omixor_actors (
  id          text primary key,
  kind        text not null check (kind in ('user','agent','system')),
  org         text,
  name        text not null,
  handle      text not null check (btrim(handle) <> ''),
  avatar      text not null default '',
  owner       text references omixor_actors(id) on delete restrict,
  disabled_at timestamptz,
  created_at  timestamptz not null default now(),
  unique (id, kind),
  check ((kind = 'user'   and id like 'usr\_%' and org is null)
      or (kind = 'agent'  and id like 'agt\_%' and org is not null)
      or (kind = 'system' and id like 'sys\_%' and org is null)),
  check (kind <> 'user' or handle = lower(handle)),
  check (owner is null or kind = 'agent')
);
create unique index if not exists omixor_actors_user_handle   on omixor_actors (lower(handle)) where kind = 'user';
create unique index if not exists omixor_actors_system_handle on omixor_actors (handle)        where kind = 'system';
create unique index if not exists omixor_actors_agent_handle  on omixor_actors (org, handle)   where kind = 'agent' and disabled_at is null;
create index        if not exists omixor_actors_agent_org     on omixor_actors (org)           where kind = 'agent';
-- 給 omixor_agents 的複合外鍵 (actor_id, org, id) → (id, org, handle) 用(ISS-36 B-2c);id 本來就唯一,這條只是讓外鍵有可以指的唯一索引
create unique index if not exists omixor_actors_id_org_handle on omixor_actors (id, org, handle);
`

// ErrSystemActor 系統 actor 的名稱、handle、停用狀態由程式固定(model.SystemActors),不能改
var ErrSystemActor = errors.New("system actor is fixed")

// migrateActors 啟動遷移的一步(B-2a):建 omixor_actors 與索引、寫入固定的系統 actor(model.SystemActors)。
// 冪等、可以多個實例同時啟動(advisory lock 內進行);系統 actor 已經是目前的值就不寫(不產生新的列版本)。
// 位置:拆表遷移之後、migrateForeignKeys 之前(見 startupMigrations);它不碰任何既有表
func (p *PG) migrateActors() error {
	c, cancel := p.migCtx(5 * time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := xactLock(c, tx, "omixor-actors-migrate"); err != nil {
		if errors.Is(err, errLockWait) || isTimeout(c, err) {
			warnf("actor 表遷移:%v,略過這次(下次啟動再做)", err)
			return nil
		}
		return err
	}
	if _, err := tx.Exec(c, pgActorSchema); err != nil {
		return err
	}
	for _, s := range model.SystemActors {
		if _, err := tx.Exec(c, `insert into omixor_actors (id, kind, name, handle) values ($1, 'system', $2, $3)
on conflict (id) do update set name = excluded.name, handle = excluded.handle, disabled_at = null
where (omixor_actors.name, omixor_actors.handle, omixor_actors.disabled_at) is distinct from (excluded.name, excluded.handle, null::timestamptz)`,
			s.ID, s.Name, s.Handle); err != nil {
			return fmt.Errorf("寫入系統 actor %s:%w", s.ID, err)
		}
	}
	return tx.Commit(c)
}

// ---- 讀寫 ----

const actorCols = `id, kind, coalesce(org, ''), name, handle, avatar, coalesce(owner, ''), disabled_at, created_at`

func scanActor(row pgx.Row) (*model.ActorRecord, error) {
	var a model.ActorRecord
	var kind string
	var disabled *time.Time
	var created time.Time
	if err := row.Scan(&a.ID, &kind, &a.Org, &a.Name, &a.Handle, &a.Avatar, &a.Owner, &disabled, &created); err != nil {
		return nil, notFound(err)
	}
	a.Kind = model.ActorKind(kind)
	a.DisabledAt, a.CreatedAt = tp(disabled), utcTime(created)
	return &a, nil
}

// actorWriteErr 唯一約束衝突 → ErrDuplicateKey(訊息帶約束名稱);其餘原樣
func actorWriteErr(err error) error {
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %s", ErrDuplicateKey, err.Error())
	}
	return fkErr(err)
}

// CreateActor 新增一個 actor,回傳存好的。
//   - a.ID 空 = 依種類產生(user、agent;system 的 id 是固定名稱,一定要自己給);有給的要符合格式與種類。
//   - a.Handle 會先依種類規範化(使用者小寫並去 @、角色符合 model.ValidAgentID…,見 model.CleanActorHandle),規範化的結果寫入。
//   - 格式不對(model.ValidateActorRecord)回一般錯誤;id 或 handle 衝突回 ErrDuplicateKey(使用者 handle 全伺服器唯一、
//     角色現役 (org, handle) 唯一、系統名稱唯一);owner 不存在回 ErrBadRef。
//   - a.CreatedAt 零值 = 現在;a.DisabledAt 非 nil 可以直接建成退役狀態(遷移用)
func (p *PG) CreateActor(a model.ActorRecord) (model.ActorRecord, error) {
	a, err := prepareActor(a)
	if err != nil {
		return a, err
	}
	c, cancel := ctx()
	defer cancel()
	out, err := insertActor(c, p.pool, a)
	if err != nil {
		return a, err
	}
	return *out, nil
}

// prepareActor CreateActor 的寫入前處理:補 id、規範化 handle、驗格式、補建立時間(角色與 SaveAgent 在交易裡建 actor 時共用)
func prepareActor(a model.ActorRecord) (model.ActorRecord, error) {
	if !a.Kind.Valid() {
		return a, fmt.Errorf("不認得的 actor 種類 %q", a.Kind)
	}
	if a.ID == "" {
		id, err := model.NewActorID(a.Kind, time.Now())
		if err != nil {
			return a, err
		}
		a.ID = id
	}
	h, err := model.CleanActorHandle(a.Kind, a.Handle)
	if err != nil {
		return a, err
	}
	a.Handle = h
	if err := model.ValidateActorRecord(a); err != nil {
		return a, err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = model.Now()
	}
	return a, nil
}

// actorQ 單一連線或交易(insertActor 等在 p.pool 與 pgx.Tx 上都能用)
type actorQ interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertActor 寫入一列(a 要已經過 prepareActor);唯一約束衝突回 ErrDuplicateKey、owner 不存在回 ErrBadRef
func insertActor(c context.Context, q actorQ, a model.ActorRecord) (*model.ActorRecord, error) {
	var org, owner *string
	if a.Org != "" {
		org = &a.Org
	}
	if a.Owner != "" {
		owner = &a.Owner
	}
	row := q.QueryRow(c, `insert into omixor_actors (id, kind, org, name, handle, avatar, owner, disabled_at, created_at)
values ($1,$2,$3,$4,$5,$6,$7,$8,$9) returning `+actorCols,
		a.ID, string(a.Kind), org, a.Name, a.Handle, a.Avatar, owner, nullSecs(derefTime(a.DisabledAt)), secs(a.CreatedAt.Time))
	out, err := scanActor(row)
	if err != nil {
		return nil, actorWriteErr(err)
	}
	return out, nil
}

func derefTime(t *model.Time) model.Time {
	if t == nil {
		return model.Time{}
	}
	return *t
}

// ActorByID 依 actor id 取(精確比對,含退役的);沒有回 ErrNotFound
func (p *PG) ActorByID(id string) (*model.ActorRecord, error) {
	c, cancel := ctx()
	defer cancel()
	return scanActor(p.pool.QueryRow(c, `select `+actorCols+` from omixor_actors where id = $1`, id))
}

// ActorsByIDs 批次取(補名字用):id → actor;不存在的 id 不在結果裡(不是錯誤),重複的 id 只算一次
func (p *PG) ActorsByIDs(ids []string) (map[string]model.ActorRecord, error) {
	out := map[string]model.ActorRecord{}
	if len(ids) == 0 {
		return out, nil
	}
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+actorCols+` from omixor_actors where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanActor(rows)
		if err != nil {
			return nil, err
		}
		out[a.ID] = *a
	}
	return out, rows.Err()
}

// ActorByHandle 依 handle 找「現役」的 actor。handle 先依種類規範化(不分大小寫、可帶 @),規範化失敗或找不到回 ErrNotFound。
//   - user:org 要是空字串(不看),handle = email(全伺服器唯一);停用的使用者仍找得到(它佔著名字)
//   - agent:org 必填,handle = 角色 id;只找現役的(退役的不佔名字,要找舊的用 ActorByID 或 ListActors)
//   - system:org 要是空字串,handle = 名稱(不含 sys_)
func (p *PG) ActorByHandle(kind model.ActorKind, org, handle string) (*model.ActorRecord, error) {
	h, err := model.CleanActorHandle(kind, handle)
	if err != nil {
		return nil, ErrNotFound
	}
	c, cancel := ctx()
	defer cancel()
	switch kind {
	case model.ActorKindUser:
		return scanActor(p.pool.QueryRow(c, `select `+actorCols+` from omixor_actors where kind = 'user' and lower(handle) = $1`, h))
	case model.ActorKindAgent:
		if org == "" {
			return nil, ErrNotFound
		}
		return scanActor(p.pool.QueryRow(c, `select `+actorCols+` from omixor_actors where kind = 'agent' and org = $1 and handle = $2 and disabled_at is null`, org, h))
	default:
		return scanActor(p.pool.QueryRow(c, `select `+actorCols+` from omixor_actors where kind = 'system' and handle = $1`, h))
	}
}

// ActorFilter ListActors 的條件;全部不填 = 所有現役 actor
type ActorFilter struct {
	Kind            model.ActorKind // 空 = 不分種類
	Org             string          // 非空 = 只列這個組織的角色(使用者、系統沒有組織,不會出現)
	IncludeDisabled bool            // 含退役、停用的
}

// ListActors 依建立時間(舊的在前)、id 排序
func (p *PG) ListActors(f ActorFilter) ([]model.ActorRecord, error) {
	c, cancel := ctx()
	defer cancel()
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Kind != "" {
		add("kind = $%d", string(f.Kind))
	}
	if f.Org != "" {
		add("org = $%d", f.Org)
	}
	if !f.IncludeDisabled {
		where = append(where, "disabled_at is null")
	}
	q := `select ` + actorCols + ` from omixor_actors`
	if len(where) > 0 {
		q += ` where ` + strings.Join(where, " and ")
	}
	rows, err := p.pool.Query(c, q+` order by created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ActorRecord{}
	for rows.Next() {
		a, err := scanActor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// actorMutable 系統 actor 不能改(回 ErrSystemActor);id 格式不對視為不存在
func actorMutable(id string) error {
	if k, ok := model.ActorKindOfID(id); !ok {
		return ErrNotFound
	} else if k == model.ActorKindSystem {
		return ErrSystemActor
	}
	return nil
}

// SetActorDisabled 停用(退役)或啟用 actor:disabled = true 設 disabled_at = at(已經停用的保留原時間,不報錯);
// false 清成 NULL(本來就啟用就什麼都不做)。不存在回 ErrNotFound,系統 actor 回 ErrSystemActor。
// 角色重新啟用時如果組織裡已有同名的現役角色回 ErrDuplicateKey。使用者的登入開關仍是 omixor_users.disabled(這一批不接)
func (p *PG) SetActorDisabled(id string, disabled bool, at time.Time) error {
	if err := actorMutable(id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	var tag int64
	var err error
	if disabled {
		t := secs(at)
		if at.IsZero() {
			t = secs(time.Now())
		}
		ct, e := p.pool.Exec(c, `update omixor_actors set disabled_at = coalesce(disabled_at, $2) where id = $1`, id, t)
		tag, err = ct.RowsAffected(), e
	} else {
		ct, e := p.pool.Exec(c, `update omixor_actors set disabled_at = null where id = $1`, id)
		tag, err = ct.RowsAffected(), e
	}
	if err != nil {
		return actorWriteErr(err)
	}
	if tag == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateActorProfile 改顯示名稱與頭像網址(nil = 不改)。不存在回 ErrNotFound,系統 actor 回 ErrSystemActor
func (p *PG) UpdateActorProfile(id string, name, avatar *string) error {
	if err := actorMutable(id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	ct, err := p.pool.Exec(c, `update omixor_actors set name = coalesce($2, name), avatar = coalesce($3, avatar) where id = $1`, id, name, avatar)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateActorHandle 改 handle(使用者換 email、角色改名):依種類規範化後寫入;衝突回 ErrDuplicateKey,格式不對回一般錯誤。
// 不存在回 ErrNotFound,系統 actor 回 ErrSystemActor。這一批不連帶改別的表(角色改名要同步 omixor_agents 是 B-2c 的事)
func (p *PG) UpdateActorHandle(id, handle string) error {
	if err := actorMutable(id); err != nil {
		return err
	}
	kind, _ := model.ActorKindOfID(id)
	h, err := model.CleanActorHandle(kind, handle)
	if err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	ct, err := p.pool.Exec(c, `update omixor_actors set handle = $2 where id = $1`, id, h)
	if err != nil {
		return actorWriteErr(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetActorOwner 設定或清除角色 actor 的負責人(owner = 使用者 actor id;空字串 = 清成 NULL)。
// 只有角色有 owner(其他種類回一般錯誤);owner 不存在或不是使用者回 ErrBadRef(或一般錯誤);角色不存在回 ErrNotFound,系統 actor 回 ErrSystemActor
func (p *PG) SetActorOwner(id, owner string) error {
	if err := actorMutable(id); err != nil {
		return err
	}
	if k, _ := model.ActorKindOfID(id); k != model.ActorKindAgent {
		return fmt.Errorf("只有角色 actor 有 owner(%s)", id)
	}
	var o *string
	if owner != "" {
		if !model.ValidActorIDOf(model.ActorKindUser, owner) {
			return fmt.Errorf("owner %q 要是使用者 actor id", owner)
		}
		o = &owner
	}
	c, cancel := ctx()
	defer cancel()
	ct, err := p.pool.Exec(c, `update omixor_actors set owner = $2 where id = $1`, id, o)
	if err != nil {
		return actorWriteErr(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
