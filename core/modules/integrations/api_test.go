package integrations_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omixor-core/api"
	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/module"
	"omixor-core/modules/integrations"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// 有編進模組時(這個測試套件 import 了模組):API 的行為和搬之前一樣(從 core/api/integration_test.go 搬過來)。
// 沒編進模組的情況在 core/api/integration_test.go 測(那個套件不 import 模組)。

// 假的 evoleap /api/me
func fakeEvoleap(t *testing.T) *httptest.Server {
	users := map[string]string{
		"evo-boss":  `{"user":{"id":"e1","email":"Boss@Example.com","name":"老闆","google":true}}`,
		"evo-alice": `{"user":{"id":"e2","email":"alice@example.com","name":"Alice","google":true}}`,
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]; ok {
			w.Write([]byte(u))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	t.Cleanup(s.Close)
	return s
}

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  store.Store
}

func newEnv(t *testing.T) *env {
	st := storetest.New(t)
	svc := &service.Service{Store: st, Evoleap: fakeEvoleap(t).URL, Bootstrap: "boss@example.com", TTL: 90 * 24 * time.Hour, LoginPerIP: 1000, LoginTotal: 1000}
	srv := httptest.NewServer(api.New(svc).Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, st: st}
}

func (e *env) login(evo string) *client.Client {
	e.t.Helper()
	c := client.New(e.srv.URL)
	r, err := c.LoginWithEvoleap(evo, "test")
	if err != nil {
		e.t.Fatalf("%s 登入:%v", evo, err)
	}
	tok := r.Token
	c.Token = func() string { return tok }
	return c
}

func status(err error) int { return client.Status(err) }

func agentNamed(id string) model.Agent { return model.Agent{ID: id, Name: id, Active: true} }

func newClient(e *env) *client.Client { return client.New(e.srv.URL) }

// 模組註冊:功能 development.repos 登記在開發進度的分頁最後(Pull Requests 之後),是真功能(stable、預設開、不是實驗);
// 退場的 development.github 不再登記。GET /api/modules 列出模組
func TestModuleRegistered(t *testing.T) {
	if !module.Has(integrations.ModuleID) {
		t.Fatal("模組沒註冊")
	}
	f, ok := model.FeatureByKey("development.repos")
	if !ok || f.Edition != model.EditionStable || !f.Default || f.Experimental || f.Parent != "development" || f.Name != "Repositories" {
		t.Fatalf("%+v %v", f, ok)
	}
	if _, ok := model.FeatureByKey("development.github"); ok {
		t.Fatal("development.github 已退場,不該再登記")
	}
	prev, next := "", ""
	for i, x := range model.Features {
		if x.Key == "development.repos" {
			if i+1 < len(model.Features) {
				next = model.Features[i+1].Key
			}
			break
		}
		prev = x.Key
	}
	if prev != "development.pulls" || next != "pipelines" {
		t.Fatal("要排在開發進度的分頁最後", prev, next)
	}
	e := newEnv(t)
	boss := e.login("evo-boss")
	var infos []module.Info
	if err := boss.Do("GET", "/api/modules", nil, &infos); err != nil || len(infos) != 1 || infos[0].ID != "integrations" || len(infos[0].Features) != 1 || infos[0].Features[0] != "development.repos" {
		t.Fatal(infos, err)
	}
	sf, err := boss.GetServerFeatures()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range sf.Features {
		found = found || (x.Key == "development.repos" && x.SystemDefault && x.Available)
	}
	if !found {
		t.Fatal("系統層清單要有 development.repos(預設開放)")
	}
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	ss, err := boss.GetSystemSettings("o1")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, x := range ss.Features {
		found = found || (x.Key == "development.repos" && x.Enabled && !x.Changed)
	}
	if !found {
		t.Fatal("組織層清單要有 development.repos(預設開)")
	}
}

