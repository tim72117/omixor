package service

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// Issue 匯入(第一次搬資料、可重跑的 merge):從 issues.go 拆出,行為不變

// importIssue 整理匯入的一筆(strict 和 merge 共用):狀態、優先度、repo、標籤、留言、時間補預設;專案 id 轉成標準寫法;
// 垃圾桶欄位(沒有 deletedAt 就不帶 deletedBy)。id、key 的規則與驗證在存放處(store.prepareImport,和寫入同一個交易)
func (s *Service) importIssue(w *importWho, i model.Issue) (model.Issue, error) {
	i.ID = strings.TrimSpace(i.ID)
	i.Key = strings.ToUpper(strings.TrimSpace(i.Key))
	i.Project = model.NormalizeProjectID(i.Project)
	if !model.OneOf(i.Status, model.IssueStatuses) {
		i.Status = "open"
	}
	if !model.OneOf(i.Priority, model.IssuePriorities) {
		i.Priority = "normal"
	}
	r, ok := model.CleanRepo(i.Repo)
	if !ok {
		return i, fail(400, "%s 的 repo 格式不對:%s", importRef(i), i.Repo)
	}
	i.Repo = r
	i.Labels = model.CleanLabels(i.Labels)
	if i.CreatedAt.IsZero() {
		i.CreatedAt = s.mt()
	}
	if i.UpdatedAt.IsZero() {
		i.UpdatedAt = i.CreatedAt
	}
	if i.DeletedAt != nil && i.DeletedAt.IsZero() {
		i.DeletedAt = nil
	}
	if i.DeletedAt == nil {
		i.DeletedBy = ""
	}
	if i.Version < 0 {
		i.Version = 0
	}
	cs := make([]model.Comment, 0, len(i.Comments))
	for _, c := range i.Comments {
		if c.ID == "" {
			c.ID = uuid()
		}
		if c.At.IsZero() {
			c.At = i.UpdatedAt
		}
		cs = append(cs, c)
	}
	i.Comments = cs
	// 建立者、指派、刪除者、留言作者的舊字串 → actor id(伺服器管理員寬鬆;組織管理員只能署名本組織成員、角色、系統與自己)
	if err := s.importIssueActors(w, &i); err != nil {
		return i, err
	}
	return i, nil
}

// importRef 錯誤訊息裡指這一筆的寫法
func importRef(i model.Issue) string {
	for _, v := range []string{i.Key, i.ID, i.Title} {
		if v != "" {
			return v
		}
	}
	return "(沒有 key、id 與標題)"
}

// importStoreErr 存放處匯入的錯誤 → 給使用者看的(*store.ImportError 照它的狀態碼:400 資料有問題、409 衝突)
func importStoreErr(err error) error {
	var ie *store.ImportError
	if errors.As(err, &ie) {
		return fail(ie.Code, "%s", ie.Msg)
	}
	return err
}

// importKeyOf 重複檢查用的 id(不分大小寫);沒有 id 回 ""
func importKeyOf(id string) string { return strings.ToUpper(id) }

