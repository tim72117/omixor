// pg_agent.go 虛擬角色、任務與角色憑證的 PG 實作

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 虛擬角色、任務與憑證 ----

// docAccess、routines 在新表(見 pg_split_docs_agents.go),不在這裡讀
const agentCols = `id, name, role, instructions, active, symbol, color, created_at, coalesce(actor_id, '')`

func (p *PG) ListAgents(org string) ([]model.Agent, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.roTx(c) // 角色列與子表在同一個快照
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select `+agentCols+` from omixor_agents where org = $1 order by created_at, id`, org)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, p.loadAgentChildren(c, tx, org, "", out)
}

func scanAgent(row pgx.Row) (*model.Agent, error) {
	var g model.Agent
	var created time.Time
	if err := row.Scan(&g.ID, &g.Name, &g.Role, &g.Instructions, &g.Active, &g.Symbol, &g.Color, &created, &g.ActorID); err != nil {
		return nil, err
	}
	g.CreatedAt = model.Time{Time: created.UTC()}
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
	tx, err := p.roTx(c) // 角色列與子表在同一個快照
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	g, err := scanAgent(tx.QueryRow(c, `select `+agentCols+` from omixor_agents where org = $1 and id = $2`, org, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	one := []model.Agent{*g}
	if err := p.loadAgentChildren(c, tx, org, id, one); err != nil {
		return nil, err
	}
	return &one[0], nil
}

// SaveAgent 新增或整筆取代角色;角色的 actor(omixor_actors)在同一個交易裡建立或同步(見 pg_actor_agents.go)。
// a.ActorID 寫入時忽略(由 store 決定);新增時 owner 用組織預設,要明確指定用 SaveAgentOwned
func (p *PG) SaveAgent(org string, a model.Agent) error { return p.saveAgent(org, a, "") }

func (p *PG) saveAgent(org string, a model.Agent, owner string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := p.guardUnsplit(c, tx, splitAgents, org, a.ID); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	aid, err := syncAgentActor(c, tx, org, a, owner) // actor 先建(複合外鍵要它先存在),和角色列同一個交易
	if err != nil {
		return err
	}
	// 不再寫舊的 doc_access、routines jsonb;split_at 標記「已展開到新表」,啟動遷移不會拿舊 jsonb 覆蓋
	if _, err := tx.Exec(c, `insert into omixor_agents (org, id, name, role, instructions, active, symbol, color, created_at, split_at, actor_id)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9, now(), $10)
		on conflict (org, id) do update set name = $3, role = $4, instructions = $5, active = $6, symbol = $7, color = $8, split_at = now(),
			actor_id = coalesce(omixor_agents.actor_id, $10)`,
		org, a.ID, a.Name, a.Role, a.Instructions, a.Active, a.Symbol, a.Color, secs(a.CreatedAt.Time), nullStr(aid)); err != nil {
		return err
	}
	if err := replaceAgentChildren(c, tx, org, a.ID, a.DocAccess, a.Routines); err != nil {
		return err
	}
	return tx.Commit(c)
}

// DeleteAgent 刪角色列;角色的 actor 退役(disabled_at,列不刪,同 id 重建是新的 actor),同一個交易
func (p *PG) DeleteAgent(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	var aid *string
	if err := tx.QueryRow(c, `delete from omixor_agents where org = $1 and id = $2 returning actor_id`, org, id).Scan(&aid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fkErrDelete(err)
	}
	if aid != nil {
		if _, err := tx.Exec(c, `update omixor_actors set disabled_at = coalesce(disabled_at, now()) where id = $1`, *aid); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}

// taskCols created_by、claimed_by 是 actor id(可為 NULL);對外的相容字串由服務層 present 補
const taskCols = `id, agent_id, title, instructions, priority, status, result, routine_id, created_by,
	created_at, updated_at, claimed_at, completed_at, locked_until, claimed_by`

func scanTask(row pgx.Row) (*model.AgentTask, error) {
	var t model.AgentTask
	var created, updated time.Time
	var claimed, completed, locked *time.Time
	var createdBy, claimedBy NullID
	if err := row.Scan(&t.ID, &t.AgentID, &t.Title, &t.Instructions, &t.Priority, &t.Status, &t.Result,
		&t.RoutineID, &createdBy, &created, &updated, &claimed, &completed, &locked, &claimedBy); err != nil {
		return nil, err
	}
	t.CreatedBy, t.ClaimedBy = createdBy.String(), claimedBy.String()
	t.CreatedAt, t.UpdatedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}
	t.ClaimedAt, t.CompletedAt, t.LockedUntil = tp(claimed), tp(completed), tp(locked)
	t.Links, t.Notes = []string{}, []model.AgentTaskNote{} // 備註、連結在子表(見 pg_split_incidents_tasks.go),readTasks 補上
	return &t, nil
}

func (p *PG) ListAgentTasks(org string) ([]model.AgentTask, error) { return p.readTasks(org, "") }

func (p *PG) GetAgentTask(org, id string) (*model.AgentTask, error) {
	l, err := p.readTasks(org, id)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (p *PG) CreateAgentTask(org string, t model.AgentTask) (model.AgentTask, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return t, err
	}
	defer tx.Rollback(c)
	// 號碼由序號表單句原子取得(刪掉的號碼不再使用)
	num, err := seqNext(c, tx, org, "task", 0)
	if err != nil {
		return t, err
	}
	t.ID = fmt.Sprintf("T-%d", num)
	err = insertTask(c, tx, org, num, t)
	if err != nil {
		return t, fkErr(err)
	}
	return t, tx.Commit(c)
}

func (p *PG) ImportAgents(org string, agents []model.Agent, tasks []model.AgentTask) error {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-agent-tasks:' || $1))`, org); err != nil {
		return err
	}
	if err := seqBump(c, tx, org, "task", 0); err != nil { // 鎖住序號列,和 CreateAgentTask 互斥
		return err
	}
	var na, nt int
	if err := tx.QueryRow(c, `select (select count(*) from omixor_agents where org = $1), (select count(*) from omixor_agent_tasks where org = $1)`, org).Scan(&na, &nt); err != nil {
		return err
	}
	if na > 0 || nt > 0 {
		return fmt.Errorf("組織 %s 已經有 %d 個角色、%d 筆任務", org, na, nt)
	}
	for _, a := range agents {
		aid, err := syncAgentActor(c, tx, org, a, "")
		if err != nil {
			return fmt.Errorf("角色 %s:%w", a.ID, err)
		}
		if _, err := tx.Exec(c, `insert into omixor_agents (org, id, name, role, instructions, active, symbol, color, created_at, split_at, actor_id)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9, now(), $10)`,
			org, a.ID, a.Name, a.Role, a.Instructions, a.Active, a.Symbol, a.Color, secs(a.CreatedAt.Time), nullStr(aid)); err != nil {
			return fmt.Errorf("角色 %s:%w", a.ID, err)
		}
		if err := replaceAgentChildren(c, tx, org, a.ID, a.DocAccess, a.Routines); err != nil {
			return fmt.Errorf("角色 %s:%w", a.ID, err)
		}
	}
	for _, t := range tasks {
		if err := insertTask(c, tx, org, t.Number(), t); err != nil {
			return fmt.Errorf("任務 %s:%w", t.ID, fkErr(err))
		}
		if err := seqBump(c, tx, org, "task", t.Number()); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}

