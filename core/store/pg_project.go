package store

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 專案(PG;表 omixor_projects 的定義在 pg.go 的 pgSchema;專案的文件櫃結構在 omixor_cabinet_layouts 的 (org, project)) ----

const projectCols = `id, name, description, created_at, updated_at`

func scanProject(row pgx.Row) (*model.Project, error) {
	var x model.Project
	var created, updated time.Time
	if err := row.Scan(&x.ID, &x.Name, &x.Description, &created, &updated); err != nil {
		return nil, notFound(err)
	}
	x.CreatedAt, x.UpdatedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}
	return &x, nil
}

func (p *PG) ListProjects(org string) ([]model.Project, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select `+projectCols+` from omixor_projects where org = $1 order by number, id`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		x, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

func (p *PG) GetProject(org, id string) (*model.Project, error) {
	c, cancel := ctx()
	defer cancel()
	return scanProject(p.pool.QueryRow(c, `select `+projectCols+` from omixor_projects where org = $1 and id = $2`, org, id))
}

func (p *PG) CreateProject(org string, x model.Project) (model.Project, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return x, err
	}
	defer tx.Rollback(c)
	// 同一個組織同時只建一個,號碼才不會撞
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-projects:' || $1))`, org); err != nil {
		return x, err
	}
	// 號碼不重複使用:取「最後發出去的號碼」和現有最大號碼的較大者再加一,並記下這次發出去的號碼
	var num int
	if err := tx.QueryRow(c, `select greatest(
		coalesce((select last from omixor_project_seq where org = $1), 0),
		coalesce((select max(number) from omixor_projects where org = $1), 0))`, org).Scan(&num); err != nil {
		return x, err
	}
	if _, err := tx.Exec(c, `insert into omixor_project_seq (org, last) values ($1, $2)
		on conflict (org) do update set last = excluded.last`, org, num+1); err != nil {
		return x, err
	}
	x.ID = fmt.Sprintf("%s%d", model.ProjectPrefix, num+1)
	if _, err := tx.Exec(c, `insert into omixor_projects (org, id, number, name, description, created_at, updated_at) values ($1,$2,$3,$4,$5,$6,$7)`,
		org, x.ID, num+1, x.Name, x.Description, secs(x.CreatedAt.Time), secs(x.UpdatedAt.Time)); err != nil {
		return x, err
	}
	return x, tx.Commit(c)
}

func (p *PG) UpdateProject(org string, x model.Project) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_projects set name = $3, description = $4, updated_at = $5 where org = $1 and id = $2`,
		org, x.ID, x.Name, x.Description, secs(x.UpdatedAt.Time))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) DeleteProject(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	tag, err := tx.Exec(c, `delete from omixor_projects where org = $1 and id = $2`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// 未分專案(空字串)不是專案,不會走到這裡;只刪這個專案的結構
	if _, err := tx.Exec(c, `delete from omixor_cabinet_layouts where org = $1 and project = $2 and project <> ''`, org, id); err != nil {
		return err
	}
	return tx.Commit(c)
}