// 相容舊資料:系統層、組織層存著 development.github 而沒有 development.repos 時,舊值當作 development.repos 的值;
// 寫入舊 key 轉成 development.repos,存的時候舊值搬到新 key、拿掉舊 key
func TestLegacyGitHubFeatureKey(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	stateOf := func(ss *service.SystemSettingsView, key string) *service.FeatureState {
		for i := range ss.Features {
			if ss.Features[i].Key == key {
				return &ss.Features[i]
			}
		}
		return nil
	}

	// 組織層:舊資料把 GitHub 分頁關掉 → Repositories 也關
	if err := e.st.SaveSystemSettings("o1", model.OrgSystemSettings{Features: map[string]bool{"development.github": false}}); err != nil {
		t.Fatal(err)
	}
	ss, err := boss.GetSystemSettings("o1")
	if err != nil {
		t.Fatal(err)
	}
	if f := stateOf(ss, "development.repos"); f == nil || f.On || f.Enabled || !f.Changed {
		t.Fatalf("舊的 development.github=false 要當作 development.repos 關:%+v", f)
	}
	if stateOf(ss, "development.github") != nil {
		t.Fatal("清單不該出現 development.github")
	}
	// 新 key 有值時以新 key 為準
	if err := e.st.SaveSystemSettings("o1", model.OrgSystemSettings{Features: map[string]bool{"development.github": false, "development.repos": true}}); err != nil {
		t.Fatal(err)
	}
	if ss, err = boss.GetSystemSettings("o1"); err != nil {
		t.Fatal(err)
	}
	if f := stateOf(ss, "development.repos"); f == nil || !f.On {
		t.Fatalf("新 key 有值時以新 key 為準:%+v", f)
	}
	// 寫入舊 key:轉成 development.repos;存下來的只剩新 key
	if _, err := boss.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"development.github": false}}); err != nil {
		t.Fatal("寫入舊 key 要轉成 development.repos", err)
	}
	x, err := e.st.GetSystemSettings("o1")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := x.Features["development.repos"]; !ok || v {
		t.Fatalf("要存成 development.repos=false:%v", x.Features)
	}
	if _, ok := x.Features["development.github"]; ok {
		t.Fatalf("舊 key 要拿掉:%v", x.Features)
	}
	// reset 舊 key = reset development.repos(回到預設開)
	ss, err = boss.SaveSystemSettings("o1", service.SystemSettingsInput{Reset: []string{"development.github"}})
	if err != nil {
		t.Fatal(err)
	}
	if f := stateOf(ss, "development.repos"); f == nil || !f.On || f.Changed {
		t.Fatalf("reset 舊 key 要回到預設:%+v", f)
	}
	// 存別的功能時,舊 key 的值也搬到新 key
	if err := e.st.SaveSystemSettings("o1", model.OrgSystemSettings{Features: map[string]bool{"development.github": false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"cabinet": false}}); err != nil {
		t.Fatal(err)
	}
	if x, _ = e.st.GetSystemSettings("o1"); x.Features["development.repos"] || len(x.Features) != 2 {
		t.Fatalf("舊值要搬到 development.repos:%v", x.Features)
	}

	// 系統層:舊資料不開放 GitHub 分頁 → Repositories 也不開放,組織層清單不列
	if err := e.st.SaveServerFeatures(model.ServerFeatures{Features: map[string]bool{"development.github": false}}); err != nil {
		t.Fatal(err)
	}
	sf, err := boss.GetServerFeatures()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sf.Features {
		if f.Key == "development.repos" && (f.On || f.Available || !f.Changed) {
			t.Fatalf("系統層舊值要當作 development.repos 不開放:%+v", f)
		}
	}
	if ss, err = boss.GetSystemSettings("o1"); err != nil {
		t.Fatal(err)
	}
	if stateOf(ss, "development.repos") != nil || ss.ServerFeatures["development.repos"] {
		t.Fatal("系統層沒開放時組織層清單不列 development.repos")
	}
	// 系統層寫入舊 key 也轉成新 key
	if _, err := boss.SaveServerFeatures(service.SystemSettingsInput{Features: map[string]bool{"development.github": true}}); err != nil {
		t.Fatal(err)
	}
	srv, err := e.st.GetServerFeatures()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := srv.Features["development.repos"]; !ok || !v {
		t.Fatalf("系統層要存成 development.repos=true:%v", srv.Features)
	}
	if _, ok := srv.Features["development.github"]; ok {
		t.Fatalf("系統層的舊 key 要拿掉:%v", srv.Features)
	}
}

