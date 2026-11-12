package core

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Facebook 粉絲專頁:假的 graph.facebook.com(FakeNet + FBFakeGraph),照 Swift 的 FacebookTests.swift 搬,絕不送出真的請求

type fbReq struct {
	Method, URL, Body string
	Header            http.Header
	Host, Path        string
	Query             url.Values
}

type fbFake struct {
	net  *FakeNet
	mu   sync.Mutex
	reqs []fbReq
}

func (f *fbFake) all() []fbReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fbReq(nil), f.reqs...)
}

func (f *fbFake) posts() []fbReq {
	out := []fbReq{}
	for _, r := range f.all() {
		if r.Method == "POST" {
			out = append(out, r)
		}
	}
	return out
}

func newFBTest(t *testing.T) (*App, *fbFake) {
	t.Helper()
	Vault = NewMemoryVault()
	a := newTestApp(t)
	f := &fbFake{}
	f.net = NewFakeNet(nil)
	f.net.Handler = func(r *http.Request, body []byte) (int, any) {
		f.mu.Lock()
		f.reqs = append(f.reqs, fbReq{Method: r.Method, URL: "https://" + r.Host + r.URL.RequestURI(), Body: string(body),
			Header: r.Header.Clone(), Host: r.Host, Path: r.URL.Path, Query: r.URL.Query()})
		f.mu.Unlock()
		return FBFakeGraph(r, body)
	}
	saved := FBConfirmOwner
	FBConfirmOwner = func(string) bool { return true }
	t.Cleanup(func() { f.net.Close(); FBConfirmOwner = saved })
	return a, f
}

func fbConnect(t *testing.T, a *App) {
	t.Helper()
	if err := FBAddToken(a, FBFakePageToken, Person("tim")); err != nil {
		t.Fatal(err)
	}
}

func fbForm(body string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(body, "&") {
		kv := strings.SplitN(pair, "=", 2)
		k, _ := url.PathUnescape(kv[0])
		v := ""
		if len(kv) > 1 {
			v, _ = url.PathUnescape(kv[1])
		}
		out[k] = v
	}
	return out
}

func fbActivity0(a *App) (Actor, string) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	it := a.C["activity"][0]
	return ActorFrom(it["actor"]), it["text"].(string)
}

// MARK: - 連線與權杖

func TestFBPastePageTokenStoresInVaultNotJSON(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	a.Mu.Lock()
	s := FBSettingsNow(a)
	a.Mu.Unlock()
	if len(s.Pages) != 1 || s.Pages[0].ID != FBFakePage1 || *s.Pages[0].FollowersCount != 150 {
		t.Fatalf("pages = %+v", s.Pages)
	}
	if tok, _ := FBPageToken(a, FBFakePage1); tok != FBFakePageToken {
		t.Fatalf("token = %q", tok)
	}
	if !FBIsConnected(a) {
		t.Fatal("應該已連線")
	}
	r := f.all()[0]
	if r.Host != "graph.facebook.com" || r.Header.Get("Authorization") != "Bearer "+FBFakePageToken {
		t.Fatalf("請求 %+v", r)
	}
	if strings.Contains(r.URL, FBFakePageToken) {
		t.Fatal("權杖不放在網址裡")
	}
	b, _ := os.ReadFile(filepath.Join(a.DataDir(), "facebook.json"))
	if !strings.Contains(string(b), FBFakePage1) || strings.Contains(string(b), "EAAG") {
		t.Fatalf("facebook.json = %s", b)
	}
	// 帳號名稱帶組織前綴
	if _, ok := fbPageTokenScoped("other/", FBFakePage1); ok {
		t.Fatal("別的組織不應該讀得到")
	}
	if _, ok := Vault.Get("facebook:page:" + FBFakePage1); !ok {
		t.Fatal("帳號名稱應該是 facebook:page:<id>")
	}
	// mode 與動態牆
	if s.Mode != "page_token" {
		t.Fatalf("mode = %q", s.Mode)
	}
	if _, text := fbActivity0(a); text != "設定 Facebook 粉絲專頁權杖(evoleap)" {
		t.Fatalf("動態 = %q", text)
	}
}

