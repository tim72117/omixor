package api_test

import (
	"testing"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

func agentNamed(id string) model.Agent { return model.Agent{ID: id, Name: id, Active: true} }

func newClient(e *env) *client.Client { return client.New(e.srv.URL) }

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
	g, err := alice.GetGitHubSettings("o1")
	if err != nil || g.Saved || len(g.Repos) != 0 {
		t.Fatal(g, err)
	}
	if _, err := alice.SaveGitHubSettings("o1", service.GitHubSettingsInput{Add: []string{"a/b"}}); status(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	if _, err := alice.ImportGitHubSettings("o1", service.GitHubImport{Repos: []string{"a/b"}}); status(err) != 403 {
		t.Fatal("成員不能匯入", err)
	}
	r, err := boss.ImportGitHubSettings("o1", service.GitHubImport{Repos: []string{"https://github.com/a/b", "nope"}})
	if err != nil || !r.Imported || len(r.Settings.Repos) != 1 || len(r.Skipped) != 1 {
		t.Fatal(r, err)
	}
	g, err = boss.SaveGitHubSettings("o1", service.GitHubSettingsInput{Add: []string{"c/d"}})
	if err != nil || len(g.Repos) != 2 || g.UpdatedBy != "boss@example.com" {
		t.Fatal(g, err)
	}
	if _, err := boss.SaveGitHubSettings("o1", service.GitHubSettingsInput{Add: []string{"bad"}}); status(err) != 400 {
		t.Fatal(err)
	}
	if got, err := alice.GetGitHubSettings("o1"); err != nil || !got.Saved || len(got.Repos) != 2 {
		t.Fatal(got, err)
	}
	r, err = boss.ImportGitHubSettings("o1", service.GitHubImport{Repos: []string{"x/y"}})
	if err != nil || r.Imported || len(r.Settings.Repos) != 2 {
		t.Fatal("第二次匯入不動", r, err)
	}
	if _, err := boss.GetGitHubSettings("nope"); status(err) != 404 {
		t.Fatal(err)
	}

	base := "https://gitlab.example.com/"
	l, err := boss.SaveGitLabSettings("o1", service.GitLabSettingsInput{BaseURL: &base, Add: []string{"https://gitlab.example.com/g/p"}})
	if err != nil || l.BaseURL != "https://gitlab.example.com" || len(l.Projects) != 1 || l.Projects[0] != "g/p" {
		t.Fatal(l, err)
	}
	if _, err := alice.SaveGitLabSettings("o1", service.GitLabSettingsInput{Add: []string{"g/q"}}); status(err) != 403 {
		t.Fatal(err)
	}
	if got, err := alice.GetGitLabSettings("o1"); err != nil || got.BaseURL != "https://gitlab.example.com" {
		t.Fatal(got, err)
	}
	if lr, err := boss.ImportGitLabSettings("o1", service.GitLabImport{BaseURL: "https://other"}); err != nil || lr.Imported {
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
	if got, err := role.GetGitHubSettings("o1"); err != nil || len(got.Repos) != 2 {
		t.Fatal("角色憑證可以讀", got, err)
	}
	if got, err := role.GetGitLabSettings("o1"); err != nil || len(got.Projects) != 1 {
		t.Fatal(got, err)
	}
	if _, err := role.SaveGitHubSettings("o1", service.GitHubSettingsInput{Add: []string{"z/z"}}); status(err) != 401 {
		t.Fatal("角色憑證不能改(不是 omixor token)", err)
	}
}
