package service

import (
	"errors"
	"strings"
	"sync"

	"omixor-core/model"
	"omixor-core/store"
)

// 組織的 GitHub、GitLab 設定(資料見 model/integration.go;只有非機密設定,token 留在各裝置)。
//
//	讀:組織成員(角色憑證也可以,只能讀自己組織的)
//	改、匯入:組織管理員(伺服器管理員視同管理員)
//
// 改的輸入可以整份取代(repos / projects),也可以只加減(add、remove),兩個裝置同時加不同的 repo 不會互相蓋掉。
// 匯入(第一次連上時把裝置的本機舊設定搬進來):核心還沒存過才寫入,已經存過就不動、回傳目前的(imported = false)。

// integrationMu 「讀了再改」在這把鎖裡(單一程式內)
var integrationMu sync.Mutex

// GitHubSettingsInput 修改 GitHub 設定。Repos 不是 nil 就整份取代,再套用 Add、Remove
type GitHubSettingsInput struct {
	Repos  *[]string `json:"repos,omitempty"`
	Add    []string  `json:"add,omitempty"`
	Remove []string  `json:"remove,omitempty"`
}

// GitLabSettingsInput 修改 GitLab 設定。BaseURL 不是 nil 就換網址(空字串 = 清掉);Projects 不是 nil 就整份取代,再套用 Add、Remove
type GitLabSettingsInput struct {
	BaseURL  *string   `json:"baseUrl,omitempty"`
	Projects *[]string `json:"projects,omitempty"`
	Add      []string  `json:"add,omitempty"`
	Remove   []string  `json:"remove,omitempty"`
}

// GitHubImport 第一次匯入的內容(格式不對的 repo 略過,列在結果的 skipped)
type GitHubImport struct {
	Repos []string `json:"repos"`
}

// GitLabImport 第一次匯入的內容
type GitLabImport struct {
	BaseURL  string   `json:"baseUrl"`
	Projects []string `json:"projects"`
}

type GitHubImportResult struct {
	Imported bool                 `json:"imported"`
	Skipped  []string             `json:"skipped"`
	Settings model.GitHubSettings `json:"settings"`
}

type GitLabImportResult struct {
	Imported bool                 `json:"imported"`
	Skipped  []string             `json:"skipped"`
	Settings model.GitLabSettings `json:"settings"`
}

// whoName 署名:email 優先,否則名稱
func whoName(p *model.Principal) string {
	if p == nil {
		return ""
	}
	if p.User.Email != "" {
		return p.User.Email
	}
	return p.User.Name
}

// containsFold 不分大小寫找
func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// applyList 整份取代(replace 不是 nil)後加減;norm 正規化,不對時回錯誤訊息。回傳新的清單
func applyList(cur []string, replace *[]string, add, remove []string, norm func(string) (string, bool), what string) ([]string, error) {
	out := append([]string{}, cur...)
	if replace != nil {
		out = []string{}
		for _, r := range *replace {
			n, ok := norm(r)
			if !ok {
				return nil, fail(400, "%s格式不對:%s", what, strings.TrimSpace(r))
			}
			if !containsFold(out, n) {
				out = append(out, n)
			}
		}
	}
	for _, r := range add {
		n, ok := norm(r)
		if !ok {
			return nil, fail(400, "%s格式不對:%s", what, strings.TrimSpace(r))
		}
		if !containsFold(out, n) {
			out = append(out, n)
		}
	}
	for _, r := range remove {
		key := strings.TrimSpace(r)
		if n, ok := norm(r); ok {
			key = n
		}
		kept := out[:0:0]
		for _, x := range out {
			if !strings.EqualFold(x, key) {
				kept = append(kept, x)
			}
		}
		out = kept
	}
	if len(out) > model.MaxIntegrationItems {
		return nil, fail(400, "%s最多 %d 個", what, model.MaxIntegrationItems)
	}
	return out, nil
}

// importList 匯入用:格式不對的略過(回傳在 skipped),重複的合併
func importList(in []string, norm func(string) (string, bool)) (out, skipped []string) {
	out, skipped = []string{}, []string{}
	for _, r := range in {
		n, ok := norm(r)
		if !ok {
			skipped = append(skipped, r)
			continue
		}
		if !containsFold(out, n) && len(out) < model.MaxIntegrationItems {
			out = append(out, n)
		}
	}
	return out, skipped
}

// ---- GitHub ----

func (s *Service) loadGitHub(org string) (model.GitHubSettings, error) {
	x, err := s.Store.GetGitHubSettings(org)
	if errors.Is(err, store.ErrNotFound) {
		return model.GitHubSettings{Repos: []string{}}, nil
	}
	if err != nil {
		return model.GitHubSettings{}, err
	}
	if x.Repos == nil {
		x.Repos = []string{}
	}
	x.Saved = true
	return *x, nil
}

