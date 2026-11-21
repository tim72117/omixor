package service

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 專案:組織底下的一層,目前只有文件櫃(結構與文件)分專案(見 model/project.go)。
//
// 權限:
//   - 組織成員(含角色身分):列出、讀取專案(組織成員看得到所有專案,沒有另外的專案成員)
//   - 組織管理員:新增、修改、刪除專案(和改文件櫃結構一樣)
//
// 名稱必填、組織內不重複(不分大小寫,去掉前後空白比);代號選填(專案裡的 Issue 要有代號才能建,key 是 <代號>-<n>),
// 組織內不重複、專案已經有 Issue 時不能改(第一版);刪除時專案裡還有文件或 Issue(含垃圾桶)就拒絕(409)。

// ProjectInput 新增專案
type ProjectInput struct {
	Name string `json:"name"`
	// Key 專案代號(選填,例如 APP;規則見 model.CleanProjectKey,組織內不重複);沒有代號的專案不能放 Issue
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
}

// ProjectPatch 修改專案:有給的欄位才改
type ProjectPatch struct {
	Name *string `json:"name,omitempty"`
	// Key 改代號("" = 清掉);專案裡已經有 Issue(含垃圾桶)時不能改(409)
	Key         *string `json:"key,omitempty"`
	Description *string `json:"description,omitempty"`
}

// findProject 依 id 找專案(不分大小寫);找不到回 404
func (s *Service) findProject(org, id string) (*model.Project, error) {
	id = strings.TrimSpace(id)
	if x, err := s.Store.GetProject(org, model.NormalizeProjectID(id)); err == nil {
		return x, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	list, err := s.Store.ListProjects(org)
	if err != nil {
		return nil, err
	}
	for _, x := range list {
		if strings.EqualFold(x.ID, id) {
			return &x, nil
		}
	}
	return nil, fail(404, "找不到專案 %s", id)
}

// projectID 文件櫃操作的專案參數:空白 = 未分專案(回 "");其他要是存在的專案(回存起來的 id),不存在 404
func (s *Service) projectID(org, project string) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", nil
	}
	x, err := s.findProject(org, project)
	if err != nil {
		return "", err
	}
	return x.ID, nil
}

// checkProjectName 名稱不空、組織內不重複(self 是正在改的專案 id)
func (s *Service) checkProjectName(org, self, name string) error {
	if name == "" {
		return fail(400, "要給專案名稱")
	}
	list, err := s.Store.ListProjects(org)
	if err != nil {
		return err
	}
	for _, x := range list {
		if x.ID != self && strings.EqualFold(strings.TrimSpace(x.Name), name) {
			return fail(409, "已經有專案叫「%s」(%s)", x.Name, x.ID)
		}
	}
	return nil
}

// checkProjectKey 整理並檢查代號:格式(400)、組織內不重複(不分大小寫,409;self 是正在改的專案 id)。空字串 = 沒有代號
func (s *Service) checkProjectKey(org, self, key string) (string, error) {
	k, err := model.CleanProjectKey(key)
	if err != nil {
		return "", fail(400, "%s", err.Error())
	}
	if k == "" {
		return "", nil
	}
	list, err := s.Store.ListProjects(org)
	if err != nil {
		return "", err
	}
	for _, x := range list {
		if x.ID != self && strings.EqualFold(x.Key, k) {
			return "", fail(409, "專案代號 %s 已經被「%s」(%s)用了", k, x.Name, x.ID)
		}
	}
	// 別名表裡過期的別名(搬走的 Issue 的舊 key、匯入的舊編號)不能被新專案的號碼蓋掉:有 <代號>-<n> 的別名就拒絕。
	// 代號沒變(self 的目前代號)不檢查,不然改名字也會被擋
	if self != "" {
		for _, x := range list {
			if x.ID == self && strings.EqualFold(x.Key, k) {
				return k, nil
			}
		}
	}
	if a, err := s.Store.ProjectKeyAliasTaken(org, k); err != nil {
		return "", err
	} else if a != "" {
		return "", fail(400, "專案代號 %s 不能用:舊編號 %s 還留在編號別名裡(搬走的 Issue 或匯入的舊編號),請換一個代號", k, a)
	}
	return k, nil
}

// projectKeyErr 存放處存專案的錯誤:代號重複 409、已有 Issue 不能改代號 409
func projectKeyErr(err error) error {
	switch {
	case errors.Is(err, store.ErrDuplicateKey):
		return fail(409, "專案代號已經被別的專案用了")
	case errors.Is(err, store.ErrInUse):
		return fail(409, "專案裡已經有 Issue,不能再改代號")
	}
	return err
}

// ListProjects 組織的專案(依建立順序)
func (s *Service) ListProjects(p *model.Principal, org string) ([]model.Project, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	return s.Store.ListProjects(org)
}

