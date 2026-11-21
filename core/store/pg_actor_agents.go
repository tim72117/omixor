// pg_actor_agents.go 虛擬角色(omixor_agents)與 actor 的連動(ISS-36 階段 B-2c;規格見雲端 omixor DOC-13 §1.2「案 B」、§2.3、§6)。
//
// 設計(DOC-13 案 B):角色維持 (org, id) 主鍵,id 就是對外的角色 id;另外加一欄 actor_id(唯一),指到 omixor_actors 的 agt_<uuidv7>。
// 角色專屬的關聯(任務、例行、文件權限、憑證、組織鏈上的角色節點)仍用 (org, 角色 id),只有「誰」(作者、指派對象、領取者…,C-1 之後)才用 actor id。
//
//	omixor_agents.actor_id   可為 NULL(見下);unique;(actor_id, org, id) → omixor_actors (id, org, handle) 複合外鍵:
//	                         actor 的 org、handle 一定等於角色的 org、id(兩份資料靠外鍵鎖住一致)。
//	                         沒有 on update cascade:角色 id 不能改(service 沒有改名功能,用不同 id 存 = 新角色),
//	                         改 actor handle(UpdateActorHandle)會因為被角色參照而失敗,免得兩邊對不上;要做改名要同時改外鍵與子表(另案)。
//	omixor_actors (kind='agent')   一個角色一列:org、handle = 角色 id、name = 角色名稱、owner = 負責人(使用者 actor id,可為 NULL)、
//	                         disabled_at = 角色停用(active = false)或被刪(退役,列不刪,同名可以再建一個新的 actor)的時間
//
// 同步規則(都在 SaveAgent、DeleteAgent、ImportAgents 的同一個交易裡,任何一步失敗全部回滾):
//   - 新增角色 → 建 actor(owner 預設 = 該組織第一位管理員的使用者 actor,找不到就 NULL;SaveAgentOwned 可以明確指定)
//   - 修改角色 → actor 的 name 與 disabled_at 跟著(active = false 停用、true 啟用)
//   - 刪除角色 → actor 退役(disabled_at),列保留;同 id 重建 = 新的 actor(現役的 (org, handle) 唯一索引放行)
//   - 角色 id 不符 handle 規則(model.ValidAgentID 以前的舊資料)→ 沒有 actor,actor_id 是 NULL,其餘功能照舊;遷移會寫警告
//
// 為什麼 actor_id 不加 NOT NULL:第一次正式部署是滾動更新,舊版本在服務時寫入的角色還沒有 actor_id;
// 這一版啟動時補、之後每次啟動只查一次有沒有 NULL(D11:第一次部署只轉換,不加會擋舊版寫入的約束)。
//
// owner 與使用者側:owner 要指到使用者 actor(B-2b:omixor_users.actor_id)。使用者還沒有 actor 時找不到對應的人,
// owner 先留 NULL;之後每次啟動的 backfillAgentOwners 會補(也可以呼叫 Store.BackfillAgentOwners 或 SetActorOwner)。

package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

const (
	agentActorUniqueName = "omixor_agents_actor_id_key"
	agentActorFKName     = "omixor_agents_actor_fk"
)

// agentHandleOK 角色 id 能不能當 actor handle(規範化後要和 id 一樣,複合外鍵才對得上)
func agentHandleOK(id string) bool {
	h, err := model.CleanActorHandle(model.ActorKindAgent, id)
	return err == nil && h == id
}

// defaultAgentOwnerSQL 組織的預設 owner:該組織第一位管理員成員 → 最早的啟用中伺服器管理員;只認已經有使用者 actor 的人(omixor_users.actor_id)
// ($1 = 組織;沒有人回 NULL)
var defaultAgentOwnerSQL = strings.ReplaceAll(defaultAgentOwnerSQL2, "ag.org", "$1")

func defaultAgentOwner(c context.Context, q actorQ, org string) (string, error) {
	var o *string
	if err := q.QueryRow(c, defaultAgentOwnerSQL, org).Scan(&o); err != nil {
		return "", err
	}
	if o == nil {
		return "", nil
	}
	return *o, nil
}

