package store

import (
	"encoding/json"
	"time"

	"omixor-core/model"
)

// ---- 組織的 GitHub、GitLab 設定(PG;表 omixor_org_integrations 的定義在 pg.go 的 pgSchema) ----
// 一列一份設定(kind = github、gitlab),設定本身存在 data(jsonb);updated_at、updated_by 另外存一份。

func (p *PG) getIntegration(org, kind string, out any) (time.Time, string, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	var at time.Time
	var by string
	err := p.pool.QueryRow(c, `select data, updated_at, updated_by from omixor_org_integrations where org = $1 and kind = $2`, org, kind).
		Scan(&b, &at, &by)
	if err != nil {
		return at, by, notFound(err)
	}
	return secs(at), by, json.Unmarshal(b, out)
}

func (p *PG) saveIntegration(org, kind string, data any, updatedAt *model.Time, updatedBy string) error {
	c, cancel := ctx()
	defer cancel()
	at := time.Now()
	if updatedAt != nil {
		at = updatedAt.Time
	}
	_, err := p.pool.Exec(c, `insert into omixor_org_integrations (org, kind, data, updated_at, updated_by) values ($1, $2, $3, $4, $5)
		on conflict (org, kind) do update set data = $3, updated_at = $4, updated_by = $5`, org, kind, jb(data), secs(at), updatedBy)
	return err
}

func (p *PG) GetGitHubSettings(org string) (*model.GitHubSettings, error) {
	var s model.GitHubSettings
	at, by, err := p.getIntegration(org, "github", &s)
	if err != nil {
		return nil, err
	}
	if s.Repos == nil {
		s.Repos = []string{}
	}
	s.Saved, s.UpdatedAt, s.UpdatedBy = true, &model.Time{Time: at}, by
	return &s, nil
}

func (p *PG) SaveGitHubSettings(org string, s model.GitHubSettings) error {
	d := s.Clone()
	d.Saved, d.UpdatedAt, d.UpdatedBy = false, nil, ""
	return p.saveIntegration(org, "github", d, s.UpdatedAt, s.UpdatedBy)
}

func (p *PG) GetGitLabSettings(org string) (*model.GitLabSettings, error) {
	var s model.GitLabSettings
	at, by, err := p.getIntegration(org, "gitlab", &s)
	if err != nil {
		return nil, err
	}
	if s.Projects == nil {
		s.Projects = []string{}
	}
	s.Saved, s.UpdatedAt, s.UpdatedBy = true, &model.Time{Time: at}, by
	return &s, nil
}

func (p *PG) SaveGitLabSettings(org string, s model.GitLabSettings) error {
	d := s.Clone()
	d.Saved, d.UpdatedAt, d.UpdatedBy = false, nil, ""
	return p.saveIntegration(org, "gitlab", d, s.UpdatedAt, s.UpdatedBy)
}
