package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"omixor-core/backend"
	"omixor-core/client"
	"omixor-core/env"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// Issue(與本機的組織、角色、文件櫃…)一律交給 omixor 核心(omixor-core)的共同介面 backend.Backend,依模式(mode.go)二選一,兩邊完全分開:
//
//	遠端模式:OMIXOR_SERVER 指向線上 omixor server,用 backend.Remote(core/client)讀寫
//	      token:OMIXOR_SERVER_TOKEN 固定的;沒設時用 omixor 自己的 token(鑰匙圈的 omixor-issues),
//	      沒有或被拒絕(401)就拿 evoleap 連線的 token 去 /api/auth/evoleap 換一個再重送(issueRemoteClient)
//	本機模式:backend.Local,核心的 service 跑在同一個程式裡,連本機的 PostgreSQL(OMIXOR_DATABASE_URL);
//	      沒有資料庫就不能用(回 errNoDB),不再退回存檔案。測試改用記憶體的核心(UseMemoryCores)
//
// 讀:組織載入時讀、每次修改前重讀;遠端模式每 5 秒在背景同步一次。
// 寫:每個動作各自送給核心,用回傳的內容。本機模式以「omixor-web」(伺服器管理員)身分呼叫核心。

const (
	issueRemoteEvoleapConn = "evoleap"
	issueRemoteTokenID     = "omixor-issues"
)

// errNoDB 本機模式沒有資料庫
var errNoDB = errors.New("本機模式要連 PostgreSQL:設環境變數 OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY(本機開發可以用 console/docker-compose.dev.yml 開一個)")

// coreBackend 目前的核心:遠端模式是 omixor server(scope 是換 token 時用哪個連線設定的 evoleap 登入);
// 本機模式是同一個程式裡的核心(service + PostgreSQL)。本機沒有資料庫回 errNoDB
func coreBackend(scope string) (backend.Backend, error) {
	if url := IssueRemoteURL(); url != "" {
		return &backend.Remote{Client: func() *client.Client { return issueRemoteClient(url, scope) }, OrgID: serverOrgID}, nil
	}
	if l := localCore(); l != nil {
		return l, nil
	}
	return nil, errNoDB
}

var (
	localCoreMu  sync.Mutex
	localCoreVal *backend.Local
	localCoreKey any // 建立核心時的資料庫連線池,換了就重建

	// newTestCore 沒有資料庫時改用的核心(測試用;UseMemoryCores 設定)。nil = 沒有資料庫就不能用
	newTestCore func() *backend.Local
)

// omixor-web 以伺服器管理員身分呼叫核心(畫面與 MCP 的權限由 omixor-web 自己管)
var localPrincipal = &model.Principal{User: model.User{ID: LocalIdentity, Email: LocalIdentity, Role: "admin"}}

const (
	// LocalIdentity 本機模式的身分(寫進任務的 claimedBy、頻道成員 user:<身分>、建立者等)
	LocalIdentity = "omixor-console"
	// legacyLocalIdentity 產品早期叫 Ops 時的本機身分;本機資料庫裡的舊值在啟動時改成 LocalIdentity(見 migrateLocalIdentity)
	legacyLocalIdentity = "ops-web"
)

// migrateLocalIdentity 把本機核心資料庫裡的舊身分 ops-web 改成 omixor-console。
// 只在本機模式、連自己的 PostgreSQL 建核心時跑(遠端模式不會走到這裡,不碰雲端);
// 冪等(沒有舊值就什麼都不改),失敗只記 log,不影響啟動。
func migrateLocalIdentity(st *store.PG) {
	n, err := st.RenameIdentity(legacyLocalIdentity, LocalIdentity)
	if err != nil {
		log.Printf("本機身分 %s 改成 %s 失敗(下次啟動再試):%v", legacyLocalIdentity, LocalIdentity, err)
		return
	}
	if n > 0 {
		log.Printf("本機身分 %s 已改成 %s(%d 筆資料)", legacyLocalIdentity, LocalIdentity, n)
	}
}