// GetGitHubSettings 組織的 GitHub 設定(成員,含角色身分);沒存過回空白預設(saved = false)
func (s *Service) GetGitHubSettings(p *model.Principal, org string) (*model.GitHubSettings, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	x, err := s.loadGitHub(org)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// SaveGitHubSettings 修改組織的 GitHub 設定(組織管理員)
func (s *Service) SaveGitHubSettings(p *model.Principal, org string, in GitHubSettingsInput) (*model.GitHubSettings, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	integrationMu.Lock()
	defer integrationMu.Unlock()
	cur, err := s.loadGitHub(org)
	if err != nil {
		return nil, err
	}
	repos, err := applyList(cur.Repos, in.Repos, in.Add, in.Remove, model.NormalizeGitHubRepo, "repo(要寫成 owner/name)")
	if err != nil {
		return nil, err
	}
	now := s.mt()
	x := model.GitHubSettings{Repos: repos, Saved: true, UpdatedAt: &now, UpdatedBy: whoName(p)}
	if err := s.Store.SaveGitHubSettings(org, x); err != nil {
		return nil, err
	}
	s.Audit(p, "integration.github.save", org, map[string]any{"repos": len(repos)})
	return &x, nil
}

// ImportGitHubSettings 第一次搬資料用(組織管理員):核心還沒存過才寫入
func (s *Service) ImportGitHubSettings(p *model.Principal, org string, in GitHubImport) (*GitHubImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	integrationMu.Lock()
	defer integrationMu.Unlock()
	cur, err := s.loadGitHub(org)
	if err != nil {
		return nil, err
	}
	repos, skipped := importList(in.Repos, model.NormalizeGitHubRepo)
	if cur.Saved {
		return &GitHubImportResult{Skipped: skipped, Settings: cur}, nil
	}
	now := s.mt()
	x := model.GitHubSettings{Repos: repos, Saved: true, UpdatedAt: &now, UpdatedBy: whoName(p)}
	if err := s.Store.SaveGitHubSettings(org, x); err != nil {
		return nil, err
	}
	s.Audit(p, "integration.github.import", org, map[string]any{"repos": len(repos), "skipped": len(skipped)})
	return &GitHubImportResult{Imported: true, Skipped: skipped, Settings: x}, nil
}

// ---- GitLab ----

func (s *Service) loadGitLab(org string) (model.GitLabSettings, error) {
	x, err := s.Store.GetGitLabSettings(org)
	if errors.Is(err, store.ErrNotFound) {
		return model.GitLabSettings{Projects: []string{}}, nil
	}
	if err != nil {
		return model.GitLabSettings{}, err
	}
	if x.Projects == nil {
		x.Projects = []string{}
	}
	x.Saved = true
	return *x, nil
}

// GetGitLabSettings 組織的 GitLab 設定(成員,含角色身分);沒存過回空白預設(saved = false)
func (s *Service) GetGitLabSettings(p *model.Principal, org string) (*model.GitLabSettings, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	x, err := s.loadGitLab(org)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

func normalizeBase(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	b, ok := model.NormalizeGitLabBaseURL(raw)
	if !ok {
		return "", fail(400, "GitLab 網址要寫成 https://gitlab.example.com")
	}
	return b, nil
}

// SaveGitLabSettings 修改組織的 GitLab 設定(組織管理員)
func (s *Service) SaveGitLabSettings(p *model.Principal, org string, in GitLabSettingsInput) (*model.GitLabSettings, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	integrationMu.Lock()
	defer integrationMu.Unlock()
	cur, err := s.loadGitLab(org)
	if err != nil {
		return nil, err
	}
	base := cur.BaseURL
	if in.BaseURL != nil {
		if base, err = normalizeBase(*in.BaseURL); err != nil {
			return nil, err
		}
	}
	norm := func(x string) (string, bool) { return model.NormalizeGitLabProject(x, base) }
	projects, err := applyList(cur.Projects, in.Projects, in.Add, in.Remove, norm, "專案(要寫成 group/project、專案 id 或專案網址)")
	if err != nil {
		return nil, err
	}
	now := s.mt()
	x := model.GitLabSettings{BaseURL: base, Projects: projects, Saved: true, UpdatedAt: &now, UpdatedBy: whoName(p)}
	if err := s.Store.SaveGitLabSettings(org, x); err != nil {
		return nil, err
	}
	s.Audit(p, "integration.gitlab.save", org, map[string]any{"baseUrl": base, "projects": len(projects)})
	return &x, nil
}

// ImportGitLabSettings 第一次搬資料用(組織管理員):核心還沒存過才寫入;網址不對時當作沒設定(列在 skipped)
func (s *Service) ImportGitLabSettings(p *model.Principal, org string, in GitLabImport) (*GitLabImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	integrationMu.Lock()
	defer integrationMu.Unlock()
	cur, err := s.loadGitLab(org)
	if err != nil {
		return nil, err
	}
	skipped := []string{}
	base, err := normalizeBase(in.BaseURL)
	if err != nil {
		skipped = append(skipped, in.BaseURL)
		base = ""
	}
	projects, sk := importList(in.Projects, func(x string) (string, bool) { return model.NormalizeGitLabProject(x, base) })
	skipped = append(skipped, sk...)
	if cur.Saved {
		return &GitLabImportResult{Skipped: skipped, Settings: cur}, nil
	}
	now := s.mt()
	x := model.GitLabSettings{BaseURL: base, Projects: projects, Saved: true, UpdatedAt: &now, UpdatedBy: whoName(p)}
	if err := s.Store.SaveGitLabSettings(org, x); err != nil {
		return nil, err
	}
	s.Audit(p, "integration.gitlab.import", org, map[string]any{"baseUrl": base, "projects": len(projects), "skipped": len(skipped)})
	return &GitLabImportResult{Imported: true, Skipped: skipped, Settings: x}, nil
}
