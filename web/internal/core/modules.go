package core

import (
	"sort"
)

// web 的選用模組(做法一,同 omixor-core/module):模組是獨立的套件(例如 internal/modules/integrations),在 init() 用這裡的
// 註冊函式接上 web(view、action、MCP 工具、組織載入時的工作…);要不要編進去由入口程式 cmd/omixor-web/modules.go 的 `import _` 決定。
// 這個套件(internal/core)本身不 import 任何模組。
//
// 這裡提供:
//   - RegisterWebModule:記下編進來的模組(view「system」的 modules,畫面依此顯示設定頁、分頁)
//   - RegisterWebOnlyTab:web 才有、核心沒登記功能 key 的分頁(目前沒有模組用;原本的開發進度 › GitLab 已併進 Repositories)
//   - OnConnLoad、OnConnRefresh:讀組織的連線後(拿著 Mu)、背景確認登入後(不拿 Mu)要做的事
//   - SetIssueRepoSource:Issue 所屬倉庫的候選(view「issue_repos」)
//   - 模組要用的內部工具的公開版本(module_api.go)
//
// 都只能在 init() 呼叫(不上鎖)。

var (
	webModules     []string
	connLoadHooks  []func(a *App)
	connRefreshers []func(a *App)
	// issueRepoSource 見 SetIssueRepoSource;nil = 沒有模組提供(只列 Issue 已經用過的)
	issueRepoSource func(a *App) (github, gitlab []string)
)

// RegisterWebModule 記下編進來的模組 id(重複會 panic)
func RegisterWebModule(id string) {
	for _, x := range webModules {
		if x == id {
			panic("web 模組重複註冊:" + id)
		}
	}
	webModules = append(webModules, id)
}

// WebModules 編進來的 web 模組(排序過)
func WebModules() []string {
	out := append([]string{}, webModules...)
	sort.Strings(out)
	return out
}

// HasWebModule 這次編譯有沒有這個 web 模組
func HasWebModule(id string) bool {
	for _, x := range webModules {
		if x == id {
			return true
		}
	}
	return false
}

// RegisterWebOnlyTab web 才有、核心沒登記功能 key 的分頁(永遠顯示;頁面自己的開關開著時,即使核心登記的分頁全關,頁面也顯示)
func RegisterWebOnlyTab(section, tab string) {
	webOnlyTabs[section] = append(webOnlyTabs[section], tab)
}

// WebOnlyTabs 目前的 web 獨有分頁(view「system」的 webOnlyTabs;畫面的 features.ts 照這個)
func WebOnlyTabs() map[string][]string {
	out := map[string][]string{}
	for k, v := range webOnlyTabs {
		out[k] = append([]string{}, v...)
	}
	return out
}

// OnConnLoad 讀目前組織的連線之後(拿著 Mu,在背景確認登入之前)呼叫
func OnConnLoad(f func(a *App)) { connLoadHooks = append(connLoadHooks, f) }

// OnConnRefresh 讀組織資料、背景確認登入之後,切換組織時更新服務資料用(不拿 Mu;會打網路)
func OnConnRefresh(f func(a *App)) { connRefreshers = append(connRefreshers, f) }

// SetIssueRepoSource 設定 Issue 所屬倉庫的候選(拿著 Mu 呼叫,不能打網路);只能有一個
func SetIssueRepoSource(f func(a *App) (github, gitlab []string)) {
	if issueRepoSource != nil {
		panic("Issue 倉庫候選重複設定")
	}
	issueRepoSource = f
}