// ImportIssues 第一次搬資料用(組織管理員):這個組織已經有 Issue 就拒絕(409)。之後一律單筆新增或修改,不做差異比對與同步。
// 每一筆只要有 title;id、key 都可以不給(伺服器發新的 UUIDv7 與 key),規則:
//   - id 是 UUID:保留;不是(舊格式 ISS-<n>、gh-1…)或沒給:發新的 UUIDv7,舊 id 進別名表仍查得到。
//   - key:有給的檢查格式(組織層級 ISS-<n>;專案 <代號>-<n>,代號要等於該專案目前的代號),不重複,序號推進到最大號碼;
//     沒給的依 project 發新的(舊格式 id 是組織層級的 ISS-<n> 時當作 key)。
//   - project 要存在(400,訊息指出是哪一筆);專案沒有代號只能不指派專案。
//   - deletedAt、deletedBy(垃圾桶)與 version(>0 保留,否則 1)照原樣寫入。
//
// 整批在同一個交易:任何一筆有問題都不寫入
func (s *Service) ImportIssues(p *model.Principal, org string, list []model.Issue) (*model.ImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fail(400, "沒有要匯入的 Issue")
	}
	seen := map[string]bool{}
	keep := make([]model.Issue, 0, len(list))
	who := s.newImportWho(p, org)
	for _, i := range list {
		i, err := s.importIssue(who, i)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(i.Title) == "" {
			return nil, fail(400, "每一筆都要有 title(%s)", importRef(i))
		}
		if i.ID != "" {
			if seen[importKeyOf(i.ID)] {
				return nil, fail(400, "id 重複:%s", i.ID)
			}
			seen[importKeyOf(i.ID)] = true
		}
		keep = append(keep, i)
	}
	cur, err := s.Store.ListIssues(org)
	if err != nil {
		return nil, err
	}
	if len(cur) > 0 {
		return nil, fail(409, "伺服器上 %s 已經有 %d 筆 Issue;匯入只在第一次搬資料時用,之後請用單筆新增或修改", org, len(cur))
	}
	n, err := s.Store.ImportIssues(org, keep)
	if err != nil {
		var ie *store.ImportError
		if errors.As(err, &ie) {
			return nil, importStoreErr(err)
		}
		return nil, fail(409, "%v", err)
	}
	s.Audit(p, "issues.import", org, map[string]any{"inserted": n})
	return &model.ImportResult{Org: org, Inserted: n}, nil
}

// IssueImportOptions ImportIssuesWith 的選項
type IssueImportOptions struct {
	// Merge 可以重跑的匯入(搬舊資料、omixor-web import 用):組織已經有 Issue 也照樣匯入(不回 409);
	// id 重複(不分大小寫,後面的)的略過、算進 Skipped(不回 400);title 可以是空的(照原資料搬)。
	// 先找組織裡是不是已經有同一個 Issue(id、key、別名對得到;沒給 id 或舊格式 id 時以 key 為準),有的看 Overwrite;
	// 沒有的當新的:保留 id(UUID)、key、專案、作者、時間與垃圾桶狀態,沒給 id、key 的由伺服器發,依 list 的順序排在既有的後面。
	// 沒有 id 也沒有 key 的每次都會新增(沒有東西可以認出是同一筆,重跑會重複)
	Merge bool
	// Overwrite Merge 時已經有的用匯入的內容整筆取代(位置、key、專案、號碼、垃圾桶狀態不動、留言整份取代);false 是略過(保留現有的)
	Overwrite bool
}

// IssueImportResult ImportIssuesWith 的結果
type IssueImportResult struct {
	Org      string `json:"org"`
	Inserted int    `json:"inserted"`
	Updated  int    `json:"updated"`
	Skipped  int    `json:"skipped"`
}

// ImportIssuesWith 匯入 Issue(組織管理員)。opt.Merge 是 false 時同 ImportIssues(第一次搬資料;組織已經有 Issue 回 409)。
// 驗證與 id、key 規則同 ImportIssues;key 和組織裡別的 Issue 衝突回 409,整批不寫入
func (s *Service) ImportIssuesWith(p *model.Principal, org string, list []model.Issue, opt IssueImportOptions) (*IssueImportResult, error) {
	if !opt.Merge {
		r, err := s.ImportIssues(p, org, list)
		if err != nil {
			return nil, err
		}
		return &IssueImportResult{Org: org, Inserted: r.Inserted}, nil
	}
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	res := &IssueImportResult{Org: org}
	seen := map[string]bool{}
	keep := []model.Issue{}
	who := s.newImportWho(p, org)
	for _, i := range list {
		i, err := s.importIssue(who, i)
		if err != nil {
			return nil, err
		}
		if i.ID != "" {
			if seen[importKeyOf(i.ID)] {
				res.Skipped++
				continue
			}
			seen[importKeyOf(i.ID)] = true
		}
		keep = append(keep, i)
	}
	if len(keep) > 0 {
		n, err := s.Store.MergeIssues(org, keep, opt.Overwrite)
		if err != nil {
			return nil, importStoreErr(err)
		}
		res.Inserted, res.Updated, res.Skipped = n.Inserted, n.Updated, res.Skipped+n.Skipped
	}
	s.Audit(p, "issues.import", org, map[string]any{"inserted": res.Inserted, "updated": res.Updated, "skipped": res.Skipped, "merge": true})
	return res, nil
}
