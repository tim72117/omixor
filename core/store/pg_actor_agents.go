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
	"strings"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
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
// 回傳補了幾個。找不到人的維持 NULL,可以重複呼叫。
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
