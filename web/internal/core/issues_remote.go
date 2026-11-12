package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// Issue(與本機的組織)一律交給 omixor 核心(omixor-core),依模式(mode.go)二選一,兩邊完全分開:
//
//	遠端模式:OMIXOR_SERVER 指向線上 omixor server,用 core/client 讀寫
//	      token:OPS_ISSUE_SERVER_TOKEN 固定的;沒設時用 omixor 自己的 token(鑰匙圈的 omixor-issues),
//	      沒有或被拒絕(401)就拿 evoleap 連線的 token 去 /api/auth/evoleap 換一個再重送
//	本機模式:核心的 service 跑在同一個程式裡,連本機的 PostgreSQL(OPS_DATABASE_URL);
//	      沒有資料庫就不能用(回 errNoDB),不再退回存檔案。測試時沒有資料庫就用核心的記憶體存放處
//
// 讀:組織載入時讀、每次修改前重讀;遠端模式每 5 秒在背景同步一次。
// 寫:每個動作各自送給核心,用回傳的內容。本機模式以「omixor-web」(伺服器管理員)身分呼叫核心。

const (
	issueRemoteEvoleapConn = "evoleap"
	issueRemoteTokenID     = "omixor-issues"
)

// issueBackend 核心的 Issue 操作(遠端或內建)
type issueBackend interface {
	List(scope, org string) ([]model.Issue, error)
	Create(scope, org string, in service.IssueInput) (*model.Issue, error)
	Update(scope, org, id string, p service.IssuePatch) (*model.Issue, error)
	Delete(scope, org, id string) error
}

var (
	backendOnce sync.Once
	backendVal  issueBackend
	backendMu   sync.Mutex
)

// errNoDB 本機模式沒有資料庫
var errNoDB = errors.New("本機模式要連 PostgreSQL:設環境變數 OPS_DATABASE_URL 與 OPS_SECRET_KEY(本機開發可以用 web/docker-compose.dev.yml 開一個)")

// currentIssueBackend 目前的核心:遠端模式是 omixor server;本機模式是同一個程式裡的核心(service + PostgreSQL)。
// 本機沒有資料庫時回傳一個一律回錯誤的 backend(不再退回存檔案)。
func currentIssueBackend() issueBackend {
	if url := IssueRemoteURL(); url != "" {
		backendMu.Lock()
		defer backendMu.Unlock()
		if r, ok := backendVal.(*remoteBackend); !ok || r.base != url {
			backendVal = &remoteBackend{base: url}
		}
		return backendVal
	}
	if l := localCore(); l != nil {
		return l
	}
	return noDBBackend{}
}

var (
	localCoreMu  sync.Mutex
	localCoreVal *localBackend
	localCoreKey any // 建立核心時的資料庫連線池(或記憶體資料夾),換了就重建
	memCores     = map[string]*localBackend{}
)

// localCore 本機模式的核心:連 PostgreSQL(OPS_DATABASE_URL);測試時沒有資料庫就用記憶體(每個資料夾一份)。都沒有回傳 nil
func localCore() *localBackend {
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	if pgPool != nil {
		if localCoreVal == nil || localCoreKey != any(pgPool) {
			st, err := store.NewPGFromPool(pgPool)
			if err != nil {
				log.Printf("核心建表失敗:%v", err)
				return nil
			}
			svc := &service.Service{Store: st}
			if err := svc.EnsureOrgsForIssues(); err != nil {
				log.Printf("補建組織失敗:%v", err)
			}
			localCoreVal, localCoreKey = &localBackend{svc: svc}, any(pgPool)
		}
		return localCoreVal
	}
	if testing.Testing() {
		k := DataRoot()
		if memCores[k] == nil {
			memCores[k] = &localBackend{svc: &service.Service{Store: store.NewMem()}}
		}
		return memCores[k]
	}
	return nil
}

// noDBBackend 本機模式沒有資料庫:所有操作都回錯誤
type noDBBackend struct{}

func (noDBBackend) List(string, string) ([]model.Issue, error) { return nil, errNoDB }
func (noDBBackend) Create(string, string, service.IssueInput) (*model.Issue, error) {
	return nil, errNoDB
}
func (noDBBackend) Update(string, string, string, service.IssuePatch) (*model.Issue, error) {
	return nil, errNoDB
}
func (noDBBackend) Delete(string, string, string) error { return errNoDB }

// ---- 遠端:core/client ----

type remoteBackend struct{ base string }

var issueRemoteExchangeMu sync.Mutex

