package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- Issue 子項(PG;表 omixor_issue_items 的定義在 pg_schema.go 的 pgSchema,規則在 service/issue_items.go) ----
//
//	序號:omixor_seq 的 item:<issueID> scope(seqScopeMax 取 max(seq) 自癒),刪掉的號碼不再使用;Issue 永久刪除時序號列一併清(DeleteIssue)
//	assignee、claimed_by:actor id,沒有是 NULL(外鍵 set null);created_by:actor id(restrict);外鍵見 fkSpecs,程式裡的空字串 = NULL
//	depends_on:同一 Issue 內另一子項的 id(自參照外鍵,被依賴的刪了清成 NULL);「同一 Issue」與「無環」由這裡在交易內檢查
//	files:text[];evidence:jsonb(NULL = 沒有)

const itemCols = `org, id, issue_id, seq, title, status, assignee, depends_on, files, acceptance, evidence, block_reason, wait_for_kind, wait_for,
  claimed_by, locked_until, coalesce(task_id, ''), coalesce(promoted_issue_id, ''), created_by, created_at, updated_at, completed_at`

func scanIssueItem(row pgx.Row) (*model.IssueItem, error) {
	var it model.IssueItem
	var assignee, dep, claimed, createdBy NullID
	var evidence []byte
	var locked, completed *time.Time
	var created, updated time.Time
	if err := row.Scan(&it.Org, &it.ID, &it.IssueID, &it.Seq, &it.Title, &it.Status, &assignee, &dep, &it.Files, &it.Acceptance, &evidence,
		&it.BlockReason, &it.WaitForKind, &it.WaitFor, &claimed, &locked, &it.TaskID, &it.PromotedIssueID, &createdBy, &created, &updated, &completed); err != nil {
		return nil, notFound(err)
	}
	it.Assignee, it.DependsOn, it.ClaimedBy, it.CreatedBy = assignee.String(), dep.String(), claimed.String(), createdBy.String()
	if len(it.Files) == 0 {
		it.Files = nil
	}
	if len(evidence) > 0 {
		it.Evidence = evidence
	}
	it.LockedUntil, it.CompletedAt = tp(locked), tp(completed)
	it.CreatedAt, it.UpdatedAt = utcTime(created), utcTime(updated)
	return &it, nil
}

func collectIssueItems(rows pgx.Rows) ([]model.IssueItem, error) {
	defer rows.Close()
	out := []model.IssueItem{}
	for rows.Next() {
		it, err := scanIssueItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

func (p *PG) ListIssueItems(org, issueID string) ([]model.IssueItem, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+itemCols+` from omixor_issue_items where org = $1 and issue_id = $2 order by seq`, org, issueID)
	if err != nil {
		return nil, err
	}
	return collectIssueItems(rows)
}

func (p *PG) ListIssueItemsByOrg(org string, issueIDs []string) (map[string][]model.IssueItem, error) {
	c, cancel := ctx()
	defer cancel()
	var rows pgx.Rows
	var err error
	if len(issueIDs) == 0 {
		rows, err = p.pool.Query(c, `select `+itemCols+` from omixor_issue_items where org = $1 order by issue_id, seq`, org)
	} else {
		rows, err = p.pool.Query(c, `select `+itemCols+` from omixor_issue_items where org = $1 and issue_id = any($2) order by issue_id, seq`, org, issueIDs)
	}
	if err != nil {
		return nil, err
	}
	l, err := collectIssueItems(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]model.IssueItem{}
	for _, it := range l {
		out[it.IssueID] = append(out[it.IssueID], it)
	}
	return out, nil
}

func (p *PG) GetIssueItem(org, id string) (*model.IssueItem, error) {
	c, cancel := ctx()
	defer cancel()
	return scanIssueItem(p.pool.QueryRow(c, `select `+itemCols+` from omixor_issue_items where org = $1 and id = $2`, org, id))
}

// itemCheckDependsOn 依賴要是同一 Issue 內另一個子項,而且不成環。要在已鎖住該 Issue 全部子項的交易裡呼叫(同時修改依賴的人排隊)。
// selfID 是正在建立或修改的子項(建立時尚未存在也沒關係)
func itemCheckDependsOn(c context.Context, tx pgx.Tx, org, issueID, selfID, dep string) error {
	if dep == "" {
		return nil
	}
	if dep == selfID {
		return fmt.Errorf("%w: 子項不能依賴自己(成環)", ErrBadRef)
	}
	cur := dep
	for range model.MaxItemsPerIssue + 1 {
		var iid string
		var next NullID
		err := tx.QueryRow(c, `select issue_id, depends_on from omixor_issue_items where org = $1 and id = $2`, org, cur).Scan(&iid, &next)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: 依賴的子項不存在", ErrBadRef)
		}
		if err != nil {
			return err
		}
		if iid != issueID {
			return fmt.Errorf("%w: 依賴的子項不屬於同一個 Issue", ErrBadRef)
		}
		if next == "" {
			return nil
		}
		if next.String() == selfID {
			return fmt.Errorf("%w: 依賴會成環", ErrBadRef)
		}
		cur = next.String()
	}
	return fmt.Errorf("%w: 依賴鏈太長(成環)", ErrBadRef)
}