// syncAgentActor 在呼叫端的交易裡讓角色 (org, a.ID) 有對應、一致的 actor,回傳 actor id("" = 這個角色沒有 actor:id 不符 handle 規則)。
// 要在寫 omixor_agents 之前呼叫(複合外鍵要 actor 先存在)。
//   - 角色列已有 actor_id:name、disabled_at 跟 a 同步(沒有差別就不寫)
//   - 沒有角色列或 actor_id 是 NULL:建新 actor。先把同名、而且沒有任何角色列指著的現役 actor 退役(舊版本刪角色留下的殘留),
//     否則現役的 (org, handle) 唯一索引會擋掉
//
// owner 空字串 = 用組織預設(defaultAgentOwner);已經有 actor 的角色不改 owner
func syncAgentActor(c context.Context, q actorQ, org string, a model.Agent, owner string) (string, error) {
	if !agentHandleOK(a.ID) {
		return "", nil
	}
	var cur *string
	if err := q.QueryRow(c, `select actor_id from omixor_agents where org = $1 and id = $2 for update`, org, a.ID).Scan(&cur); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	now := model.Now()
	if cur != nil && *cur != "" {
		if _, err := q.Exec(c, `update omixor_actors set name = $2, disabled_at = case when $3::boolean then null else coalesce(disabled_at, $4) end
where id = $1 and (name <> $2 or (disabled_at is null) <> $3::boolean)`, *cur, a.Name, a.Active, secs(now.Time)); err != nil {
			return "", actorWriteErr(err)
		}
		return *cur, nil
	}
	if _, err := q.Exec(c, `update omixor_actors set disabled_at = $3 where kind = 'agent' and org = $1 and handle = $2 and disabled_at is null
and not exists (select 1 from omixor_agents g where g.actor_id = omixor_actors.id)`, org, a.ID, secs(now.Time)); err != nil {
		return "", err
	}
	if owner == "" {
		o, err := defaultAgentOwner(c, q, org)
		if err != nil {
			return "", err
		}
		owner = o
	}
	rec := model.ActorRecord{Kind: model.ActorKindAgent, Org: org, Name: a.Name, Handle: a.ID, Owner: owner, CreatedAt: a.CreatedAt}
	if !a.Active {
		rec.DisabledAt = &now
	}
	rec, err := prepareActor(rec)
	if err != nil {
		return "", err
	}
	out, err := insertActor(c, q, rec)
	if err != nil {
		return "", err
	}
	return out.ID, nil
}

// nullStr 空字串 → NULL
func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// SaveAgentOwned 同 SaveAgent;新增角色時 owner(使用者 actor id)寫進角色 actor 的 owner(空字串 = 組織預設)。已經存在的角色不改 owner
func (p *PG) SaveAgentOwned(org string, a model.Agent, owner string) error {
	if owner != "" && !model.ValidActorIDOf(model.ActorKindUser, owner) {
		return fmt.Errorf("owner %q 要是使用者 actor id", owner)
	}
	return p.saveAgent(org, a, owner)
}

// BackfillAgentOwners 把還沒有 owner 的角色 actor 補上預設 owner(該組織第一位管理員成員 → 最早的伺服器管理員,只認有使用者 actor 的人);
// 回傳補了幾個。找不到人的維持 NULL,可以重複呼叫。啟動遷移每次都會跑一次(使用者 actor 在 B-2b 轉換之後才有)
func (p *PG) BackfillAgentOwners() (int, error) {
	c, cancel := ctx()
	defer cancel()
	ct, err := p.pool.Exec(c, `update omixor_actors a set owner = o.uid
from (select ag.id as aid, (`+defaultAgentOwnerSQL2+`) as uid from omixor_actors ag where ag.kind = 'agent' and ag.owner is null) o
where a.id = o.aid and o.uid is not null`)
	if err != nil {
		return 0, actorWriteErr(err)
	}
	return int(ct.RowsAffected()), nil
}

// defaultAgentOwnerSQL2 defaultAgentOwnerSQL 的相關子查詢版($1 換成 ag.org),給 BackfillAgentOwners 一次處理全部角色
const defaultAgentOwnerSQL2 = `select coalesce(
  (select u.actor_id from omixor_org_members m join omixor_users u on u.id = m.user_id
     join omixor_actors ua on ua.id = u.actor_id and ua.kind = 'user'
     where m.org = ag.org and m.role = 'admin' order by m.added_at, m.user_id limit 1),
  (select u.actor_id from omixor_users u join omixor_actors ua on ua.id = u.actor_id and ua.kind = 'user'
     where u.role = 'admin' and not u.disabled order by u.created_at, u.id limit 1))`

// ---- 啟動遷移 ----

