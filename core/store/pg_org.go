// pg_org.go 組織與成員的 PG 實作

package store

import (
	"fmt"
	"time"

	"omixor-core/model"
)

// ---- 組織與成員 ----

func (p *PG) ListOrgs() ([]model.Org, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select id, name, symbol, color, note, created_at from omixor_orgs order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Org{}
	for rows.Next() {
		var o model.Org
		var created time.Time
		if err := rows.Scan(&o.ID, &o.Name, &o.Symbol, &o.Color, &o.Note, &created); err != nil {
			return nil, err
		}
		o.CreatedAt = model.Time{Time: created.UTC()}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *PG) GetOrg(id string) (*model.Org, error) {
	c, cancel := ctx()
	defer cancel()
	var o model.Org
	var created time.Time
	if err := p.pool.QueryRow(c, `select id, name, symbol, color, note, created_at from omixor_orgs where id = $1`, id).Scan(&o.ID, &o.Name, &o.Symbol, &o.Color, &o.Note, &created); err != nil {
		return nil, notFound(err)
	}
	o.CreatedAt = model.Time{Time: created.UTC()}
	return &o, nil
}

func (p *PG) CreateOrg(o model.Org) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `insert into omixor_orgs (id, name, symbol, color, note, created_at) values ($1,$2,$3,$4,$5,$6) on conflict (id) do nothing`, o.ID, o.Name, o.Symbol, o.Color, o.Note, o.CreatedAt.Time)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("組織 %s 已經存在", o.ID)
	}
	return err
}

func (p *PG) UpdateOrg(o model.Org) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_orgs set name = $2, symbol = $3, color = $4, note = $5 where id = $1`, o.ID, o.Name, o.Symbol, o.Color, o.Note)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *PG) DeleteOrg(id string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	for _, q := range []string{
		`delete from omixor_decisions where org = $1`,
		`delete from omixor_issue_items where org = $1`,
		`delete from ops_issues where org = $1`, // 留言跟著刪(on delete cascade)
		`delete from omixor_attachments where org = $1`,
		`delete from omixor_agent_credentials where org = $1`,
		`delete from omixor_agent_tasks where org = $1`,
		`update omixor_actors set disabled_at = coalesce(disabled_at, now()) where kind = 'agent' and org = $1`, // 角色 actor 退役、不刪列(同名組織重建是新的 actor)
		`delete from omixor_agents where org = $1`,
		`delete from omixor_docs where org = $1`,
		`delete from omixor_cabinet_layouts where org = $1`,
		`delete from omixor_cabinet_sections where org = $1`,
		`delete from omixor_cabinet_tabs where org = $1`,
		`delete from omixor_project_repos where org = $1`,
		`delete from omixor_projects where org = $1`,
		`delete from omixor_project_seq where org = $1`,
		`delete from omixor_seq where org = $1`,
		`delete from omixor_org_chains where org = $1`,
		`delete from omixor_channel_reads where org = $1`,
		`delete from omixor_channel_messages where org = $1`,
		`delete from omixor_channels where org = $1`,
		`delete from omixor_incidents where org = $1`,
		`delete from omixor_org_integrations where org = $1`,
		`delete from omixor_org_system where org = $1`,
		`delete from omixor_org_members where org = $1`,
	} {
		if _, err := tx.Exec(c, q, id); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(c, `delete from omixor_orgs where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(c)
}

func (p *PG) MemberRole(org, userID string) (string, error) {
	c, cancel := ctx()
	defer cancel()
	var r string
	err := p.pool.QueryRow(c, `select role from omixor_org_members where org = $1 and user_id = $2`, org, userID).Scan(&r)
	return r, notFound(err)
}

func (p *PG) ListMembers(org string) ([]model.Member, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select m.org, m.user_id, u.email, u.name, m.role, m.added_at, coalesce(u.actor_id, '')
from omixor_org_members m join omixor_users u on u.id = m.user_id where m.org = $1 order by u.email`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Member{}
	for rows.Next() {
		var m model.Member
		var added time.Time
		if err := rows.Scan(&m.Org, &m.UserID, &m.Email, &m.Name, &m.Role, &added, &m.ActorID); err != nil {
			return nil, err
		}
		m.AddedAt = model.Time{Time: added.UTC()}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *PG) SaveMember(m model.Member) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `insert into omixor_org_members (org, user_id, role, added_at) values ($1,$2,$3,$4)
on conflict (org, user_id) do update set role = excluded.role`, m.Org, m.UserID, m.Role, m.AddedAt.Time)
	return err
}

func (p *PG) RemoveMember(org, userID string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_org_members where org = $1 and user_id = $2`, org, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) OrgsOfUser(userID string) ([]model.Org, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select o.id, o.name, o.symbol, o.color, o.note, o.created_at, m.role
from omixor_org_members m join omixor_orgs o on o.id = m.org where m.user_id = $1 order by o.created_at, o.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Org{}
	for rows.Next() {
		var o model.Org
		var created time.Time
		if err := rows.Scan(&o.ID, &o.Name, &o.Symbol, &o.Color, &o.Note, &created, &o.MyRole); err != nil {
			return nil, err
		}
		o.CreatedAt = model.Time{Time: created.UTC()}
		out = append(out, o)
	}
	return out, rows.Err()
}