// issueRemoteToken 固定的 token,或存著的 omixor token(沒有就回 "")
func issueRemoteToken(scope string) string {
	if t := os.Getenv("OPS_ISSUE_SERVER_TOKEN"); t != "" {
		return t
	}
	if t := vaultTokens(remoteTokenScope, issueRemoteTokenID); t != nil {
		return t.AccessToken
	}
	return ""
}

func (r *remoteBackend) client(scope string) *client.Client {
	c := client.New(r.base)
	c.HTTP.Timeout = 8 * time.Second
	c.Token = func() string { return issueRemoteToken(scope) }
	if os.Getenv("OPS_ISSUE_SERVER_TOKEN") == "" {
		c.Refresh = func(stale string) (string, error) { return issueRemoteExchange(r.base, scope, stale) }
	}
	return c
}

// issueRemoteExchange 用 evoleap 連線的登入,向 omixor server 換一個 omixor 自己的 token(存進鑰匙圈)。
// 同時只換一次:別的請求剛換好(鑰匙圈裡已經有、而且不是 stale 那一把)就直接用
func issueRemoteExchange(base, scope, stale string) (string, error) {
	issueRemoteExchangeMu.Lock()
	defer issueRemoteExchangeMu.Unlock()
	if t := vaultTokens(remoteTokenScope, issueRemoteTokenID); t != nil && t.AccessToken != "" && t.AccessToken != stale {
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
	vaultSetTokens(remoteTokenScope, issueRemoteTokenID, &TokenSet{AccessToken: res.Token, ExpiresAt: exp, TokenType: strPtr("Bearer")})
	return res.Token, nil
}

// 組織 id 是內部的(遠端組織加了 "~" 前綴),送給伺服器前換回伺服器上的 id
func (r *remoteBackend) List(scope, org string) ([]model.Issue, error) {
	return r.client(scope).ListIssues(serverOrgID(org))
}
func (r *remoteBackend) Create(scope, org string, in service.IssueInput) (*model.Issue, error) {
	return r.client(scope).CreateIssue(serverOrgID(org), in)
}
func (r *remoteBackend) Update(scope, org, id string, p service.IssuePatch) (*model.Issue, error) {
	return r.client(scope).UpdateIssue(serverOrgID(org), id, p)
}
func (r *remoteBackend) Delete(scope, org, id string) error {
	return r.client(scope).DeleteIssue(serverOrgID(org), id)
}

// ---- 內建:核心 service 跑在同一個程式 ----

type localBackend struct{ svc *service.Service }

// omixor-web 以伺服器管理員身分呼叫核心(畫面與 MCP 的權限由 omixor-web 自己管)
var localPrincipal = &model.Principal{User: model.User{ID: "ops-web", Email: "ops-web", Role: "admin"}}

// ensureOrg 組織還沒建就補建(omixor-web 的組織是自己管的)
func (l *localBackend) ensureOrg(org string) {
	if _, err := l.svc.Store.GetOrg(org); err != nil {
		_ = l.svc.Store.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()})
	}
}

func (l *localBackend) List(_, org string) ([]model.Issue, error) {
	l.ensureOrg(org)
	return l.svc.ListIssues(localPrincipal, org)
}
func (l *localBackend) Create(_, org string, in service.IssueInput) (*model.Issue, error) {
	l.ensureOrg(org)
	return l.svc.CreateIssue(localPrincipal, org, in)
}
func (l *localBackend) Update(_, org, id string, p service.IssuePatch) (*model.Issue, error) {
	l.ensureOrg(org)
	return l.svc.UpdateIssue(localPrincipal, org, id, p)
}
func (l *localBackend) Delete(_, org, id string) error {
	l.ensureOrg(org)
	return l.svc.DeleteIssue(localPrincipal, org, id)
}

// ---- 一次存多筆(Jira 匯入用) ----

// issueSaver 一次改多筆、新增多筆(新增的由核心給編號,新的在前)
type issueSaver interface {
	Save(scope, org string, updates, creates []model.Issue) error
}

// Save 本機:直接用核心的存放處,保留 Jira 的建立時間與留言作者
func (l *localBackend) Save(_, org string, updates, creates []model.Issue) error {
	l.ensureOrg(org)
	for _, i := range updates {
		if err := l.svc.Store.UpdateIssue(org, i); err != nil {
			return err
		}
	}
	for _, i := range creates {
		if _, err := l.svc.Store.CreateIssue(org, i); err != nil {
			return err
		}
	}
	return nil
}

