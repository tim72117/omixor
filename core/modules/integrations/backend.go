package integrations

import (
	"omixor-core/backend"
)

// 給用戶端(omixor-web)用的共同介面:原本是 core/backend.Backend 的一部分,搬成模組自己的介面,
// 核心的 Backend 不再依賴 GitHub / GitLab。
//
//	Of(be) 依核心的 backend 取得對應的實作:*backend.Local → 同一個程式裡的核心;*backend.Remote → 連線上的 server;
//	       自己實作了 Backend 的(例如測試替身)照用;其他一律回「這個模式不支援」
//
// 權限同 service.go:成員讀、組織管理員改與匯入;只有非機密設定。

// Backend 組織的 GitHub、GitLab 設定
type Backend interface {
	GetGitHubSettings(org string) (*GitHubSettings, error)
	SaveGitHubSettings(org string, in GitHubSettingsInput) (*GitHubSettings, error)
	// ImportGitHubSettings 第一次匯入裝置的本機設定(核心存過就不動,Imported = false)
	ImportGitHubSettings(org string, in GitHubImport) (*GitHubImportResult, error)
	GetGitLabSettings(org string) (*GitLabSettings, error)
	SaveGitLabSettings(org string, in GitLabSettingsInput) (*GitLabSettings, error)
	ImportGitLabSettings(org string, in GitLabImport) (*GitLabImportResult, error)
}

// Of 依核心的 backend 取得這個模組的實作
func Of(be backend.Backend) Backend {
	switch b := be.(type) {
	case *backend.Local:
		return local{b}
	case *backend.Remote:
		return remote{b}
	case Backend:
		return b
	}
	return unsupported{}
}

// ---- 不支援 ----

type unsupported struct{}

func (unsupported) GetGitHubSettings(string) (*GitHubSettings, error) {
	return nil, backend.ErrUnsupported
}
func (unsupported) SaveGitHubSettings(string, GitHubSettingsInput) (*GitHubSettings, error) {
	return nil, backend.ErrUnsupported
}
func (unsupported) ImportGitHubSettings(string, GitHubImport) (*GitHubImportResult, error) {
	return nil, backend.ErrUnsupported
}
func (unsupported) GetGitLabSettings(string) (*GitLabSettings, error) {
	return nil, backend.ErrUnsupported
}
func (unsupported) SaveGitLabSettings(string, GitLabSettingsInput) (*GitLabSettings, error) {
	return nil, backend.ErrUnsupported
}
func (unsupported) ImportGitLabSettings(string, GitLabImport) (*GitLabImportResult, error) {
	return nil, backend.ErrUnsupported
}

// ---- Local(同一個程式裡的核心) ----

type local struct{ l *backend.Local }

func (x local) svc(org string) *Service {
	x.l.PrepareOrg(org)
	return For(x.l.Svc)
}

func (x local) GetGitHubSettings(org string) (*GitHubSettings, error) {
	return x.svc(org).GetGitHubSettings(x.l.P, org)
}

func (x local) SaveGitHubSettings(org string, in GitHubSettingsInput) (*GitHubSettings, error) {
	return x.svc(org).SaveGitHubSettings(x.l.P, org, in)
}

func (x local) ImportGitHubSettings(org string, in GitHubImport) (*GitHubImportResult, error) {
	return x.svc(org).ImportGitHubSettings(x.l.P, org, in)
}

func (x local) GetGitLabSettings(org string) (*GitLabSettings, error) {
	return x.svc(org).GetGitLabSettings(x.l.P, org)
}

func (x local) SaveGitLabSettings(org string, in GitLabSettingsInput) (*GitLabSettings, error) {
	return x.svc(org).SaveGitLabSettings(x.l.P, org, in)
}

func (x local) ImportGitLabSettings(org string, in GitLabImport) (*GitLabImportResult, error) {
	return x.svc(org).ImportGitLabSettings(x.l.P, org, in)
}

// ---- Remote(連線上的 server;錯誤轉成 *service.Error,同核心的 Remote) ----

type remote struct{ r *backend.Remote }

func (x remote) GetGitHubSettings(org string) (*GitHubSettings, error) {
	v, err := GetGitHubSettings(x.r.Client(), x.r.ServerOrg(org))
	if err != nil {
		return nil, backend.Wrap(err)
	}
	return v, nil
}

func (x remote) SaveGitHubSettings(org string, in GitHubSettingsInput) (*GitHubSettings, error) {
	v, err := SaveGitHubSettings(x.r.Client(), x.r.ServerOrg(org), in)
	if err != nil {
		return nil, backend.Wrap(err)
	}
	return v, nil
}

func (x remote) ImportGitHubSettings(org string, in GitHubImport) (*GitHubImportResult, error) {
	v, err := ImportGitHubSettings(x.r.Client(), x.r.ServerOrg(org), in)
	if err != nil {
		return nil, backend.Wrap(err)
	}
	return v, nil
}

func (x remote) GetGitLabSettings(org string) (*GitLabSettings, error) {
	v, err := GetGitLabSettings(x.r.Client(), x.r.ServerOrg(org))
	if err != nil {
		return nil, backend.Wrap(err)
	}
	return v, nil
}

func (x remote) SaveGitLabSettings(org string, in GitLabSettingsInput) (*GitLabSettings, error) {
	v, err := SaveGitLabSettings(x.r.Client(), x.r.ServerOrg(org), in)
	if err != nil {
		return nil, backend.Wrap(err)
	}
	return v, nil
}

func (x remote) ImportGitLabSettings(org string, in GitLabImport) (*GitLabImportResult, error) {
	v, err := ImportGitLabSettings(x.r.Client(), x.r.ServerOrg(org), in)
	if err != nil {
		return nil, backend.Wrap(err)
	}
	return v, nil
}
