package core

import (
	"testing"

	"omixor-core/model"
)

// 沒編進選用模組 integrations(這個測試套件不 import 模組):GitHub、GitLab、Repositories 的 view 與 action 不存在,
// 功能清單沒有 development.repos(退場的 development.github 也沒有),view「system」的 modules 與 webOnlyTabs 是空的
// (畫面因此不顯示 Repositories 分頁與 設定 › 程式碼倉庫)。
// 有模組的情況在 internal/modules/integrations 測
func TestWithoutIntegrationsModule(t *testing.T) {
	a := connTestApp(t)
	if HasWebModule("integrations") || len(WebModules()) != 0 {
		t.Fatal("internal/core 的測試不該編進模組", WebModules())
	}
	for _, k := range []string{"development.repos", "development.github"} {
		if _, ok := model.FeatureByKey(k); ok {
			t.Fatal("沒編進模組時不該登記", k)
		}
	}
	for _, v := range []string{"github", "gitlab", "code_repos"} {
		if _, ok := View(a, v); ok {
			t.Fatal("不該有 view", v)
		}
	}
	for _, n := range []string{"github_add_repo", "github_refresh", "gitlab_settings", "gitlab_refresh", "code_repos_refresh"} {
		if _, err := Action(a, n, nil); err == nil || err.Error() != "沒有這個動作:"+n {
			t.Fatal(n, err)
		}
	}
	a.Mu.Lock()
	v := a.systemViewData()
	a.Mu.Unlock()
	if len(v.Modules) != 0 || len(v.WebOnlyTabs) != 0 {
		t.Fatalf("%+v %+v", v.Modules, v.WebOnlyTabs)
	}
	for _, f := range v.Features {
		if f.Key == "development.repos" || f.Key == "development.github" {
			t.Fatal("清單不該有", f.Key)
		}
	}
	r, _ := View(a, "issue_repos")
	m := r.(map[string]any)
	if len(m["github"].([]any)) != 0 || len(m["gitlab"].([]any)) != 0 {
		t.Fatalf("%v", m)
	}
	// 沒有模組時清單沒有 development.repos:畫面(features.ts 的 flagsOf)清單沒列的 key 當作關,所以不顯示 Repositories 分頁
	for _, f := range v.Features {
		if f.Parent == "development" && f.Name == "Repositories" {
			t.Fatal("沒有模組時清單不該有 Repositories", f.Key)
		}
	}
}