// localCore 本機模式的核心:連 PostgreSQL(OMIXOR_DATABASE_URL);沒有資料庫時用 newTestCore(測試)。都沒有回傳 nil
func localCore() *backend.Local {
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	if pgPool != nil {
		if localCoreVal == nil || localCoreKey != any(pgPool) {
			st, err := store.NewPGFromPool(pgPool)
			if err != nil {
				log.Printf("核心建表失敗:%v", err)
				return nil
			}
			migrateLocalIdentity(st)
			svc := &service.Service{Store: st}
			if err := svc.EnsureOrgsForIssues(); err != nil {
				log.Printf("補建組織失敗:%v", err)
			}
			localCoreVal, localCoreKey = &backend.Local{Svc: svc, P: localPrincipal, EnsureOrg: true}, any(pgPool)
		}
		return localCoreVal
	}
	if newTestCore != nil {
		return newTestCore()
	}
	return nil
}

// UseMemoryCores 沒有資料庫時改用記憶體的核心(每個資料夾 DataRoot 一份)。只給測試用(各測試套件的 TestMain 呼叫);
// 正式程式不呼叫,本機模式沒有資料庫就回 errNoDB
func UseMemoryCores() {
	cores := map[string]*backend.Local{}
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	newTestCore = func() *backend.Local { // 在 localCoreMu 裡呼叫
		k := DataRoot()
		if cores[k] == nil {
			cores[k] = &backend.Local{Svc: &service.Service{Store: store.NewMem()}, P: localPrincipal, EnsureOrg: true}
		}
		return cores[k]
	}
}

// ensureLocalOrg 組織還沒建就補建(omixor-web 的組織是自己管的;同 backend.Local 的 EnsureOrg)
func ensureLocalOrg(l *backend.Local, org string) {
	if _, err := l.Svc.Store.GetOrg(org); err != nil {
		_ = l.Svc.Store.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()})
	}
}

// ---- 遠端:token 提供者(給 backend.Remote 的 client) ----

var issueRemoteExchangeMu sync.Mutex

// issueRemoteToken 固定的 token,或這台伺服器存著的 omixor token(沒有就回 "")
func issueRemoteToken(base string) string {
	if t := env.Get("OMIXOR_SERVER_TOKEN", "OPS_ISSUE_SERVER_TOKEN"); t != "" {
		return t
	}
	if t := remoteToken(base); t != nil {
		return t.AccessToken
	}
	return ""
}

// issueRemoteClient 連 base 的 client:token 用 issueRemoteToken;沒設固定 token 時,401 用 scope 的 evoleap 登入換一個再重送
func issueRemoteClient(base, scope string) *client.Client {
	c := client.New(base)
	c.HTTP.Timeout = 8 * time.Second
	c.Token = func() string { return issueRemoteToken(base) }
	if env.Get("OMIXOR_SERVER_TOKEN", "OPS_ISSUE_SERVER_TOKEN") == "" {
		c.Refresh = func(stale string) (string, error) { return issueRemoteExchange(base, scope, stale) }
	}
	return c
}

