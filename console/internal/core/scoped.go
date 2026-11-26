package core

import "slices"

// 每位登入者各自的「目前組織」(啟用 LDAP 時)。組織由請求帶進來(DOC-37 的方向:組織跟著請求走,不是伺服器的狀態;
// 之後核心頁改走 /api/orgs/{org}/… 時,這一層連同 view / action 一起拿掉)。
//
// App 原本的「目前組織」(orgs.json)從此是「系統工作組織」:沒啟用登入的畫面、API token、背景工作、MCP 3765、自動執行,
// 以及還沒分組織的功能(整合、範例資料、旁白、office…)用它;web 管理員用 system_org_set 換。
//
// 請求的組織不是系統工作組織時,核心資料的 view / action / 工具用 Scoped 做的一次性 App 執行:組織固定、自己的鎖與快取、
// 用完就丟,所以不會動到別人看到的東西,也沒有跨請求、跨使用者的共用狀態。它不跑 OnStart、OnOrgLoad(沒有計時器、
// 不碰整合與舊資料匯入);變更通知轉給原本的 App。哪些名稱可以這樣跑見 web/orgscope.go。

// releaseHooks 一次性 App 丟掉時清掉以它為鍵的狀態
var releaseHooks []func(*App)

// OnRelease 有以 *App 為鍵的套件層級狀態時登記(只能在 init() 呼叫)
func OnRelease(f func(a *App)) { releaseHooks = append(releaseHooks, f) }

func init() {
	OnRelease(func(a *App) {
		agentStates.Delete(a)
		cabStates.Delete(a)
		projStates.Delete(a)
		sysStates.Delete(a)
		mcpTokens.Delete(a)
		runnerStates.Delete(a)
		voicePipes.Delete(a)
	})
}

// root 一次性 App 回原本的 App,其他回自己
func (a *App) root() *App {
	if a.parent != nil {
		return a.parent
	}
	return a
}

// IsScoped 是不是 Scoped 做的一次性 App
func (a *App) IsScoped() bool { return a.parent != nil }

// SystemOrgID 系統工作組織(不要拿著 Mu)
func (a *App) SystemOrgID() string { return a.root().currentOrgNow() }

// OrgList 組織清單的複本(建立順序;清單是空的時重讀一次)。不要拿著 Mu
func (a *App) OrgList() []Organization {
	r := a.root()
	r.Mu.Lock()
	defer r.Mu.Unlock()
	r.refreshEmptyOrgs()
	return slices.Clone(r.Orgs().Orgs)
}

// Scoped 組織固定是 org 的一次性 App;readOnly(GET)時不發變更通知(讀取時順手更新快取,通知出去畫面會一直重讀)。
// 只給本機模式;org 要已經確認存在而且呼叫者能用。用完一定要呼叫回傳的 release。不要拿著 a.Mu
func (a *App) Scoped(org string, readOnly bool) (*App, func()) {
	r := a.root()
	orgs := r.OrgList()
	r.Mu.Lock()
	s := &App{Me: r.Me, C: emptyMock().C, Sprint: map[string]any{}, Nav: r.Nav, MCP: r.MCP,
		Ext: map[string]any{}, subs: map[chan Event]struct{}{}, parent: r, muted: true}
	r.Mu.Unlock()
	s.Nav.FocusID = ""
	s.Ext["orgs"] = &OrgState{Index: OrgIndex{Current: org, Orgs: orgs}}
	s.Ext["mock"] = &mockState{org: org, stash: map[string]*mockData{}}
	useScopedSystem(s, org) // 功能開關(徽章、system view)照這個組織的
	s.muted = readOnly
	return s, func() {
		for _, f := range releaseHooks {
			f(s)
		}
	}
}

// scopedBadges 一次性 App 的徽章:只算核心資料(Issue、角色任務、對話);其他徽章屬於系統工作組織。拿著 Mu 呼叫
func (a *App) scopedBadges(who *Caller) map[string]int {
	out := map[string]int{}
	if a.FeatureOn("development.issues") {
		out["development"] = openUrgentIssues(a)
	}
	if a.FeatureOn("automation.agents") || a.FeatureOn("automation.dispatch") {
		out["automation"] = blockedTasks(a)
	}
	if b, err := coreBackendFor(who, "", a.CurrentOrgID()); err == nil {
		if list, err := b.ListChannels(a.CurrentOrgID(), channelFilterAll); err == nil {
			for _, c := range list {
				out["channels"] += c.Unread
			}
		}
	}
	for k, v := range out {
		if v <= 0 || !a.SectionVisible(k) {
			delete(out, k)
		}
	}
	return out
}