func TestFBPasteUserTokenListsPagesAcrossPaging(t *testing.T) {
	a, f := newFBTest(t)
	if err := FBAddToken(a, FBFakeUserToken, Person("tim")); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	pages := FBSettingsNow(a).Pages
	a.Mu.Unlock()
	if len(pages) != 2 || pages[0].ID != FBFakePage1 || pages[1].ID != FBFakePage2 {
		t.Fatalf("pages = %+v", pages)
	}
	if tok, _ := FBPageToken(a, FBFakePage2); tok != FBFakePage2Token {
		t.Fatalf("token = %q", tok)
	}
	n := 0
	for _, r := range f.all() {
		if strings.HasSuffix(r.Path, "/me/accounts") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("/me/accounts 問了 %d 次", n)
	}
}

func TestFBMetaLoginExchangesLongLivedTokenThenPageTokens(t *testing.T) {
	a, _ := newFBTest(t)
	secret := "APPSECRET"
	FBSetApp(a, "APPID", &secret)
	a.Mu.Lock()
	c, ok := ConnFind(a, FBConnectionID)
	a.Mu.Unlock()
	if !ok || c.OAuth.Scopes != "pages_show_list,pages_read_engagement,pages_manage_posts,read_insights" ||
		c.OAuth.RegisteredPort == nil || *c.OAuth.RegisteredPort != 3767 ||
		!strings.HasPrefix(c.OAuth.AuthorizationEndpoint, "https://www.facebook.com/v24.0/dialog/oauth") || c.OAuth.ClientID != "APPID" {
		t.Fatalf("連線 = %+v", c)
	}
	if err := FBCompleteLogin(a, "SHORT", Person("tim")); err != nil {
		t.Fatal(err)
	}
	if tk := vaultTokens("", FBConnectionID); tk == nil || tk.AccessToken != FBFakeLongUser || tk.ExpiresAt == nil {
		t.Fatalf("使用者權杖 = %+v", tk)
	}
	a.Mu.Lock()
	s := FBSettingsNow(a)
	a.Mu.Unlock()
	if s.Mode != "oauth" || len(s.Pages) != 2 {
		t.Fatalf("settings = %+v", s)
	}
	if tok, _ := FBPageToken(a, FBFakePage1); tok != FBFakePageToken {
		t.Fatalf("token = %q", tok)
	}
	for _, name := range []string{"facebook.json", "connections.json"} {
		b, _ := os.ReadFile(filepath.Join(a.DataDir(), name))
		if strings.Contains(string(b), "APPSECRET") || strings.Contains(string(b), "EAAG") {
			t.Fatalf("%s 有密碼:%s", name, b)
		}
	}
	if _, text := fbActivity0(a); text != "連線 Facebook 粉絲專頁(Meta 登入,2 個專頁)" {
		t.Fatalf("動態 = %q", text)
	}
}

