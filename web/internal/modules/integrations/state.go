package integrations

import "omixor-web/internal/core"

// state 每個 App 一份(目前組織的):設定、快照、GitHub 條件請求的快取。原本是 internal/core 的 connState 的欄位,
// 搬成模組自己的狀態(存在 App.Ext["integrations"])。讀寫都要拿著 App.Mu;換組織時 ghLoad、glLoad 重設(見 module.go)
type state struct {
	GH      GHSettings
	GHSnap  GHSnapshot
	GHETags map[string]ghCached // GitHub 條件請求的快取(ETag → 上次的內容),只在記憶體

	GL     GLSettings
	GLSnap GLSnapshot
}

const extKey = "integrations"

// stateOf 目前的狀態(拿著 Mu)
func stateOf(a *core.App) *state {
	if s, ok := a.Ext[extKey].(*state); ok {
		return s
	}
	s := &state{GH: DefaultGHSettings(), GHSnap: emptyGHSnapshot(), GL: DefaultGLSettings(), GLSnap: emptyGLSnapshot()}
	a.Ext[extKey] = s
	return s
}
