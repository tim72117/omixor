package core

import (
	"slices"
	"strings"
)

// 畫面專用的範例資料動作(只有人能做、MCP 沒有工具的),行為同 Swift AppState。
// 找不到 id 時 Swift 是什麼都不做;這裡回傳錯誤讓畫面知道。

func argString(args map[string]any, k string) string {
	if s, ok := ScalarString(args[k]); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func argBool(args map[string]any, k string) bool {
	switch x := args[k].(type) {
	case bool:
		return x
	case string:
		l := strings.ToLower(x)
		return l == "true" || l == "1" || l == "yes"
	}
	return false
}

// mockAction 拿鎖、以登入者身分執行 f;成功時通知 topics,回傳 collection 裡該 id 的最新內容
func mockAction(a *App, args map[string]any, collection, id string, f func() bool, topics ...string) (any, error) {
	a.Mu.Lock()
	var ok bool
	a.As(UserActor(a, args), func() { ok = f() })
	var out any
	if ok {
		if _, it := a.Find(collection, id); it != nil {
			out = Clone(it)
		}
	}
	a.Mu.Unlock()
	if !ok {
		return nil, UserError{"找不到 " + id}
	}
	a.Changed(topics...)
	return out, nil
}

func init() {
	RegisterAction("approval_decide", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return mockAction(a, args, "approvals", id, func() bool { return a.DecideApproval(id, argBool(args, "approve")) }, "approvals")
	})
	RegisterAction("deployment_approve", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return mockAction(a, args, "deployments", id, func() bool { return a.ApproveDeployment(id) }, "deployments")
	})
	RegisterAction("deployment_rollback", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		a.Mu.Lock()
		var nd map[string]any
		a.As(UserActor(a, args), func() {
			if nd = a.Rollback(id); nd != nil {
				a.trackDeployment(Str(nd, "id")) // 同 PipeDeploymentsTab:回滾完成後回寫服務版本
			}
		})
		var out any
		if nd != nil {
			out = Clone(nd)
		}
		a.Mu.Unlock()
		if nd == nil {
			return nil, UserError{"找不到 " + id}
		}
		a.Changed("deployments")
		return out, nil
	})
	RegisterAction("automation_autonomy", func(a *App, args map[string]any) (any, error) {
		id, au := argString(args, "id"), argString(args, "autonomy")
		if !slices.Contains(Autonomies, au) {
			return nil, UserError{"autonomy 必須是 " + strings.Join(Autonomies, "、") + " 之一,收到「" + au + "」"}
		}
		return mockAction(a, args, "automations", id, func() bool { return a.SetAutonomy(au, id) }, "automations")
	})
	RegisterAction("automation_toggle", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return mockAction(a, args, "automations", id, func() bool { return a.ToggleAutomation(id) }, "automations")
	})
	RegisterAction("integration_set", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		return mockAction(a, args, "integrations", id, func() bool { return a.SetIntegration(id, argBool(args, "connected")) }, "integrations")
	})
	RegisterAction("incident_note", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		text := argString(args, "text")
		if text == "" {
			return nil, UserError{"註記不能是空的"}
		}
		return mockAction(a, args, "incidents", id, func() bool { return a.AddIncidentNote(id, text, a.Actor()) }, "incidents")
	})
}
