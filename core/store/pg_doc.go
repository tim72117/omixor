// pg_doc.go 文件櫃文件的 PG 實作

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 文件櫃 ----

// 標籤在 omixor_doc_tags(見 pg_split_docs_agents.go),用相關子查詢在同一個語句裡組成陣列(沒有 N+1,一致的快照);
// 還沒展開(split_at 為 NULL)的文件退回讀舊的 tags jsonb(去重、非字串照字面轉成文字)
// 作者直接存 actor id(沒有時是 NULL,用 NullID 掃描)
var docCols = `id, project, title, body, section, tab,
	case when omixor_docs.split_at is null
		then coalesce((select array_agg(x.v order by x.o) from (select e.v, min(e.o) as o from jsonb_array_elements_text(case when jsonb_typeof(omixor_docs.tags) = 'array' then omixor_docs.tags else '[]'::jsonb end) with ordinality as e(v, o) group by e.v) x), '{}'::text[])
		else coalesce((select array_agg(t.tag order by t.pos) from omixor_doc_tags t where t.org = omixor_docs.org and t.doc_id = omixor_docs.id), '{}'::text[]) end,
	author, created_at, updated_at`

func scanDoc(row pgx.Row) (*model.CabinetDoc, error) {
	var d model.CabinetDoc
	var tags []string
	var author NullID
	var created, updated time.Time
	if err := row.Scan(&d.ID, &d.Project, &d.Title, &d.Body, &d.Section, &d.Tab, &tags, &author, &created, &updated); err != nil {
		return nil, err
	}
	d.Author = author.String()
	d.CreatedAt, d.UpdatedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}
	d.Tags = tags
	if d.Tags == nil {
		d.Tags = []string{}
	}
	return &d, nil
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
	// 不再寫舊的 tags jsonb;split_at 標記「已展開到新表」
	_, err := tx.Exec(c, `insert into omixor_docs (org, id, number, pos, title, body, section, tab, author, created_at, updated_at, project, split_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, now())`,
		org, d.ID, d.Number(), pos, d.Title, d.Body, d.Section, d.Tab, IDOrNil(d.Author), secs(d.CreatedAt.Time), secs(d.UpdatedAt.Time), d.Project)
	if err != nil {
		return fkErr(err)
	}
	return replaceDocTags(c, tx, org, d.ID, d.Tags)
}

func (p *PG) CreateDoc(org string, d model.CabinetDoc) (model.CabinetDoc, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return d, err
	}
	defer tx.Rollback(c)
	// 號碼由序號表單句原子取得;序號列的行鎖到交易結束,同一個組織的建立排隊,所以讀 max(pos) 不會撞
	num, err := seqNext(c, tx, org, "doc", 0)
	if err != nil {
		return d, err
	}
	var pos int64
	if err := tx.QueryRow(c, `select coalesce(max(pos),0) from omixor_docs where org = $1`, org).Scan(&pos); err != nil {
		return d, err
	}
	d.ID = fmt.Sprintf("%s%d", model.DocPrefix, num)
	if err := insertDoc(c, tx, org, d, pos+1); err != nil {
		return d, err
	}
	return d, tx.Commit(c)
}

func (p *PG) UpdateDoc(org string, d model.CabinetDoc) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := p.guardUnsplit(c, tx, splitDocs, org, d.ID); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	tag, err := tx.Exec(c, `update omixor_docs set title = $3, body = $4, section = $5, tab = $6, author = $7, updated_at = $8, project = $9, split_at = now()
		where org = $1 and id = $2`, org, d.ID, d.Title, d.Body, d.Section, d.Tab, IDOrNil(d.Author), secs(d.UpdatedAt.Time), d.Project)
	if err != nil {
		return fkErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := replaceDocTags(c, tx, org, d.ID, d.Tags); err != nil {
		return err
	}
	return tx.Commit(c)
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
	if err := seqBump(c, tx, org, "doc", 0); err != nil { // 鎖住序號列,和 CreateDoc 互斥
		return 0, err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from omixor_docs where org = $1`, org).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 %d 份文件", org, n)
	}
	top := 0
	for k, d := range list {
		if err := insertDoc(c, tx, org, d, int64(len(list)-k)); err != nil {
			return 0, fmt.Errorf("%s:%w", d.ID, fkErr(err))
		}
		top = max(top, d.Number())
	}
	if err := seqBump(c, tx, org, "doc", top); err != nil {
		return 0, err
	}
	return len(list), tx.Commit(c)
}
