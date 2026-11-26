package mcp

import (
	"strings"

	"omixor-core/backend"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// console 專有工具上單一 OAuth 入口(統一入口 /mcp 的宿主工具,分級見 core/mcp/host_tools.go)。
//
// 權限不得比同一個人在網頁上寬(web/access.go、orgscope.go;TestOAuthConsoleToolsMatchWebRules 會對照):
//   - 呼叫以 OAuth 使用者執行;org 由統一入口先確認是成員(非成員拿不到任何資料)
//   - 啟用 LDAP 時只有 LDAP 身分能用(網頁只有 LDAP 帳號能登入)
//   - 沒標 orgScoped 的是整台一份的功能:org 必須是系統工作組織
//   - TierAdmin(網頁限管理員的):mcp:admin 授權、該組織管理員,而且是網頁管理員(管理員帳號名單;
//     沒啟用 LDAP 時沒有名單,改要核心的伺服器管理員)
//   - 不是網頁管理員時,受限專案的資料照網頁的規則拿掉
//   - navigate 只移動呼叫者自己的畫面;沒有「自己的畫面」(沒啟用 LDAP)就拒絕
//   - 遠端模式、web 層還沒接上時一律拒絕
//
// 新增 console 工具要在下面兩張表之一分類,否則不會出現在入口上(TestOAuthConsoleToolsClassified)。

// OAuthToolRule 一個專有工具在 OAuth 入口的規則
type OAuthToolRule struct {
	Tier coremcp.ToolTier
	// OrgScoped 核心資料:任何自己是成員的組織都能用;false = 只能在系統工作組織
	OrgScoped bool
}

var (
	ruleRead     = OAuthToolRule{Tier: coremcp.TierRead}
	ruleWrite    = OAuthToolRule{Tier: coremcp.TierWrite}
	ruleAdmin    = OAuthToolRule{Tier: coremcp.TierAdmin}
	ruleOrgWrite = OAuthToolRule{Tier: coremcp.TierWrite, OrgScoped: true}
)

var oauthConsoleTools = map[string]OAuthToolRule{
	"overview": ruleRead, "list": ruleRead, "get": ruleRead, "server": ruleRead,
	"voice_jobs": ruleRead, "office_state": ruleRead, "site_visitors": ruleRead,
	"github": ruleRead, "github_runs": ruleRead, "github_run": ruleRead, "github_pull": ruleRead,
	"gcp": ruleRead, "gcp_logs": ruleRead, "gcp_metrics": ruleRead,
	"fb_pages": ruleRead, "fb_posts": ruleRead, "fb_insights": ruleRead, "fb_drafts": ruleRead,

	"alert_update": ruleWrite, "work_item_create": ruleWrite, "work_item_move": ruleWrite,
	"pipeline_update": ruleWrite, "activity_post": ruleWrite, "office_move": ruleWrite,
	"issue_decision_withdraw": ruleOrgWrite, "issue_decision_reopen": ruleOrgWrite, "navigate": ruleOrgWrite,

	"alert_create": ruleAdmin, "log_append": ruleAdmin, "request_approval": ruleAdmin, "fb_draft_save": ruleAdmin,
}

// oauthClosedTools 刻意不開放給 OAuth 的專有工具與原因
var oauthClosedTools = map[string]string{
	"voice_submit": "在這台主機執行程式;DOC-37 第 12 節列為需另行判斷,決定前不開放給遠端的 OAuth 連線",
}

// OAuthConsoleTools 開放的專有工具與規則(複本;給 web 的對照測試)
func OAuthConsoleTools() map[string]OAuthToolRule {
	out := make(map[string]OAuthToolRule, len(oauthConsoleTools))
	for k, v := range oauthConsoleTools {
		out[k] = v
	}
	return out
}

// oauthHostTools 掛到統一入口的工具(沒編進模組的工具不列)
func oauthHostTools(app *core.App) []coremcp.HostTool {
	var out []coremcp.HostTool
	for _, d := range ToolDefs() {
		def := d.(map[string]any)
		name, _ := def["name"].(string)
		rule, ok := oauthConsoleTools[name]
		if !ok {
			continue
		}
		out = append(out, coremcp.HostTool{Tier: rule.Tier, Tool: coremcp.Tool{Def: def, Fn: func(c *coremcp.Call) (any, error) {
			sess, ok := c.Ctx.(*coremcp.Session)
			if !ok {
				return nil, oauthDenied("This tool is only available on the unified entry point")
			}
			bk, ok := sess.B.(*backend.Local)
			if !ok || bk.P == nil || bk.P.Via != model.ViaOAuth || sess.OrgID == "" {
				return nil, oauthDenied("This tool requires an OAuth connection and an organization")
			}
			return runOAuthTool(app, name, rule, bk, sess.OrgID, c.Args.Raw)
		}}})
	}
	return out
}

func oauthDenied(msg string) error { return ToolError{Message: msg, Status: 403} }

// oauthWho 這個 OAuth 呼叫者在 console 是誰
type oauthWho struct {
	// account 署名用:啟用 LDAP 時是 LDAP 帳號,否則是 email
	account string
	// admin 網頁管理員
	admin bool
	// caller 只在啟用 LDAP 時有(自己的畫面)。一律不帶管理員權限:核心的寫入走 Call.OAuth,不走它
	caller *core.Caller
}

func identifyOAuth(app *core.App, u model.User) (oauthWho, error) {
	if core.IsRemote() {
		return oauthWho{}, oauthDenied("Host-specific tools are not available while this console is in remote mode")
	}
	isAdmin, ok := app.ConsoleAdmins()
	if !ok {
		return oauthWho{}, oauthDenied("The console is still starting; try again")
	}
	if !app.PerUserUI() { // 沒啟用 LDAP:沒有管理員名單
		return oauthWho{account: u.Email, admin: u.Role == "admin"}, nil
	}
	account, ok := strings.CutPrefix(u.EvoleapID, service.LDAPIDPrefix)
	if !ok || account == "" {
		return oauthWho{}, oauthDenied("This console uses LDAP sign-in, so its host-specific tools require authorizing with your LDAP account")
	}
	return oauthWho{account: account, admin: isAdmin(account),
		caller: core.NewCaller(account, u.Name, u.Email, false, service.LDAPSettings{})}, nil
}

// runOAuthTool 一次呼叫:org 已由統一入口確認是呼叫者能用的組織;TierAdmin 的授權與組織管理員身分也在那裡確認過
func runOAuthTool(app *core.App, name string, rule OAuthToolRule, bk *backend.Local, org string, args map[string]any) (any, error) {
	who, err := identifyOAuth(app, bk.P.User)
	if err != nil {
		return nil, err
	}
	if rule.Tier == coremcp.TierAdmin && !who.admin {
		return nil, oauthDenied("Only console administrators can use " + name)
	}
	if err := rejectIdentity(name, args); err != nil {
		return nil, err
	}
	if v, ok := args["refresh"]; ok && v != false && v != nil && bk.ReadOnly() {
		return nil, oauthDenied(coremcp.ReadOnlyMessage + ", so it cannot refresh the data (refresh)")
	}
	if name == "navigate" && who.caller == nil {
		return nil, oauthDenied("Sign-in is not enabled on this console, so there is no screen that belongs only to you; nothing was moved")
	}

	lock := app.SystemOrgLock()
	lock.RLock()
	locked := true
	unlock := func() {
		if locked {
			lock.RUnlock()
			locked = false
		}
	}
	defer unlock()
	target := app
	if org != app.SystemOrgID() {
		if !rule.OrgScoped {
			return nil, oauthDenied(name + " is not separated by organization yet: it only works with org set to the system working organization of this console, and you must be a member of it")
		}
		unlock()
		known := false
		for _, o := range app.OrgList() {
			known = known || o.ID == org
		}
		if !known {
			return nil, oauthDenied("The console does not know organization " + org + " yet; try again")
		}
		scoped, release := app.Scoped(org, false)
		defer release()
		target = scoped
	} else if strings.HasPrefix(name, "office_") {
		unlock() // 虛擬辦公室是整台一份、不讀組織資料(office_move 可能等很久,不能擋住換系統工作組織)
	}
	if f, closed := closedFeature(target, name); closed {
		return nil, featureClosedError(name, f)
	}

	var restricted map[string]bool
	unknown, active := false, false
	if !who.admin {
		if restricted, err = target.RestrictedProjectIDs(); err != nil {
			unknown = true
		}
		active = unknown || len(restricted) > 0
	}
	notFound := ToolError{Message: "Not found", Status: 404}
	if active && core.RestrictedArg(args, restricted, unknown) {
		return nil, notFound
	}
	out, err := srv.Run(name, args, &callCtx{App: target, Actor: core.Person(who.account), who: who.caller, oauth: bk})
	if err != nil || !active {
		return out, err
	}
	res, drop := core.ScrubRestricted(core.Object(out), restricted, unknown)
	if drop {
		return nil, notFound
	}
	return res, nil
}
