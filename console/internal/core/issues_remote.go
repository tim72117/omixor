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
	"omixor-core/config"
	"omixor-core/httperr"
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
//	      沒有資料庫就不能用(回 errNoDB),不再退回存檔案。測試改用 testcores.go 的 UseTestStores(連測試用 PostgreSQL)
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
		return authorBackend{&backend.Remote{Client: func() *client.Client { return issueRemoteClient(url, scope) }, OrgID: serverOrgID}}, nil
	}
	if l := localCore(); l != nil {
		return authorBackend{l}, nil
	}
	return nil, errNoDB
}

var (
	localCoreMu  sync.Mutex
	localCoreVal *backend.Local
	localCoreKey any // 建立核心時的資料庫連線池,換了就重建

	// newTestCore 沒有資料庫時改用的核心(測試用;UseTestStores 設定)。nil = 沒有資料庫就不能用
	newTestCore func() *backend.Local
)

const (
	// LocalIdentity 本機使用者的 handle(固定,不可設定;ISS-36 D4):console 本機模式的操作者就是這位使用者(伺服器管理員),
	// 寫進任務領取者、頻道成員、建立者、留言與稽核的「誰」都是它的 actor
	LocalIdentity = "omixor-console"
	// localUserKey 本機使用者那一列的固定 evoleap_id(辨識「本機建立的那一列」;一般登入的 evoleap id 沒有這個前綴、LDAP 是 ldap:)
	localUserKey = "local:" + LocalIdentity
	// legacyLocalIdentity 產品早期叫 Ops 時的本機身分(舊資料裡的字串;遷移時對到本機使用者)
	legacyLocalIdentity = "ops-web"
	// legacyLocalPerson 範例資料的人員 id(App.Me;舊資料的建立者、留言作者常是它)
	legacyLocalPerson = "tim"
)

// LocalAliases 舊資料裡會對到本機使用者的字串(遷移與寬鬆解析器用;mcp 由遷移規則內建)
func LocalAliases() []string { return []string{legacyLocalIdentity, legacyLocalPerson, MCPAuthor} }

// ConfigureLocalIdentity 在建 store(NewPG、NewPGFromPool)之前告訴遷移本機使用者是誰:
// 舊資料裡的 mcp、ops-web、omixor-console、tim 都對到這位使用者的 actor,不會留下孤兒。冪等;要在遷移開始前呼叫
func ConfigureLocalIdentity() { store.SetMigrationLocalUser(LocalIdentity, LocalAliases()...) }

// ensureLocalPrincipal 確保本機使用者存在、是伺服器管理員、有 actor,回傳呼叫核心時用的 Principal(Actor、Via = ViaLocal)。
// 與 service.saveUser 同樣語意:Store.SaveUser 在同一個交易建立使用者並建立或接回 actor
// (遷移對到本機別名時建的「未註冊使用者」actor 會被接回,舊資料的署名因此全都是這位使用者);之後重讀取回 actor_id。冪等
func ensureLocalPrincipal(st *store.PG) (*model.Principal, error) {
	// 只採用「本機建立的那一列」:以固定的 evoleap_id(localUserKey)辨識。舊版沒有這個標記的列,只有在 email 剛好是本機 handle、
	// 而且沒有綁任何外部身分(evoleap_id 空)時才接手並補上標記;email 是 omixor-console 卻綁了別的身分(例如 LDAP 的 mail 被填成這個字串、
	// 與雲端共用資料庫)的列不採用、也不升級,直接報錯,免得外部登入的人被升成管理員
	u, err := st.UserByEvoleapID(localUserKey)
	if errors.Is(err, store.ErrNotFound) {
		u, err = st.UserByEmail(LocalIdentity)
		switch {
		case errors.Is(err, store.ErrNotFound):
			u, err = &model.User{ID: service.RandID("u_", 9), EvoleapID: localUserKey, Email: LocalIdentity, Name: LocalIdentity, Role: "admin", CreatedAt: model.Now()}, nil
			if err = st.SaveUser(*u); err == nil {
				u, err = st.UserByEvoleapID(localUserKey)
			}
		case err == nil && u.EvoleapID == "":
			u.EvoleapID, u.Role, u.Disabled = localUserKey, "admin", false
			if err = st.SaveUser(*u); err == nil {
				u, err = st.UserByEvoleapID(localUserKey)
			}
		case err == nil:
			return nil, fmt.Errorf("使用者 %s 已經被另一個登入身分佔用(%s),不能當本機使用者", LocalIdentity, u.ID)
		}
	}
	if err != nil {
		return nil, err
	}
	if u.Role != "admin" || u.Disabled || u.ActorID == "" {
		u.Role, u.Disabled = "admin", false
		if err := st.SaveUser(*u); err != nil {
			return nil, err
		}
		if u, err = st.UserByEvoleapID(localUserKey); err != nil {
			return nil, err
		}
	}
	p := &model.Principal{User: *u, Via: model.ViaLocal}
	ref, err := (&service.Service{Store: st}).SelfActor(p)
	if err != nil {
		return nil, fmt.Errorf("本機使用者 %s 的 actor 不可用:%w", LocalIdentity, err)
	}
	p.Actor = ref
	return p, nil
}

