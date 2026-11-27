package core

import (
	"slices"

	"omixor-core/version"
)

// AccountView 畫面用的「我是誰、資料在哪、支援什麼功能」(view「account」)
type AccountView struct {
	LocalStorage string `json:"localStorage"` // "postgres" 或 "files"(沒設資料庫)
	Error        string `json:"error,omitempty"`
	// Self 目前操作的使用者的 handle(本機使用者,或登入者的 email);畫面用它比對 Issue 的 assigneeActor 判斷「是不是我接受的」
	Self string `json:"self,omitempty"`
	// Features 支援的功能代號(version.Features 加上 console 自己登記的)
	Features []string `json:"features"`
}

// ConsoleFeatures console 自己登記的功能代號(由 internal/mcp 在 init 設定:有登記對應工具才宣告,例如 issue_decision_withdraw、issue_decision_reopen);
// account view 併進 Features。core 不能 import mcp,所以用掛鉤
var ConsoleFeatures func() []string

func (a *App) accountView() AccountView {
	v := AccountView{LocalStorage: "files", Self: LocalIdentity, Features: append([]string{}, version.Info().Features...)}
	if DB() != nil {
		v.LocalStorage = "postgres"
	} else {
		v.Error = errNoDB.Error()
	}
	if ConsoleFeatures != nil {
		for _, f := range ConsoleFeatures() {
			if !slices.Contains(v.Features, f) {
				v.Features = append(v.Features, f)
			}
		}
	}
	return v
}

func init() {
	RegisterView("account", func(a *App) any { return a.accountView() })
}