// issueRemoteExchange 用 evoleap 連線的登入,向 omixor server 換一個 omixor 自己的 token(存進鑰匙圈)。
// 同時只換一次:別的請求剛換好(鑰匙圈裡已經有、而且不是 stale 那一把)就直接用
func issueRemoteExchange(base, scope, stale string) (string, error) {
	issueRemoteExchangeMu.Lock()
	defer issueRemoteExchangeMu.Unlock()
	if t := remoteToken(base); t != nil && t.AccessToken != "" && t.AccessToken != stale {
		return t.AccessToken, nil
	}
	evo := vaultTokens(scope, issueRemoteEvoleapConn)
	if evo == nil || evo.AccessToken == "" {
		evo = vaultTokens("", issueRemoteEvoleapConn) // 遠端組織自己沒有連線設定時,用預設組織的 evoleap 登入
	}
	if evo == nil || evo.AccessToken == "" {
		return "", errors.New("還沒登入 omixor server:請從左下角帳號區切到遠端模式,用 evoleap 登入")
	}
	c := client.New(base)
	c.HTTP.Timeout = 8 * time.Second
	res, err := c.LoginWithEvoleap(evo.AccessToken, clientLabel()+" 網頁版")
	if err != nil {
		if client.Status(err) == 0 {
			return "", err
		}
		return "", fmt.Errorf("omixor 拒絕登入:%v", err)
	}
	var exp *Time
	if res.ExpiresAt != nil {
		exp = &Time{res.ExpiresAt.Time}
	}
	setRemoteToken(base, &TokenSet{AccessToken: res.Token, ExpiresAt: exp, TokenType: strPtr("Bearer")})
	return res.Token, nil
}

// ---- 一次存多筆(Jira 匯入用) ----

