package server

import (
	"omixor-core/oauth"
	"omixor-core/service"
)

// configureGitLab 啟動時從環境變數開 GitLab 登入(要在 configureLDAP 之後);規則與變數見 oauth.ConfigureGitLab(console 共用)
func configureGitLab(svc *service.Service, getenv func(string) string, public string) error {
	return oauth.ConfigureGitLab(svc, getenv, public)
}
