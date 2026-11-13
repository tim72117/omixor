package client

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 事件(見 core/api/incident.go;用角色憑證當 token 也可以)

func (c *Client) incidents(org string) string { return "/api/orgs/" + esc(org) + "/incidents" }

// ListIncidents 事件(新的在前)
func (c *Client) ListIncidents(org string) ([]model.Incident, error) {
	out := []model.Incident{}
	return out, c.Do("GET", c.incidents(org), nil, &out)
}

func (c *Client) GetIncident(org, id string) (*model.Incident, error) {
	var x model.Incident
	return &x, c.Do("GET", c.incidents(org)+"/"+esc(id), nil, &x)
}

// DeclareIncident 宣告事件
func (c *Client) DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error) {
	var x model.Incident
	return &x, c.Do("POST", c.incidents(org), in, &x)
}

func (c *Client) UpdateIncident(org, id string, in service.IncidentPatch) (*model.Incident, error) {
	var x model.Incident
	return &x, c.Do("POST", c.incidents(org)+"/"+esc(id), in, &x)
}

// ImportIncidents 第一次搬資料用(組織管理員;已經有事件就拒絕)
func (c *Client) ImportIncidents(org string, list []model.Incident) (*model.ImportResult, error) {
	var x model.ImportResult
	return &x, c.Do("POST", c.incidents(org)+"/import", service.IncidentImport{Incidents: list}, &x)
}
