package store

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 專案(PG;表 omixor_projects 的定義在 pg_schema.go 的 pgSchema;專案的文件櫃結構在 omixor_cabinet_layouts 的 (org, project)) ----

const projectCols = `id, name, key, description, restricted, created_at, updated_at`

func scanProject(row pgx.Row) (*model.Project, error) {
	var x model.Project
	var created, updated time.Time
	if err := row.Scan(&x.ID, &x.Name, &x.Key, &x.Description, &x.Restricted, &created, &updated); err != nil {
		return nil, notFound(err)
	}
	x.Repos = []string{}
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
	x.Repos = []string{}
	if _, err := tx.Exec(c, `insert into omixor_projects (org, id, number, name, key, description, restricted, created_at, updated_at) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		org, x.ID, num+1, x.Name, x.Key, x.Description, x.Restricted, secs(x.CreatedAt.Time), secs(x.UpdatedAt.Time)); err != nil {
		if isUniqueViolation(err) {
			return x, fmt.Errorf("%w: %s", ErrDuplicateKey, x.Key)
		}
		return x, err
	}
	return x, tx.Commit(c)
}

// UpdateProject 改名稱、代號、描述、受限旗標。代號變了而且專案裡已經有 Issue(含垃圾桶)時回 ErrInUse(第一版只能在還沒有 Issue 時改代號);
// 代號在組織內重複回 ErrDuplicateKey。和建立 Issue 用同一把鎖(lockIssues),檢查與修改之間不會有新的 Issue 進來
func (p *PG) UpdateProject(org string, x model.Project) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := lockIssues(c, tx, org); err != nil {
		return err
	}
	var cur string
	err = tx.QueryRow(c, `select key from omixor_projects where org = $1 and id = $2 for update`, org, x.ID).Scan(&cur)
	if err != nil {
		return notFound(err)
	}
	if cur != x.Key {
		var n int
		if err := tx.QueryRow(c, `select count(*) from ops_issues where org = $1 and project = $2`, org, x.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: 專案 %s 裡已經有 %d 張 Issue", ErrInUse, x.ID, n)
		}
	}
	if _, err := tx.Exec(c, `update omixor_projects set name = $3, key = $4, description = $5, restricted = $6, updated_at = $7 where org = $1 and id = $2`,
		org, x.ID, x.Name, x.Key, x.Description, x.Restricted, secs(x.UpdatedAt.Time)); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %s", ErrDuplicateKey, x.Key)
		}
		return err
	}
	return tx.Commit(c)
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
		return fkErrDelete(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// 未分專案(空字串)不是專案,不會走到這裡;只刪這個專案的結構
	if _, err := tx.Exec(c, `delete from omixor_cabinet_layouts where org = $1 and project = $2 and project <> ''`, org, id); err != nil {
		return err
	}
	for _, t := range []string{"omixor_cabinet_sections", "omixor_cabinet_tabs"} {
		if _, err := tx.Exec(c, `delete from `+t+` where org = $1 and project = $2 and project <> ''`, org, id); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}
