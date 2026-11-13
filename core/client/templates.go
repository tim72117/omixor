package client

import "omixor-core/service"

func (c *Client) templatesPath(org string) string { return "/api/orgs/" + esc(org) + "/templates" }

// ListOrgTemplates 可用的組織樣板(成員;功能開關關閉的不列)
func (c *Client) ListOrgTemplates(org string) ([]service.TemplateSummary, error) {
	var x []service.TemplateSummary
	return x, c.Do("GET", c.templatesPath(org), nil, &x)
}

// PreviewOrgTemplate 預覽套用會建立哪些、略過哪些(成員)
func (c *Client) PreviewOrgTemplate(org, id string, scenarios bool) (*service.TemplatePlan, error) {
	var x service.TemplatePlan
	q := ""
	if scenarios {
		q = "?scenarios=1"
	}
	return &x, c.Do("GET", c.templatesPath(org)+"/"+esc(id)+q, nil, &x)
}

// ApplyOrgTemplate 套用樣板(組織管理員;已存在的略過)
func (c *Client) ApplyOrgTemplate(org, id string, in service.TemplateApplyInput) (*service.TemplatePlan, error) {
	var x service.TemplatePlan
	return &x, c.Do("POST", c.templatesPath(org)+"/"+esc(id)+"/apply", in, &x)
}