// GetProject 依 id 取專案(不分大小寫)
func (s *Service) GetProject(p *model.Principal, org, id string) (*model.Project, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	return s.findProject(org, id)
}

// CreateProject 新增專案(組織管理員;id 是 PRJ-<最大號+1>)
func (s *Service) CreateProject(p *model.Principal, org string, in ProjectInput) (*model.Project, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	if err := s.checkProjectName(org, "", name); err != nil {
		return nil, err
	}
	key, err := s.checkProjectKey(org, "", in.Key)
	if err != nil {
		return nil, err
	}
	now := s.mt()
	x, err := s.Store.CreateProject(org, model.Project{Name: name, Key: key, Description: strings.TrimSpace(in.Description), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return nil, projectKeyErr(err)
	}
	s.Audit(p, "project.create", org, map[string]any{"id": x.ID, "name": x.Name})
	return &x, nil
}

// UpdateProject 修改專案名稱、描述(組織管理員);沒有變就不更新時間
func (s *Service) UpdateProject(p *model.Principal, org, id string, in ProjectPatch) (*model.Project, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	cur, err := s.findProject(org, id)
	if err != nil {
		return nil, err
	}
	x := *cur
	if in.Name != nil {
		x.Name = strings.TrimSpace(*in.Name)
		if err := s.checkProjectName(org, x.ID, x.Name); err != nil {
			return nil, err
		}
	}
	if in.Description != nil {
		x.Description = strings.TrimSpace(*in.Description)
	}
	if in.Key != nil {
		k, err := s.checkProjectKey(org, x.ID, *in.Key)
		if err != nil {
			return nil, err
		}
		x.Key = k
	}
	if x.Name == cur.Name && x.Description == cur.Description && x.Key == cur.Key {
		return &x, nil
	}
	x.UpdatedAt = s.mt()
	if err := s.Store.UpdateProject(org, x); errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到專案 %s", id)
	} else if err != nil {
		return nil, projectKeyErr(err)
	}
	s.Audit(p, "project.update", org, map[string]any{"id": x.ID})
	return &x, nil
}

// DeleteProject 刪除專案與它的文件櫃結構(組織管理員)。專案裡還有文件就拒絕(409),要先搬走或刪除;
// 角色 docAccess 裡指向這個專案的設定一併拿掉。
// 鎖的順序 agents.mu → cabinetMu(見 locks.go):清 docAccess 要在 agents.mu 內重讀角色再存,
// 和 SaveAgent 互斥(SaveAgent 也在 agents.mu 內檢查專案是否存在),不會在刪除途中替這個專案加回權限
func (s *Service) DeleteProject(p *model.Principal, org, id string) error {
	if err := s.needOrgAdmin(p, org); err != nil {
		return err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	x, err := s.findProject(org, id)
	if err != nil {
		return err
	}
	docs, err := s.Store.ListDocs(org)
	if err != nil {
		return err
	}
	n := 0
	for _, d := range docs {
		if d.Project == x.ID {
			n++
		}
	}
	if n > 0 {
		return fail(409, "專案「%s」(%s)裡還有 %d 份文件,先把文件搬到別的專案或刪除,才能刪除專案", x.Name, x.ID, n)
	}
	n, err = s.Store.CountProjectIssues(org, x.ID) // 含垃圾桶;fk_issues_project(restrict)仍是最後防線
	if err != nil {
		return err
	}
	if n > 0 {
		return fail(409, "專案「%s」(%s)裡還有 %d 張 Issue(含垃圾桶),先把 Issue 搬到別的專案或永久刪除,才能刪除專案", x.Name, x.ID, n)
	}
	if err := s.Store.DeleteProject(org, x.ID); errors.Is(err, store.ErrNotFound) {
		return fail(404, "找不到專案 %s", id)
	} else if errors.Is(err, store.ErrInUse) {
		return fail(409, "專案「%s」(%s)裡還有 Issue 或文件,不能刪除", x.Name, x.ID)
	} else if err != nil {
		return err
	}
	s.Audit(p, "project.delete", org, map[string]any{"id": x.ID})
	// 角色的 docAccess:拿掉這個專案的 key(專案 id 不重複使用,這裡是清掉殘留的設定)。
	// 在 agents.mu 內重讀最新的角色;失敗時專案已經刪了,把錯誤回給呼叫端(殘留的 key 之後存角色時會自動清掉)
	agents, err := s.Store.ListAgents(org)
	if err != nil {
		return err
	}
	for _, g := range agents {
		changed := false
		for k := range g.DocAccess {
			if pid, _ := model.SplitDocAccessKey(k); pid == x.ID {
				delete(g.DocAccess, k)
				changed = true
			}
		}
		if changed {
			if err := s.Store.SaveAgent(org, g); err != nil {
				return err
			}
		}
	}
	return nil
}
