package store

import (
	"time"

	"omixor-core/model"
)

// ---- 專案成員(表 omixor_project_members 的定義在 pg_schema.go 的 pgSchema)----
// 成員以 actor id 存(使用者或角色)。受限專案(omixor_projects.restricted)只有成員與管理員看得到;可見性的判斷在 core/service(project_access.go),
// 這裡只存取資料:專案刪除、actor 刪除時成員跟著刪(外鍵 cascade)。

func (p *PG) ListProjectMembers(org, project string) ([]model.ProjectMember, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select m.project, m.actor_id, m.role, m.created_at from omixor_project_members m
		join omixor_projects j on j.org = m.org and j.id = m.project
		where m.org = $1 and ($2 = '' or m.project = $2) order by j.number, m.created_at, m.actor_id`, org, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProjectMember{}
	for rows.Next() {
		var x model.ProjectMember
		var at time.Time
		if err := rows.Scan(&x.Project, &x.ActorID, &x.Role, &at); err != nil {
			return nil, err
		}
		x.CreatedAt = model.Time{Time: at.UTC()}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ProjectMemberships 這個 actor 在組織裡每個專案的角色(專案 id → admin/member);不是任何專案的成員回空 map
func (p *PG) ProjectMemberships(org, actorID string) (map[string]string, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select project, role from omixor_project_members where org = $1 and actor_id = $2`, org, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var pr, role string
		if err := rows.Scan(&pr, &role); err != nil {
			return nil, err
		}
		out[pr] = role
	}
	return out, rows.Err()
}

// SetProjectMember 加入成員或改角色(upsert)。回傳有沒有變動(新加入、角色改變);專案不存在或 actor 不存在回 ErrBadRef
func (p *PG) SetProjectMember(org, project, actorID, role string, at time.Time) (bool, error) {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `insert into omixor_project_members (org, project, actor_id, role, created_at) values ($1,$2,$3,$4,$5)
		on conflict (org, project, actor_id) do update set role = excluded.role where omixor_project_members.role <> excluded.role`,
		org, project, actorID, role, secs(at))
	if err != nil {
		return false, fkErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// RemoveProjectMember 移除成員;原本不是成員回 removed=false
func (p *PG) RemoveProjectMember(org, project, actorID string) (bool, error) {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_project_members where org = $1 and project = $2 and actor_id = $3`, org, project, actorID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RemoveProjectMembershipsOf 把 actor 從組織所有專案的成員拿掉(使用者被移出組織、角色被刪時);回傳拿掉的專案 id
func (p *PG) RemoveProjectMembershipsOf(org, actorID string) ([]string, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `delete from omixor_project_members where org = $1 and actor_id = $2 returning project`, org, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var pr string
		if err := rows.Scan(&pr); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}
