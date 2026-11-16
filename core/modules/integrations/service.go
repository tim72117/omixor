package integrations

import (
	"encoding/json"
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 組織的 GitHub、GitLab 設定的規則(原本在 core/service/integration.go;資料見 model.go;只有非機密設定,token 留在各裝置)。
//
//	讀:組織成員(角色憑證也可以,只能讀自己組織的)
//	改、匯入:組織管理員(伺服器管理員視同管理員)
//
// 改的輸入可以整份取代(repos / projects),也可以只加減(add、remove),兩個裝置同時加不同的 repo 不會互相蓋掉。
// 匯入(第一次連上時把裝置的本機舊設定搬進來):核心還沒存過才寫入,已經存過就不動、回傳目前的(imported = false)。
//
// 存放:核心 store 的通用組織設定文件(GetOrgDoc / SaveOrgDoc),kind = github、gitlab。
// data 的內容和以前 PG 專屬方法存的一模一樣(設定本身,saved = false、不含 updatedAt / updatedBy),線上資料照讀。

// 存放用的 kind(PG 表 omixor_org_integrations 的 kind 欄位,不能改)
const (
	kindGitHub = "github"
	kindGitLab = "gitlab"
)

// Service 模組的服務層:包核心的 service.Service(權限、稽核、時鐘、store 都用核心的)
type Service struct{ Svc *service.Service }

// For 用核心的 service 建立
func For(svc *service.Service) *Service { return &Service{Svc: svc} }

// lock GitHub / GitLab 設定的「讀了再改」在這把鎖裡(單一程式內;見 service.ModuleLock)
func (s *Service) lock() func() {
	m := s.Svc.ModuleLock(ModuleID)
	m.Lock()
	return m.Unlock
}

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
	Imported bool           `json:"imported"`
	Skipped  []string       `json:"skipped"`
	Settings GitHubSettings `json:"settings"`
}

type GitLabImportResult struct {
	Imported bool           `json:"imported"`
	Skipped  []string       `json:"skipped"`
	Settings GitLabSettings `json:"settings"`
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
				return nil, service.Fail(400, "%s格式不對:%s", what, strings.TrimSpace(r))
			}
			if !containsFold(out, n) {
				out = append(out, n)
			}
		}
	}
	for _, r := range add {
		n, ok := norm(r)
		if !ok {
			return nil, service.Fail(400, "%s格式不對:%s", what, strings.TrimSpace(r))
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
	if len(out) > MaxIntegrationItems {
		return nil, service.Fail(400, "%s最多 %d 個", what, MaxIntegrationItems)
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
		if !containsFold(out, n) && len(out) < MaxIntegrationItems {
			out = append(out, n)
		}
	}
	return out, skipped
}

// ---- 存放(store 的通用組織設定文件) ----

// getDoc 讀一份設定到 out;沒存過回 (false, nil)。回傳 updatedAt、updatedBy
func (s *Service) getDoc(org, kind string, out any) (bool, *model.Time, string, error) {
	d, err := s.Svc.Store.GetOrgDoc(org, kind)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil, "", nil
	}
	if err != nil {
		return false, nil, "", err
	}
	if err := json.Unmarshal(d.Data, out); err != nil {
		return false, nil, "", err
	}
	return true, d.UpdatedAt, d.UpdatedBy, nil
}

// saveDoc data 是設定本身(Saved = false、UpdatedAt = nil、UpdatedBy = "",和以前 PG 存的一樣)
func (s *Service) saveDoc(org, kind string, data any, at *model.Time, by string) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.Svc.Store.SaveOrgDoc(org, kind, store.OrgDoc{Data: b, UpdatedAt: at, UpdatedBy: by})
}

// ---- GitHub ----

func (s *Service) loadGitHub(org string) (GitHubSettings, error) {
	var x GitHubSettings
	ok, at, by, err := s.getDoc(org, kindGitHub, &x)
	if err != nil {
		return GitHubSettings{}, err
	}
	if !ok {
		return GitHubSettings{Repos: []string{}}, nil
	}
	if x.Repos == nil {
		x.Repos = []string{}
	}
	x.Saved, x.UpdatedAt, x.UpdatedBy = true, at, by
	return x, nil
}

func (s *Service) storeGitHub(org string, x GitHubSettings) error {
	d := x.Clone()
	d.Saved, d.UpdatedAt, d.UpdatedBy = false, nil, ""
	return s.saveDoc(org, kindGitHub, d, x.UpdatedAt, x.UpdatedBy)
}

