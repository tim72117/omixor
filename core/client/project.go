package client

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 專案(路由見 core/api)

func (c *Client) projects(org string) string { return "/api/orgs/" + esc(org) + "/projects" }

func (c *Client) ListProjects(org string) ([]model.Project, error) {
	var l []model.Project
	return l, c.Do("GET", c.projects(org), nil, &l)
}

func (c *Client) CreateProject(org string, in service.ProjectInput) (*model.Project, error) {
	var x model.Project
	return &x, c.Do("POST", c.projects(org), in, &x)
}

func (c *Client) UpdateProject(org, id string, p service.ProjectPatch) (*model.Project, error) {
	var x model.Project
	return &x, c.Do("PUT", c.projects(org)+"/"+esc(id), p, &x)
}

func (c *Client) DeleteProject(org, id string) error {
	return c.Do("POST", c.projects(org)+"/"+esc(id)+"/delete", map[string]any{}, nil)
}
