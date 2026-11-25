package mcp

import (
	"omixor-web/internal/core"
)

// activity 核心端點(本機的角色端點 /mcp/agent、對外 MCP 連接埠)共用的宿主掛勾:
// 每個請求計入 MCP 狀態(請求數、最後請求時間、最後工具),每次「會改資料」的工具處理完後,重讀快取並通知畫面
// (核心可能多了 Issue、留言、任務狀態、文件;重讀的內容見 core.App.RoleCallFinished)。
// 重讀掛在每次工具呼叫自己的收尾(Tool 回傳的函式),不用跨請求共用的旗標:並行請求時,先結束的唯讀請求不會洗掉還在跑的寫入請求的標記
type activity struct {
	a *core.App
	// writes 這個工具會不會改資料;nil = 每個工具都算(角色端點:白名單內的工具大多會寫)
	writes func(tool string) bool
	// finished 寫入後的重讀;nil = a.RoleCallFinished(測試注入用)
	finished func()
}

// Begin 請求開始;回傳的函式在處理完後呼叫
func (t *activity) Begin() func() {
	a := t.a
	a.Mu.Lock()
	a.MCP.Requests++
	now := core.Now()
	a.MCP.LastRequestAt = &now
	a.Mu.Unlock()
	return func() { a.Changed("mcp") }
}

// Tool 工具執行前呼叫;會寫資料的工具回傳收尾函式(處理完後重讀快取)
func (t *activity) Tool(name string) func() {
	t.a.Mu.Lock()
	t.a.MCP.LastTool = name
	t.a.Mu.Unlock()
	if t.writes == nil || t.writes(name) {
		if t.finished != nil {
			return t.finished
		}
		return t.a.RoleCallFinished
	}
	return nil
}
