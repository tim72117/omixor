package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue(與組織、角色、文件櫃…)一律交給 omixor 核心(omixor-core)的共同介面 backend.Backend:
// 同一個程式裡的 service,連 PostgreSQL(OMIXOR_DATABASE_URL)。沒有資料庫就不能用(回 errNoDB),不退回存檔案。
// 測試改用 testcores.go 的 UseTestStores(連測試用 PostgreSQL)
//
// 讀:組織載入時讀、每次修改前重讀。
// 寫:每個動作各自送給核心,用回傳的內容。以「omixor-console」(伺服器管理員)身分呼叫核心;
// 網頁登入者的寫入改用本人身分(見 caller.go)。

// errNoDB 沒有資料庫
var errNoDB = errors.New("console 要連 PostgreSQL:設環境變數 OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY(本機開發可以用 console/docker-compose.dev.yml 開一個)")

// coreBackend 核心(service + PostgreSQL),系統身分。沒有資料庫回 errNoDB
func coreBackend() (backend.Backend, error) {
	if l := localCore(); l != nil {
		return authorBackend{l}, nil
	}
	return nil, errNoDB
}

// ---- 一次存多筆(Jira 匯入用) ----

// issueSave 一次改多筆、新增多筆(新增的由核心給編號,新的在前)。
// 直接用核心的存放處,保留 Jira 的建立時間與留言作者
func issueSave(who *Caller, org string, updates, creates []model.Issue) error {
	l, err := localCoreFor(who, org)
	if err != nil {
		return err
	}
	ensureLocalOrg(l, org)
	for _, i := range updates {
		if err := legacyIssueActors(l, org, &i); err != nil {
			return err
		}
		if err := l.Svc.Store.UpdateIssue(org, i); err != nil {
			return err
		}
	}
	for _, i := range creates {
		if err := legacyIssueActors(l, org, &i); err != nil {
			return err
		}
		if _, err := l.Svc.Store.CreateIssue(org, i); err != nil {
			return err
		}
	}
	return nil
}

// legacyIssueActors 直接寫 Store 之前,把 Issue 裡的「誰」從相容字串(Jira 的人名、核心輸出的 email、agent:<id>、舊的 mcp/tim…)
// 換成 actor id(Store 的欄位是 actor id,有外鍵):用寬鬆解析器 Store.ResolveLegacy(本機別名對到本機使用者;對不上的建「未註冊使用者」或 sys_legacy_*)
func legacyIssueActors(l *backend.Local, org string, i *model.Issue) error {
	st := l.Svc.Store
	conv := func(raw string) (string, error) {
		if strings.TrimSpace(raw) == "" {
			return "", nil
		}
		return st.ResolveLegacy(org, raw)
	}
	var err error
	if i.CreatedBy, err = conv(i.CreatedBy); err != nil {
		return err
	}
	if i.CreatedBy == "" { // 欄位不可為 NULL:沒署名的當本機使用者
		i.CreatedBy = l.P.Actor.ID
	}
	if i.DeletedBy, err = conv(i.DeletedBy); err != nil {
		return err
	}
	if i.Assignee != "" && !model.ValidActorID(i.Assignee) {
		if i.Assignee, err = conv(AgentPrefix + i.Assignee); err != nil {
			return err
		}
	}
	cs := make([]model.Comment, len(i.Comments))
	for k, c := range i.Comments {
		if c.Author, err = conv(c.Author); err != nil {
			return err
		}
		if c.Author == "" {
			c.Author = l.P.Actor.ID
		}
		cs[k] = c
	}
	i.Comments = cs
	return nil
}

func (a *App) issueRemoteSave(org string, updates, creates []model.Issue) error {
	return issueSave(a.caller, org, updates, creates)
}

// ---- 匯入 issues.json(omixor-web import / import-issues):交給本機核心的 service.ImportIssuesWith ----

// ImportIssues 把一份 Issue 清單匯入本機核心的 org(可以重跑):id 已經有的,overwrite=false 時略過(保留核心裡的版本),
// overwrite=true 時用清單的內容整筆取代;新匯入的依清單順序排在既有的後面(清單第一筆最新)。id 重複的略過;沒有 id 的照樣新增(id、key 由核心發,key 有給的保留)
func ImportIssues(org string, list []model.Issue, overwrite bool) (service.IssueImportResult, error) {
	res := service.IssueImportResult{Org: org}
	if !ValidOrgID(org) {
		return res, fmt.Errorf("組織 id 不對:%q", org)
	}
	l := localCore()
	if l == nil {
		return res, errors.New("沒有連資料庫")
	}
	ensureLocalOrg(l, org)
	r, err := l.Svc.ImportIssuesWith(l.P, org, list, service.IssueImportOptions{Merge: true, Overwrite: overwrite})
	if err != nil {
		return res, err
	}
	return *r, nil
}

