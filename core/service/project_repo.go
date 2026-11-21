package service

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 專案與 repo 的連結(第 2 階段;模型見 model.ProjectRepo)。
//
// 權限:組織成員(含角色身分)可以看專案連了哪些 repo(Project.Repos);連結、取消連結只有組織管理員(同改專案)。
// 連結只用於「預設值與篩選」,不是權限來源:
//   - 建立 Issue 沒指定 project、repo 只連到一個專案(而且有代號)時,預設放進那個專案(見 defaultProjectForRepo);
//   - Issue 依 repo 篩選時,沒標 repo 但所屬專案連到該 repo 的 Issue 也算(見 withRepoLinks)。
//
// 不會自動搬動既有 Issue。

// cleanLinkRepo 連結用的 repo:格式同 Issue 的 repo(model.CleanRepo),而且不能空
func cleanLinkRepo(repo string) (string, error) {
	r, ok := model.CleanRepo(repo)
	if !ok {
		return "", repoErr()
	}
	if r == "" || model.RepoKey(r) == "" {
		return "", fail(400, "要給 repo")
	}
	return r, nil
}

// reposOf 把連結整理成「專案 id → repo 清單」
func reposOf(links []model.ProjectRepo) map[string][]string {
	m := map[string][]string{}
	for _, l := range links {
		m[l.Project] = append(m[l.Project], l.Repo)
	}
	return m
}

// fillProjectRepos 把連結填進專案的 Repos(沒有連結是空陣列)
func (s *Service) fillProjectRepos(org string, list ...*model.Project) error {
	links, err := s.Store.ListProjectRepos(org)
	if err != nil {
		return err
	}
	m := reposOf(links)
	for _, x := range list {
		x.Repos = append([]string{}, m[x.ID]...)
	}
	return nil
}

// LinkProjectRepo 連結專案與 repo(組織管理員)。repo 以不分大小寫、github:/gitlab: 前綴可省的規則比對,已經連過是成功(不重複);
// repo 格式不合 400、專案不存在 404。回傳連結後的專案(含 Repos)
func (s *Service) LinkProjectRepo(p *model.Principal, org, project, repo string) (*model.Project, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	r, err := cleanLinkRepo(repo)
	if err != nil {
		return nil, err
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	x, err := s.findProject(org, project)
	if err != nil {
		return nil, err
	}
	created, err := s.Store.LinkProjectRepo(org, x.ID, r, s.mt().Time)
	if errors.Is(err, store.ErrBadRef) {
		return nil, fail(404, "找不到專案 %s", project)
	} else if err != nil {
		return nil, err
	}
	if created {
		s.Audit(p, "project.repo_link", org, map[string]any{"id": x.ID, "repo": r})
	}
	return x, s.fillProjectRepos(org, x)
}

// UnlinkProjectRepo 取消專案與 repo 的連結(組織管理員)。原本沒連回 404;不影響已經建立的 Issue。回傳取消後的專案(含 Repos)
func (s *Service) UnlinkProjectRepo(p *model.Principal, org, project, repo string) (*model.Project, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	r, err := cleanLinkRepo(repo)
	if err != nil {
		return nil, err
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	x, err := s.findProject(org, project)
	if err != nil {
		return nil, err
	}
	removed, err := s.Store.UnlinkProjectRepo(org, x.ID, r)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, fail(404, "專案 %s 沒有連結 %s", x.ID, r)
	}
	s.Audit(p, "project.repo_unlink", org, map[string]any{"id": x.ID, "repo": r})
	return x, s.fillProjectRepos(org, x)
}

// ListProjectRepos 專案連結的 repo(組織成員);project 空白 = 整個組織的連結
func (s *Service) ListProjectRepos(p *model.Principal, org, project string) ([]model.ProjectRepo, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	id := ""
	if strings.TrimSpace(project) != "" {
		x, err := s.findProject(org, project)
		if err != nil {
			return nil, err
		}
		if !a.Sees(x.ID) { // 看不到的受限專案:和不存在一樣
			return nil, errProjectNotFound(project)
		}
		id = x.ID
	}
	all, err := s.Store.ListProjectRepos(org)
	if err != nil {
		return nil, err
	}
	if id == "" {
		out := []model.ProjectRepo{}
		for _, l := range all {
			if a.Sees(l.Project) {
				out = append(out, l)
			}
		}
		return out, nil
	}
	out := []model.ProjectRepo{}
	for _, l := range all {
		if l.Project == id {
			out = append(out, l)
		}
	}
	return out, nil
}

// linkedProjects repo 連到哪些專案(依專案號碼;比對規則同 model.RepoMatch)
func linkedProjects(links []model.ProjectRepo, repo string) []string {
	out := []string{}
	for _, l := range links {
		if model.RepoMatch(l.Repo, repo) && !containsStr(out, l.Project) {
			out = append(out, l.Project)
		}
	}
	return out
}

func containsStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// defaultProjectForRepo 建立 Issue 沒指定專案時的預設:repo 只連到一個專案、而且那個專案有代號才回那個專案 id,否則 ""(組織層級)。
// repo 空白、沒連任何專案(組織共用)、連到多個專案(不猜)都是 ""
func (s *Service) defaultProjectForRepo(org, repo string) (string, error) {
	if model.RepoKey(repo) == "" {
		return "", nil
	}
	links, err := s.Store.ListProjectRepos(org)
	if err != nil {
		return "", err
	}
	ids := linkedProjects(links, repo)
	if len(ids) != 1 {
		return "", nil
	}
	x, err := s.Store.GetProject(org, ids[0])
	if errors.Is(err, store.ErrNotFound) || (err == nil && x.Key == "") {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return x.ID, nil
}

// withRepoLinks 依 repo 篩選時帶入連結:q.Repo 是 repo(不是 IssueFilterNone)時,填 q.RepoProjects = 連到這個 repo 的專案,
// 這些專案裡沒標 repo 的 Issue 也算符合(見 model.IssueQuery.RepoProjects)
func (s *Service) withRepoLinks(org string, q model.IssueQuery) (model.IssueQuery, error) {
	r := strings.TrimSpace(q.Repo)
	if r == "" || r == model.IssueFilterNone {
		return q, nil
	}
	links, err := s.Store.ListProjectRepos(org)
	if err != nil {
		return q, err
	}
	q.RepoProjects = linkedProjects(links, r)
	return q, nil
}
