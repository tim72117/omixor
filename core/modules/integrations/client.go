package integrations

import (
	"net/url"

	"omixor-core/client"
)

// 組織的 GitHub、GitLab 設定的 HTTP client(原本是 core/client 的方法;見 module.go 的路由)

func integPath(org, kind string) string {
	return "/api/orgs/" + url.PathEscape(org) + "/integrations/" + kind
}

// GetGitHubSettings 組織的 GitHub 設定(成員;沒存過 Saved = false)
func GetGitHubSettings(c *client.Client, org string) (*GitHubSettings, error) {
	var x GitHubSettings
	return &x, c.Do("GET", integPath(org, kindGitHub), nil, &x)
}

// SaveGitHubSettings 修改(組織管理員)
func SaveGitHubSettings(c *client.Client, org string, in GitHubSettingsInput) (*GitHubSettings, error) {
	var x GitHubSettings
	return &x, c.Do("POST", integPath(org, kindGitHub), in, &x)
}

// ImportGitHubSettings 第一次匯入(組織管理員;核心存過就不動)
func ImportGitHubSettings(c *client.Client, org string, in GitHubImport) (*GitHubImportResult, error) {
	var x GitHubImportResult
	return &x, c.Do("POST", integPath(org, kindGitHub)+"/import", in, &x)
}

// GetGitLabSettings 組織的 GitLab 設定(成員;沒存過 Saved = false)
func GetGitLabSettings(c *client.Client, org string) (*GitLabSettings, error) {
	var x GitLabSettings
	return &x, c.Do("GET", integPath(org, kindGitLab), nil, &x)
}

// SaveGitLabSettings 修改(組織管理員)
func SaveGitLabSettings(c *client.Client, org string, in GitLabSettingsInput) (*GitLabSettings, error) {
	var x GitLabSettings
	return &x, c.Do("POST", integPath(org, kindGitLab), in, &x)
}

// ImportGitLabSettings 第一次匯入(組織管理員;核心存過就不動)
func ImportGitLabSettings(c *client.Client, org string, in GitLabImport) (*GitLabImportResult, error) {
	var x GitLabImportResult
	return &x, c.Do("POST", integPath(org, kindGitLab)+"/import", in, &x)
}
