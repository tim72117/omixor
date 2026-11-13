package client

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 組織的 GitHub、GitLab 設定(見 core/api/integration.go;只有非機密設定)

func (c *Client) integ(org, kind string) string {
	return "/api/orgs/" + esc(org) + "/integrations/" + kind
}

// GetGitHubSettings 組織的 GitHub 設定(成員;沒存過 Saved = false)
func (c *Client) GetGitHubSettings(org string) (*model.GitHubSettings, error) {
	var x model.GitHubSettings
	return &x, c.Do("GET", c.integ(org, "github"), nil, &x)
}

// SaveGitHubSettings 修改(組織管理員)
func (c *Client) SaveGitHubSettings(org string, in service.GitHubSettingsInput) (*model.GitHubSettings, error) {
	var x model.GitHubSettings
	return &x, c.Do("POST", c.integ(org, "github"), in, &x)
}

// ImportGitHubSettings 第一次匯入(組織管理員;核心存過就不動)
func (c *Client) ImportGitHubSettings(org string, in service.GitHubImport) (*service.GitHubImportResult, error) {
	var x service.GitHubImportResult
	return &x, c.Do("POST", c.integ(org, "github")+"/import", in, &x)
}

// GetGitLabSettings 組織的 GitLab 設定(成員;沒存過 Saved = false)
func (c *Client) GetGitLabSettings(org string) (*model.GitLabSettings, error) {
	var x model.GitLabSettings
	return &x, c.Do("GET", c.integ(org, "gitlab"), nil, &x)
}

// SaveGitLabSettings 修改(組織管理員)
func (c *Client) SaveGitLabSettings(org string, in service.GitLabSettingsInput) (*model.GitLabSettings, error) {
	var x model.GitLabSettings
	return &x, c.Do("POST", c.integ(org, "gitlab"), in, &x)
}

// ImportGitLabSettings 第一次匯入(組織管理員;核心存過就不動)
func (c *Client) ImportGitLabSettings(org string, in service.GitLabImport) (*service.GitLabImportResult, error) {
	var x service.GitLabImportResult
	return &x, c.Do("POST", c.integ(org, "gitlab")+"/import", in, &x)
}
