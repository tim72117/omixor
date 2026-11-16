package integrations_test

import (
	"encoding/json"
	"testing"

	"omixor-core/model"
	"omixor-core/modules/integrations"
	"omixor-core/service"
	"omixor-core/store"
)

// 從 core/service/integration_test.go 搬過來(規則不變)

func strs(s ...string) *[]string { return &s }
func sp(s string) *string        { return &s }

func code(err error) int {
	if err == nil {
		return 0
	}
	return service.Code(err)
}

// chainEnv 組織 o1:boss(組織管理員)、alice、bob(成員);回傳模組的服務層
func chainEnv(t *testing.T) (*integrations.Service, *store.Mem, *model.Principal, *model.Principal, *model.Principal) {
	t.Helper()
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.CreateOrg(model.Org{ID: "o1", Name: "組織一", CreatedAt: now})
	for _, u := range []struct{ id, email, role string }{{"u1", "boss@x", "admin"}, {"u2", "alice@x", "member"}, {"u3", "bob@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		_ = st.SaveMember(model.Member{Org: "o1", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	alice := &model.Principal{User: model.User{ID: "u2", Email: "Alice@X", Role: "member"}}
	bob := &model.Principal{User: model.User{ID: "u3", Email: "bob@x", Role: "member"}}
	return integrations.For(svc), st, boss, alice, bob
}

// 存放格式:store 的通用組織設定文件,data 和以前 PG 專屬方法存的一模一樣(設定本身、saved = false、不含 updatedAt / updatedBy)
func TestStoredDocFormatUnchanged(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	if _, err := svc.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Add: []string{"a/b"}}); err != nil {
		t.Fatal(err)
	}
	base := "https://gl.example.com"
	if _, err := svc.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{BaseURL: &base, Add: []string{"g/p"}}); err != nil {
		t.Fatal(err)
	}
	d, err := st.GetOrgDoc("o1", "github")
	if err != nil || string(d.Data) != `{"repos":["a/b"],"saved":false}` || d.UpdatedBy != "boss@x" || d.UpdatedAt == nil {
		t.Fatalf("%s %+v %v", d.Data, d, err)
	}
	d, err = st.GetOrgDoc("o1", "gitlab")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(d.Data, &m)
	if len(m) != 3 || m["baseUrl"] != base || m["saved"] != false {
		t.Fatalf("%s", d.Data)
	}
	// 以前 PG 專屬方法存的資料(data 沒有 updatedAt)照讀
	_ = st.SaveOrgDoc("o1", "github", store.OrgDoc{Data: json.RawMessage(`{"repos":["x/y"],"saved":false}`), UpdatedBy: "old@x"})
	g, err := svc.GetGitHubSettings(boss, "o1")
	if err != nil || !g.Saved || len(g.Repos) != 1 || g.Repos[0] != "x/y" || g.UpdatedBy != "old@x" {
		t.Fatalf("%+v %v", g, err)
	}
}