const updateTaskSQL = `update omixor_agent_tasks set agent_id = $3, title = $4, instructions = $5, priority = $6, status = $7,
		result = $8, routine_id = $9, created_by = $10, created_at = $11, updated_at = $12, claimed_at = $13,
		completed_at = $14, locked_until = $15, claimed_by = $16, split_at = now() where org = $1 and id = $2`

// taskArgs updateTaskSQL 的參數(備註、連結在子表,見 syncTaskChildren);created_by、claimed_by 是 actor id,空字串存 NULL
func taskArgs(org string, t model.AgentTask) []any {
	return []any{org, t.ID, t.AgentID, t.Title, t.Instructions, t.Priority, t.Status, t.Result,
		t.RoutineID, IDOrNil(t.CreatedBy), secs(t.CreatedAt.Time), secs(t.UpdatedAt.Time), optSecs(t.ClaimedAt), optSecs(t.CompletedAt), optSecs(t.LockedUntil), IDOrNil(t.ClaimedBy)}
}

func (p *PG) UpdateAgentTask(org string, t model.AgentTask) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := p.guardUnsplit(c, tx, splitTasks, org, t.ID); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	// 先更新父列(行鎖,同一個任務的並行更新排隊),再讓備註、連結子表等於 t(追加備註只插入新的那一列)
	tag, err := tx.Exec(c, updateTaskSQL, taskArgs(org, t)...)
	if err != nil {
		return fkErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := syncTaskChildren(c, tx, org, t); err != nil {
		return err
	}
	return tx.Commit(c)
}

// ClaimAgentTask 條件式更新:只有任務「不是進行中、或沒有鎖、或鎖已到期」才寫入;
// 影響 0 列時再查一次,任務還在就是被別的程式搶先領取(ErrLocked),不在就是 ErrNotFound
func (p *PG) ClaimAgentTask(org string, t model.AgentTask, now time.Time) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := p.guardUnsplit(c, tx, splitTasks, org, t.ID); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	args := append(taskArgs(org, t), now)
	tag, err := tx.Exec(c, updateTaskSQL+` and (status <> 'inProgress' or locked_until is null or locked_until <= $17)`, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if err := syncTaskChildren(c, tx, org, t); err != nil {
			return err
		}
		return tx.Commit(c)
	}
	{
		var one int
		err := tx.QueryRow(c, `select 1 from omixor_agent_tasks where org = $1 and id = $2`, org, t.ID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return ErrLocked
	}
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
		return fkErr(err)
	}
	return tx.Commit(c)
}

func (p *PG) AgentCredentialForTask(org, taskID string) (*model.AgentCredential, error) {
	c, cancel := ctx()
	defer cancel()
	var cr model.AgentCredential
	var issued, exp time.Time
	err := p.pool.QueryRow(c, `select token_hash, org, agent_id, task_id, issued_at, expires_at from omixor_agent_credentials
		where org = $1 and task_id = $2`, org, taskID).Scan(&cr.Hash, &cr.Org, &cr.AgentID, &cr.TaskID, &issued, &exp)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	cr.IssuedAt, cr.ExpiresAt = model.Time{Time: issued.UTC()}, model.Time{Time: exp.UTC()}
	return &cr, nil
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
