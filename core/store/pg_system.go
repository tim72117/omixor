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

// ---- 系統層的功能設定(整台伺服器一列;表 omixor_server_features,主鍵 id 固定是 'server') ----
// 不放進 omixor_org_system(那張表以組織為主鍵,刪組織時一併刪;系統層不屬於任何組織),另外開一張只有一列的表。

func (p *PG) GetServerFeatures() (*model.ServerFeatures, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	var at time.Time
	var by string
	err := p.pool.QueryRow(c, `select features, updated_at, updated_by from omixor_server_features where id = 'server'`).Scan(&b, &at, &by)
	if err != nil {
		return nil, notFound(err)
	}
	s := model.ServerFeatures{Features: map[string]bool{}}
	if err := json.Unmarshal(b, &s.Features); err != nil {
		return nil, err
	}
	if s.Features == nil {
		s.Features = map[string]bool{}
	}
	s.Saved, s.UpdatedAt, s.UpdatedBy = true, &model.Time{Time: secs(at)}, by
	return &s, nil
}

func (p *PG) SaveServerFeatures(s model.ServerFeatures) error {
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
	_, err := p.pool.Exec(c, `insert into omixor_server_features (id, features, updated_at, updated_by) values ('server', $1, $2, $3)
		on conflict (id) do update set features = $1, updated_at = $2, updated_by = $3`, jb(f), secs(at), s.UpdatedBy)
	return err
}