// GetGitHubSettings 組織的 GitHub 設定(成員,含角色身分);沒存過回空白預設(saved = false)
func (s *Service) GetGitHubSettings(p *model.Principal, org string) (*GitHubSettings, error) {
	if err := s.Svc.RequireViewer(p, org); err != nil {
		return nil, err
	}
	x, err := s.loadGitHub(org)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// SaveGitHubSettings 修改組織的 GitHub 設定(組織管理員)
func (s *Service) SaveGitHubSettings(p *model.Principal, org string, in GitHubSettingsInput) (*GitHubSettings, error) {
	if err := s.Svc.RequireOrgAdmin(p, org); err != nil {
		return nil, err
	}
	defer s.lock()()
	cur, err := s.loadGitHub(org)
	if err != nil {
		return nil, err
	}
	repos, err := applyList(cur.Repos, in.Repos, in.Add, in.Remove, NormalizeGitHubRepo, "repo(要寫成 owner/name)")
	if err != nil {
		return nil, err
	}
	now := s.Svc.NowTime()
	x := GitHubSettings{Repos: repos, Saved: true, UpdatedAt: &now, UpdatedBy: service.WhoName(p)}
	if err := s.storeGitHub(org, x); err != nil {
		return nil, err
	}
	s.Svc.Audit(p, "integration.github.save", org, map[string]any{"repos": len(repos)})
	return &x, nil
}

// ImportGitHubSettings 第一次搬資料用(組織管理員):核心還沒存過才寫入
func (s *Service) ImportGitHubSettings(p *model.Principal, org string, in GitHubImport) (*GitHubImportResult, error) {
	if err := s.Svc.RequireOrgAdmin(p, org); err != nil {
		return nil, err
	}
	defer s.lock()()
	cur, err := s.loadGitHub(org)
	if err != nil {
		return nil, err
	}
	repos, skipped := importList(in.Repos, NormalizeGitHubRepo)
	if cur.Saved {
		return &GitHubImportResult{Skipped: skipped, Settings: cur}, nil
	}
	now := s.Svc.NowTime()
	x := GitHubSettings{Repos: repos, Saved: true, UpdatedAt: &now, UpdatedBy: service.WhoName(p)}
	if err := s.storeGitHub(org, x); err != nil {
		return nil, err
	}
	s.Svc.Audit(p, "integration.github.import", org, map[string]any{"repos": len(repos), "skipped": len(skipped)})
	return &GitHubImportResult{Imported: true, Skipped: skipped, Settings: x}, nil
}

// ---- GitLab ----

func (s *Service) loadGitLab(org string) (GitLabSettings, error) {
	var x GitLabSettings
	ok, at, by, err := s.getDoc(org, kindGitLab, &x)
	if err != nil {
		return GitLabSettings{}, err
	}
	if !ok {
		return GitLabSettings{Projects: []string{}}, nil
	}
	if x.Projects == nil {
		x.Projects = []string{}
	}
	x.Saved, x.UpdatedAt, x.UpdatedBy = true, at, by
	return x, nil
}

func (s *Service) storeGitLab(org string, x GitLabSettings) error {
	d := x.Clone()
	d.Saved, d.UpdatedAt, d.UpdatedBy = false, nil, ""
	return s.saveDoc(org, kindGitLab, d, x.UpdatedAt, x.UpdatedBy)
}

// GetGitLabSettings 組織的 GitLab 設定(成員,含角色身分);沒存過回空白預設(saved = false)
func (s *Service) GetGitLabSettings(p *model.Principal, org string) (*GitLabSettings, error) {
	if err := s.Svc.RequireViewer(p, org); err != nil {
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
	b, ok := NormalizeGitLabBaseURL(raw)
	if !ok {
		return "", service.Fail(400, "GitLab 網址要寫成 https://gitlab.example.com")
	}
	return b, nil
}

// SaveGitLabSettings 修改組織的 GitLab 設定(組織管理員)
func (s *Service) SaveGitLabSettings(p *model.Principal, org string, in GitLabSettingsInput) (*GitLabSettings, error) {
	if err := s.Svc.RequireOrgAdmin(p, org); err != nil {
		return nil, err
	}
	defer s.lock()()
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
	norm := func(x string) (string, bool) { return NormalizeGitLabProject(x, base) }
	projects, err := applyList(cur.Projects, in.Projects, in.Add, in.Remove, norm, "專案(要寫成 group/project、專案 id 或專案網址)")
	if err != nil {
		return nil, err
	}
	now := s.Svc.NowTime()
	x := GitLabSettings{BaseURL: base, Projects: projects, Saved: true, UpdatedAt: &now, UpdatedBy: service.WhoName(p)}
	if err := s.storeGitLab(org, x); err != nil {
		return nil, err
	}
	s.Svc.Audit(p, "integration.gitlab.save", org, map[string]any{"baseUrl": base, "projects": len(projects)})
	return &x, nil
}

// ImportGitLabSettings 第一次搬資料用(組織管理員):核心還沒存過才寫入;網址不對時當作沒設定(列在 skipped)
func (s *Service) ImportGitLabSettings(p *model.Principal, org string, in GitLabImport) (*GitLabImportResult, error) {
	if err := s.Svc.RequireOrgAdmin(p, org); err != nil {
		return nil, err
	}
	defer s.lock()()
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
	projects, sk := importList(in.Projects, func(x string) (string, bool) { return NormalizeGitLabProject(x, base) })
	skipped = append(skipped, sk...)
	if cur.Saved {
		return &GitLabImportResult{Skipped: skipped, Settings: cur}, nil
	}
	now := s.Svc.NowTime()
	x := GitLabSettings{BaseURL: base, Projects: projects, Saved: true, UpdatedAt: &now, UpdatedBy: service.WhoName(p)}
	if err := s.storeGitLab(org, x); err != nil {
		return nil, err
	}
	s.Svc.Audit(p, "integration.gitlab.import", org, map[string]any{"baseUrl": base, "projects": len(projects), "skipped": len(skipped)})
	return &GitLabImportResult{Imported: true, Skipped: skipped, Settings: x}, nil
}
