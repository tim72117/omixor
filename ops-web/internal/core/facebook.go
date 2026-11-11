package core

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Facebook 粉絲專頁:連線(Meta 登入或貼上權杖)、讀貼文與成效、草稿、由人發佈與排程。同 Swift 版 Facebook/FacebookStore.swift。
//
// 鎖:fbState 由 a.Mu 保護;鑰匙圈(core.Vault)與 Graph API 請求都在鎖外。
// 函式名稱後面註明「拿著鎖呼叫」的要先拿 a.Mu,其他的不能拿著 a.Mu 呼叫。

// FBNotConnected 還沒連線時的訊息
const FBNotConnected = "Ops 還沒連上 Facebook 粉絲專頁。請人在 Ops 的 AI 自動化 › 粉絲專頁 設定連線(Meta 登入或貼上 Page access token),並勾選要管理的專頁。"

// FBConfirmOwner 發佈與排程前確認是本人(Swift 用 Touch ID / 電腦密碼)。Go 版畫面的登入就是身分確認,預設通過;測試可以換掉
var FBConfirmOwner = func(reason string) bool { return true }

type fbState struct {
	Settings FBSettings
	Snap     FBSnapshot
	// dead 這次執行中確認過已淘汰或不能用的指標,之後就不再問
	dead map[string]FBError
}

func (a *App) fbSt() *fbState {
	if s, ok := a.Ext["facebook"].(*fbState); ok {
		return s
	}
	s := &fbState{Settings: DefaultFBSettings(), Snap: emptyFBSnapshot(), dead: map[string]FBError{}}
	a.Ext["facebook"] = s
	return s
}

func (a *App) fbFile() string { return filepath.Join(a.DataDir(), "facebook.json") }

// fbLoad 讀目前組織的 facebook.json(拿著鎖呼叫)
func fbLoad(a *App) {
	s := a.fbSt()
	s.Settings = readFBSettings(a.fbFile())
	s.Snap = emptyFBSnapshot()
}

func readFBSettings(path string) FBSettings {
	var st FBSettings
	if err := ReadJSON(path, &st); err != nil {
		return DefaultFBSettings()
	}
	return st
}

// fbSave 拿著鎖呼叫
func fbSave(a *App) {
	_ = WriteJSON(a.fbFile(), a.fbSt().Settings)
	go a.Changed("facebook")
}

// FBSettingsNow 設定的副本(拿著鎖呼叫)
func FBSettingsNow(a *App) FBSettings { return cloneFBSettings(a.fbSt().Settings) }

func fbVersion(a *App) string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.fbSt().Settings.APIVersion
}

// MARK: - 權杖(core.Vault,帶組織前綴)

func fbPageAccount(scope, id string) string { return scope + "facebook:page:" + id }

func fbScope(a *App) string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.conns().Scope
}

func fbPageTokenScoped(scope, id string) (string, bool) {
	d, ok := vaultGet(fbPageAccount(scope, id))
	if !ok || len(d) == 0 {
		return "", false
	}
	return string(d), true
}

// FBPageToken 專頁的 Page access token(會讀鑰匙圈,不能拿著鎖)
func FBPageToken(a *App, pageID string) (string, bool) { return fbPageTokenScoped(fbScope(a), pageID) }

// FBSetPageToken token 是 nil 表示刪除(不能拿著鎖)
func FBSetPageToken(a *App, token *string, pageID string) {
	scope := fbScope(a)
	if token == nil {
		vaultSet(fbPageAccount(scope, pageID), nil)
		return
	}
	vaultSet(fbPageAccount(scope, pageID), []byte(*token))
}

// fbHasTokenCached 只看快取(畫面用,拿著鎖呼叫)
func fbHasTokenCached(a *App, pageID string) bool {
	has, _ := vaultPeek(fbPageAccount(a.conns().Scope, pageID))
	return has
}

// FBTokens 每個專頁有沒有權杖(不能拿著鎖)
func FBTokens(a *App) map[string]bool {
	a.Mu.Lock()
	pages := append([]FBPage{}, a.fbSt().Settings.Pages...)
	scope := a.conns().Scope
	a.Mu.Unlock()
	out := map[string]bool{}
	for _, p := range pages {
		_, out[p.ID] = fbPageTokenScoped(scope, p.ID)
	}
	return out
}