// Save 遠端:一筆一筆送(伺服器的 API 沒有批次;新增的建立時間會是送出的時間)
func (r *remoteBackend) Save(scope, org string, updates, creates []model.Issue) error {
	c := r.client(scope)
	org = serverOrgID(org)
	for _, i := range updates {
		labels, comments := i.Labels, i.Comments
		if _, err := c.UpdateIssue(org, i.ID, service.IssuePatch{Title: &i.Title, Body: &i.Body, Status: &i.Status,
			Priority: &i.Priority, Labels: &labels, Source: &i.Source, Link: &i.Link, Comments: &comments, Author: i.CreatedBy}); err != nil {
			return err
		}
	}
	for _, i := range creates {
		if _, err := c.CreateIssue(org, service.IssueInput{Title: i.Title, Body: i.Body, Priority: i.Priority,
			Labels: i.Labels, Source: i.Source, Link: i.Link, Author: i.CreatedBy}); err != nil {
			return err
		}
	}
	return nil
}

func (noDBBackend) Save(string, string, []model.Issue, []model.Issue) error { return errNoDB }

func toModelIssues(l []OpsIssue) []model.Issue {
	b, _ := json.Marshal(l)
	var out []model.Issue
	_ = json.Unmarshal(b, &out)
	return out
}

func (a *App) issueRemoteSave(org string, updates, creates []OpsIssue) error {
	sv, ok := currentIssueBackend().(issueSaver)
	if !ok {
		return errors.New("這個模式不能一次存多筆")
	}
	return sv.Save(a.conns().Scope, org, toModelIssues(updates), toModelIssues(creates))
}

// ---- 型別轉換(JSON 格式相同) ----

func toOps(i model.Issue) OpsIssue {
	b, _ := json.Marshal(i)
	var o OpsIssue
	_ = json.Unmarshal(b, &o)
	return o
}

func toOpsList(l []model.Issue) []OpsIssue {
	out := []OpsIssue{}
	for _, i := range l {
		out = append(out, toOps(i))
	}
	return out
}

func toModelComments(cs []IssueComment) []model.Comment {
	b, _ := json.Marshal(cs)
	var out []model.Comment
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = []model.Comment{}
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

// issueRemoteLoad 從核心讀 s.Org 的全部 Issue;失敗時保留原本的清單
func (a *App) issueRemoteLoad(s *issueState) {
	b := currentIssueBackend()
	if b == nil {
		return
	}
	list, err := b.List(a.conns().Scope, s.Org)
	if err != nil {
		a.issueRemoteFailed(err)
		if s.Issues == nil {
			s.Issues = []OpsIssue{}
		}
		return
	}
	s.Issues, s.remoteErr, s.fetched = toOpsList(list), "", time.Now()
	s.gen++
}

// RefreshIssuesIfStale 交給核心時:超過 maxAge 沒讀就重讀(拿著 Mu 呼叫;存檔案時不做事)
func (a *App) RefreshIssuesIfStale(maxAge time.Duration) {
	s := a.issueSt()
	if time.Since(s.fetched) >= maxAge {
		a.issueRemoteLoad(s)
	}
}

func (a *App) issueRemoteCreate(title, body, priority string, labels []string, source, link, author string) (OpsIssue, error) {
	b := currentIssueBackend()
	i, err := b.Create(a.conns().Scope, a.issueSt().Org, service.IssueInput{Title: title, Body: body, Priority: priority,
		Labels: labels, Source: source, Link: link, Author: author})
	if err != nil {
		return OpsIssue{}, err
	}
	return toOps(*i), nil
}

func (a *App) issueRemoteUpdate(n OpsIssue, author string) (OpsIssue, error) {
	b := currentIssueBackend()
	labels, comments := n.Labels, toModelComments(n.Comments)
	i, err := b.Update(a.conns().Scope, a.issueSt().Org, n.ID, service.IssuePatch{Title: &n.Title, Body: &n.Body, Status: &n.Status,
		Priority: &n.Priority, Labels: &labels, Source: &n.Source, Link: &n.Link, Comments: &comments, Author: author})
	if err != nil {
		return OpsIssue{}, err
	}
	return toOps(*i), nil
}

func (a *App) issueRemoteDelete(id string) error {
	return currentIssueBackend().Delete(a.conns().Scope, a.issueSt().Org, id)
}

// issueRemoteSync 在鎖外讀 org 的 Issue,讀到就換上並通知畫面(組織換了或清單在這期間變過就丟掉)
func (a *App) issueRemoteSync(org, scope string, gen int) {
	b := currentIssueBackend()
	if b == nil {
		return
	}
	list, err := b.List(scope, org)
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
	ops := toOpsList(list)
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
		if os.Getenv("OPS_NO_TIMERS") == "1" {
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