func (p *PG) constraintState(c context.Context, name string) (exists, validated bool, err error) {
	err = p.pool.QueryRow(c, `select exists (select 1 from pg_constraint where conname = $1 and conrelid = to_regclass('omixor_agents')),
coalesce((select convalidated from pg_constraint where conname = $1 and conrelid = to_regclass('omixor_agents')), false)`, name).Scan(&exists, &validated)
	return
}

// skipAgentActors、skipUserActors migNotes 裡記「這一步被略過」的鍵(「誰」欄位引擎要用 omixor_agents.actor_id、omixor_users.actor_id,見 actorColumnsPrecheck)
const (
	skipAgentActors = "角色 actor 遷移"
	skipUserActors  = "使用者 actor 遷移"
)

// agentActorBatch 一次補建的角色數
const agentActorBatch = 200

// migrateAgentActors 啟動遷移的一步(B-2c):角色接上 actor。位置:migrateActors 之後(actors 表要先在)、migrateForeignKeys 之前。
// 冪等、多實例同時啟動只有一個在做(session advisory lock,等不到就略過這一輪);每次啟動的成本是幾個 catalog 查詢與一次 "有沒有 NULL" 的查詢。
//
//  1. omixor_agents 加 actor_id 欄(可為 NULL,沒有預設值,不重寫表)
//  2. 為沒有 actor_id 的角色建 agt_ actor(每批一個交易;id 不符 handle 規則的略過並警告;預算用完就停,下次續做)
//  3. 對齊:角色名稱、停用狀態以角色列為準;沒有角色列指著的現役角色 actor(舊版本刪角色的殘留)退役
//  4. 加唯一約束與複合外鍵(not valid → validate;失敗只警告)
//  5. 補 owner(找不到人的維持 NULL)
//
// 表大小超過 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB 時整段略過並警告(角色表平常只有幾十列)
func (p *PG) migrateAgentActors() error {
	p.note.setSkip(skipAgentActors, "")
	c, cancel := p.migCtx(10 * time.Minute)
	defer cancel()
	unlock, ok, err := p.sessionLock(c, "omixor-agent-actors-migrate", "角色 actor 遷移")
	if err != nil {
		return err
	}
	if !ok {
		p.note.setSkip(skipAgentActors, "等不到遷移鎖(另一個實例正在做角色 actor 遷移)")
		return nil
	}
	defer unlock()
	var have bool
	if err := p.pool.QueryRow(c, `select to_regclass('omixor_actors') is not null`).Scan(&have); err != nil {
		return err
	}
	if !have {
		warnf("角色 actor 遷移:找不到 omixor_actors,略過這次")
		p.note.setSkip(skipAgentActors, "找不到 omixor_actors")
		return nil
	}
	if size, err := tableBytes(c, p.pool, "omixor_agents"); err != nil {
		return err
	} else if oversized(size) {
		warnf("角色 actor 遷移:omixor_agents 約 %s,超過自動遷移的門檻 %s,略過;請在維護窗口處理或調大 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB", humanBytes(size), humanBytes(maxAutoRewriteBytes))
		p.note.setSkip(skipAgentActors, fmt.Sprintf("omixor_agents 約 %s,超過自動遷移的門檻 %s", humanBytes(size), humanBytes(maxAutoRewriteBytes)))
		return nil
	}
	// 1. 欄位
	hasCol, err := p.columnExists(c, "omixor_agents", "actor_id")
	if err != nil {
		return err
	}
	if !hasCol {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `alter table omixor_agents add column if not exists actor_id text`)
			return err
		}); err != nil {
			return fmt.Errorf("omixor_agents 加 actor_id 欄:%w", err)
		}
	}
	// 2. 補建 actor
	created, skipped, err := p.backfillAgentActors(c)
	if err != nil {
		return err
	}
	if len(skipped) > 0 {
		warnf("角色 actor 遷移:%d 個角色的 id 不符 handle 規則(小寫英數字與 -),沒有建 actor(actor_id 維持 NULL):%v", len(skipped), skipped)
	}
	if created > 0 {
		log.Printf("[遷移] 角色 actor:為 %d 個既有角色建立 actor", created)
	}
	if p.budgetExpired() {
		warnf("角色 actor 遷移:啟動遷移整體預算已用完,後面的對齊與外鍵留到下次啟動")
		p.note.setSkip(skipAgentActors, "啟動遷移整體預算已用完,角色 actor 補建沒做完")
		return nil
	}
	// 3. 對齊
	if _, err := p.pool.Exec(c, `update omixor_actors a set name = g.name,
  disabled_at = case when g.active then null else coalesce(a.disabled_at, now()) end
from omixor_agents g where g.actor_id = a.id and (a.name <> g.name or (a.disabled_at is null) <> g.active)`); err != nil {
		warnf("角色 actor 遷移:對齊名稱與停用狀態失敗:%v", err)
	}
	if _, err := p.pool.Exec(c, `update omixor_actors a set disabled_at = now() where a.kind = 'agent' and a.disabled_at is null
and not exists (select 1 from omixor_agents g where g.actor_id = a.id)`); err != nil {
		warnf("角色 actor 遷移:退役沒有角色列的 actor 失敗:%v", err)
	}
	// 4. 約束
	if ex, _, err := p.constraintState(c, agentActorUniqueName); err != nil {
		return err
	} else if !ex {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `alter table omixor_agents add constraint `+agentActorUniqueName+` unique (actor_id)`)
			return err
		}); err != nil {
			warnf("角色 actor 遷移:加 actor_id 唯一約束失敗:%v", err)
		}
	}
	ex, valid, err := p.constraintState(c, agentActorFKName)
	if err != nil {
		return err
	}
	if !ex {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `alter table omixor_agents add constraint `+agentActorFKName+
				` foreign key (actor_id, org, id) references omixor_actors (id, org, handle) not valid`)
			return err
		}); err != nil {
			warnf("角色 actor 遷移:加複合外鍵失敗:%v", err)
		} else {
			ex = true
		}
	}
	if ex && !valid {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `alter table omixor_agents validate constraint `+agentActorFKName)
			return err
		}); err != nil {
			warnf("角色 actor 遷移:驗證複合外鍵失敗(角色與 actor 的 org、handle 對不上?):%v", err)
		}
	}
	// 5. owner
	if n, err := p.BackfillAgentOwners(); err != nil {
		warnf("角色 actor 遷移:補 owner 失敗:%v", err)
	} else if n > 0 {
		log.Printf("[遷移] 角色 actor:補了 %d 個 owner", n)
	}
	return nil
}

