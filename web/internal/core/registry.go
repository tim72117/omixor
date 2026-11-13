package core

import "fmt"

// 畫面(React)透過 /api 讀寫。各功能在 init() 登記:
//   - View:GET /api/view/<名稱> 回傳的資料(呼叫時已拿著 Mu,不能在裡面發網路請求)
//   - Action:POST /api/action/<名稱> 的動作,給只有人能在畫面上做、MCP 沒有工具的事
//     (核准部署、回滾、切換組織、登入、發佈旁白…)。呼叫時沒有拿 Mu,要自己拿。
// MCP 也有的動作,畫面直接用 POST /api/tool/<工具名稱>,以目前使用者的身分執行。

type ViewFunc func(a *App) any
type ActionFunc func(a *App, args map[string]any) (any, error)

var (
	views   = map[string]ViewFunc{}
	actions = map[string]ActionFunc{}
)

func RegisterView(name string, f ViewFunc) {
	if _, dup := views[name]; dup {
		panic("View 重複登記:" + name)
	}
	views[name] = f
}

func RegisterAction(name string, f ActionFunc) {
	if _, dup := actions[name]; dup {
		panic("Action 重複登記:" + name)
	}
	actions[name] = f
}

func View(a *App, name string) (any, bool) {
	f, ok := views[name]
	if !ok {
		return nil, false
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return Object(f(a)), true
}

func Action(a *App, name string, args map[string]any) (any, error) {
	f, ok := actions[name]
	if !ok {
		return nil, fmt.Errorf("沒有這個動作:%s", name)
	}
	return f(a, args)
}

func ViewNames() []string {
	out := []string{}
	for k := range views {
		out = append(out, k)
	}
	return out
}

// UserError 給畫面看的錯誤訊息
type UserError struct{ Message string }

func (e UserError) Error() string { return e.Message }

// 基本的 View:範例資料集合、導覽、MCP 狀態
func init() {
	RegisterView("nav", func(a *App) any { return a.Nav })
	RegisterView("mcp", func(a *App) any {
		s := a.MCP
		return map[string]any{"status": s, "url": fmt.Sprintf("http://127.0.0.1:%d/mcp", s.Port)}
	})
	RegisterView("me", func(a *App) any { return map[string]any{"id": a.Me, "name": a.Name(Person(a.Me))} })
	RegisterView("sprint", func(a *App) any { return a.Sprint })
	for _, c := range []string{"people", "services", "servers", "repos", "pull_requests", "work_items", "pipeline_runs",
		"deployments", "alerts", "alert_rules", "logs", "dashboards", "incidents", "on_call", "automations",
		"automation_runs", "approvals", "activity", "integrations"} {
		name := c
		RegisterView(name, func(a *App) any { return a.C[name] })
	}
	RegisterAction("navigate", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		f, _ := args["focusId"].(string)
		notice := ""
		if s, ok := args["section"].(string); ok && s != "" {
			// 系統設定關掉的頁面:改去總覽(提示放在 system view 的 notice,畫面上方會顯示)
			if to, n := a.GuardSection(s); n != "" {
				s, f, notice = to, "", n
			}
			a.Nav.Section = s
		}
		a.Nav.FocusID = f
		if s, ok := args["env"].(string); ok && s != "" {
			a.Nav.Env = s
		}
		if s, ok := args["timeRange"].(string); ok && s != "" {
			a.Nav.TimeRange = s
		}
		nav := a.Nav
		a.Mu.Unlock()
		if notice != "" {
			a.Changed("nav", "system")
			return map[string]any{"section": nav.Section, "focusId": nav.FocusID, "env": nav.Env, "timeRange": nav.TimeRange, "notice": notice}, nil
		}
		a.Changed("nav")
		return nav, nil
	})
}

// 側邊欄徽章:各功能登記自己的數字(呼叫時拿著 Mu),同一頁的數字相加。
// 對應 Swift 的 badge(for:):監控=觸發中告警、事件=未結束事件、AI 自動化=待核准+需要協助的角色任務、
// CI/CD=執行中流程+待發佈旁白、開發進度=開著的 urgent/high Issue。
var badgeFuncs []func(a *App) map[string]int

func OnBadge(f func(a *App) map[string]int) { badgeFuncs = append(badgeFuncs, f) }

func init() {
	RegisterView("badges", func(a *App) any {
		out := map[string]int{}
		for _, f := range badgeFuncs {
			for k, v := range f(a) {
				out[k] += v
			}
		}
		for k, v := range out {
			if v <= 0 {
				delete(out, k)
			}
		}
		return out
	})
}

// UserKey 畫面呼叫 Action 時,後端會把登入者的 id 放在 args[UserKey]
const UserKey = "_user"

// UserActor 這個 Action 是誰做的:有登入時是登入者,否則是 a.Me(拿不拿鎖都可以呼叫)
func UserActor(a *App, args map[string]any) Actor {
	if id, ok := args[UserKey].(string); ok && id != "" {
		return Person(id)
	}
	return Person(a.Me)
}