// FBIsConnected 有管理中且有權杖的專頁(不能拿著鎖)
func FBIsConnected(a *App) bool {
	a.Mu.Lock()
	pages := a.fbSt().Settings.ManagedPages()
	scope := a.conns().Scope
	a.Mu.Unlock()
	for _, p := range pages {
		if _, ok := fbPageTokenScoped(scope, p.ID); ok {
			return true
		}
	}
	return false
}

// FBFindPage id 完全相同,或名稱不分大小寫(拿著鎖呼叫)
func FBFindPage(a *App, ref string) *FBPage {
	if ref == "" {
		return nil
	}
	pages := a.fbSt().Settings.Pages
	for i := range pages {
		if pages[i].ID == ref {
			p := pages[i]
			return &p
		}
	}
	for i := range pages {
		if strings.EqualFold(pages[i].Name, ref) {
			p := pages[i]
			return &p
		}
	}
	return nil
}

// FBEnsureConnection 連線用的 Connection(Meta App 的 App ID 與 App secret 存在這裡,secret 在鑰匙圈;不能拿著鎖)
func FBEnsureConnection(a *App) Connection {
	a.Mu.Lock()
	c, ok := ConnFind(a, FBConnectionID)
	version := a.fbSt().Settings.APIVersion
	a.Mu.Unlock()
	if ok {
		return c
	}
	c = FBPresetConnection(version)
	ConnUpsert(a, c)
	return c
}

// FBSetApp Meta App 的 App ID 與 App secret
func FBSetApp(a *App, id string, secret *string) {
	c := FBEnsureConnection(a)
	version := fbVersion(a)
	c.OAuth.ClientID = strings.TrimSpace(id)
	c.OAuth.AuthorizationEndpoint = "https://www.facebook.com/" + version + "/dialog/oauth"
	c.OAuth.TokenEndpoint = "https://" + FBHost + "/" + version + "/oauth/access_token"
	port := FBCallbackPort
	c.OAuth.RegisteredPort = &port
	ConnUpsert(a, c)
	if secret != nil && *secret != "" {
		ConnSetClientSecret(a, c.ID, secret)
	}
}

// FBDisconnect 中斷連線:清掉所有權杖(專頁清單與草稿保留)
func FBDisconnect(a *App, actor Actor) {
	a.Mu.Lock()
	pages := append([]FBPage{}, a.fbSt().Settings.Pages...)
	scope := a.conns().Scope
	a.Mu.Unlock()
	for _, p := range pages {
		vaultSet(fbPageAccount(scope, p.ID), nil)
	}
	vaultSetTokens(scope, FBConnectionID, nil)
	a.Mu.Lock()
	s := a.fbSt()
	s.Settings.Mode = ""
	fbSave(a)
	s.Snap = emptyFBSnapshot()
	a.Log(actor, "xmark.circle", "中斷 Facebook 粉絲專頁連線", "automation")
	a.Mu.Unlock()
}

// MARK: - 連線

// FBLoginWithMeta (a) Meta 登入:OAuth 授權碼 → 長期使用者權杖 → 各專頁的 Page access token(會開瀏覽器、等回呼)
func FBLoginWithMeta(a *App, actor Actor) error {
	c := FBEnsureConnection(a)
	scope := fbScope(a)
	if _, ok := vaultSecret(scope, c.ID); c.OAuth.ClientID == "" || !ok {
		return fbErr("先填 Meta App 的 App ID 與 App secret")
	}
	tokens, settings, err := oauthLogin(c, scope)
	if err != nil {
		return err
	}
	n := c
	n.OAuth = settings
	ConnUpsert(a, n)
	return FBCompleteLogin(a, tokens.AccessToken, actor)
}

