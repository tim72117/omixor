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
	if p.hasUnsplit() { // 還沒展開的組織鏈:退回讀舊的 edges(jsonb)
		edges, ok, err := unsplitChainEdges(c, tx, org)
		if err != nil {
			return nil, err
		}
		if ok {
			w := p.newWho(c) // 舊 jsonb 的端點是舊字串:換成 actor id(寬鬆解析器,見 legacyActorIDs)
			kept := make([]model.ChainEdge, 0, len(edges))
			for _, e := range edges {
				f, err1 := w.col("omixor_org_chain_edges.from_actor", org, e.From)
				t, err2 := w.col("omixor_org_chain_edges.to_actor", org, e.To)
				fs, ok1 := f.(string)
				ts, ok2 := t.(string)
				if err1 == nil && err2 == nil && ok1 && ok2 && fs != "" && ts != "" {
					kept = append(kept, model.ChainEdge{From: fs, To: ts, Label: e.Label})
				}
			}
			out.Edges, out.UpdatedAt = kept, &model.Time{Time: secs(at)}
			return &out, nil
		}
	}
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
	if err := p.guardUnsplit(c, tx, splitChains, org); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	// 不再寫舊的 edges 欄位;split_at 設成 now() 讓啟動遷移不會拿舊 jsonb 覆蓋新表(見 pg_split_channels.go)
	if _, err := tx.Exec(c, `insert into omixor_org_chains (org, updated_at, split_at) values ($1, $2, now())
		on conflict (org) do update set updated_at = $2, split_at = now()`, org, secs(at)); err != nil {
		return fkErr(err)
	}
	if err := replaceChainEdges(c, tx, org, edges); err != nil {
		return err
	}
	return tx.Commit(c)
}
