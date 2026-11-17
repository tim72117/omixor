package integrations

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
)

// 有編進模組(這個測試套件就是模組本身):view、action、MCP 工具、核心的功能 development.repos 與 view「system」的 modules 都在;
// GitLab 併進 Repositories,不再登記 web 獨有分頁(webOnlyTabs 是空的)。
// 沒編進模組的情況在 internal/core 的 modules_test.go 與 internal/mcp 的 modules_test.go 測

func TestModuleRegisteredInWeb(t *testing.T) {
	a := connTestApp(t)
	if !core.HasWebModule(ModuleID) {
		t.Fatal("web 模組沒註冊")
	}
	if f, ok := model.FeatureByKey("development.repos"); !ok || f.Experimental || !f.Default {
		t.Fatal("核心模組也要編進來(功能 development.repos,真功能、預設開)", f)
	}
	if _, ok := model.FeatureByKey("development.github"); ok {
		t.Fatal("development.github 已退場")
	}
	for _, v := range []string{"github", "gitlab", "code_repos"} {
		if _, ok := core.View(a, v); !ok {
			t.Fatal("要有 view", v)
		}
	}
	v, _ := core.View(a, "system")
	b, _ := json.Marshal(v)
	var sys struct {
		Modules     []string            `json:"modules"`
		WebOnlyTabs map[string][]string `json:"webOnlyTabs"`
		Features    []struct{ Key string }
	}
	if err := json.Unmarshal(b, &sys); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sys.Modules, ModuleID) || len(sys.WebOnlyTabs) != 0 {
		t.Fatalf("%s", b)
	}
	repos := false
	for _, f := range sys.Features {
		repos = repos || f.Key == "development.repos"
	}
	if !repos {
		t.Fatalf("清單要有 development.repos:%s", b)
	}
	for _, n := range []string{"github", "github_runs", "github_run", "github_pull"} {
		if !slices.Contains(mcp.ToolNames(), n) {
			t.Fatal("tools/list 要有", n)
		}
	}
	// Issue 所屬倉庫的候選由模組提供
	a.Mu.Lock()
	stateOf(a).GH.Repos = []string{"a/b"}
	a.Mu.Unlock()
	r, _ := core.View(a, "issue_repos")
	if gh := r.(map[string]any)["github"]; gh == nil || len(gh.([]any)) != 1 {
		t.Fatalf("%v", r)
	}
}

// GitHub 的 view 與 action(原本在 internal/core 的 conn_test.go TestConnectionViewsAndActions)
func TestGitHubViewAndActions(t *testing.T) {
	a := connTestApp(t)
	f := core.NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.URL.Path == "/user" && r.Header.Get("Authorization") == "Bearer gho_x" {
			return 200, map[string]any{"login": "octo"}
		}
		return 401, map[string]any{"message": "Bad credentials"}
	})
	defer f.Close()
	view := func(name string) map[string]any {
		v, ok := core.View(a, name)
		if !ok {
			t.Fatalf("沒有 view %s", name)
		}
		return v.(map[string]any)
	}
	gh := view("github")
	if gh["connection"] != nil || gh["loggedIn"] != false || gh["settings"].(map[string]any)["connection"] != "github" {
		t.Fatalf("%v", gh)
	}
	if _, err := core.Action(a, "github_ensure_connection", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Action(a, "connection_bearer", map[string]any{"id": "github", "token": "gho_x"}); err != nil {
		t.Fatal(err)
	}
	gh = view("github")
	if gh["loggedIn"] != true || gh["state"].(map[string]any)["account"] != "octo" {
		t.Fatalf("%v", gh)
	}
	if _, err := core.Action(a, "github_add_repo", map[string]any{"repo": "x"}); err == nil {
		t.Fatal("格式錯誤要擋")
	}
	if v := view("connections"); len(v["connections"].([]any)) != 3 {
		t.Fatalf("多了 GitHub 連線:%v", v)
	}
	if out, err := mcp.Run(a, "github", map[string]any{}, core.MCPActor); err != nil && strings.Contains(err.Error(), "沒有編進") {
		t.Fatalf("有模組時 github 工具要能用:%v %v", out, err)
	}
}
