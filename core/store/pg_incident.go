package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 事件(PG;表 omixor_incidents 的定義在 pg.go 的 pgSchema) ----
// 整筆事件存在 data(jsonb);number、status、started_at 另外存一份方便排序與日後查詢。

func scanIncident(row pgx.Row) (*model.Incident, error) {
	var b []byte
	if err := row.Scan(&b); err != nil {
		return nil, notFound(err)
	}
	var i model.Incident
	if err := json.Unmarshal(b, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

func incidentData(i model.Incident) ([]byte, error) {
	i = i.Clone()
	return json.Marshal(i)
}

func insertIncident(c context.Context, tx pgx.Tx, org string, i model.Incident, pos int64) error {
	b, err := incidentData(i)
	if err != nil {
		return err
	}
	_, err = tx.Exec(c, `insert into omixor_incidents (org, id, number, pos, status, started_at, data) values ($1, $2, $3, $4, $5, $6, $7)`,
		org, i.ID, i.Number(), pos, i.Status, secs(i.StartedAt.Time), b)
	return err
}

func (p *PG) ListIncidents(org string) ([]model.Incident, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select data from omixor_incidents where org = $1 order by pos desc`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Incident{}
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

func (p *PG) GetIncident(org, id string) (*model.Incident, error) {
	c, cancel := ctx()
	defer cancel()
	return scanIncident(p.pool.QueryRow(c, `select data from omixor_incidents where org = $1 and id = $2`, org, id))
}

func (p *PG) CreateIncident(org string, i model.Incident) (model.Incident, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return i, err
	}
	defer tx.Rollback(c)
	// 同一個組織同時只建一筆,號碼才不會撞
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-incidents:' || $1))`, org); err != nil {
		return i, err
	}
	var num int
	var pos int64
	if err := tx.QueryRow(c, `select coalesce(max(number),0), coalesce(max(pos),0) from omixor_incidents where org = $1`, org).Scan(&num, &pos); err != nil {
		return i, err
	}
	i.ID = fmt.Sprintf("%s%d", model.IncidentPrefix, max(num, model.IncidentNumberBase)+1)
	if err := insertIncident(c, tx, org, i, pos+1); err != nil {
		return i, err
	}
	return i, tx.Commit(c)
}

func (p *PG) UpdateIncident(org string, i model.Incident) error {
	c, cancel := ctx()
	defer cancel()
	b, err := incidentData(i)
	if err != nil {
		return err
	}
	tag, err := p.pool.Exec(c, `update omixor_incidents set status = $3, started_at = $4, data = $5 where org = $1 and id = $2`,
		org, i.ID, i.Status, secs(i.StartedAt.Time), b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) ImportIncidents(org string, list []model.Incident) (int, error) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-incidents:' || $1))`, org); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from omixor_incidents where org = $1`, org).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 %d 筆事件", org, n)
	}
	for k, i := range list {
		if err := insertIncident(c, tx, org, i, int64(len(list)-k)); err != nil {
			return 0, fmt.Errorf("%s:%w", i.ID, err)
		}
	}
	return len(list), tx.Commit(c)
}

var _ Store = (*PG)(nil)
