// pg_chain.go 組織鏈的 PG 實作

package store

import (
	"time"

	"omixor-core/model"
)

// ---- 組織鏈 ----

func (p *PG) GetOrgChain(org string) (*model.OrgChain, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.roTx(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	var at time.Time
	if err := tx.QueryRow(c, `select updated_at from omixor_org_chains where org = $1`, org).Scan(&at); err != nil {
		return nil, notFound(err)
	}
	out := model.OrgChain{Edges: []model.ChainEdge{}}
	rows, err := tx.Query(c, `select from_actor, to_actor, label from omixor_org_chain_edges where org = $1 order by pos`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e model.ChainEdge
		if err := rows.Scan(&e.From, &e.To, &e.Label); err != nil {
			return nil, err
		}
		out.Edges = append(out.Edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.UpdatedAt = &model.Time{Time: secs(at)}
	return &out, nil
}

func (p *PG) SaveOrgChain(org string, ch model.OrgChain) error {
	c, cancel := ctx()
	defer cancel()
	edges := ch.Edges
	if edges == nil {
		edges = []model.ChainEdge{}
	}
	at := time.Now()
	if ch.UpdatedAt != nil {
		at = ch.UpdatedAt.Time
	}
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	// 不再寫舊的 edges 欄位;split_at 設成 now()
	if _, err := tx.Exec(c, `insert into omixor_org_chains (org, updated_at, split_at) values ($1, $2, now())
		on conflict (org) do update set updated_at = $2, split_at = now()`, org, secs(at)); err != nil {
		return fkErr(err)
	}
	if err := replaceChainEdges(c, tx, org, edges); err != nil {
		return err
	}
	return tx.Commit(c)
}
