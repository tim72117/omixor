package core

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

// callerViews 內容因呼叫者而異的 view(名稱要同時用 RegisterView 登記;沒有呼叫者時用那一份)
var callerViews = map[string]func(a *App, who *Caller) any{}

// RegisterCallerView 見 callerViews
func RegisterCallerView(name string, f func(a *App, who *Caller) any) {
	if _, dup := callerViews[name]; dup {
		panic("View 重複登記:" + name)
	}
	callerViews[name] = f
}

// ViewFor 同 View;who 不是 nil 而且這個 view 因人而異時,用呼叫者的身分算。
// hidden 不是 nil 時先拿掉受限專案的衍生資料(見 restricted_hidden.go)
func ViewFor(a *App, name string, who *Caller, hidden *Hidden) (any, bool) {
	f, ok := views[name]
	cf, perCaller := callerViews[name]
	perCaller = perCaller && who != nil
	if !ok && !perCaller {
		return nil, false
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	var v any
	if perCaller {
		v = cf(a, who)
	} else {
		v = f(a)
	}
	if hf := hiddenViews[name]; hidden != nil && hf != nil {
		v = hf(a, hidden, v)
	}
	return Object(v), true
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
		return nil, StatusError{Status: 404, Message: "沒有這個動作:" + ShortName(name)}
	}
	if !orgFreeActions[name] {
		if err := a.RequireOrg(); err != nil {
			return nil, err
		}
	}
	return f(a, args)
}

// HasAction 有登記這個 action 嗎
func HasAction(name string) bool { return actions[name] != nil }

// ActionNames 全部登記的 action(web 的權限分類測試用,見 web/access.go)
func ActionNames() []string {
	out := []string{}
	for k := range actions {
		out = append(out, k)
	}
	return out
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

// HTTPError 給畫面看的錯誤(httperr.Public):沒有指定狀態碼
func (e UserError) HTTPError() (int, map[string]any) { return 0, nil }

// StatusError 給畫面看、而且要用特定 HTTP 狀態碼回應的錯誤(例如 409:畫面顯示的組織已經不是目前的組織)
type StatusError struct {
	Status  int
	Message string
	// Data 要一併回給畫面的欄位(併進回應的最上層,例如刪角色 409 的 code、taskCounts);沒有是 nil
	Data map[string]any
}

func (e StatusError) Error() string { return e.Message }

// HTTPError 指定狀態碼與要併進回應的欄位(httperr.Public)
func (e StatusError) HTTPError() (int, map[string]any) { return e.Status, e.Data }

// 基本的 View:範例資料集合、導覽、MCP 狀態
func init() {
	RegisterView("nav", func(a *App) any { return a.Nav })
	RegisterView("mcp", func(a *App) any {
		s := a.MCP
		return map[string]any{"status": s, "url": s.URL}
	})
	RegisterView("me", func(a *App) any { return map[string]any{"id": a.Me, "name": a.MeName()} })
	RegisterView("sprint", func(a *App) any { return a.Sprint })
	for _, c := range []string{"people", "services", "servers", "repos", "pull_requests", "work_items", "pipeline_runs",
		"deployments", "alerts", "alert_rules", "logs", "dashboards", "incidents", "on_call", "automations",
		"automation_runs", "approvals", "activity", "integrations"} {
		name := c
		RegisterView(name, func(a *App) any { return a.C[name] })
	}
	// navigate 只改呼叫者自己的導覽狀態(沒分人時是全站那一份),見 nav_user.go
	RegisterAction("navigate", func(a *App, args map[string]any) (any, error) {
		who := CallerOf(args)
		a.Mu.Lock()
		f, _ := args["focusId"].(string)
		section, _ := args["section"].(string)
		notice := ""
		if section != "" {
			// 系統設定關掉的頁面:改去總覽(沒分人時提示放在 system view 的 notice,畫面上方會顯示)
			if to, n := a.GuardSectionFor(who, section); n != "" {
				section, f, notice = to, "", n
			}
		}
		nav := a.UpdateNav(who, func(nav *Nav) {
			if section != "" {
				nav.Section = section
			}
			nav.FocusID = f
			if s, ok := args["env"].(string); ok && s != "" {
				nav.Env = s
			}
			if s, ok := args["timeRange"].(string); ok && s != "" {
				nav.TimeRange = s
			}
		})
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
// 只算開著的頁面與分頁(各功能用 a.FeatureOn 看自己的分頁;關掉的頁面整個不顯示)。
// 對應 Swift 的 badge(for:):監控=觸發中告警、事件=未結束事件、AI 自動化=待核准+需要協助的角色任務、
// CI/CD=執行中流程+待發佈旁白、開發進度=開著的 urgent/high Issue。
var badgeFuncs []func(a *App) map[string]int

func OnBadge(f func(a *App) map[string]int) { badgeFuncs = append(badgeFuncs, f) }

func init() {
	RegisterView("badges", func(a *App) any { return a.badges() })
}

// badges 各功能登記的徽章加總(拿著 Mu 呼叫)
func (a *App) badges() map[string]int {
	out := map[string]int{}
	for _, f := range badgeFuncs {
		for k, v := range f(a) {
			out[k] += v
		}
	}
	for k, v := range out {
		// 關掉的頁面不顯示徽章(同 App 的 badge(for:) 先看 isVisible)
		if v <= 0 || !a.SectionVisible(k) {
			delete(out, k)
		}
	}
	return out
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

// ShortName 使用者送來的名稱放進訊息前先截短(避免超長的路徑片段被原樣回顯)
func ShortName(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
