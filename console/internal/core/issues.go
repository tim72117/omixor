package core

import (
	"slices"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// Issue:型別是核心的 model.Issue / model.Comment(JSON 格式同 Swift 版 Issues/Issues.swift);
// 資料在核心(issues_remote.go:本機核心 + PostgreSQL,或遠端 omixor server)。
// 作者字串:person id、"mcp" 或 "agent:<角色 id>"。

const (
	MCPAuthor   = "mcp"
	AgentPrefix = "agent:"
)

func iaOneOf(v string, list []string) bool { return slices.Contains(list, v) }

// nowPrecise 現在時間(保留小數秒,排序用;存檔與輸出時只到秒,同 Swift)
func nowPrecise() Time { return Time{time.Now().UTC()} }

type issueState struct {
	Issues []model.Issue
	// Org 這份清單屬於哪個組織
	Org string
	// quiet 不是目前組織(透過 /api/issues/<組織> 操作別的組織):不記動態牆、不通知畫面
	quiet bool
	// Issue 伺服器模式(issues_remote.go):最近一次讀取的時間與錯誤
	fetched   time.Time
	remoteErr string
	// lastErr 最近一次核心錯誤的原型別(*service.Error、client.Error…):409 的 Data(current、currentVersion)要原樣帶給呼叫者,
	// 不能只剩字串。remoteErr 為空時視為沒有錯誤(見 IssueRemoteErr)
	lastErr error
	// gen 清單每變一次加一:背景讀取期間清單變過(新增、修改、同步讀取),讀回來的舊結果就丟掉
	gen int
}

func (a *App) issueSt() *issueState {
	if s, ok := a.Ext["issues"].(*issueState); ok {
		return s
	}
	s := &issueState{}
	a.Ext["issues"] = s
	a.loadIssues()
	return s
}

// Issues 全部 Issue(最新的在前;呼叫時要拿著 Mu)
func (a *App) Issues() []model.Issue { return a.issueSt().Issues }

// ReloadIssues 從存放處重讀目前組織的 Issue(呼叫時要拿著 Mu)
func (a *App) ReloadIssues() { a.loadIssues() }

func (a *App) loadIssues() {
	s := a.issueSt()
	s.Org = a.CurrentOrgID()
	if IsRemote() {
		// 遠端模式:不在拿著鎖時連網(伺服器連不上會卡住畫面),先清空,在背景讀回來再通知畫面
		s.Issues, s.fetched = []model.Issue{}, time.Time{}
		s.gen++
		go a.issueRemoteSync(s.Org, a.conns().Scope, s.gen)
		return
	}
	a.issueRemoteLoad(s) // 本機模式:核心在同一個程式裡(連本機 PostgreSQL),直接讀
}

// changedIssues 通知畫面(別的組織不用)
func (a *App) changedIssues() {
	if !a.issueSt().quiet {
		go a.Changed("issues")
	}
}

// logIssue 記到動態牆(別的組織不記:動態牆屬於目前組織)
func (a *App) logIssue(actor Actor, icon, text string) {
	if !a.issueSt().quiet {
		a.Log(actor, icon, text, "development")
	}
}

// ValidOrgID 組織 id 只能是英數字、- 與 _(1~100 字;同核心的 model.ValidOrgID)
func ValidOrgID(id string) bool { return model.ValidOrgID(id) }

// WithOrgIssues 在指定組織的 Issue 清單上執行 f(呼叫時要拿著 Mu)。org 是空的或目前組織時就是平常的清單;
// 其他組織從存放處讀一份來改、改完存回去,不記動態牆也不通知畫面。給 /api/issues/<組織>(Swift 版遠端存放)用。
func (a *App) WithOrgIssues(org string, f func()) {
	if org == "" || org == a.CurrentOrgID() {
		a.issueSt()
		f()
		return
	}
	cur := a.Ext["issues"]
	s := &issueState{Org: org, quiet: true}
	a.Ext["issues"] = s
	a.issueRemoteLoad(s)
	defer func() { a.Ext["issues"] = cur }()
	f()
}

// issueIs 這張 Issue 是不是編號 ref:內部 id 或目前的 key(不分大小寫;舊的 key 要問核心,見 Issue)
func issueIs(i model.Issue, ref string) bool {
	ref = strings.TrimSpace(ref)
	return ref != "" && (strings.EqualFold(i.ID, ref) || strings.EqualFold(i.Key, ref))
}

// issueIndex 清單裡編號 ref 的位置(內部 id 或目前的 key);沒有回 -1
func issueIndex(list []model.Issue, ref string) int {
	for k := range list {
		if issueIs(list[k], ref) {
			return k
		}
	}
	return -1
}

// Issue 依編號找:內部 id 或目前的 key(不分大小寫);清單裡沒有時問核心(舊的 key 透過別名表解析)。找不到回 nil
func (a *App) Issue(id string) *model.Issue {
	s := a.issueSt()
	if k := issueIndex(s.Issues, id); k >= 0 {
		c := s.Issues[k]
		return &c
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if b, err := a.issueBackend(); err == nil {
		if i, err := b.GetIssue(s.Org, id); err == nil && i != nil {
			c := normIssue(*i)
			return &c
		}
	}
	return nil
}

// AuthorActor 作者字串 → Actor(同 Swift 的 IssueStore.actor)
func AuthorActor(author string) Actor {
	if author == MCPAuthor {
		return MCPActor
	}
	if strings.HasPrefix(author, AgentPrefix) {
		return AI(strings.TrimPrefix(author, AgentPrefix))
	}
	return Person(author)
}

// ActorAuthor Actor → 作者字串(MCP 呼叫是 "mcp",畫面呼叫是使用者 id)
func ActorAuthor(actor Actor) string {
	if actor.Kind == "person" {
		return actor.ID
	}
	if actor.Kind == "ai" && actor.ID != MCPActorID && actor.ID != "" {
		return AgentPrefix + actor.ID
	}
	return MCPAuthor
}

// IssueAuthorName 同 Swift 的 IssueStore.authorName
func (a *App) IssueAuthorName(author string) string { return a.Name(AuthorActor(author)) }

// CleanLabels 去頭尾空白、去空、不分大小寫去重(同 Swift 的 IssueStore.clean;用核心的 model.CleanLabels)
func CleanLabels(labels []string) []string { return model.CleanLabels(labels) }

// CreateIssue 同 Swift 的 IssueStore.create(呼叫時要拿著 Mu)
func (a *App) CreateIssue(title, body, priority string, labels []string, source, link, author string) model.Issue {
	return a.CreateIssueFor(title, body, priority, labels, source, link, author, "", "")
}

// CreateIssueFor 同 CreateIssue,多一個指派的角色 id(空 = 不指派;有給核心會自動替該角色排任務)
// 與所屬倉庫 repo(空 = 不標記;格式不對核心回 400)
func (a *App) CreateIssueFor(title, body, priority string, labels []string, source, link, author, assignee, repo string) model.Issue {
	return a.CreateIssueIn(service.IssueInput{Title: title, Body: body, Priority: priority, Labels: labels,
		Source: source, Link: link, Author: author, Assignee: assignee, Repo: repo})
}

// CreateIssueIn 同 CreateIssueFor,用完整的輸入(多了 Project:放進的專案 id,空 = 組織層級;專案要存在而且有代號,否則核心回錯)
func (a *App) CreateIssueIn(in service.IssueInput) model.Issue {
	s := a.issueSt()
	in.Labels = CleanLabels(in.Labels)
	title, author := in.Title, in.Author
	issue, err := a.issueRemoteCreate(in)
	if err != nil {
		// 核心存不進去(伺服器連不上、沒有資料庫、欄位格式不對…):回傳沒有存起來的草稿(id 是空的),同 Swift
		a.issueRemoteFailed(err)
		return model.Issue{Title: in.Title, Body: in.Body, Priority: in.Priority, Labels: in.Labels, Repo: in.Repo, Project: in.Project, Status: "open", CreatedBy: in.Author, Comments: []model.Comment{}}
	}
	a.issueRemoteOK(s)
	s.gen++
	kept := []model.Issue{issue}
	for _, i := range s.Issues {
		if i.ID != issue.ID {
			kept = append(kept, i)
		}
	}
	s.Issues = kept
	a.logIssue(AuthorActor(author), "exclamationmark.bubble", "建立 "+issue.Display()+"「"+title+"」")
	a.changedIssues()
	if issue.Assignee != "" {
		go a.AgentsChanged() // 核心替角色排了任務
	}
	return issue
}

func cloneIssue(i model.Issue) model.Issue {
	i.Labels = append([]string{}, i.Labels...)
	i.Comments = append([]model.Comment{}, i.Comments...)
	return i
}

// UpdateIssue 修改一筆,回傳修改後的內容;狀態變了記到動態牆(同 Swift 的 IssueStore.update;呼叫時要拿著 Mu)。
// 只送有變的欄位給核心;change 裡在留言最後加的留言走核心的只附加路徑(見 issueRemoteUpdate)
func (a *App) UpdateIssue(id, author string, change func(*model.Issue)) *model.Issue {
	return a.UpdateIssueAppend(id, author, "", change)
}

// UpdateIssueAppend 同 UpdateIssue,另外在內文最後附加 appendBody(和現有內文空一行;交給核心在 Issue 的鎖裡
// 以最新內文附加,不會蓋掉同時進來的修改)。change 可以是 nil
func (a *App) UpdateIssueAppend(id, author, appendBody string, change func(*model.Issue)) *model.Issue {
	return a.updateIssue(id, author, appendBody, false, 0, change)
}

// UpdateIssueAtVersion 同 UpdateIssueAppend,另外帶呼叫者讀到的版本(version > 0 才檢查):核心的版本和它不同就回 409
// (回傳 nil,原因在 IssueRemoteErr:*service.Error,Data 有 current、currentVersion),不會悄悄蓋掉別人先做的修改。
// UI 的編輯畫面、API 的 If-Match/version、MCP 的 version 都走這條
func (a *App) UpdateIssueAtVersion(id, author, appendBody string, version int, change func(*model.Issue)) *model.Issue {
	return a.updateIssue(id, author, appendBody, false, version, change)
}

// UpdateIssueReplacingComments 同 UpdateIssue,但 change 改過的留言整份送給核心取代(保留給的留言 id 與時間)。
// 只給明確要「整份取代」的呼叫者用(/api/issues 給了 comments);一般新增留言用 CommentIssue
func (a *App) UpdateIssueReplacingComments(id, author string, change func(*model.Issue)) *model.Issue {
	return a.updateIssue(id, author, "", true, 0, change)
}

// UpdateIssueReplacingCommentsAtVersion 同 UpdateIssueReplacingComments,另外帶呼叫者讀到的版本(version > 0 才檢查,不同回 409)
func (a *App) UpdateIssueReplacingCommentsAtVersion(id, author string, version int, change func(*model.Issue)) *model.Issue {
	return a.updateIssue(id, author, "", true, version, change)
}

func (a *App) updateIssue(id, author, appendBody string, replaceComments bool, version int, change func(*model.Issue)) *model.Issue {
	s := a.issueSt()
	a.issueRemoteLoad(s) // 用核心裡最新的內容來改
	idx := issueIndex(s.Issues, id)
	if idx < 0 { // 舊的 key:問核心換成內部 id
		if i := a.Issue(id); i != nil {
			idx = issueIndex(s.Issues, i.ID)
		}
	}
	if idx < 0 {
		return nil
	}
	before := s.Issues[idx]
	n := cloneIssue(before)
	if change != nil {
		change(&n)
	}
	n.Labels = CleanLabels(n.Labels)
	statusChanged := n.Status != before.Status
	if statusChanged {
		if n.Status == "closed" {
			t := model.Time{Time: time.Now().UTC()}
			n.ClosedAt = &t
		} else {
			n.ClosedAt = nil
		}
	}
	// 狀態變了記到動態牆:存成功之後才記(伺服器模式存失敗時不能留下「關閉了」的紀錄)
	logStatus := func(i model.Issue) {
		if !statusChanged {
			return
		}
		if i.Status == "closed" {
			a.logIssue(AuthorActor(author), "checkmark.circle", "關閉 "+i.Display()+"「"+i.Title+"」")
		} else {
			a.logIssue(AuthorActor(author), "arrow.uturn.backward.circle", "重新打開 "+i.Display()+"「"+i.Title+"」")
		}
	}
	if appendBody == "" && string(Marshal(n)) == string(Marshal(before)) {
		return &n
	}
	saved, err := a.issueRemoteUpdate(n, before, author, appendBody, replaceComments, version)
	if err != nil {
		a.issueRemoteFailed(err)
		// 失敗前已經重讀過核心(版本衝突就是因為別處改過):畫面握著的是舊內容與舊版本,要通知它重讀,否則之後的動作都會 409
		a.changedIssues()
		return nil
	}
	a.issueRemoteOK(s)
	s.gen++
	s.Issues[idx] = saved
	logStatus(saved)
	a.changedIssues()
	if saved.Assignee != before.Assignee {
		go a.AgentsChanged() // 核心取消舊角色未領取的任務、替新角色排任務
	}
	return &saved
}

// CommentIssue 加留言;空白留言不做事(同 Swift 的 IssueStore.comment)。失敗回傳 nil(錯誤在 IssueRemoteError)
func (a *App) CommentIssue(id, text, author string) *model.Issue {
	if strings.TrimSpace(text) == "" {
		return a.Issue(id)
	}
	i, err := a.AddIssueComment(id, text, author)
	if err != nil {
		return nil
	}
	return i
}

// AddIssueComment 新增一則留言:走核心的只附加路徑(本機 service.AddIssueComment、遠端 POST …/issues/{id}/comments),
// 不讀整份留言再送回,同時多則留言不會互相蓋掉。空白留言回錯誤。呼叫時要拿著 Mu
func (a *App) AddIssueComment(id, text, author string) (*model.Issue, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil, UserError{Message: "留言不能是空的"}
	}
	s := a.issueSt()
	b, err := a.issueBackend()
	var i *model.Issue
	if err == nil {
		i, err = b.AddIssueComment(s.Org, id, t, author)
	}
	if err != nil {
		a.issueRemoteFailed(err)
		return nil, err
	}
	saved := normIssue(*i)
	a.issueRemoteOK(s)
	s.gen++
	found := false
	for k := range s.Issues {
		if strings.EqualFold(s.Issues[k].ID, saved.ID) {
			s.Issues[k], found = saved, true
			break
		}
	}
	if !found {
		a.issueRemoteLoad(s)
	}
	a.changedIssues()
	return &saved, nil
}

// DeleteIssue 把 Issue 移到垃圾桶(軟刪除,核心的 DeleteIssue;id 或 key 都可以)。失敗時原因在 IssueRemoteErr(呼叫時要拿著 Mu)
func (a *App) DeleteIssue(id string) { _, _ = a.trashIssue(id) }

// TrashIssueAs 移到垃圾桶並記到動態牆(actor 做的);回傳被刪的那筆。失敗回傳核心的原錯誤(找不到 404、沒權限 403、連線錯誤…),
// 原因同時在 IssueRemoteErr。呼叫時要拿著 Mu
func (a *App) TrashIssueAs(actor Actor, id string) (model.Issue, error) {
	gone, err := a.trashIssue(id)
	if err != nil {
		return gone, err
	}
	a.logIssue(actor, "trash", "將 "+gone.Display()+"「"+gone.Title+"」移到垃圾桶")
	return gone, nil
}

// trashIssue 移到垃圾桶並把本機清單裡的那筆拿掉;回傳被刪的那筆(找不到時只有 ID 欄位)。失敗回傳核心的原錯誤
func (a *App) trashIssue(id string) (model.Issue, error) {
	s := a.issueSt()
	gone := model.Issue{ID: id}
	if k := issueIndex(s.Issues, id); k >= 0 {
		gone = s.Issues[k]
	}
	if err := a.issueRemoteDelete(id); err != nil {
		a.issueRemoteFailed(err)
		return gone, err
	}
	a.issueRemoteOK(s)
	s.gen++
	kept := []model.Issue{}
	for _, i := range s.Issues {
		if !issueIs(i, id) && !(gone.ID != "" && i.ID == gone.ID) {
			kept = append(kept, i)
		}
	}
	s.Issues = kept
	a.changedIssues()
	return gone, nil
}

func init() {
	OnOrgLoad(func(a *App) {
		a.issueSt()
		a.loadIssues()
	})
	RegisterView("issues", func(a *App) any { return a.Issues() })
	// issue_repos 設定 Issue 所屬倉庫時的候選:選用模組 integrations 提供的 GitHub 追蹤的 repos 與 GitLab 追蹤的專案
	// (SetIssueRepoSource;沒編進模組時是空的);外加目前 Issue 已經用過的 repo
	RegisterView("issue_repos", func(a *App) any {
		gh, gl := []string{}, []string{}
		if issueRepoSource != nil {
			x, y := issueRepoSource(a)
			gh, gl = append(gh, x...), append(gl, y...)
		}
		used := []string{}
		seen := map[string]bool{}
		for _, i := range a.Issues() {
			if i.Repo != "" && !seen[strings.ToLower(i.Repo)] {
				seen[strings.ToLower(i.Repo)] = true
				used = append(used, i.Repo)
			}
		}
		return map[string]any{"github": gh, "gitlab": gl, "used": used}
	})
	RegisterAction("issue_delete", func(a *App, args map[string]any) (any, error) {
		// 移到垃圾桶(軟刪除;任何組織成員都可以)。還原、永久刪除是 issue_restore、issue_purge(組織管理員)
		id, _ := args["id"].(string)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if _, err := a.TrashIssueAs(UserActor(a, args), id); err != nil {
			return nil, trashErr(err)
		}
		return nil, nil
	})
	OnBadge(func(a *App) map[string]int {
		if !a.FeatureOn("development.issues") {
			return nil
		}
		n := 0
		for _, i := range a.Issues() {
			if i.Status == "open" && slices.Index(model.IssuePriorities, i.Priority) <= 1 { // urgent、high(不認得的也算,同 Swift 的 firstIndex ?? 0)
				n++
			}
		}
		return map[string]int{"development": n}
	})
}