// decodeIssueFile 讀 issues.json 的內容;沒寫 createdBy 的當作本機使用者建的(舊的 issues.json 是 MCP 建的,本機的 mcp 署名就是本機使用者)
func decodeIssueFile(b []byte) ([]model.Issue, error) {
	var list []model.Issue
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	var raw []struct {
		CreatedBy *string `json:"createdBy"`
	}
	_ = json.Unmarshal(b, &raw)
	for k := range list {
		if k < len(raw) && raw[k].CreatedBy == nil {
			list[k].CreatedBy = LocalIdentity
		}
	}
	return list, nil
}

// ImportIssuesPath 匯入 issues.json:path 是檔案(匯入到 org),或資料夾(匯入 orgs/<組織>/issues.json,org 不用給)
func ImportIssuesPath(path, org string, overwrite bool) ([]service.IssueImportResult, error) {
	if localCore() == nil {
		return nil, errors.New("沒有連資料庫")
	}
	var out []service.IssueImportResult
	one := func(file, org string) error {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		list, err := decodeIssueFile(b)
		if err != nil {
			return fmt.Errorf("%s 不是 Issue 清單:%w", file, err)
		}
		r, err := ImportIssues(org, list, overwrite)
		if err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}
	if strings.HasSuffix(path, ".json") {
		if org == "" {
			if _, err := localCore().Svc.Store.GetOrg(DefaultOrgID); err != nil { // 不自動建立預設組織
				return nil, fmt.Errorf("沒有 %s 組織:用 -org 指定要匯入到哪個組織", DefaultOrgID)
			}
			org = DefaultOrgID
		}
		return out, one(path, org)
	}
	matches, _ := filepath.Glob(filepath.Join(path, "orgs", "*", "issues.json"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s 底下沒有 orgs/<組織>/issues.json", path)
	}
	for _, m := range matches {
		o := filepath.Base(filepath.Dir(m))
		if org != "" && o != org {
			continue
		}
		if err := one(m, o); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ---- 輸出邊界:畫面、/api/issues 與 MCP 看到的 JSON 和以前一樣(labels、comments 一律是陣列;狀態、優先度是認得的值) ----

func normIssue(i model.Issue) model.Issue {
	if i.Labels == nil {
		i.Labels = []string{}
	}
	if i.Comments == nil {
		i.Comments = []model.Comment{}
	}
	if !model.OneOf(i.Status, model.IssueStatuses) {
		i.Status = "open"
	}
	if !model.OneOf(i.Priority, model.IssuePriorities) {
		i.Priority = "normal"
	}
	return i
}

func normIssues(l []model.Issue) []model.Issue {
	out := make([]model.Issue, 0, len(l))
	for _, i := range l {
		out = append(out, normIssue(i))
	}
	return out
}

// ---- App 用的操作(拿著 Mu 呼叫) ----

// IssueRemoteError 最近一次核心的錯誤(成功後清掉;拿著 Mu 呼叫)
func (a *App) IssueRemoteError() string { return a.issueSt().remoteErr }

// IssueRemoteErr 同 IssueRemoteError,但回傳原本的錯誤(沒有錯誤是 nil):409 版本衝突的 Data(current、currentVersion)
// 還在,呼叫者(MCP、web)可以原樣帶給用戶端。拿著 Mu 呼叫
func (a *App) IssueRemoteErr() error {
	s := a.issueSt()
	if s.remoteErr == "" {
		return nil
	}
	if s.lastErr != nil {
		return s.lastErr
	}
	return errors.New(s.remoteErr)
}

func (a *App) issueRemoteFailed(err error) {
	s := a.issueSt()
	if s.remoteErr != err.Error() {
		a.logCoreErr(s.Org, err.Error())
	}
	s.remoteErr, s.lastErr = err.Error(), err
}

// issueRemoteOK 核心操作成功:清掉上一次的錯誤(原錯誤也清,不讓 A 失敗的 409 殘留到 B 成功之後)
func (a *App) issueRemoteOK(s *issueState) { s.remoteErr, s.lastErr = "", nil }

// issueBackend Issue 讀取用的核心(系統身分)
func (a *App) issueBackend() (backend.Backend, error) { return coreBackend() }

// issueRemoteLoad 從核心讀 s.Org 的全部 Issue;失敗時保留原本的清單
func (a *App) issueRemoteLoad(s *issueState) {
	if s.Org == "" { // 還沒有組織
		s.Issues = []model.Issue{}
		return
	}
	b, err := a.issueBackend()
	var list []model.Issue
	if err == nil {
		list, err = b.ListIssues(s.Org)
	}
	if err != nil {
		a.issueRemoteFailed(err)
		if s.Issues == nil {
			s.Issues = []model.Issue{}
		}
		return
	}
	s.Issues, s.fetched = normIssues(list), time.Now()
	a.issueRemoteOK(s)
}

// RefreshIssuesIfStale 交給核心時:超過 maxAge 沒讀就重讀(拿著 Mu 呼叫)
func (a *App) RefreshIssuesIfStale(maxAge time.Duration) {
	s := a.issueSt()
	if time.Since(s.fetched) >= maxAge {
		a.issueRemoteLoad(s)
	}
}

func (a *App) issueRemoteCreate(in service.IssueInput) (model.Issue, error) {
	b, err := a.issueWriteBackend()
	if err != nil {
		return model.Issue{}, err
	}
	i, err := b.CreateIssue(a.issueSt().Org, in)
	if err != nil {
		return model.Issue{}, err
	}
	return normIssue(*i), nil
}

// issueRemoteUpdate 把 before → n 的變更送給核心:只送有變的欄位(沒變的不送,才不會蓋掉別處同時做的修改);
// appendBody 交給核心在 Issue 的鎖裡以最新內文附加。
// 留言:只是在最後新增的,一則一則走核心的只附加路徑(AddIssueComment;id 與時間由核心給);改了或刪了已有的留言,
// 或呼叫者要求整份取代(replaceComments,/api/issues 給了 comments)才整份送(omixor-web 對核心是伺服器管理員,整份取代)。
//
// version > 0:呼叫者讀到的版本(UI 的編輯畫面、API 的 If-Match/version、MCP 的 version),以它做樂觀鎖;
// 0 = 呼叫者沒給,不帶 version(規格:沒帶 version 照舊直接套用,核心不檢查)
func (a *App) issueRemoteUpdate(n, before model.Issue, author, appendBody string, replaceComments bool, version int) (model.Issue, error) {
	b, err := a.issueWriteBackend()
	if err != nil {
		return model.Issue{}, err
	}
	org := a.issueSt().Org
	patch, changed := issuePatchFor(n, before, author)
	if version > 0 {
		v := version
		patch.Version = &v
	}
	if appendBody != "" {
		patch.AppendBody, changed = &appendBody, true
	}
	added, replace := commentsDiff(before.Comments, n.Comments)
	if replaceComments && len(added) > 0 {
		added, replace = nil, true
	}
	if replace {
		cs := append([]model.Comment{}, n.Comments...)
		patch.Comments, changed = &cs, true
	}
	var cur *model.Issue
	if changed {
		i, err := b.UpdateIssue(org, n.ID, patch)
		if err != nil {
			return model.Issue{}, err
		}
		cur = i
	}
	for _, c := range added {
		who := c.Author
		if who == "" {
			who = author
		}
		i, err := b.AddIssueComment(org, n.ID, c.Text, who)
		if err != nil {
			return model.Issue{}, err
		}
		cur = i
	}
	if cur == nil { // 沒有東西要送(呼叫者已經擋掉了;保險起見回傳原本的)
		return before, nil
	}
	return normIssue(*cur), nil
}

// issuePatchFor 只放 before → n 有變的欄位(留言另外處理);changed 是有沒有任何欄位要送
func issuePatchFor(n, before model.Issue, author string) (p service.IssuePatch, changed bool) {
	p.Author = author
	str := func(dst **string, nv, ov string) {
		if nv != ov {
			v := nv
			*dst, changed = &v, true
		}
	}
	str(&p.Title, n.Title, before.Title)
	str(&p.Body, n.Body, before.Body)
	str(&p.Status, n.Status, before.Status)
	str(&p.Priority, n.Priority, before.Priority)
	str(&p.Source, n.Source, before.Source)
	str(&p.Link, n.Link, before.Link)
	str(&p.Assignee, n.Assignee, before.Assignee) // 指派變了才送(核心會取消舊角色未領取的任務、替新角色排任務)
	if n.Assignee == "" && before.Assignee == "" && before.AssigneeActor != nil && n.AssigneeActor == nil { // 取消人類接受者(字串本來就是空的,只有 assigneeActor):送 "" 讓核心走放回
		empty := ""
		p.Assignee, changed = &empty, true
	}
	str(&p.Repo, n.Repo, before.Repo)            // 所屬倉庫變了才送("" = 清除;格式由核心檢查)
	str(&p.Project, n.Project, before.Project)    // 搬專案(核心發新的 key,舊的留在別名表)
	if !slices.Equal(n.Labels, before.Labels) {
		l := append([]string{}, n.Labels...)
		p.Labels, changed = &l, true
	}
	return p, changed
}

// commentsDiff 比較留言:n 是 before 後面再加幾則時回傳加的那幾則;改了或刪了已有的留言時 replace = true
func commentsDiff(before, n []model.Comment) (added []model.Comment, replace bool) {
	if len(n) < len(before) {
		return nil, true
	}
	for k := range before {
		if string(Marshal(before[k])) != string(Marshal(n[k])) {
			return nil, true
		}
	}
	for _, c := range n[len(before):] {
		if strings.TrimSpace(c.Text) != "" {
			added = append(added, c)
		}
	}
	return added, false
}

func (a *App) issueRemoteDelete(id string) error {
	b, err := a.issueWriteBackend()
	if err != nil {
		return err
	}
	return b.DeleteIssue(a.issueSt().Org, id)
}
