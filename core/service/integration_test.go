package service_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func strs(s ...string) *[]string { return &s }

// GitHub 設定:成員讀、管理員改;正規化、去重、加減;沒存過是空白預設(saved = false)
func TestGitHubSettings(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	g, err := svc.GetGitHubSettings(alice, "o1")
	if err != nil || g.Saved || len(g.Repos) != 0 || g.Repos == nil {
		t.Fatalf("沒存過是空白預設:%+v %v", g, err)
	}
	if _, err := svc.SaveGitHubSettings(alice, "o1", service.GitHubSettingsInput{Add: []string{"a/b"}}); code(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	if _, err := svc.GetGitHubSettings(&model.Principal{User: model.User{ID: "nobody"}}, "o1"); code(err) != 403 {
		t.Fatal("不是成員不能讀", err)
	}
	if _, err := svc.SaveGitHubSettings(boss, "o1", service.GitHubSettingsInput{Add: []string{"evoleap"}}); code(err) != 400 {
		t.Fatal("格式不對 400", err)
	}
	g, err = svc.SaveGitHubSettings(boss, "o1", service.GitHubSettingsInput{
		Repos: strs("https://github.com/tim72117/evoleap.git", "TIM72117/EVOLEAP", "git@github.com:a/b.git")})
	if err != nil || len(g.Repos) != 2 || g.Repos[0] != "tim72117/evoleap" || g.Repos[1] != "a/b" || !g.Saved || g.UpdatedBy != "boss@x" || g.UpdatedAt == nil {
		t.Fatalf("%+v %v", g, err)
	}
	// 只加減:不影響其他的
	g, err = svc.SaveGitHubSettings(boss, "o1", service.GitHubSettingsInput{Add: []string{"c/d", "A/B"}, Remove: []string{"https://github.com/Tim72117/Evoleap"}})
	if err != nil || len(g.Repos) != 2 || g.Repos[0] != "a/b" || g.Repos[1] != "c/d" {
		t.Fatalf("%+v %v", g, err)
	}
	if got, err := svc.GetGitHubSettings(alice, "o1"); err != nil || !got.Saved || len(got.Repos) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	// 清空也算存過
	g, _ = svc.SaveGitHubSettings(boss, "o1", service.GitHubSettingsInput{Repos: strs()})
	if len(g.Repos) != 0 || !g.Saved {
		t.Fatalf("%+v", g)
	}
	// 太多
	many := []string{}
	for i := 0; i <= model.MaxIntegrationItems; i++ {
		many = append(many, "o/r"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if _, err := svc.SaveGitHubSettings(boss, "o1", service.GitHubSettingsInput{Repos: &many}); code(err) != 400 {
		t.Fatal("超過上限 400", err)
	}
}

// 匯入:核心還沒存過才寫入;格式不對的略過;成員不能匯入
func TestGitHubImport(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	if _, err := svc.ImportGitHubSettings(alice, "o1", service.GitHubImport{Repos: []string{"a/b"}}); code(err) != 403 {
		t.Fatal(err)
	}
	r, err := svc.ImportGitHubSettings(boss, "o1", service.GitHubImport{Repos: []string{"a/b", "bad", "A/B", "c/d"}})
	if err != nil || !r.Imported || len(r.Settings.Repos) != 2 || len(r.Skipped) != 1 || r.Skipped[0] != "bad" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = svc.ImportGitHubSettings(boss, "o1", service.GitHubImport{Repos: []string{"x/y"}})
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
	if _, err := svc.SaveGitLabSettings(boss, "o1", service.GitLabSettingsInput{BaseURL: sp("ftp://x")}); code(err) != 400 {
		t.Fatal(err)
	}
	if _, err := svc.SaveGitLabSettings(alice, "o1", service.GitLabSettingsInput{BaseURL: sp("https://gl.example.com")}); code(err) != 403 {
		t.Fatal(err)
	}
	g, err = svc.SaveGitLabSettings(boss, "o1", service.GitLabSettingsInput{BaseURL: sp("https://gl.example.com/api/v4/"),
		Add: []string{"https://gl.example.com/grp/sub/proj/-/merge_requests", "42", "git@gl.example.com:grp/other.git", "GRP/OTHER"}})
	if err != nil || g.BaseURL != "https://gl.example.com" || len(g.Projects) != 3 || g.Projects[0] != "grp/sub/proj" || g.Projects[1] != "42" || g.Projects[2] != "grp/other" {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := svc.SaveGitLabSettings(boss, "o1", service.GitLabSettingsInput{Add: []string{"solo"}}); code(err) != 400 {
		t.Fatal("專案格式不對 400", err)
	}
	// 只改專案,網址不動;網址給空字串 = 清掉
	g, _ = svc.SaveGitLabSettings(boss, "o1", service.GitLabSettingsInput{Remove: []string{"42"}})
	if g.BaseURL != "https://gl.example.com" || len(g.Projects) != 2 {
		t.Fatalf("%+v", g)
	}
	g, _ = svc.SaveGitLabSettings(boss, "o1", service.GitLabSettingsInput{BaseURL: sp("")})
	if g.BaseURL != "" || len(g.Projects) != 2 {
		t.Fatalf("%+v", g)
	}
	// 匯入:另一個組織
	svc2, _, boss2, _, _ := chainEnv(t)
	r, err := svc2.ImportGitLabSettings(boss2, "o1", service.GitLabImport{BaseURL: "not a url", Projects: []string{"g/p", "x"}})
	if err != nil || !r.Imported || r.Settings.BaseURL != "" || len(r.Settings.Projects) != 1 || len(r.Skipped) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	r, _ = svc2.ImportGitLabSettings(boss2, "o1", service.GitLabImport{BaseURL: "https://other"})
	if r.Imported || r.Settings.Projects[0] != "g/p" {
		t.Fatalf("%+v", r)
	}
}
