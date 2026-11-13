package store

import "omixor-core/model"

// ---- 組織的外部 MCP 登記(PG):和 GitHub、GitLab 設定共用表 omixor_org_integrations(kind = mcp,定義在 pg.go 的 pgSchema;
// 刪組織時一併刪除)。整份登記存在 data(jsonb);沒有任何密鑰(見 model/mcp.go)。

func (p *PG) GetMCPConfig(org string) (*model.OrgMCPConfig, error) {
	var c model.OrgMCPConfig
	at, by, err := p.getIntegration(org, "mcp", &c)
	if err != nil {
		return nil, err
	}
	c = c.Clone() // 補上空的 slice / map
	c.UpdatedAt, c.UpdatedBy = &model.Time{Time: at}, by
	return &c, nil
}

func (p *PG) SaveMCPConfig(org string, c model.OrgMCPConfig) error {
	d := c.Clone()
	d.UpdatedAt, d.UpdatedBy = nil, ""
	return p.saveIntegration(org, "mcp", d, c.UpdatedAt, c.UpdatedBy)
}