// FBCompleteLogin 短期使用者權杖換長期,再讀管理的專頁與各自的權杖
func FBCompleteLogin(a *App, userToken string, actor Actor) error {
	c := FBEnsureConnection(a)
	scope := fbScope(a)
	version := fbVersion(a)
	secret, ok := vaultSecret(scope, c.ID)
	if !ok {
		return fbErr("鑰匙圈裡沒有 App secret")
	}
	r, err := FBSend("GET", FBURL(version, "/oauth/access_token", map[string]string{
		"grant_type": "fb_exchange_token", "client_id": c.OAuth.ClientID, "client_secret": secret, "fb_exchange_token": userToken,
	}), "", nil, "")
	if err != nil {
		return err
	}
	long, ok := r["access_token"].(string)
	if !ok {
		return fbErr("換長期權杖失敗:回應沒有 access_token")
	}
	t := &TokenSet{AccessToken: long, TokenType: strPtr("bearer")}
	if n, ok := jNum(r["expires_in"]); ok {
		t.ExpiresAt = &Time{time.Now().Add(time.Duration(n * float64(time.Second)))}
	}
	vaultSetTokens(scope, c.ID, t)
	if err := fbLoadAccounts(a, version, scope, long); err != nil {
		return err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.fbSt()
	s.Settings.Mode = "oauth"
	fbSave(a)
	a.Log(actor, "person.badge.key", "連線 Facebook 粉絲專頁(Meta 登入,"+strconv.Itoa(len(s.Settings.Pages))+" 個專頁)", "automation")
	return nil
}

func fbLoadAccounts(a *App, version, scope, userToken string) error {
	list, err := FBGetAll(version, "/me/accounts", userToken, map[string]string{"fields": fbPageFields + ",access_token", "limit": "50"}, 5, 0)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fbErr("這個帳號沒有可以管理的粉絲專頁(或沒有授權 pages_show_list)")
	}
	found := []fbFound{}
	for _, d := range list {
		f := fbFound{page: fbParsePage(d)}
		if t, ok := d["access_token"].(string); ok {
			f.token = &t
		}
		found = append(found, f)
	}
	fbMergePages(a, scope, found)
	return nil
}

type fbFound struct {
	page  FBPage
	token *string
}