func TestFBLenientDecoding(t *testing.T) {
	raw := `{"apiVersion":"v18.0","pages":[{"id":"9","name":1},{"name":"沒有 id"}],"drafts":[{"id":"FD-2","status":"weird","message":"hi"}],"x":1}`
	var s FBSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if s.APIVersion != FBDefaultVersion || len(s.Pages) != 1 || s.Pages[0].ID != "9" || s.Pages[0].Name != "9" || !s.Pages[0].Managed {
		t.Fatalf("%+v", s)
	}
	if len(s.Drafts) != 1 || s.Drafts[0].Status != "draft" || s.Drafts[0].Message != "hi" || s.Drafts[0].Author != "tim" {
		t.Fatalf("%+v", s.Drafts)
	}
	if err := json.Unmarshal([]byte("{}"), &s); err != nil || s.Pages == nil || s.Drafts == nil {
		t.Fatalf("{} 解碼:%v %+v", err, s)
	}
	// 存檔格式:欄位名同 Swift,nil 的 optional 不輸出
	b := string(MarshalIndent(FBSettings{APIVersion: "v24.0", Pages: []FBPage{{ID: "1", Name: "x", Managed: true}},
		Drafts: []FBDraft{NewFBDraft("FD-1", "mcp")}}))
	for _, want := range []string{`"apiVersion"`, `"managed": true`, `"imageURL"`, `"localImage"`, `"pageId"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("少了 %s:%s", want, b)
		}
	}
	for _, no := range []string{"fanCount", "postId", "proposedTime", "scheduledFor"} {
		if strings.Contains(b, no) {
			t.Fatalf("不該有 %s:%s", no, b)
		}
	}
}

// MARK: - 錯誤

func TestFBGraphErrorAndExpiredToken(t *testing.T) {
	a, _ := newFBTest(t)
	fbConnect(t, a)
	tok := "EXPIRED"
	FBSetPageToken(a, &tok, FBFakePage1)
	_, err := FBPosts(a, FBFakePage1, 10, true)
	fe, ok := err.(FBError)
	if !ok || fe.Code != 190 || fe.Subcode != 463 || !fe.IsTokenExpired() || !strings.Contains(fe.Error(), "重新連線") {
		t.Fatalf("err = %#v", err)
	}
	FBRefreshAll(a)
	a.Mu.Lock()
	snap := a.fbSt().Snap
	a.Mu.Unlock()
	if !snap.TokenExpired || snap.Errors[FBFakePage1] == "" || snap.Busy || snap.FetchedAt == nil {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestFBPagingNextToOtherHostIsIgnored(t *testing.T) {
	_, f := newFBTest(t)
	f.net.Handler = func(r *http.Request, body []byte) (int, any) {
		f.mu.Lock()
		f.reqs = append(f.reqs, fbReq{Host: r.Host})
		f.mu.Unlock()
		return 200, map[string]any{"data": []any{map[string]any{"id": "a"}}, "paging": map[string]any{"next": "https://evil.example/steal"}}
	}
	list, err := FBGetAll("v24.0", "/x/posts", "T", nil, 5, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	for _, r := range f.all() {
		if r.Host != FBHost {
			t.Fatalf("送到 %s", r.Host)
		}
	}
	if _, err := FBSend("GET", "https://evil.example/x", "T", nil, ""); err == nil || err.Error() != "不會把權杖送到 evil.example" {
		t.Fatalf("err = %v", err)
	}
}

func TestFBNoInterceptorRefusesNetwork(t *testing.T) {
	// 跑 go test 時預設的 transport 拒絕連外
	_, err := FBGet("v24.0", "/me", "T", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "連不上 Facebook:") {
		t.Fatalf("err = %v", err)
	}
}

// MARK: - 貼文與成效

func TestFBPostsWithInsightsAndDeprecatedFallback(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	posts, err := FBPosts(a, FBFakePage1, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 || posts[0].ID != "1001_1" || posts[2].ID != "1001_3" {
		t.Fatalf("跟著 paging.next:%+v", posts)
	}
	p := posts[0]
	if *p.Reactions != 7 || *p.Engagement() != 10 {
		t.Fatalf("%+v", p)
	}
	byKey := map[string]FBMetric{}
	for _, m := range p.Insights {
		byKey[m.Key] = m
	}
	reach := byKey["reach"]
	if reach.Status != "ok" || *reach.Metric != "post_total_media_view_unique" || *reach.Total != 345 {
		t.Fatalf("reach = %+v", reach)
	}
	if *byKey["clicks"].Total != 12 {
		t.Fatal("clicks")
	}
	if b := byKey["reactions"].Breakdown; b["like"] != 5 || b["love"] != 2 || len(b) != 2 || *byKey["reactions"].Total != 7 {
		t.Fatalf("reactions = %+v", byKey["reactions"])
	}
	asked := 0
	for _, r := range f.all() {
		if strings.Contains(r.Query.Get("metric"), "post_impressions_unique") {
			asked++
		}
	}
	if asked != 2 {
		t.Fatalf("第一篇:一次全部問 + 單獨問一次;之後不再問。實際 %d", asked)
	}
	// limit 參數上限 100
	for _, r := range f.all() {
		if strings.HasSuffix(r.Path, "/posts") && r.Query.Get("after") == "" && r.Query.Get("limit") != "3" {
			t.Fatalf("url = %s", r.URL)
		}
	}
}

func TestFBPageInsightsShowDeprecatedAndMissingMetrics(t *testing.T) {
	a, _ := newFBTest(t)
	fbConnect(t, a)
	r, err := FBFetchPageInsights(a, FBFakePage1, "day", 7)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]FBMetric{}
	for _, x := range r.Metrics {
		m[x.Key] = x
	}
	if m["reach"].Status != "ok" || *m["reach"].Total != 60 || len(m["reach"].Values) != 3 {
		t.Fatalf("reach(day 期間加總)= %+v", m["reach"])
	}
	if *m["followers"].Total != 152 {
		t.Fatal("追蹤數取最後一天")
	}
	if m["engagement"].Status != "deprecated" || m["engagement"].Total != nil || !strings.Contains(*m["engagement"].Note, "valid insights metric") {
		t.Fatalf("engagement = %+v", m["engagement"])
	}
	if m["new_followers"].Status != "deprecated" || m["views"].Status != "ok" || len(r.Metrics) != len(FBPageMetrics) {
		t.Fatalf("%+v", r.Metrics)
	}
	if r.Until.Sub(r.Since) != 7*24*time.Hour {
		t.Fatal("期間 7 天")
	}
	// week 期間:不是加總,取最後一期
	r2, _ := FBFetchPageInsights(a, FBFakePage1, "week", 7)
	for _, x := range r2.Metrics {
		if x.Key == "reach" && *x.Total != 30 {
			t.Fatalf("week reach = %v", *x.Total)
		}
	}
}

func TestFBMissingMetricIsUnavailableNotError(t *testing.T) {
	a, _ := newFBTest(t)
	fbConnect(t, a)
	defs := []FBMetricDef{{Key: "x", Label: "X", Candidates: []string{"page_daily_follows_unique"}}}
	m, err := FBInsights(a, FBFakePage1, FBFakePageToken, defs, map[string]string{"period": "day"}, false)
	if err != nil || len(m) != 1 || m[0].Status != "unavailable" || m[0].StatusLabel() != "無資料" || *m[0].Note != "Meta 沒有回傳這個指標的資料" {
		t.Fatalf("%+v %v", m, err)
	}
}

// MARK: - 排程時間

func TestFBScheduleBoundaries(t *testing.T) {
	now := time.Unix(1800000000, 0)
	if FBValidateSchedule(now.Add(600*time.Second), now) != nil {
		t.Fatal("剛好 10 分鐘可以")
	}
	if FBValidateSchedule(now.Add(599*time.Second), now) == nil {
		t.Fatal("599 秒不行")
	}
	if FBValidateSchedule(now.Add(30*86400*time.Second), now) != nil {
		t.Fatal("剛好 30 天可以")
	}
	if FBValidateSchedule(now.Add((30*86400+1)*time.Second), now) == nil {
		t.Fatal("30 天又 1 秒不行")
	}
	err := FBValidateSchedule(now.Add(-60*time.Second), now)
	if err == nil || err.Error() != "排程時間至少要在 10 分鐘後("+FBTaipeiString(now.Add(600*time.Second))+" 之後)" {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(FBTaipeiString(time.Unix(0, 0)), "1970/01/01 08:00") || !strings.HasSuffix(FBTaipeiString(time.Unix(0, 0)), "(台北)") {
		t.Fatal("台北時間 UTC+8")
	}
}

// MARK: - 發佈(人)

func fbMakeDraft(a *App, msg, link, image, local string) FBDraft {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	d := NewFBDraft(FBNewDraftID(a), "agent:marketing")
	d.PageID = FBFakePage1
	d.Message, d.Link, d.ImageURL, d.LocalImage = msg, link, image, local
	return FBSaveDraft(a, d, "agent:marketing")
}

func fbDraftNow(a *App, id string) FBDraft {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return *FBFindDraft(a, id)
}

func TestFBPublishNowSignsAsPerson(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	d := fbMakeDraft(a, "新課上線", "https://evoleap.io/c/1", "", "")
	id, err := FBPublish(a, d.ID, nil, time.Now(), Person("tim"))
	if err != nil || id != "1001_555" {
		t.Fatalf("%q %v", id, err)
	}
	r := f.posts()[len(f.posts())-1]
	if r.Path != "/v24.0/1001/feed" || r.Header.Get("Authorization") != "Bearer "+FBFakePageToken || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("%+v", r)
	}
	form := fbForm(r.Body)
	if form["message"] != "新課上線" || form["link"] != "https://evoleap.io/c/1" {
		t.Fatalf("form = %v", form)
	}
	if _, ok := form["published"]; ok {
		t.Fatal("立即發佈不帶 published")
	}
	n := fbDraftNow(a, d.ID)
	if n.Status != "published" || *n.PublishedBy != "tim" || n.PublishedAt == nil || *n.PostID != "1001_555" {
		t.Fatalf("%+v", n)
	}
	actor, text := fbActivity0(a)
	if actor != Person("tim") || text != "發佈 Facebook 貼文到「evoleap」「新課上線」(草稿:行銷)" {
		t.Fatalf("%v %q", actor, text)
	}
	if _, err := FBPublish(a, d.ID, nil, time.Now(), Person("tim")); err == nil || err.Error() != "這份草稿已經已發佈" {
		t.Fatalf("不能再發一次:%v", err)
	}
}

func TestFBScheduleSendsUnpublishedWithTime(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	d := fbMakeDraft(a, "下週見", "", "https://img.example/a.png", "")
	when := time.Now().Add(time.Hour)
	if _, err := FBPublish(a, d.ID, &when, time.Now(), Person("tim")); err != nil {
		t.Fatal(err)
	}
	r := f.posts()[len(f.posts())-1]
	form := fbForm(r.Body)
	if r.Path != "/v24.0/1001/photos" || form["published"] != "false" || form["scheduled_publish_time"] != itoa(int(when.Unix())) ||
		form["url"] != "https://img.example/a.png" || form["caption"] != "下週見" {
		t.Fatalf("%s %v", r.Path, form)
	}
	n := fbDraftNow(a, d.ID)
	if n.Status != "scheduled" || *n.PostID != "1001_777" || n.ScheduledFor == nil || n.PublishedAt != nil {
		t.Fatalf("%+v", n)
	}
	_, text := fbActivity0(a)
	if !strings.Contains(text, "排程 Facebook 貼文") || !strings.Contains(text, "台北") {
		t.Fatalf("%q", text)
	}
}

func TestFBLocalImageUsesMultipart(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	img := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(img, []byte{0x89, 0x50, 0x4E, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}
	d := fbMakeDraft(a, "圖", "https://evoleap.io", "", img)
	if _, err := FBPublish(a, d.ID, nil, time.Now(), Person("tim")); err != nil {
		t.Fatal(err)
	}
	r := f.posts()[len(f.posts())-1]
	if r.Path != "/v24.0/1001/photos" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Body, `filename="pic.png"`) || !strings.Contains(r.Body, "Content-Type: image/png") || !strings.Contains(r.Body, "圖\n\nhttps://evoleap.io") {
		t.Fatalf("有圖時連結接在內文後:%q", r.Body)
	}
	// 讀不到圖片
	d2 := fbMakeDraft(a, "x", "", "", filepath.Join(t.TempDir(), "nope.jpg"))
	if _, err := FBPublish(a, d2.ID, nil, time.Now(), Person("tim")); err == nil || err.Error() != "讀不到圖片 nope.jpg" {
		t.Fatalf("err = %v", err)
	}
}

func TestFBPublishNeedsConfirmationAndValidTime(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	d := fbMakeDraft(a, "要確認", "", "", "")
	FBConfirmOwner = func(string) bool { return false }
	if _, err := FBPublish(a, d.ID, nil, time.Now(), Person("tim")); err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("err = %v", err)
	}
	FBConfirmOwner = func(string) bool { return true }
	soon := time.Now().Add(300 * time.Second)
	if _, err := FBPublish(a, d.ID, &soon, time.Now(), Person("tim")); err == nil || !strings.Contains(err.Error(), "10 分鐘") {
		t.Fatalf("err = %v", err)
	}
	late := time.Now().Add(31 * 86400 * time.Second)
	if _, err := FBPublish(a, d.ID, &late, time.Now(), Person("tim")); err == nil || !strings.Contains(err.Error(), "30 天") {
		t.Fatalf("err = %v", err)
	}
	if len(f.posts()) != 0 || fbDraftNow(a, d.ID).Status != "draft" {
		t.Fatal("沒有送出,草稿不變")
	}
	// 其他前置檢查
	if _, err := FBPublish(a, "FD-99", nil, time.Now(), Person("tim")); err == nil || err.Error() != "找不到草稿 FD-99" {
		t.Fatalf("err = %v", err)
	}
	a.Mu.Lock()
	e := NewFBDraft(FBNewDraftID(a), "tim")
	e.PageID = FBFakePage1
	e = FBSaveDraft(a, e, "tim")
	np := NewFBDraft(FBNewDraftID(a), "tim")
	np.Message = "沒有專頁"
	np = FBSaveDraft(a, np, "tim")
	a.Mu.Unlock()
	if _, err := FBPublish(a, e.ID, nil, time.Now(), Person("tim")); err == nil || err.Error() != "草稿是空的:至少要有文字、連結或圖片" {
		t.Fatalf("err = %v", err)
	}
	if _, err := FBPublish(a, np.ID, nil, time.Now(), Person("tim")); err == nil || err.Error() != "先選要發到哪個粉絲專頁" {
		t.Fatalf("err = %v", err)
	}
}

func TestFBPublishErrorKeepsDraft(t *testing.T) {
	a, _ := newFBTest(t)
	fbConnect(t, a)
	d := fbMakeDraft(a, "FAIL", "", "", "")
	_, err := FBPublish(a, d.ID, nil, time.Now(), Person("tim"))
	fe, ok := err.(FBError)
	if !ok || fe.Code != 200 || !strings.Contains(fe.Error(), "pages_manage_posts") || fe.Error() != "Facebook:(#200) Requires pages_manage_posts permission(code 200)" {
		t.Fatalf("err = %#v", err)
	}
	if fbDraftNow(a, d.ID).Status != "draft" {
		t.Fatal("失敗時草稿不變")
	}
}

func TestFBCancelScheduled(t *testing.T) {
	a, f := newFBTest(t)
	fbConnect(t, a)
	d := fbMakeDraft(a, "排程的", "", "", "")
	a.Mu.Lock()
	d.Status = "scheduled"
	d.PostID = strPtr("1001_900")
	FBSaveDraft(a, d, "tim")
	a.Mu.Unlock()
	list, err := FBScheduled(a, FBFakePage1)
	if err != nil || len(list) != 1 || !list[0].ScheduledTime.Equal(time.Unix(1800000000, 0)) || list[0].IsPublished {
		t.Fatalf("%+v %v", list, err)
	}
	if err := FBCancelScheduled(a, "1001_900", FBFakePage1, Person("amy")); err != nil {
		t.Fatal(err)
	}
	reqs := f.all()
	if reqs[len(reqs)-1].Method != "DELETE" {
		t.Fatal("應該送 DELETE")
	}
	n := fbDraftNow(a, d.ID)
	if n.Status != "draft" || n.PostID != nil {
		t.Fatalf("%+v", n)
	}
	a.Mu.Lock()
	left := a.fbSt().Snap.Scheduled[FBFakePage1]
	a.Mu.Unlock()
	if len(left) != 0 {
		t.Fatal("排程清單應該清掉")
	}
	actor, text := fbActivity0(a)
	if actor != Person("amy") || text != "取消 Facebook 排程貼文 1001_900(evoleap)" {
		t.Fatalf("%v %q", actor, text)
	}
}

func TestFBDisconnectClearsTokensKeepsDrafts(t *testing.T) {
	a, _ := newFBTest(t)
	fbConnect(t, a)
	fbMakeDraft(a, "留著", "", "", "")
	FBDisconnect(a, Person("tim"))
	if FBIsConnected(a) {
		t.Fatal("應該已中斷")
	}
	a.Mu.Lock()
	s := FBSettingsNow(a)
	a.Mu.Unlock()
	if len(s.Pages) != 1 || len(s.Drafts) != 1 || s.Mode != "" {
		t.Fatalf("%+v", s)
	}
	if _, text := fbActivity0(a); text != "中斷 Facebook 粉絲專頁連線" {
		t.Fatalf("%q", text)
	}
}

func TestFBOrgReloadAndDelete(t *testing.T) {
	a, _ := newFBTest(t)
	fbConnect(t, a)
	fbMakeDraft(a, "存檔", "", "", "")
	a.Mu.Lock()
	a.LoadOrgData()
	s := FBSettingsNow(a)
	a.Mu.Unlock()
	if len(s.Drafts) != 1 || s.Drafts[0].Message != "存檔" || len(s.Pages) != 1 {
		t.Fatalf("重新讀檔:%+v", s)
	}
}

func TestFBViewAndActions(t *testing.T) {
	a, _ := newFBTest(t)
	if _, err := Action(a, "facebook_token", map[string]any{"token": FBFakePageToken, UserKey: "amy"}); err != nil {
		t.Fatal(err)
	}
	d, err := Action(a, "facebook_draft_new", map[string]any{UserKey: "amy"})
	if err != nil {
		t.Fatal(err)
	}
	id := d.(map[string]any)["id"].(string)
	if d.(map[string]any)["author"] != "amy" || d.(map[string]any)["pageId"] != FBFakePage1 {
		t.Fatalf("%v", d)
	}
	if _, err := Action(a, "facebook_draft_update", map[string]any{"id": id, "message": "畫面寫的", "proposedTime": "nope"}); err == nil {
		t.Fatal("時間格式不對要失敗")
	}
	if _, err := Action(a, "facebook_draft_update", map[string]any{"id": id, "message": "畫面寫的"}); err != nil {
		t.Fatal(err)
	}
	out, err := Action(a, "facebook_publish", map[string]any{"id": id, UserKey: "amy"})
	if err != nil || out.(map[string]any)["postId"] != "1001_555" {
		t.Fatalf("%v %v", out, err)
	}
	if actor, _ := fbActivity0(a); actor != Person("amy") {
		t.Fatalf("發佈以登入者署名:%v", actor)
	}
	if _, err := Action(a, "facebook_draft_update", map[string]any{"id": id, "message": "改"}); err == nil || err.Error() != "草稿 "+id+" 已發佈,不能修改" {
		t.Fatalf("err = %v", err)
	}
	if _, err := Action(a, "facebook_managed", map[string]any{"pageId": FBFakePage1, "managed": false}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	v := fbView(a).(map[string]any)
	a.Mu.Unlock()
	if v["connected"] != false || v["tokens"].(map[string]bool)[FBFakePage1] != true {
		t.Fatalf("view = %v", v)
	}
	if _, err := Action(a, "facebook_draft_delete", map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	n := len(FBSettingsNow(a).Drafts)
	a.Mu.Unlock()
	if n != 0 {
		t.Fatal("應該刪掉")
	}
	if b := string(Marshal(v)); strings.Contains(b, "EAAG") {
		t.Fatalf("view 不能有權杖:%s", b)
	}
}
