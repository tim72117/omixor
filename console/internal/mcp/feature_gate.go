package mcp

import (
	"bytes"
	"encoding/json"

	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-web/internal/core"
)

// 範例資料工具依功能開關過濾(只在 /mcp 端點):工具對應的功能(core/model/features.go 的 key)在目前組織關閉時,
// tools/list 不列、tools/call 回「這個功能已關閉」。
//
// 只擋回傳或修改範例資料、而且明確屬於某個功能的工具;核心工具(Issue、角色、頻道、文件櫃…)、
// 跨很多集合的 list / get、切頁面的 navigate(本來就會擋關掉的頁面)不在這裡。
// 畫面透過 /api/tool 呼叫(Run)不經過這裡:畫面自己依開關顯示,而且有些開著的功能也會用到這些工具
// (例如事件的後續工作用 work_item_create)。
var featureTools = map[string]string{
	// 總覽是必要功能(永遠開),登記起來只是讓對應關係完整
	"overview":      "overview",
	"activity_post": "overview",

	"alert_update": "monitoring.alerts",
	"alert_create": "monitoring.alerts",
	"log_append":   "monitoring.logs",

	// 工作項目(範例資料)現在只出現在 開發進度 › 概覽 的 Sprint 進度(看板已改成顯示真的 Issue)
	"work_item_create": "development.overview",
	"work_item_move":   "development.overview",

	"pipeline_update":  "pipelines.runs",
	"request_approval": "automation.inbox",
}

// FeatureOfTool 工具對應的功能 key(""= 不依功能開關)
func FeatureOfTool(name string) string { return featureTools[name] }

// closedFeature 工具對應的功能在目前組織關閉時回傳那個功能;沒對應或開著回傳 ok = false。不要拿著 App.Mu 呼叫
func closedFeature(a *core.App, name string) (model.Feature, bool) {
	key := featureTools[name]
	if key == "" || a.FeatureEnabledNow(key) {
		return model.Feature{}, false
	}
	f, ok := model.FeatureByKey(key)
	if !ok {
		f = model.Feature{Key: key, Name: key}
	}
	return f, true
}

// featureLabel 功能的顯示名稱(分頁帶上所屬頁面,例如「監控與告警 › 告警」)
func featureLabel(f model.Feature) string {
	if f.Parent != "" {
		if p, ok := model.FeatureByKey(f.Parent); ok {
			return p.Name + " › " + f.Name
		}
	}
	return f.Name
}

// featureClosedError tools/call 呼叫到關掉的功能的工具
func featureClosedError(name string, f model.Feature) error {
	return ToolError{Message: "這個功能已關閉:「" + featureLabel(f) + "」在系統設定裡關閉了,不能使用 " + name + " 工具。請組織管理員到 設定 › 功能 開啟。"}
}

// handleMessages 一則或一批 JSON-RPC 訊息:每則分開交給核心處理,tools/list 才套用「不列關掉的功能」
// (核心的 Allow 在 tools/call 會回「沒有這個工具」,所以 tools/call 改用 Guard 回清楚的錯誤)
func (h *Handler) handleMessages(data []byte, base coremcp.Hooks) []byte {
	if t := bytes.TrimSpace(data); len(t) > 0 && t[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(t, &batch) == nil {
			var out [][]byte
			for _, m := range batch {
				if r := h.handleOne(m, base); r != nil {
					out = append(out, r)
				}
			}
			if len(out) == 0 {
				return nil
			}
			return append(append([]byte{'['}, bytes.Join(out, []byte{','})...), ']')
		}
	}
	return h.handleOne(data, base)
}

func (h *Handler) handleOne(data []byte, hooks coremcp.Hooks) []byte {
	a := h.App
	var peek struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(data, &peek)
	if peek.Method == "tools/list" {
		hooks.Allow = func(name string) bool {
			_, closed := closedFeature(a, name)
			return !closed && !coremcp.HumanHiddenTools[name] // 委派者只能由角色憑證決定,本機 /mcp 不列
		}
	}
	hooks.Guard = func(name string, args map[string]any) error {
		if err := rejectIdentity(name, args); err != nil { // 不能指定角色身分:400,不執行、不寫入
			return err
		}
		if f, closed := closedFeature(a, name); closed {
			return featureClosedError(name, f)
		}
		return nil
	}
	return srv.Handle(data, hooks)
}