// issueSave 一次改多筆、新增多筆(新增的由核心給編號,新的在前)。
// 本機:直接用核心的存放處,保留 Jira 的建立時間與留言作者;
// 遠端:一筆一筆送(伺服器的 API 沒有批次;新增的建立時間會是送出的時間)
func issueSave(scope, org string, updates, creates []model.Issue) error {
	b, err := coreBackend(scope)
	if err != nil {
		return err
	}
	if l, ok := b.(*backend.Local); ok {
		ensureLocalOrg(l, org)
		for _, i := range updates {
			if err := l.Svc.Store.UpdateIssue(org, i); err != nil {
				return err
			}
		}
		for _, i := range creates {
			if _, err := l.Svc.Store.CreateIssue(org, i); err != nil {
				return err
			}
		}
		return nil
	}
	for _, i := range updates {
		labels, comments := i.Labels, i.Comments
		if _, err := b.UpdateIssue(org, i.ID, service.IssuePatch{Title: &i.Title, Body: &i.Body, Status: &i.Status,
			Priority: &i.Priority, Labels: &labels, Source: &i.Source, Link: &i.Link, Repo: &i.Repo, Comments: &comments, Author: i.CreatedBy}); err != nil {
			return err
		}
	}
	for _, i := range creates {
		if _, err := b.CreateIssue(org, service.IssueInput{Title: i.Title, Body: i.Body, Priority: i.Priority,
			Labels: i.Labels, Source: i.Source, Link: i.Link, Repo: i.Repo, Author: i.CreatedBy}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) issueRemoteSave(org string, updates, creates []model.Issue) error {
	return issueSave(a.conns().Scope, org, updates, creates)
}

// ---- 匯入 issues.json(omixor-web import / import-issues):交給本機核心的 service.ImportIssuesWith ----

// ImportIssues 把一份 Issue 清單匯入本機核心的 org(可以重跑):id 已經有的,overwrite=false 時略過(保留核心裡的版本),
// overwrite=true 時用清單的內容整筆取代;新匯入的依清單順序排在既有的後面(清單第一筆最新)。沒有 id 或 id 重複的略過
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
	r, err := l.Svc.ImportIssuesWith(localPrincipal, org, list, service.IssueImportOptions{Merge: true, Overwrite: overwrite})
	if err != nil {
		return res, err
	}
	return *r, nil
}

// decodeIssueFile 讀 issues.json 的內容;沒寫 createdBy 的當作 MCP 建的(同舊的 issues.json 讀法)
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
			list[k].CreatedBy = MCPAuthor
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

func (a *App) issueRemoteFailed(err error) {
	s := a.issueSt()
	if s.remoteErr != err.Error() {
		log.Printf("Issue 伺服器:%v", err)
	}
	s.remoteErr = err.Error()
}

// issueBackend 目前組織用的核心(scope 是目前組織的連線設定)
func (a *App) issueBackend() (backend.Backend, error) { return coreBackend(a.conns().Scope) }

// issueRemoteLoad 從核心讀 s.Org 的全部 Issue;失敗時保留原本的清單
func (a *App) issueRemoteLoad(s *issueState) {
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
	s.Issues, s.remoteErr, s.fetched = normIssues(list), "", time.Now()
	s.gen++
}

// RefreshIssuesIfStale 交給核心時:超過 maxAge 沒讀就重讀(拿著 Mu 呼叫;存檔案時不做事)
func (a *App) RefreshIssuesIfStale(maxAge time.Duration) {
	s := a.issueSt()
	if time.Since(s.fetched) >= maxAge {
		a.issueRemoteLoad(s)
	}
}

func (a *App) issueRemoteCreate(title, body, priority string, labels []string, source, link, author, assignee, repo string) (model.Issue, error) {
	b, err := a.issueBackend()
	if err != nil {
		return model.Issue{}, err
	}
	i, err := b.CreateIssue(a.issueSt().Org, service.IssueInput{Title: title, Body: body, Priority: priority,
		Labels: labels, Source: source, Link: link, Author: author, Assignee: assignee, Repo: repo})
	if err != nil {
		return model.Issue{}, err
	}
	return normIssue(*i), nil
}

// issueRemoteUpdate 把 before → n 的變更送給核心:只送有變的欄位(沒變的不送,才不會蓋掉別處同時做的修改);
// appendBody 交給核心在 Issue 的鎖裡以最新內文附加。
// 留言:只是在最後新增的,一則一則走核心的只附加路徑(AddIssueComment;id 與時間由核心給);改了或刪了已有的留言,
// 或呼叫者要求整份取代(replaceComments,/api/issues 給了 comments)才整份送(omixor-web 對核心是伺服器管理員,整份取代)。
func (a *App) issueRemoteUpdate(n, before model.Issue, author, appendBody string, replaceComments bool) (model.Issue, error) {
	b, err := a.issueBackend()
	if err != nil {
		return model.Issue{}, err
	}
	org := a.issueSt().Org
	patch, changed := issuePatchFor(n, before, author)
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
	str(&p.Repo, n.Repo, before.Repo)             // 所屬倉庫變了才送("" = 清除;格式由核心檢查)
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
	b, err := a.issueBackend()
	if err != nil {
		return err
	}
	return b.DeleteIssue(a.issueSt().Org, id)
}

// issueRemoteSync 在鎖外讀 org 的 Issue,讀到就換上並通知畫面(組織換了或清單在這期間變過就丟掉)
func (a *App) issueRemoteSync(org, scope string, gen int) {
	b, err := coreBackend(scope)
	var list []model.Issue
	if err == nil {
		list, err = b.ListIssues(org)
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.issueSt()
	if s.Org != org || s.gen != gen {
		return
	}
	if err != nil {
		a.issueRemoteFailed(err)
		return
	}
	ops := normIssues(list)
	changed := string(Marshal(s.Issues)) != string(Marshal(ops))
	s.Issues, s.fetched, s.remoteErr = ops, time.Now(), ""
	s.gen++
	if changed {
		a.changedIssues()
	}
}

// 遠端模式:每 5 秒在背景重讀一次(網路請求在鎖外),別處(app、其他 omixor-web)的修改才看得到
func init() {
	OnStart(func(a *App) {
		if env.Get("OMIXOR_NO_TIMERS", "OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(5 * time.Second) {
				if !IsRemote() { // 本機模式只有這個程式在寫,不用輪詢
					continue
				}
				a.Mu.Lock()
				st := a.issueSt()
				org, scope, gen := st.Org, a.conns().Scope, st.gen
				a.Mu.Unlock()
				a.issueRemoteSync(org, scope, gen)
			}
		}()
	})
}