// localCore 本機模式的核心:連 PostgreSQL(OMIXOR_DATABASE_URL);沒有資料庫時用 newTestCore(測試)。都沒有回傳 nil
func localCore() *backend.Local {
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	if pool := pgPool(); pool != nil {
		if localCoreVal == nil || localCoreKey != any(pool) {
			ConfigureLocalIdentity() // 要在 NewPGFromPool(啟動遷移)之前
			st, err := store.NewPGFromPool(pool)
			if err != nil {
				log.Printf("核心建表失敗:%v", err)
				return nil
			}
			p, err := ensureLocalPrincipal(st)
			if err != nil {
				log.Printf("本機使用者建立失敗:%v", err)
				return nil
			}
			svc := &service.Service{Store: st}
			if err := svc.EnsureOrgsForIssues(); err != nil {
				log.Printf("補建組織失敗:%v", err)
			}
			localCoreVal, localCoreKey = &backend.Local{Svc: svc, P: p, EnsureOrg: true}, any(pool)
		}
		return localCoreVal
	}
	if newTestCore != nil {
		return newTestCore()
	}
	return nil
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
	if t := config.Current().Str(config.ServerToken); t != "" {
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
	if config.Current().Str(config.ServerToken) == "" {
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
	if l, ok := rawBackend(b).(*backend.Local); ok {
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
	for _, i := range updates {
		labels, comments := i.Labels, remoteExternalComments(i.Comments)
		if _, err := b.UpdateIssue(org, i.ID, service.IssuePatch{Title: &i.Title, Body: &i.Body, Status: &i.Status,
			Priority: &i.Priority, Labels: &labels, Source: &i.Source, Link: &i.Link, Repo: &i.Repo, Comments: &comments, Author: remoteExternalAuthor(i.CreatedBy)}); err != nil {
			return err
		}
	}
	for _, i := range creates {
		if _, err := b.CreateIssue(org, service.IssueInput{Title: i.Title, Body: i.Body, Priority: i.Priority,
			Labels: i.Labels, Source: i.Source, Link: i.Link, Repo: i.Repo, Author: remoteExternalAuthor(i.CreatedBy)}); err != nil {
			return err
		}
	}
	return nil
}

// ---- 遠端模式的外部作者(Jira 人名):伺服器只接受署名是呼叫者本人,外部人名會被 AuthorReject 擋 400 ----

// remoteExternalAuthor 遠端送出時一律署名本人(空字串 = 呼叫者);外部人名改放進留言內文(見 remoteExternalComments)
func remoteExternalAuthor(string) string { return "" }

// remoteExternalComments 把 Jira 來的留言(id 以 jira- 開頭)的外部作者名放進內文開頭「【Jira:名字】」,作者改本人;
// 本機加的留言(作者已是合法 actor)原樣保留。不改動傳入的 slice
func remoteExternalComments(in []model.Comment) []model.Comment {
	out := make([]model.Comment, len(in))
	for k, c := range in {
		if strings.HasPrefix(c.ID, "jira-") && strings.TrimSpace(c.Author) != "" {
			c.Text = "【Jira:" + strings.TrimSpace(c.Author) + "】\n" + c.Text
			c.Author = ""
		}
		out[k] = c
	}
	return out
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
	return issueSave(a.conns().Scope, org, updates, creates)
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
		log.Printf("Issue 伺服器:%v", err)
	}
	s.remoteErr, s.lastErr = err.Error(), err
}

// issueRemoteOK 核心操作成功:清掉上一次的錯誤(原錯誤也清,不讓 A 失敗的 409 殘留到 B 成功之後)
func (a *App) issueRemoteOK(s *issueState) { s.remoteErr, s.lastErr = "", nil }

// issueConnErr 給畫面「連線錯誤」用的文字:只有連不上、伺服器錯誤、登入問題;
// 使用者操作的錯誤(版本衝突 409、專案沒代號 400、找不到 404…)不算連線錯誤。拿著 Mu 呼叫
func (a *App) issueConnErr() string {
	s := a.issueSt()
	if s.remoteErr == "" || isUserIssueErr(s.lastErr) {
		return ""
	}
	return s.remoteErr
}

// isUserIssueErr 使用者操作造成的錯誤(狀態碼 400、404、409、422):不是連線或伺服器的問題
func isUserIssueErr(err error) bool {
	if err == nil {
		return false
	}
	switch httperr.Classify(err).Status {
	case 400, 404, 409, 422:
		return true
	}
	return false
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
	s.Issues, s.fetched = normIssues(list), time.Now()
	a.issueRemoteOK(s)
	s.gen++
}

// RefreshIssuesIfStale 交給核心時:超過 maxAge 沒讀就重讀(拿著 Mu 呼叫)
func (a *App) RefreshIssuesIfStale(maxAge time.Duration) {
	s := a.issueSt()
	if time.Since(s.fetched) >= maxAge {
		a.issueRemoteLoad(s)
	}
}

func (a *App) issueRemoteCreate(in service.IssueInput) (model.Issue, error) {
	b, err := a.issueBackend()
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
	b, err := a.issueBackend()
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
	s.Issues, s.fetched = ops, time.Now()
	a.issueRemoteOK(s)
	s.gen++
	if changed {
		a.changedIssues()
	}
}

// 遠端模式:每 5 秒在背景重讀一次(網路請求在鎖外),別處(app、其他 omixor-web)的修改才看得到
func init() {
	OnStart(func(a *App) {
		if config.Current().Bool(config.NoTimers) {
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
