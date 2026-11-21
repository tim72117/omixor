package store

import (
	"time"

	"omixor-core/model"
)

// ---- 專案與 repo 的連結(表 omixor_project_repos 的定義在 pg.go 的 pgSchema)----
// 多對多:一個 repo 可以連多個專案。只用於預設值與篩選,不是權限來源。repo 以 model.RepoKey(不分大小寫、去 github:/gitlab: 前綴)比對。

func (p *PG) ListProjectRepos(org string) ([]model.ProjectRepo, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select r.project, r.repo, r.created_at from omixor_project_repos r
		join omixor_projects j on j.org = r.org and j.id = r.project
		where r.org = $1 order by j.number, r.created_at, r.repo_key`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProjectRepo{}
	for rows.Next() {
		var x model.ProjectRepo
		var at time.Time
		if err := rows.Scan(&x.Project, &x.Repo, &at); err != nil {
			return nil, err
		}
		x.CreatedAt = model.Time{Time: at.UTC()}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (p *PG) LinkProjectRepo(org, project, repo string, at time.Time) (bool, error) {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `insert into omixor_project_repos (org, project, repo_key, repo, created_at) values ($1,$2,$3,$4,$5)
		on conflict (org, project, repo_key) do nothing`, org, project, model.RepoKey(repo), repo, secs(at))
	if err != nil {
		return false, fkErr(err) // 專案不存在 → ErrBadRef
	}
	return tag.RowsAffected() > 0, nil
}

func (p *PG) UnlinkProjectRepo(org, project, repo string) (bool, error) {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_project_repos where org = $1 and project = $2 and repo_key = $3`, org, project, model.RepoKey(repo))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