// lockIssueItems 鎖住這張 Issue 的全部子項列(序列化「改依賴」的並發)
func lockIssueItems(c context.Context, tx pgx.Tx, org, issueID string) error {
	rows, err := tx.Query(c, `select 1 from omixor_issue_items where org = $1 and issue_id = $2 order by id for update`, org, issueID)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

func evidenceArg(e []byte) any {
	if len(e) == 0 {
		return nil
	}
	return string(e)
}

func filesArg(f []string) []string {
	if f == nil {
		return []string{}
	}
	return f
}

func (p *PG) CreateIssueItem(org string, in *model.IssueItem) (*model.IssueItem, error) {
	c, cancel := ctx()
	defer cancel()
	it := in.Clone()
	now := time.Now().UTC().Truncate(time.Second)
	if it.ID == "" {
		it.ID = model.NewIssueID(now)
	}
	if it.CreatedAt.IsZero() {
		it.CreatedAt = model.Time{Time: now}
	}
	if it.UpdatedAt.IsZero() {
		it.UpdatedAt = it.CreatedAt
	}
	tx, err := p.pool.Begin(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	var one int
	if err := tx.QueryRow(c, `select 1 from ops_issues where org = $1 and id = $2`, org, it.IssueID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: Issue 不存在", ErrBadRef)
		}
		return nil, err
	}
	n, err := seqNext(c, tx, org, "item:"+it.IssueID, 0) // 序號列的行鎖同時序列化同一 Issue 的並發建立
	if err != nil {
		return nil, err
	}
	var cnt int
	if err := tx.QueryRow(c, `select count(*) from omixor_issue_items where org = $1 and issue_id = $2`, org, it.IssueID).Scan(&cnt); err != nil {
		return nil, err
	}
	if cnt >= model.MaxItemsPerIssue {
		return nil, fmt.Errorf("%w: 一張 Issue 最多 %d 個子項", ErrBadRef, model.MaxItemsPerIssue)
	}
	if err := lockIssueItems(c, tx, org, it.IssueID); err != nil {
		return nil, err
	}
	if err := itemCheckDependsOn(c, tx, org, it.IssueID, it.ID, it.DependsOn); err != nil {
		return nil, err
	}
	it.Seq = n
	if _, err := tx.Exec(c, `insert into omixor_issue_items (org, id, issue_id, seq, title, status, assignee, depends_on, files, acceptance, evidence,
  block_reason, wait_for_kind, wait_for, claimed_by, locked_until, task_id, promoted_issue_id, created_by, created_at, updated_at, completed_at)
  values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$16,nullif($17,''),nullif($18,''),$19,$20,$21,$22)`,
		org, it.ID, it.IssueID, it.Seq, it.Title, it.Status, NullID(it.Assignee), NullID(it.DependsOn), filesArg(it.Files), it.Acceptance, evidenceArg(it.Evidence),
		it.BlockReason, it.WaitForKind, it.WaitFor, NullID(it.ClaimedBy), optSecs(it.LockedUntil), it.TaskID, it.PromotedIssueID, NullID(it.CreatedBy),
		secs(it.CreatedAt.Time), secs(it.UpdatedAt.Time), optSecs(it.CompletedAt)); err != nil {
		return nil, fkErr(err)
	}
	if err := tx.Commit(c); err != nil {
		return nil, err
	}
	return p.GetIssueItem(org, it.ID)
}

func (p *PG) UpdateIssueItem(org string, it *model.IssueItem, fromStatus string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	var issueID string
	if err := tx.QueryRow(c, `select issue_id from omixor_issue_items where org = $1 and id = $2`, org, it.ID).Scan(&issueID); err != nil {
		return notFound(err)
	}
	if it.DependsOn != "" {
		if err := lockIssueItems(c, tx, org, issueID); err != nil {
			return err
		}
		if err := itemCheckDependsOn(c, tx, org, issueID, it.ID, it.DependsOn); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(c, `update omixor_issue_items set title = $3, status = $4, assignee = $5, depends_on = $6, files = $7, acceptance = $8, evidence = $9::jsonb,
  block_reason = $10, wait_for_kind = $11, wait_for = $12, claimed_by = $13, locked_until = $14, task_id = nullif($15,''), promoted_issue_id = nullif($16,''),
  completed_at = $17, updated_at = $18
  where org = $1 and id = $2 and status = $19`,
		org, it.ID, it.Title, it.Status, NullID(it.Assignee), NullID(it.DependsOn), filesArg(it.Files), it.Acceptance, evidenceArg(it.Evidence),
		it.BlockReason, it.WaitForKind, it.WaitFor, NullID(it.ClaimedBy), optSecs(it.LockedUntil), it.TaskID, it.PromotedIssueID,
		optSecs(it.CompletedAt), secs(it.UpdatedAt.Time), fromStatus)
	if err != nil {
		return fkErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStale // 列已確認存在(上面讀到 issue_id),0 列就是狀態對不上
	}
	return tx.Commit(c)
}

func (p *PG) ClaimIssueItem(org, id, actor string, until time.Time) (*model.IssueItem, error) {
	if actor == "" {
		return nil, fmt.Errorf("%w: 領取者不能空白", ErrBadRef)
	}
	c, cancel := ctx()
	defer cancel()
	now := time.Now().UTC().Truncate(time.Second)
	it, err := scanIssueItem(p.pool.QueryRow(c, `update omixor_issue_items set status = 'doing', claimed_by = $3, locked_until = $4, updated_at = $5
  where org = $1 and id = $2 and (status = 'todo' or (status = 'doing' and (locked_until is null or locked_until <= $5 or claimed_by = $3)))
  returning `+itemCols, org, id, actor, secs(until), now))
	if err == nil {
		return it, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fkErr(err)
	}
	var one int
	if err := p.pool.QueryRow(c, `select 1 from omixor_issue_items where org = $1 and id = $2`, org, id).Scan(&one); err != nil {
		return nil, notFound(err)
	}
	return nil, ErrLocked
}

func (p *PG) ReleaseIssueItem(org, id, actor string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_issue_items set status = 'todo', claimed_by = null, locked_until = null, updated_at = $4
  where org = $1 and id = $2 and status = 'doing' and ($3 = '' or claimed_by = $3)`, org, id, actor, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var status string
	if err := p.pool.QueryRow(c, `select status from omixor_issue_items where org = $1 and id = $2`, org, id).Scan(&status); err != nil {
		return notFound(err)
	}
	if status != model.ItemDoing {
		return ErrStale
	}
	return ErrLocked
}

func (p *PG) DeleteIssueItem(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_issue_items where org = $1 and id = $2`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
