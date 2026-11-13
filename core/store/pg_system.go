package store

import (
	"encoding/json"
	"time"

	"omixor-core/model"
)

// ---- 組織的系統設定(功能開關;PG;表 omixor_org_system 的定義在 pg.go 的 pgSchema) ----
// 一個組織一列:有改過的開關存在 features(jsonb,key → bool);updated_at、updated_by 另外存。

func (p *PG) GetSystemSettings(org string) (*model.OrgSystemSettings, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	var at time.Time
	var by string
	err := p.pool.QueryRow(c, `select features, updated_at, updated_by from omixor_org_system where org = $1`, org).Scan(&b, &at, &by)
	if err != nil {
		return nil, notFound(err)
	}
	s := model.OrgSystemSettings{Features: map[string]bool{}}
	if err := json.Unmarshal(b, &s.Features); err != nil {
		return nil, err
	}
	if s.Features == nil {
		s.Features = map[string]bool{}
	}
	s.Saved, s.UpdatedAt, s.UpdatedBy = true, &model.Time{Time: secs(at)}, by
	return &s, nil
}

func (p *PG) SaveSystemSettings(org string, s model.OrgSystemSettings) error {
	c, cancel := ctx()
	defer cancel()
	at := time.Now()
	if s.UpdatedAt != nil {
		at = s.UpdatedAt.Time
	}
	f := s.Features
	if f == nil {
		f = map[string]bool{}
	}
	_, err := p.pool.Exec(c, `insert into omixor_org_system (org, features, updated_at, updated_by) values ($1, $2, $3, $4)
		on conflict (org) do update set features = $2, updated_at = $3, updated_by = $4`, org, jb(f), secs(at), s.UpdatedBy)
	return err
}
