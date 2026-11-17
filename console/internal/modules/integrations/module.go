// Package integrations 是 omixor-web 的選用模組「程式碼倉庫(GitHub、GitLab)」:讀追蹤中 repo 的 PR、Actions(GitHub)、
// MR、Pipeline(GitLab),開發進度 › Repositories(功能 development.repos,兩個平台的 repo 一起列)、
// 設定 › 程式碼倉庫的連線與追蹤清單,以及 GitHub 的 MCP 工具(github、github_runs、github_run、github_pull)。
// 組織層的設定(追蹤的 repo、GitLab 網址與專案)存在核心的選用模組 omixor-core/modules/integrations。
//
// 用法:cmd/omixor-web/modules.go `import _ "omixor-web/internal/modules/integrations"` 才會編進去(做法一,見 internal/core/modules.go);
// 沒 import 時:
//   - view「github」「gitlab」「code_repos」、action github_* / gitlab_* / code_repos_refresh、背景更新都不存在
//   - MCP 的 github* 工具不出現在 tools/list(規格 spec/tools.json 照舊有,呼叫回「這台伺服器沒有編進這個工具」,見 core mcp 的 HideMissing)
//   - 核心模組也不會編進去:功能清單沒有 development.repos;view「system」的 modules 沒有 integrations,
//     畫面因此不顯示 Repositories 分頁與 設定 › 程式碼倉庫
//
// 原本的 GitHub 分頁(功能 development.github,已退場)與 web 獨有的 GitLab 分頁(原本用 RegisterWebOnlyTab 登記)併進 Repositories,
// 不再單獨登記。
//
// 檔案(原本在 internal/core 與 internal/mcp,搬過來時邏輯不變):
//
//	github.go        GitHub(原 internal/core/github.go)
//	gitlab.go        GitLab(原 internal/core/gitlab.go)
//	integrations.go  和核心同步組織層設定(原 internal/core/integrations.go)
//	repos.go         開發進度 › Repositories 的清單(view code_repos、action code_repos_refresh)
//	tools_github.go  GitHub 的 MCP 工具(原 internal/mcp/tools_github.go)
//	state.go         模組自己的狀態(原本是 internal/core connState 的 GH*、GL* 欄位)
package integrations

import (
	"path/filepath"

	_ "omixor-core/modules/integrations" // 核心的同名模組(組織層設定的 API 與功能 development.repos)
	"omixor-web/internal/core"
)

// ModuleID web 模組代號(view「system」的 modules)
const ModuleID = "integrations"

func init() {
	core.RegisterWebModule(ModuleID)

	// 讀組織的連線之後(拿著 Mu):這台的 github.json / gitlab.json 與快照重設,背景從核心讀組織層設定
	core.OnConnLoad(func(a *core.App) {
		ghLoad(a)
		glLoad(a)
	})
	// 背景確認登入之後(切換組織時):重新讀 GitHub / GitLab 的資料
	core.OnConnRefresh(func(a *core.App) {
		if ghLoggedIn(a) {
			GHRefresh(a)
		}
		if GLLoggedIn(a) {
			GLRefresh(a)
		}
	})
	// 新組織用「複製目前的組織」時,一併複製這台用哪個連線(github.json、gitlab.json)
	core.OnOrgCreate(func(a *core.App, dir, template string) {
		if template != "copyCurrent" {
			return
		}
		s := stateOf(a)
		_ = core.WriteJSON(filepath.Join(dir, "github.json"), s.GH)
		_ = core.WriteJSON(filepath.Join(dir, "gitlab.json"), s.GL)
	})
	// Issue 所屬倉庫的候選:GitHub 追蹤的 repos(owner/name)與 GitLab 追蹤的專案
	// (有讀到專案資料就用 path_with_namespace,否則照追蹤時寫的)
	core.SetIssueRepoSource(func(a *core.App) ([]string, []string) {
		s := stateOf(a)
		gh := append([]string{}, s.GH.Repos...)
		gl := []string{}
		paths := map[string]string{}
		for _, p := range s.GLSnap.Projects {
			if p.Key != "" && p.Path != "" {
				paths[p.Key] = p.Path
			}
		}
		for _, k := range s.GL.Projects {
			if p, ok := paths[k]; ok {
				k = p
			}
			gl = append(gl, k)
		}
		return gh, gl
	})
}
