package client

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃(路由見 core/api)

func (c *Client) cabinet(org string) string { return "/api/orgs/" + esc(org) + "/cabinet" }
func (c *Client) docs(org string) string    { return "/api/orgs/" + esc(org) + "/docs" }

func (c *Client) GetCabinet(org string) (*model.Cabinet, error) {
	var x model.Cabinet
	return &x, c.Do("GET", c.cabinet(org), nil, &x)
}

func (c *Client) SaveCabinetLayout(org string, layout []model.CabinetSection) ([]model.CabinetSection, error) {
	var l []model.CabinetSection
	return l, c.Do("POST", c.cabinet(org)+"/layout", map[string]any{"layout": layout}, &l)
}

func (c *Client) ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	var r service.CabinetImportResult
	return &r, c.Do("POST", c.cabinet(org)+"/import", in, &r)
}

func (c *Client) GetDoc(org, id string) (*model.CabinetDoc, error) {
	var d model.CabinetDoc
	return &d, c.Do("GET", c.docs(org)+"/"+esc(id), nil, &d)
}

func (c *Client) CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error) {
	var d model.CabinetDoc
	return &d, c.Do("POST", c.docs(org), in, &d)
}

func (c *Client) UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error) {
	var d model.CabinetDoc
	return &d, c.Do("POST", c.docs(org)+"/"+esc(id), p, &d)
}

func (c *Client) DeleteDoc(org, id string) error {
	return c.Do("POST", c.docs(org)+"/"+esc(id)+"/delete", map[string]any{}, nil)
}

// ReadDoc 分段讀(大綱、段落、行號範圍、搜尋)
func (c *Client) ReadDoc(org, id string, in service.DocRead) (*service.DocView, error) {
	var v service.DocView
	return &v, c.Do("POST", c.docs(org)+"/"+esc(id)+"/read", in, &v)
}

// EditDoc 局部修改(字串取代、整段替換、段尾附加)
func (c *Client) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	var r service.DocEditResult
	return &r, c.Do("POST", c.docs(org)+"/"+esc(id)+"/edit", in, &r)
}
