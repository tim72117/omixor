package core

import (
	"strings"
	"sync"
)

// 換頁指令(navigate / nav)分人。啟用 LDAP 時(本機模式):
//   - 有登入者的呼叫(畫面點選、/api/action/navigate、/api/tool/navigate)只改「這位登入者在這個組織」的導覽狀態,
//     view「nav」、overview 工具的 ui、助理的目前頁都讀它。組織也要分:焦點 id 是組織裡的資料,而且各分頁可以在不同組織
//   - 系統身分(MCP 3765、背景工作)對不到任何人:照舊改 App.Nav,但登入者的畫面不讀它,所以不會移動任何人,
//     也不在大家的畫面上放提示
//   - 同一人的多個分頁和以前一樣:同一個組織、看得到的分頁跟著走,背景分頁與其他組織的分頁不動
//
// 沒啟用登入、遠端模式:只有 App.Nav,行為不變。每人的狀態只在記憶體(App.Nav 本來也不存),重啟回預設。

type userNavKey struct {
	a            *App
	account, org string
}

var (
	userNavMu sync.Mutex
	userNavs  = map[userNavKey]Nav{}
)

func defaultNav() Nav { return Nav{Section: "overview", Env: "production", TimeRange: "24h"} }

// SetPerUserUI 由 web 層告訴 App 現在是不是「每位登入者各自的畫面」(啟用 LDAP 的本機模式;認證設定可以在執行中改)
func (a *App) SetPerUserUI(f func() bool) { a.root().perUser.Store(&f) }

// PerUserUI 見 SetPerUserUI
func (a *App) PerUserUI() bool {
	f := a.root().perUser.Load()
	return f != nil && (*f)()
}

// userNavKeyOf 呼叫者在目前組織的鍵;ok = false 表示用 App.Nav(系統身分、遠端模式)。拿著 a.Mu 呼叫
func (a *App) userNavKeyOf(who *Caller) (userNavKey, bool) {
	if who == nil || who.Account() == "" || IsRemote() {
		return userNavKey{}, false
	}
	return userNavKey{a.root(), strings.ToLower(who.Account()), a.CurrentOrgID()}, true
}

// NavOf 呼叫者的導覽狀態(nil = 系統身分的 App.Nav)。拿著 a.Mu 呼叫
func (a *App) NavOf(who *Caller) Nav {
	k, ok := a.userNavKeyOf(who)
	if !ok {
		return a.Nav
	}
	userNavMu.Lock()
	defer userNavMu.Unlock()
	if n, found := userNavs[k]; found {
		return n
	}
	return defaultNav()
}

// UpdateNav 改呼叫者的導覽狀態,回傳改完的。拿著 a.Mu 呼叫
func (a *App) UpdateNav(who *Caller, change func(*Nav)) Nav {
	k, ok := a.userNavKeyOf(who)
	if !ok {
		change(&a.Nav)
		return a.Nav
	}
	userNavMu.Lock()
	defer userNavMu.Unlock()
	n, found := userNavs[k]
	if !found {
		n = defaultNav()
	}
	change(&n)
	userNavs[k] = n
	return n
}

// GuardSectionFor 同 GuardSection,但「頁面關掉了」的提示是全部畫面看得到的,只有沒分人時才放;
// 分人時呼叫者從回傳值拿提示。拿著 a.Mu 呼叫
func (a *App) GuardSectionFor(who *Caller, section string) (string, string) {
	if who == nil && !a.PerUserUI() {
		return a.GuardSection(section)
	}
	if section == "" || a.SectionVisible(section) {
		return section, ""
	}
	return "overview", closedSectionNotice(section)
}

func init() {
	RegisterCallerView("nav", func(a *App, who *Caller) any { return a.NavOf(who) })
}