// backfillAgentActors 為沒有 actor_id 的角色建 actor 並寫回(每批一個交易)。回傳建了幾個、略過的角色(org/id)
func (p *PG) backfillAgentActors(c context.Context) (created int, skipped []string, err error) {
	lastOrg, lastID := "", ""
	for {
		if p.budgetExpired() {
			return created, skipped, nil
		}
		n, scanned, sk, e := p.backfillAgentActorsBatch(c, lastOrg, lastID)
		if e != nil {
			return created, skipped, e
		}
		created += n
		skipped = append(skipped, sk.names...)
		if scanned == 0 {
			return created, skipped, nil
		}
		lastOrg, lastID = sk.lastOrg, sk.lastID
	}
}

type agentBatchEnd struct {
	names           []string
	lastOrg, lastID string
}

func (p *PG) backfillAgentActorsBatch(c context.Context, afterOrg, afterID string) (created, scanned int, end agentBatchEnd, err error) {
	tx, err := p.pool.Begin(c)
	if err != nil {
		return
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select org, id, name, active, created_at from omixor_agents
where actor_id is null and (org, id) > ($1, $2) order by org, id limit $3`, afterOrg, afterID, agentActorBatch)
	if err != nil {
		return
	}
	type row struct {
		org string
		a   model.Agent
	}
	var list []row
	for rows.Next() {
		var r row
		var ct time.Time
		if err = rows.Scan(&r.org, &r.a.ID, &r.a.Name, &r.a.Active, &ct); err != nil {
			rows.Close()
			return
		}
		r.a.CreatedAt = utcTime(ct)
		list = append(list, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}
	for _, r := range list {
		scanned++
		end.lastOrg, end.lastID = r.org, r.a.ID
		var aid string
		if aid, err = syncAgentActor(c, tx, r.org, r.a, ""); err != nil {
			err = fmt.Errorf("角色 %s/%s:%w", r.org, r.a.ID, err)
			return
		}
		if aid == "" {
			end.names = append(end.names, r.org+"/"+r.a.ID)
			continue
		}
		if _, err = tx.Exec(c, `update omixor_agents set actor_id = $3 where org = $1 and id = $2 and actor_id is null`, r.org, r.a.ID, aid); err != nil {
			return
		}
		created++
	}
	err = tx.Commit(c)
	return
}