// fbMergePages 不能拿著鎖(權杖寫進鑰匙圈)
func fbMergePages(a *App, scope string, found []fbFound) {
	for _, f := range found {
		if f.page.ID != "" && f.token != nil {
			vaultSet(fbPageAccount(scope, f.page.ID), []byte(*f.token))
		}
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.fbSt()
	pages := s.Settings.Pages
	for _, f := range found {
		p := f.page
		if p.ID == "" {
			continue
		}
		kept := []FBPage{}
		for _, old := range pages {
			if old.ID == p.ID {
				p.Managed = old.Managed
			} else {
				kept = append(kept, old)
			}
		}
		pages = append(kept, p)
	}
	s.Settings.Pages = pages
	s.Snap.TokenExpired = false
	s.Snap.Error = nil
	go a.Changed("facebook")
}

// FBAddToken (b) 直接貼權杖:Page access token 就是那個專頁;貼的是使用者權杖就讀它管理的專頁
func FBAddToken(a *App, raw string, actor Actor) error {
	token := strings.TrimSpace(raw)
	if token == "" {
		return fbErr("權杖是空的")
	}
	version := fbVersion(a)
	scope := fbScope(a)
	me, err := FBGet(version, "/me", token, map[string]string{"fields": "id,name,category,link,fan_count,followers_count,picture{url}"})
	if err != nil {
		return err
	}
	if _, ok := me["category"]; ok {
		fbMergePages(a, scope, []fbFound{{page: fbParsePage(me), token: &token}})
	} else if err := fbLoadAccounts(a, version, scope, token); err != nil {
		return err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.fbSt()
	if s.Settings.Mode == "" {
		s.Settings.Mode = "page_token"
	}
	fbSave(a)
	name, ok := me["name"].(string)
	if !ok {
		name = "?"
	}
	a.Log(actor, "key", "設定 Facebook 粉絲專頁權杖("+name+")", "automation")
	return nil
}

// FBSetManaged 拿著鎖呼叫
func FBSetManaged(a *App, pageID string, on bool) bool {
	s := a.fbSt()
	for i := range s.Settings.Pages {
		if s.Settings.Pages[i].ID == pageID {
			s.Settings.Pages[i].Managed = on
			fbSave(a)
			return true
		}
	}
	return false
}

// MARK: - 讀資料

func fbToken(a *App, pageID string) (string, error) {
	t, ok := FBPageToken(a, pageID)
	if !ok {
		return "", fbErr(FBNotConnected)
	}
	return t, nil
}

// fbNoteError 拿著鎖呼叫
func fbNoteError(a *App, err error, page string) {
	s := a.fbSt()
	var fe FBError
	if errors.As(err, &fe) && fe.IsTokenExpired() {
		s.Snap.TokenExpired = true
	}
	s.Snap.Errors[page] = err.Error()
}

// FBRefreshPage 重新讀專頁資料
func FBRefreshPage(a *App, pageID string) (FBPage, error) {
	t, err := fbToken(a, pageID)
	if err != nil {
		return FBPage{}, err
	}
	d, err := FBGet(fbVersion(a), "/"+pageID, t, map[string]string{"fields": fbPageFields})
	if err != nil {
		return FBPage{}, err
	}
	p := fbParsePage(d)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.fbSt()
	for i := range s.Settings.Pages {
		if s.Settings.Pages[i].ID == pageID {
			p.Managed = s.Settings.Pages[i].Managed
			s.Settings.Pages[i] = p
			fbSave(a)
			break
		}
	}
	return p, nil
}

// FBPosts 最近貼文(含心情 / 留言 / 分享數);withInsights 時另外讀前幾篇的觸及、點擊、心情分類
func FBPosts(a *App, pageID string, limit int, withInsights bool) ([]FBPost, error) {
	t, err := fbToken(a, pageID)
	if err != nil {
		return nil, err
	}
	version := fbVersion(a)
	list, err := FBGetAll(version, "/"+pageID+"/posts", t, map[string]string{"fields": fbPostFields, "limit": strconv.Itoa(min(limit, 100))}, 3, limit)
	if err != nil {
		return nil, err
	}
	posts := []FBPost{}
	for _, d := range list {
		posts = append(posts, fbParsePost(d, pageID))
	}
	if withInsights {
		for i := range posts {
			if i >= 10 {
				break
			}
			m, err := fbInsights(a, version, posts[i].ID, t, FBPostMetrics, map[string]string{"period": "lifetime"}, false)
			if err != nil {
				return nil, err
			}
			posts[i].Insights = m
		}
	}
	a.Mu.Lock()
	s := a.fbSt()
	s.Snap.Posts[pageID] = posts
	delete(s.Snap.Errors, pageID)
	a.Mu.Unlock()
	a.Changed("facebook")
	return posts, nil
}

// FBScheduled 已排程貼文(排程時間早的在前)
func FBScheduled(a *App, pageID string) ([]FBPost, error) {
	t, err := fbToken(a, pageID)
	if err != nil {
		return nil, err
	}
	raw, err := FBGetAll(fbVersion(a), "/"+pageID+"/scheduled_posts", t, map[string]string{"fields": fbScheduledFields, "limit": "50"}, 3, 0)
	if err != nil {
		return nil, err
	}
	list := []FBPost{}
	for _, d := range raw {
		p := fbParsePost(d, pageID)
		p.IsPublished = false
		list = append(list, p)
	}
	key := func(p FBPost) time.Time {
		if p.ScheduledTime == nil {
			return time.Unix(1<<40, 0)
		}
		return *p.ScheduledTime
	}
	sort.SliceStable(list, func(i, j int) bool { return key(list[i]).Before(key(list[j])) })
	a.Mu.Lock()
	a.fbSt().Snap.Scheduled[pageID] = list
	a.Mu.Unlock()
	a.Changed("facebook")
	return list, nil
}

// FBFetchPageInsights 專頁成效(days 1–90)
func FBFetchPageInsights(a *App, pageID, period string, days int) (FBPageInsights, error) {
	days = min(max(days, 1), 90)
	until := time.Now()
	since := until.Add(-time.Duration(days) * 24 * time.Hour)
	t, err := fbToken(a, pageID)
	if err != nil {
		return FBPageInsights{}, err
	}
	p := period
	if !iaOneOf(p, FBPeriods) {
		p = "day"
	}
	m, err := fbInsights(a, fbVersion(a), pageID, t, FBPageMetrics, map[string]string{
		"period": p, "since": strconv.FormatInt(since.Unix(), 10), "until": strconv.FormatInt(until.Unix(), 10),
	}, period == "day")
	if err != nil {
		return FBPageInsights{}, err
	}
	r := FBPageInsights{PageID: pageID, Period: period, Since: since, Until: until, Metrics: m}
	a.Mu.Lock()
	a.fbSt().Snap.Insights[pageID] = r
	a.Mu.Unlock()
	a.Changed("facebook")
	return r, nil
}

// FBInsights 給測試與其他功能用的 fbInsights
func FBInsights(a *App, objectID, token string, defs []FBMetricDef, query map[string]string, sumDaily bool) ([]FBMetric, error) {
	return fbInsights(a, fbVersion(a), objectID, token, defs, query, sumDaily)
}

// fbInsights 先一次問全部候選指標;Meta 說有指標無效(code 100)就逐一問,問不到的標成已淘汰,不讓整頁壞掉
func fbInsights(a *App, version, objectID, token string, defs []FBMetricDef, query map[string]string, sumDaily bool) ([]FBMetric, error) {
	all := []string{}
	seen := map[string]bool{}
	for _, d := range defs {
		for _, c := range d.Candidates {
			if !seen[c] {
				seen[c] = true
				all = append(all, c)
			}
		}
	}
	a.Mu.Lock()
	failed := map[string]FBError{}
	for k, v := range a.fbSt().dead {
		failed[k] = v
	}
	a.Mu.Unlock()
	ask := []string{}
	for _, m := range all {
		if _, dead := failed[m]; !dead {
			ask = append(ask, m)
		}
	}
	withMetric := func(m string) map[string]string {
		q := map[string]string{}
		for k, v := range query {
			q[k] = v
		}
		q["metric"] = m
		return q
	}
	values := map[string][]fbValue{}
	if len(ask) > 0 {
		r, err := FBGet(version, "/"+objectID+"/insights", token, withMetric(strings.Join(ask, ",")))
		if err == nil {
			values = fbInsightValues(jMaps(r["data"]))
		} else {
			var fe FBError
			if !errors.As(err, &fe) || fe.IsTokenExpired() {
				return nil, err
			}
			// 有指標被淘汰(code 100)或沒權限:逐一問,能拿到的照樣顯示
			for _, m := range ask {
				r, err := FBGet(version, "/"+objectID+"/insights", token, withMetric(m))
				if err != nil {
					var fe FBError
					if !errors.As(err, &fe) || fe.IsTokenExpired() {
						return nil, err
					}
					failed[m] = fe
					if fe.IsInvalidMetric() {
						a.Mu.Lock()
						a.fbSt().dead[m] = fe
						a.Mu.Unlock()
					}
					continue
				}
				for k, v := range fbInsightValues(jMaps(r["data"])) {
					if _, has := values[k]; !has {
						values[k] = v
					}
				}
			}
		}
	}
	out := []FBMetric{}
	for _, def := range defs {
		name := ""
		for _, c := range def.Candidates {
			if len(values[c]) > 0 {
				name = c
				break
			}
		}
		if name != "" {
			metric := FBMetric{Key: def.Key, Label: def.Label, Metric: strPtr(name), Status: "ok", Values: []FBPoint{}}
			breakdown := map[string]float64{}
			for _, v := range values[name] {
				if n, ok := jNum(v.value); ok {
					d := time.Now()
					if v.date != nil {
						d = *v.date
					}
					metric.Values = append(metric.Values, FBPoint{d, n})
				} else if dict, ok := v.value.(map[string]any); ok {
					for k, x := range dict {
						if n, ok := jNum(x); ok {
							breakdown[k] += n
						}
					}
				}
			}
			switch {
			case len(breakdown) > 0:
				metric.Breakdown = breakdown
				sum := 0.0
				for _, v := range breakdown {
					sum += v
				}
				metric.Total = &sum
			case def.Latest || !sumDaily:
				if n := len(metric.Values); n > 0 {
					v := metric.Values[n-1].Value
					metric.Total = &v
				}
			default:
				sum := 0.0
				for _, v := range metric.Values {
					sum += v.Value
				}
				metric.Total = &sum
			}
			out = append(out, metric)
			continue
		}
		var first *FBError
		for _, c := range def.Candidates {
			if e, ok := failed[c]; ok {
				first = &e
				break
			}
		}
		if first != nil {
			status := "unavailable"
			if first.IsInvalidMetric() {
				status = "deprecated"
			}
			out = append(out, FBMetric{Key: def.Key, Label: def.Label, Status: status, Values: []FBPoint{}, Note: strPtr(first.Message)})
			continue
		}
		out = append(out, FBMetric{Key: def.Key, Label: def.Label, Status: "unavailable", Values: []FBPoint{}, Note: strPtr("Meta 沒有回傳這個指標的資料")})
	}
	return out, nil
}

// FBRefreshAll 畫面用:重新讀所有管理中專頁的資料;單一專頁失敗不影響其他
func FBRefreshAll(a *App) {
	a.Mu.Lock()
	s := a.fbSt()
	if s.Snap.Busy {
		a.Mu.Unlock()
		return
	}
	s.Snap.Busy = true
	pages := s.Settings.ManagedPages()
	a.Mu.Unlock()
	a.Changed("facebook")
	defer func() {
		a.Mu.Lock()
		s := a.fbSt()
		s.Snap.Busy = false
		now := time.Now()
		s.Snap.FetchedAt = &now
		a.Mu.Unlock()
		a.Changed("facebook")
	}()
	connected := FBIsConnected(a)
	a.Mu.Lock()
	if connected {
		a.fbSt().Snap.Error = nil
	} else {
		a.fbSt().Snap.Error = strPtr(FBNotConnected)
	}
	a.Mu.Unlock()
	for _, p := range pages {
		if _, ok := FBPageToken(a, p.ID); !ok {
			continue
		}
		err := func() error {
			if _, err := FBRefreshPage(a, p.ID); err != nil {
				return err
			}
			if _, err := FBPosts(a, p.ID, 10, true); err != nil {
				return err
			}
			if _, err := FBScheduled(a, p.ID); err != nil {
				return err
			}
			_, err := FBFetchPageInsights(a, p.ID, "day", 28)
			return err
		}()
		if err != nil {
			a.Mu.Lock()
			fbNoteError(a, err, p.ID)
			a.Mu.Unlock()
		}
	}
}

// MARK: - 草稿(拿著鎖呼叫)

// FBFindDraft id 不分大小寫
func FBFindDraft(a *App, id string) *FBDraft {
	for _, d := range a.fbSt().Settings.Drafts {
		if strings.EqualFold(d.ID, id) {
			c := cloneFBDraft(d)
			return &c
		}
	}
	return nil
}

func FBNewDraftID(a *App) string {
	n := 0
	for _, d := range a.fbSt().Settings.Drafts {
		n = max(n, d.Number())
	}
	return "FD-" + strconv.Itoa(n+1)
}

// FBSaveDraft 新增(放最前面,署名 author)或取代同 id 的草稿
func FBSaveDraft(a *App, d FBDraft, author string) FBDraft {
	s := a.fbSt()
	d = cloneFBDraft(d)
	d.UpdatedAt = nowPrecise()
	found := false
	for i := range s.Settings.Drafts {
		if s.Settings.Drafts[i].ID == d.ID {
			s.Settings.Drafts[i] = d
			found = true
			break
		}
	}
	if !found {
		d.Author = author
		d.CreatedAt = nowPrecise()
		s.Settings.Drafts = append([]FBDraft{d}, s.Settings.Drafts...)
	}
	fbSave(a)
	return cloneFBDraft(d)
}

func FBDeleteDraft(a *App, id string) {
	s := a.fbSt()
	kept := []FBDraft{}
	for _, d := range s.Settings.Drafts {
		if d.ID != id {
			kept = append(kept, d)
		}
	}
	s.Settings.Drafts = kept
	fbSave(a)
}

// FBAuthorName 同 Swift 的 FacebookStore.authorName
func FBAuthorName(a *App, author string) string {
	if author == MCPAuthor {
		return "MCP 用戶端"
	}
	if strings.HasPrefix(author, AgentPrefix) {
		id := strings.TrimPrefix(author, AgentPrefix)
		for _, g := range a.Agents() {
			if g.ID == id {
				return g.Name
			}
		}
		return id
	}
	return a.Name(Person(author))
}

// MARK: - 發佈(只有人在畫面上按)

// FBPublish 立即發佈(schedule = nil)或排程,以 actor(登入者)署名。發佈前一定要過 FBConfirmOwner
func FBPublish(a *App, draftID string, schedule *time.Time, now time.Time, actor Actor) (string, error) {
	a.Mu.Lock()
	dp := FBFindDraft(a, draftID)
	var page *FBPage
	if dp != nil {
		page = FBFindPage(a, dp.PageID)
	}
	version := a.fbSt().Settings.APIVersion
	a.Mu.Unlock()
	if dp == nil {
		return "", fbErr("找不到草稿 " + draftID)
	}
	d := *dp
	if d.Status != FBDraftDraft {
		return "", fbErr("這份草稿已經" + FBStatusLabel(d.Status))
	}
	if d.IsEmpty() {
		return "", fbErr("草稿是空的:至少要有文字、連結或圖片")
	}
	if page == nil {
		return "", fbErr("先選要發到哪個粉絲專頁")
	}
	token, err := fbToken(a, page.ID)
	if err != nil {
		return "", err
	}
	if schedule != nil {
		if err := FBValidateSchedule(*schedule, now); err != nil {
			return "", err
		}
	}
	reason := "發佈 Facebook 貼文到「" + page.Name + "」"
	if schedule != nil {
		reason = "排程 Facebook 貼文到「" + page.Name + "」," + FBTaipeiString(*schedule)
	}
	if !FBConfirmOwner(reason) {
		return "", fbErr("取消(未通過身分確認)")
	}

	fields := map[string]string{}
	if schedule != nil {
		fields["published"] = "false"
		fields["scheduled_publish_time"] = strconv.FormatInt(schedule.Unix(), 10)
	}
	parts := []string{}
	for _, s := range []string{d.Message, map[bool]string{true: "", false: d.Link}[d.ImageURL == "" && d.LocalImage == ""]} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	caption := strings.Join(parts, "\n\n")
	var result map[string]any
	switch {
	case d.LocalImage != "":
		name := filepath.Base(d.LocalImage)
		data, err := os.ReadFile(d.LocalImage)
		if err != nil {
			return "", fbErr("讀不到圖片 " + name)
		}
		fields["caption"] = caption
		mime, ok := map[string]string{"png": "image/png", "gif": "image/gif", "heic": "image/heic"}[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]
		if !ok {
			mime = "image/jpeg"
		}
		result, err = FBPostMultipart(version, "/"+page.ID+"/photos", token, fields, "source", name, mime, data)
		if err != nil {
			return "", err
		}
	case d.ImageURL != "":
		fields["url"] = d.ImageURL
		fields["caption"] = caption
		result, err = FBPostForm(version, "/"+page.ID+"/photos", token, fields)
		if err != nil {
			return "", err
		}
	default:
		if d.Message != "" {
			fields["message"] = d.Message
		}
		if d.Link != "" {
			fields["link"] = d.Link
		}
		result, err = FBPostForm(version, "/"+page.ID+"/feed", token, fields)
		if err != nil {
			return "", err
		}
	}
	postID, ok := result["post_id"].(string)
	if !ok {
		postID, _ = result["id"].(string)
	}
	me := ActorAuthor(actor)
	n := d
	n.PostID = &postID
	if schedule == nil {
		n.Status = FBDraftPublished
		n.ScheduledFor = nil
		n.PublishedAt = &Time{time.Now().UTC()}
	} else {
		n.Status = FBDraftScheduled
		n.ScheduledFor = &Time{schedule.UTC()}
		n.PublishedAt = nil
	}
	n.PublishedBy = &me
	a.Mu.Lock()
	defer a.Mu.Unlock()
	FBSaveDraft(a, n, me)
	by := ""
	if d.Author != me {
		by = "(草稿:" + FBAuthorName(a, d.Author) + ")"
	}
	if schedule != nil {
		a.Log(actor, "calendar.badge.clock", "排程 Facebook 貼文到「"+page.Name+"」:"+FBTaipeiString(*schedule)+"「"+d.TitleLine()+"」"+by, "automation")
	} else {
		a.Log(actor, "paperplane", "發佈 Facebook 貼文到「"+page.Name+"」「"+d.TitleLine()+"」"+by, "automation")
	}
	return postID, nil
}

// FBCancelScheduled 取消排程:刪掉還沒發佈的排程貼文
func FBCancelScheduled(a *App, postID, pageID string, actor Actor) error {
	t, err := fbToken(a, pageID)
	if err != nil {
		return err
	}
	if _, err := FBDelete(fbVersion(a), "/"+postID, t); err != nil {
		return err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.fbSt()
	if list, ok := s.Snap.Scheduled[pageID]; ok {
		kept := []FBPost{}
		for _, p := range list {
			if p.ID != postID {
				kept = append(kept, p)
			}
		}
		s.Snap.Scheduled[pageID] = kept
	}
	for i := range s.Settings.Drafts {
		if d := &s.Settings.Drafts[i]; d.PostID != nil && *d.PostID == postID {
			d.Status = FBDraftDraft
			d.PostID = nil
			d.ScheduledFor = nil
			fbSave(a)
			break
		}
	}
	name := pageID
	if p := FBFindPage(a, pageID); p != nil {
		name = p.Name
	}
	a.Log(actor, "calendar.badge.minus", "取消 Facebook 排程貼文 "+postID+"("+name+")", "automation")
	go a.Changed("facebook")
	return nil
}
