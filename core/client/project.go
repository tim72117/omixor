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

// GetProject 一個專案(看不到的受限專案 404)
func (c *Client) GetProject(org, id string) (*model.Project, error) {
	var x model.Project
	return &x, c.Do("GET", c.projects(org)+"/"+esc(id), nil, &x)
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

func (c *Client) LinkProjectRepo(org, project, repo string) (*model.Project, error) {
	var x model.Project
	return &x, c.Do("POST", c.projects(org)+"/"+esc(project)+"/repos", map[string]any{"repo": repo}, &x)
}

func (c *Client) UnlinkProjectRepo(org, project, repo string) (*model.Project, error) {
	var x model.Project
	return &x, c.Do("POST", c.projects(org)+"/"+esc(project)+"/repos/delete", map[string]any{"repo": repo}, &x)
}

// ListProjectRepos 專案連結的 repo;project 空 = 整個組織的連結
func (c *Client) ListProjectRepos(org, project string) ([]model.ProjectRepo, error) {
	path := c.projects(org) + "/repos"
	if project != "" {
		path = c.projects(org) + "/" + esc(project) + "/repos"
	}
	var l []model.ProjectRepo
	return l, c.Do("GET", path, nil, &l)
}

// ListProjectMembers 專案成員(看得到專案的組織成員;受限專案要是專案成員或組織管理員,看不到 404)
func (c *Client) ListProjectMembers(org, project string) ([]model.ProjectMember, error) {
	var l []model.ProjectMember
	return l, c.Do("GET", c.projects(org)+"/"+esc(project)+"/members", nil, &l)
}

// SetProjectMember 加入成員或改角色(組織管理員或專案管理員)
func (c *Client) SetProjectMember(org, project string, in service.ProjectMemberInput) (*model.ProjectMember, error) {
	var x model.ProjectMember
	return &x, c.Do("POST", c.projects(org)+"/"+esc(project)+"/members", in, &x)
}

// RemoveProjectMember 移除成員(組織管理員或專案管理員)
func (c *Client) RemoveProjectMember(org, project, actor string) error {
	return c.Do("POST", c.projects(org)+"/"+esc(project)+"/members/remove", map[string]any{"actor": actor}, nil)
}
