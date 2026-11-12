package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// Issue 交給 omixor 核心(omixor-core)。ops-web 啟動時二選一:
//
//	遠端:OMIXOR_SERVER(或舊的 OPS_ISSUE_SERVER)指向線上 omixor server,用 omixor-core/client 讀寫
//	      token:OPS_ISSUE_SERVER_TOKEN 固定的;沒設時用 omixor 自己的 token(鑰匙圈的 omixor-issues),
//	      沒有或被拒絕(401)就拿 evoleap 連線的 token 去 /api/auth/evoleap 換一個再重送
//	內建:有 OPS_DATABASE_URL(PostgreSQL)時,核心的 service 跑在同一個程式裡,用同一個資料庫
//	都沒有:照舊存組織資料夾的 issues.json(測試、跨版比對用)
//
// 讀:組織載入時在背景讀、每次修改前、ops_issues 查詢前重讀;遠端模式每 5 秒在背景同步一次。
// 寫:每個動作各自送給核心,用回傳的內容。內建模式以「ops-web」(伺服器管理員)身分呼叫核心。

const (
	issueRemoteEvoleapConn = "evoleap"
	issueRemoteTokenID     = "omixor-issues"
)

// IssueRemoteURL 設定的 omixor server(沒設回傳 "")
func IssueRemoteURL() string {
	for _, k := range []string{"OMIXOR_SERVER", "OPS_ISSUE_SERVER"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return ""
}

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

// issueRemoteOn 有沒有交給核心(遠端或內建)
func issueRemoteOn() bool { return currentIssueBackend() != nil }

func currentIssueBackend() issueBackend {
	backendMu.Lock()
	defer backendMu.Unlock()
	if url := IssueRemoteURL(); url != "" {
		if r, ok := backendVal.(*remoteBackend); !ok || r.base != url {
			backendVal = &remoteBackend{base: url}
		}
		return backendVal
	}
	if pgPool != nil {
		if _, ok := backendVal.(*localBackend); !ok {
			st, err := store.NewPGFromPool(pgPool)
			if err != nil {
				log.Printf("核心建表失敗,Issue 改存檔案:%v", err)
				backendVal = nil
				return nil
			}
			svc := &service.Service{Store: st}
			if err := svc.EnsureOrgsForIssues(); err != nil {
				log.Printf("補建組織失敗:%v", err)
			}
			backendVal = &localBackend{svc: svc}
		}
		return backendVal
	}
	backendVal = nil
	return nil
}

// ---- 遠端:omixor-core/client ----

type remoteBackend struct{ base string }

var issueRemoteExchangeMu sync.Mutex

// issueRemoteToken 固定的 token,或存著的 omixor token(沒有就回 "")
func issueRemoteToken(scope string) string {
	if t := os.Getenv("OPS_ISSUE_SERVER_TOKEN"); t != "" {
		return t
	}
	if t := vaultTokens(scope, issueRemoteTokenID); t != nil {
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
	if t := vaultTokens(scope, issueRemoteTokenID); t != nil && t.AccessToken != "" && t.AccessToken != stale {
		return t.AccessToken, nil
	}
	evo := vaultTokens(scope, issueRemoteEvoleapConn)
	if evo == nil || evo.AccessToken == "" {
		return "", errors.New("還沒登入 evoleap 連線:請在 設定 › 連線 登入「evoleap 正式站」,omixor 會用它證明身分")
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
	vaultSetTokens(scope, issueRemoteTokenID, &TokenSet{AccessToken: res.Token, ExpiresAt: exp, TokenType: strPtr("Bearer")})
	return res.Token, nil
}

func (r *remoteBackend) List(scope, org string) ([]model.Issue, error) { return r.client(scope).ListIssues(org) }
func (r *remoteBackend) Create(scope, org string, in service.IssueInput) (*model.Issue, error) {
	return r.client(scope).CreateIssue(org, in)
}
func (r *remoteBackend) Update(scope, org, id string, p service.IssuePatch) (*model.Issue, error) {
	return r.client(scope).UpdateIssue(org, id, p)
}
func (r *remoteBackend) Delete(scope, org, id string) error { return r.client(scope).DeleteIssue(org, id) }

// ---- 內建:核心 service 跑在同一個程式 ----

type localBackend struct{ svc *service.Service }

// ops-web 以伺服器管理員身分呼叫核心(畫面與 MCP 的權限由 ops-web 自己管)
var localPrincipal = &model.Principal{User: model.User{ID: "ops-web", Email: "ops-web", Role: "admin"}}

// ensureOrg 組織還沒建就補建(ops-web 的組織是自己管的)
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
	if !issueRemoteOn() {
		return
	}
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

// 遠端模式:每 5 秒在背景重讀一次(網路請求在鎖外),別處(app、其他 ops-web)的修改才看得到
func init() {
	OnStart(func(a *App) {
		if IssueRemoteURL() == "" || os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(5 * time.Second) {
				a.Mu.Lock()
				st := a.issueSt()
				org, scope, gen := st.Org, a.conns().Scope, st.gen
				a.Mu.Unlock()
				a.issueRemoteSync(org, scope, gen)
			}
		}()
	})
}