// 組織的 GitHub、GitLab 設定的 API:成員讀(角色憑證也可以)、管理員改、匯入只有第一次
func TestIntegrationAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("o1", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	g, err := integrations.GetGitHubSettings(alice, "o1")
	if err != nil || g.Saved || len(g.Repos) != 0 {
		t.Fatal(g, err)
	}
	if _, err := integrations.SaveGitHubSettings(alice, "o1", integrations.GitHubSettingsInput{Add: []string{"a/b"}}); status(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	if _, err := integrations.ImportGitHubSettings(alice, "o1", integrations.GitHubImport{Repos: []string{"a/b"}}); status(err) != 403 {
		t.Fatal("成員不能匯入", err)
	}
	r, err := integrations.ImportGitHubSettings(boss, "o1", integrations.GitHubImport{Repos: []string{"https://github.com/a/b", "nope"}})
	if err != nil || !r.Imported || len(r.Settings.Repos) != 1 || len(r.Skipped) != 1 {
		t.Fatal(r, err)
	}
	g, err = integrations.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Add: []string{"c/d"}})
	if err != nil || len(g.Repos) != 2 || g.UpdatedBy != "boss@example.com" {
		t.Fatal(g, err)
	}
	if _, err := integrations.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Add: []string{"bad"}}); status(err) != 400 {
		t.Fatal(err)
	}
	if got, err := integrations.GetGitHubSettings(alice, "o1"); err != nil || !got.Saved || len(got.Repos) != 2 {
		t.Fatal(got, err)
	}
	r, err = integrations.ImportGitHubSettings(boss, "o1", integrations.GitHubImport{Repos: []string{"x/y"}})
	if err != nil || r.Imported || len(r.Settings.Repos) != 2 {
		t.Fatal("第二次匯入不動", r, err)
	}
	if _, err := integrations.GetGitHubSettings(boss, "nope"); status(err) != 404 {
		t.Fatal(err)
	}

	base := "https://gitlab.example.com/"
	l, err := integrations.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{BaseURL: &base, Add: []string{"https://gitlab.example.com/g/p"}})
	if err != nil || l.BaseURL != "https://gitlab.example.com" || len(l.Projects) != 1 || l.Projects[0] != "g/p" {
		t.Fatal(l, err)
	}
	if _, err := integrations.SaveGitLabSettings(alice, "o1", integrations.GitLabSettingsInput{Add: []string{"g/q"}}); status(err) != 403 {
		t.Fatal(err)
	}
	if got, err := integrations.GetGitLabSettings(alice, "o1"); err != nil || got.BaseURL != "https://gitlab.example.com" {
		t.Fatal(got, err)
	}
	if lr, err := integrations.ImportGitLabSettings(boss, "o1", integrations.GitLabImport{BaseURL: "https://other"}); err != nil || lr.Imported {
		t.Fatal(lr, err)
	}

	// 角色憑證也可以讀(不能改)
	if _, err := boss.SaveAgent("o1", agentNamed("cto")); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.AssignTask("o1", service.TaskInput{AgentID: "cto", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	cl, err := boss.ClaimTask("o1", service.ClaimInput{AgentID: "cto"})
	if err != nil || cl.Credential == nil {
		t.Fatal(cl, err)
	}
	role := newClient(e)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	if got, err := integrations.GetGitHubSettings(role, "o1"); err != nil || len(got.Repos) != 2 {
		t.Fatal("角色憑證可以讀", got, err)
	}
	if got, err := integrations.GetGitLabSettings(role, "o1"); err != nil || len(got.Projects) != 1 {
		t.Fatal(got, err)
	}
	if _, err := integrations.SaveGitHubSettings(role, "o1", integrations.GitHubSettingsInput{Add: []string{"z/z"}}); status(err) != 401 {
		t.Fatal("角色憑證不能改(不是 omixor token)", err)
	}
}
