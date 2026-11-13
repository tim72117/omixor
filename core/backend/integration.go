package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 組織的 GitHub、GitLab 設定:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) GetGitHubSettings(string) (*model.GitHubSettings, error) {
	return nil, errUnsupported
}
func (Unsupported) SaveGitHubSettings(string, service.GitHubSettingsInput) (*model.GitHubSettings, error) {
	return nil, errUnsupported
}
func (Unsupported) ImportGitHubSettings(string, service.GitHubImport) (*service.GitHubImportResult, error) {
	return nil, errUnsupported
}
func (Unsupported) GetGitLabSettings(string) (*model.GitLabSettings, error) {
	return nil, errUnsupported
}
func (Unsupported) SaveGitLabSettings(string, service.GitLabSettingsInput) (*model.GitLabSettings, error) {
	return nil, errUnsupported
}
func (Unsupported) ImportGitLabSettings(string, service.GitLabImport) (*service.GitLabImportResult, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) GetGitHubSettings(org string) (*model.GitHubSettings, error) {
	l.ensureOrg(org)
	return l.Svc.GetGitHubSettings(l.P, org)
}

func (l *Local) SaveGitHubSettings(org string, in service.GitHubSettingsInput) (*model.GitHubSettings, error) {
	l.ensureOrg(org)
	return l.Svc.SaveGitHubSettings(l.P, org, in)
}

func (l *Local) ImportGitHubSettings(org string, in service.GitHubImport) (*service.GitHubImportResult, error) {
	l.ensureOrg(org)
	return l.Svc.ImportGitHubSettings(l.P, org, in)
}

func (l *Local) GetGitLabSettings(org string) (*model.GitLabSettings, error) {
	l.ensureOrg(org)
	return l.Svc.GetGitLabSettings(l.P, org)
}

func (l *Local) SaveGitLabSettings(org string, in service.GitLabSettingsInput) (*model.GitLabSettings, error) {
	l.ensureOrg(org)
	return l.Svc.SaveGitLabSettings(l.P, org, in)
}

func (l *Local) ImportGitLabSettings(org string, in service.GitLabImport) (*service.GitLabImportResult, error) {
	l.ensureOrg(org)
	return l.Svc.ImportGitLabSettings(l.P, org, in)
}

// ---- Remote ----

func (r *Remote) GetGitHubSettings(org string) (*model.GitHubSettings, error) {
	x, err := r.Client().GetGitHubSettings(r.org(org))
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) SaveGitHubSettings(org string, in service.GitHubSettingsInput) (*model.GitHubSettings, error) {
	x, err := r.Client().SaveGitHubSettings(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ImportGitHubSettings(org string, in service.GitHubImport) (*service.GitHubImportResult, error) {
	x, err := r.Client().ImportGitHubSettings(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) GetGitLabSettings(org string) (*model.GitLabSettings, error) {
	x, err := r.Client().GetGitLabSettings(r.org(org))
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) SaveGitLabSettings(org string, in service.GitLabSettingsInput) (*model.GitLabSettings, error) {
	x, err := r.Client().SaveGitLabSettings(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ImportGitLabSettings(org string, in service.GitLabImport) (*service.GitLabImportResult, error) {
	x, err := r.Client().ImportGitLabSettings(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}