// GitHub 設定:成員讀、管理員改;正規化、去重、加減;沒存過是空白預設(saved = false)
func TestGitHubSettings(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	g, err := svc.GetGitHubSettings(alice, "o1")
	if err != nil || g.Saved || len(g.Repos) != 0 || g.Repos == nil {
		t.Fatalf("沒存過是空白預設:%+v %v", g, err)
	}
	if _, err := svc.SaveGitHubSettings(alice, "o1", integrations.GitHubSettingsInput{Add: []string{"a/b"}}); code(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	if _, err := svc.GetGitHubSettings(&model.Principal{User: model.User{ID: "nobody"}}, "o1"); code(err) != 403 {
		t.Fatal("不是成員不能讀", err)
	}
	if _, err := svc.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Add: []string{"evoleap"}}); code(err) != 400 {
		t.Fatal("格式不對 400", err)
	}
	g, err = svc.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{
		Repos: strs("https://github.com/tim72117/evoleap.git", "TIM72117/EVOLEAP", "git@github.com:a/b.git")})
	if err != nil || len(g.Repos) != 2 || g.Repos[0] != "tim72117/evoleap" || g.Repos[1] != "a/b" || !g.Saved || g.UpdatedBy != "boss@x" || g.UpdatedAt == nil {
		t.Fatalf("%+v %v", g, err)
	}
	// 只加減:不影響其他的
	g, err = svc.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Add: []string{"c/d", "A/B"}, Remove: []string{"https://github.com/Tim72117/Evoleap"}})
	if err != nil || len(g.Repos) != 2 || g.Repos[0] != "a/b" || g.Repos[1] != "c/d" {
		t.Fatalf("%+v %v", g, err)
	}
	if got, err := svc.GetGitHubSettings(alice, "o1"); err != nil || !got.Saved || len(got.Repos) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	// 清空也算存過
	g, _ = svc.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Repos: strs()})
	if len(g.Repos) != 0 || !g.Saved {
		t.Fatalf("%+v", g)
	}
	// 太多
	many := []string{}
	for i := 0; i <= integrations.MaxIntegrationItems; i++ {
		many = append(many, "o/r"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if _, err := svc.SaveGitHubSettings(boss, "o1", integrations.GitHubSettingsInput{Repos: &many}); code(err) != 400 {
		t.Fatal("超過上限 400", err)
	}
}

// 匯入:核心還沒存過才寫入;格式不對的略過;成員不能匯入
func TestGitHubImport(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	if _, err := svc.ImportGitHubSettings(alice, "o1", integrations.GitHubImport{Repos: []string{"a/b"}}); code(err) != 403 {
		t.Fatal(err)
	}
	r, err := svc.ImportGitHubSettings(boss, "o1", integrations.GitHubImport{Repos: []string{"a/b", "bad", "A/B", "c/d"}})
	if err != nil || !r.Imported || len(r.Settings.Repos) != 2 || len(r.Skipped) != 1 || r.Skipped[0] != "bad" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = svc.ImportGitHubSettings(boss, "o1", integrations.GitHubImport{Repos: []string{"x/y"}})
	if err != nil || r.Imported || len(r.Settings.Repos) != 2 || r.Settings.Repos[0] != "a/b" {
		t.Fatalf("存過就不動:%+v %v", r, err)
	}
}

// GitLab 設定:網址正規化、專案(相對網址的路徑)、換網址、匯入
func TestGitLabSettings(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	g, err := svc.GetGitLabSettings(alice, "o1")
	if err != nil || g.Saved || g.BaseURL != "" || g.Projects == nil {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := svc.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{BaseURL: sp("ftp://x")}); code(err) != 400 {
		t.Fatal(err)
	}
	if _, err := svc.SaveGitLabSettings(alice, "o1", integrations.GitLabSettingsInput{BaseURL: sp("https://gl.example.com")}); code(err) != 403 {
		t.Fatal(err)
	}
	g, err = svc.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{BaseURL: sp("https://gl.example.com/api/v4/"),
		Add: []string{"https://gl.example.com/grp/sub/proj/-/merge_requests", "42", "git@gl.example.com:grp/other.git", "GRP/OTHER"}})
	if err != nil || g.BaseURL != "https://gl.example.com" || len(g.Projects) != 3 || g.Projects[0] != "grp/sub/proj" || g.Projects[1] != "42" || g.Projects[2] != "grp/other" {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := svc.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{Add: []string{"solo"}}); code(err) != 400 {
		t.Fatal("專案格式不對 400", err)
	}
	// 只改專案,網址不動;網址給空字串 = 清掉
	g, _ = svc.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{Remove: []string{"42"}})
	if g.BaseURL != "https://gl.example.com" || len(g.Projects) != 2 {
		t.Fatalf("%+v", g)
	}
	g, _ = svc.SaveGitLabSettings(boss, "o1", integrations.GitLabSettingsInput{BaseURL: sp("")})
	if g.BaseURL != "" || len(g.Projects) != 2 {
		t.Fatalf("%+v", g)
	}
	// 匯入:另一個組織
	svc2, _, boss2, _, _ := chainEnv(t)
	r, err := svc2.ImportGitLabSettings(boss2, "o1", integrations.GitLabImport{BaseURL: "not a url", Projects: []string{"g/p", "x"}})
	if err != nil || !r.Imported || r.Settings.BaseURL != "" || len(r.Settings.Projects) != 1 || len(r.Skipped) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = svc2.ImportGitLabSettings(boss2, "o1", integrations.GitLabImport{BaseURL: "https://other"})
	if r.Imported || r.Settings.Projects[0] != "g/p" {
		t.Fatalf("%+v", r)
	}
}
