package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 待決定 / 需要協助(PG;表 omixor_decisions 的定義在 pg.go 的 pgSchema,規則在 service/decision.go) ----
//
//	編號:omixor_seq 的 decision scope(DEC-<n>,一個組織一起編號,刪掉的號碼不再使用)
//	created_by(not null)、decided_by(沒處理 NULL):actor id,外鍵指向 omixor_actors(fk_decisions_created_by / fk_decisions_decided_by,
//	  由 migrateForeignKeys 補,因為 omixor_actors 在 pgSchema 之後才建;新表直接是最終結構,不經過 actor 欄位轉換引擎)
//	source_issue:NULL(沒有來源)或 ops_issues 的內部 id(外鍵,Issue 永久刪除時清成 NULL;垃圾桶裡的 Issue 列還在,不清);程式裡的空字串 = NULL

const decisionCols = `id, kind, title, body, options, status, answer, answer_note, evidence, coalesce(source_issue, ''), blocks,
  created_by, decided_by, created_at, decided_at, updated_at`

func scanDecision(row pgx.Row) (*model.Decision, error) {
	var d model.Decision
	var opts, blocks []byte
	var created, updated time.Time
	var decided *time.Time
	var createdBy, decidedBy NullID
	if err := row.Scan(&d.ID, &d.Kind, &d.Title, &d.Body, &opts, &d.Status, &d.Answer, &d.AnswerNote, &d.Evidence, &d.SourceIssue, &blocks,
		&createdBy, &decidedBy, &created, &decided, &updated); err != nil {
		return nil, notFound(err)
	}
	d.CreatedBy, d.DecidedBy = createdBy.String(), decidedBy.String()
	d.Options, d.Blocks = []model.DecisionOption{}, []string{}
	if err := json.Unmarshal(opts, &d.Options); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(blocks, &d.Blocks); err != nil {
		return nil, err
	}
	d.CreatedAt, d.UpdatedAt, d.DecidedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}, tp(decided)
	return &d, nil
}

func (p *PG) ListDecisions(org string) ([]model.Decision, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+decisionCols+` from omixor_decisions where org = $1 order by number`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Decision{}
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (p *PG) GetDecision(org, id string) (*model.Decision, error) {
	c, cancel := ctx()
	defer cancel()
	return scanDecision(p.pool.QueryRow(c, `select `+decisionCols+` from omixor_decisions where org = $1 and id = $2`, org, id))
}

func (p *PG) CreateDecision(org string, d model.Decision) (model.Decision, error) {
	c, cancel := ctx()
	defer cancel()
	d = d.Clone()
	opts, err := json.Marshal(d.Options)
	if err != nil {
		return d, err
	}
	blocks, err := json.Marshal(d.Blocks)
	if err != nil {
		return d, err
	}
	tx, err := p.pool.Begin(c)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(c)
	num, err := seqNext(c, tx, org, "decision", 0) // 號碼由序號表單句原子取得
	if err != nil {
		return d, err
	}
	d.ID = fmt.Sprintf("%s%d", model.DecisionPrefix, num)
	var decided any
	if d.DecidedAt != nil {
		decided = secs(d.DecidedAt.Time)
	}
	if _, err := tx.Exec(c, `insert into omixor_decisions (org, id, number, kind, title, body, options, status, answer, answer_note, evidence,
  source_issue, blocks, created_by, decided_by, created_at, decided_at, updated_at)
  values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,nullif($12,''),$13,$14,$15,$16,$17,$18)`,
		org, d.ID, num, d.Kind, d.Title, d.Body, opts, d.Status, d.Answer, d.AnswerNote, d.Evidence, d.SourceIssue, blocks,
		NullID(d.CreatedBy), NullID(d.DecidedBy), secs(d.CreatedAt.Time), decided, secs(d.UpdatedAt.Time)); err != nil {
		return d, fkErr(err)
	}
	return d, tx.Commit(c)
}

func (p *PG) UpdateDecision(org string, d model.Decision, fromStatus string) error {
	c, cancel := ctx()
	defer cancel()
	d = d.Clone()
	opts, err := json.Marshal(d.Options)
	if err != nil {
		return err
	}
	blocks, err := json.Marshal(d.Blocks)
	if err != nil {
		return err
	}
	var decided any
	if d.DecidedAt != nil {
		decided = secs(d.DecidedAt.Time)
	}
	// 編號、kind、建立者、建立時間不改;條件式:目前的狀態要是 fromStatus(同時處理只有一個成功)
	tag, err := p.pool.Exec(c, `update omixor_decisions set title = $3, body = $4, options = $5, status = $6, answer = $7, answer_note = $8,
  evidence = $9, source_issue = nullif($10, ''), blocks = $11, decided_by = $12, decided_at = $13, updated_at = $14
  where org = $1 and id = $2 and status = $15`,
		org, d.ID, d.Title, d.Body, opts, d.Status, d.Answer, d.AnswerNote, d.Evidence, d.SourceIssue, blocks,
		NullID(d.DecidedBy), decided, secs(d.UpdatedAt.Time), fromStatus)
	if err != nil {
		return fkErr(err)
	}
	if tag.RowsAffected() == 0 {
		var n int
		if err := p.pool.QueryRow(c, `select count(*) from omixor_decisions where org = $1 and id = $2`, org, d.ID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return ErrStale
	}
	return nil
}
