package api_test

import (
	"testing"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/module"
	"omixor-core/service"
)

func agentNamed(id string) model.Agent { return model.Agent{ID: id, Name: id, Active: true} }

func newClient(e *env) *client.Client { return client.New(e.srv.URL) }

// GitHub、GitLab 設定是選用模組(core/modules/integrations;有模組時的測試在那裡)。
// 這個測試套件不 import 模組 = 沒編進模組的伺服器:路由不存在(404)、功能清單沒有 development.repos(Repositories 分頁不出現;退場的 development.github 也沒有)、/api/modules 是空的
func TestWithoutIntegrationsModule(t *testing.T) {
	if module.Has("integrations") {
		t.Fatal("core/api 的測試不該編進模組")
	}
	for _, k := range []string{"development.repos", "development.github"} {
		if _, ok := model.FeatureByKey(k); ok {
			t.Fatal("沒編進模組時不該登記", k)
		}
	}
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/orgs/o1/integrations/github", "/api/orgs/o1/integrations/gitlab"} {
		if err := boss.Do("GET", p, nil, nil); status(err) != 404 {
			t.Fatal(p, "要 404", err)
		}
		if err := boss.Do("POST", p, map[string]any{"add": []string{"a/b"}}, nil); status(err) != 404 {
			t.Fatal(p, "要 404", err)
		}
	}
	var infos []module.Info
	if err := boss.Do("GET", "/api/modules", nil, &infos); err != nil || len(infos) != 0 {
		t.Fatal(infos, err)
	}
	sf, err := boss.GetServerFeatures()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sf.Features {
		if f.Key == "development.repos" || f.Key == "development.github" {
			t.Fatal("系統層清單不該有", f.Key)
		}
	}
	ss, err := boss.GetSystemSettings("o1")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ss.Features {
		if f.Key == "development.repos" || f.Key == "development.github" {
			t.Fatal("組織層清單不該有", f.Key)
		}
	}
	if _, ok := ss.ServerFeatures["development.repos"]; ok {
		t.Fatal("serverFeatures 不該有 development.repos")
	}
	// 沒登記的 key 不能存(像那功能不存在);退場的 development.github 轉成 development.repos,沒模組時一樣 400
	for _, k := range []string{"development.repos", "development.github"} {
		if _, err := boss.SaveServerFeatures(service.SystemSettingsInput{Features: map[string]bool{k: false}}); status(err) != 400 {
			t.Fatal("沒登記的 key 要 400", k, err)
		}
		if _, err := boss.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{k: false}}); status(err) != 400 {
			t.Fatal("沒登記的 key 要 400", k, err)
		}
	}
}
