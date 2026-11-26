package mcp

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"omixor-core/backend"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"

	"omixor-web/internal/core"
)

// console 專有工具上單一 OAuth 入口(oauth_tools.go):分類齊全、清單依分級、非成員與跨組織擋下、管理員工具、LDAP 身分、navigate 分人

// 每個專有工具(spec/tools.json、tools_console.json,共 32 個)都要在「開放」或「刻意不開放」其中一張表
func TestOAuthConsoleToolsClassified(t *testing.T) {
	defs, err := webToolDefs()
	if err != nil {
		t.Fatal(err)
	}
	spec := map[string]bool{}
	for _, d := range defs {
		spec[d.(map[string]any)["name"].(string)] = true
	}
	classified := map[string]bool{}
	for n := range oauthConsoleTools {
		classified[n] = true
	}
	for n := range oauthClosedTools {
		if classified[n] {
			t.Error(n, "同時在兩張表")
		}
		classified[n] = true
	}
	if !reflect.DeepEqual(spec, classified) {
		t.Fatalf("規格與分類不一致:規格 %v\n分類 %v", spec, classified)
	}
	count := map[coremcp.ToolTier]int{}
	for _, r := range oauthConsoleTools {
		count[r.Tier]++
	}
	want := map[coremcp.ToolTier]int{coremcp.TierRead: 18, coremcp.TierWrite: 9, coremcp.TierAdmin: 4}
	if len(spec) != 32 || len(oauthClosedTools) != 1 || !reflect.DeepEqual(count, want) {
		t.Fatal(len(spec), len(oauthClosedTools), count)
	}
}

// hostRig 對外入口和 console 用同一個核心(同正式程式):系統工作組織 sys、另一個組織 beta。
// 使用者(用假 evoleap 登入,id 就是 EvoleapID):
//
//	chief  sys、beta 的組織管理員,LDAP 帳號 alice(名單上的網頁管理員)
//	owner  sys 的組織管理員,不是網頁管理員(LDAP 帳號 olga)
//	member sys 的成員(LDAP 帳號 bob)
//	beta   只是 beta 的成員(LDAP 帳號 carol)
//	nobody 沒有任何組織(LDAP 帳號 dave)
type hostRig struct {
	*oauthRig
	system, beta string
}

func newHostRig(t *testing.T) *hostRig {
	t.Helper()
	app := iaApp(t)
	svc := core.AgentService()
	evo := newFakeEvoleap(t)
	svc.Evoleap, svc.OAuthRegPerIP, svc.OAuthRegTotal = evo.srv.URL, 1000, 1000
	system := app.SystemOrgID()
	if _, err := core.Action(app, "org_create", map[string]any{"name": "Beta"}); err != nil {
		t.Fatal(err)
	}
	beta := ""
	for _, o := range app.OrgList() {
		if o.ID != system {
			beta = o.ID
		}
	}
	if beta == "" || app.SystemOrgID() != system {
		t.Fatal("建立第二個組織", beta, app.SystemOrgID())
	}
	add := func(id, account string, orgs map[string]string) {
		u := model.User{ID: "u_" + id, EvoleapID: service.LDAPIDPrefix + account, Email: account + "@x.test", Name: account, Role: "member", CreatedAt: model.Now()}
		if err := svc.Store.SaveUser(u); err != nil {
			t.Fatal(err)
		}
		for org, role := range orgs {
			if err := svc.Store.SaveMember(model.Member{Org: org, UserID: u.ID, Role: role}); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("chief", "alice", map[string]string{system: "admin", beta: "admin"})
	add("owner", "olga", map[string]string{system: "admin"})
	add("member", "bob", map[string]string{system: "member"})
	add("beta", "carol", map[string]string{beta: "member"})
	add("nobody", "dave", nil)
	// 同 web.Handler:啟用 LDAP、管理員名單只有 alice
	app.SetPerUserUI(func() bool { return true })
	app.SetConsoleAdmins(func(a string) bool { return a == "alice" })
	r := &oauthRig{t: t, h: newOAuthHandler(app, svc, oauthConfig(t, oPublic)), app: app, svc: svc, evo: evo}
	return &hostRig{oauthRig: r, system: system, beta: beta}
}

func (r *hostRig) token(account, grant string) string {
	r.t.Helper()
	scope := map[string]string{"read": "mcp:read", "write": "mcp:read mcp:write", "admin": "mcp:read mcp:write mcp:admin"}[grant]
	return r.connect(service.LDAPIDPrefix+account, account+"@x.test", scope, grant)
}

func (r *hostRig) hostNames(access string) map[string]bool {
	out := map[string]bool{}
	for n := range r.toolNames(access) {
		if !isCoreToolName(n) {
			out[n] = true
		}
	}
	return out
}

func (r *hostRig) expectError(access, tool string, args map[string]any, want string) {
	r.t.Helper()
	if isErr, text := r.call(access, tool, args); !isErr || !strings.Contains(text, want) {
		r.t.Errorf("%s 要被拒(%s):%v %s", tool, want, isErr, text)
	}
}

func (r *hostRig) expectOK(access, tool string, args map[string]any) string {
	r.t.Helper()
	isErr, text := r.call(access, tool, args)
	if isErr {
		r.t.Errorf("%s:%s", tool, text)
	}
	return text
}

func TestOAuthConsoleToolListByGrant(t *testing.T) {
	r := newHostRig(t)
	want := func(level coremcp.ToolTier) map[string]bool {
		out := map[string]bool{}
		for _, n := range ToolNames() { // 沒編進模組的(這個套件的 github*)不在裡面
			if rule, ok := oauthConsoleTools[n]; ok && rule.Tier <= level {
				out[n] = true
			}
		}
		return out
	}
	for grant, level := range map[string]coremcp.ToolTier{"read": coremcp.TierRead, "write": coremcp.TierWrite, "admin": coremcp.TierAdmin} {
		got := r.hostNames(r.token("alice", grant))
		if !reflect.DeepEqual(got, want(level)) {
			t.Errorf("%s:%v\n要 %v", grant, got, want(level))
		}
		if got["voice_submit"] {
			t.Error("voice_submit 不開放")
		}
	}
	// 每個專有工具的規格都要求 org
	for _, d := range r.rpc(r.token("alice", "admin"), "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any) {
		m := d.(map[string]any)
		name := m["name"].(string)
		if _, ok := oauthConsoleTools[name]; !ok {
			continue
		}
		req, _ := m["inputSchema"].(map[string]any)["required"].([]any)
		if !slices.Contains(req, any("org")) {
			t.Error(name, "要求 org", req)
		}
	}
	// omx_ 使用者 token:不列、呼叫是沒有這個工具(行為不變)
	omx := r.userToken("u_chief", "omx_chief")
	for n := range r.namesAt("/mcp", omx) {
		if !isCoreToolName(n) {
			t.Error("omx_ 不該有", n)
		}
	}
	if isErr, text := r.tool("/mcp", omx, "overview", map[string]any{"org": r.system}); !isErr || !strings.Contains(text, "Unknown tool") {
		t.Error("omx_ 呼叫專有工具:", text)
	}
	// 組織端點沒有專有工具
	orgTok := r.connectTo("/mcp/org/"+r.system, service.LDAPIDPrefix+"alice", "alice@x.test", "mcp:read", "read")
	for n := range r.namesAt("/mcp/org/"+r.system, orgTok) {
		if !isCoreToolName(n) {
			t.Error("組織端點不該有", n)
		}
	}
}

func TestOAuthConsoleToolsMembershipAndOrg(t *testing.T) {
	r := newHostRig(t)
	r.expectOK(r.token("bob", "read"), "overview", map[string]any{"org": r.system})
	r.expectError(r.token("bob", "read"), "overview", map[string]any{}, "requires the org parameter")
	r.expectError(r.token("bob", "read"), "overview", map[string]any{"org": r.beta}, "do not have access")
	// 非成員(沒有任何組織、只是別組織的成員)拿不到系統工作組織的資料
	r.expectError(r.token("dave", "read"), "overview", map[string]any{"org": r.system}, "do not have access")
	carol := r.token("carol", "write")
	r.expectError(carol, "list", map[string]any{"org": r.system, "collection": "alerts"}, "do not have access")
	// 整台一份的功能只能用系統工作組織:換自己的組織拿不到
	r.expectError(carol, "list", map[string]any{"org": r.beta, "collection": "alerts"}, "system working organization")
	r.expectError(r.token("alice", "admin"), "log_append", map[string]any{"org": r.beta}, "system working organization")
	// 遠端模式、web 層沒接上:拒絕
	r.app.SetPerUserUI(func() bool { return true })
	fresh := iaApp(t)
	if _, err := identifyOAuth(fresh, model.User{EvoleapID: "ldap:alice"}); err == nil {
		t.Error("web 層沒接上要拒絕")
	}
}

func TestOAuthConsoleToolsTiers(t *testing.T) {
	r := newHostRig(t)
	post := map[string]any{"org": r.system, "text": "hello from oauth"}
	ro := r.token("bob", "read")
	r.expectError(ro, "activity_post", post, coremcp.ReadOnlyMessage)
	r.expectError(ro, "fb_pages", map[string]any{"org": r.system, "refresh": true}, coremcp.ReadOnlyMessage)
	rw := r.token("bob", "write")
	out := r.expectOK(rw, "activity_post", post)
	if !strings.Contains(out, "bob") {
		t.Error("動態要記在授權者名下:", out)
	}
	r.expectError(rw, "log_append", map[string]any{"org": r.system}, "mcp:admin")
	// 組織管理員但不是網頁管理員:網頁擋的,這裡也擋
	r.expectError(r.token("olga", "admin"), "log_append", map[string]any{"org": r.system}, "Only console administrators")
	args := map[string]any{"org": r.system, "level": "info", "service": "nope", "message": "x"}
	// 過了身分檢查:之後的錯誤來自工具本身(功能開關或參數)
	if isErr, text := r.call(r.token("alice", "admin"), "log_append", args); !isErr || strings.Contains(text, "administrator") {
		t.Error("網頁管理員要進得到工具本身:", text)
	}
	// 沒啟用 LDAP:管理員工具要核心的伺服器管理員;navigate 沒有自己的畫面可移動
	r.app.SetPerUserUI(func() bool { return false })
	r.expectError(r.token("alice", "admin"), "log_append", args, "Only console administrators")
	before := r.app.Nav
	r.expectError(rw, "navigate", map[string]any{"org": r.system, "section": "settings"}, "nothing was moved")
	if r.app.Nav != before {
		t.Error("不能移動任何畫面", r.app.Nav)
	}
}

func TestOAuthConsoleToolsLDAPIdentityAndNavigate(t *testing.T) {
	r := newHostRig(t)
	// 啟用 LDAP 時,非 LDAP 身分(網頁登入不了)不能用專有工具
	u := model.User{ID: "u_evo", EvoleapID: "ev-x", Email: "evo@x.test", Name: "evo", Role: "member", CreatedAt: model.Now()}
	if err := r.svc.Store.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Store.SaveMember(model.Member{Org: r.system, UserID: u.ID, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	evo := r.connect("ev-x", "evo@x.test", "mcp:read", "read")
	r.expectError(evo, "overview", map[string]any{"org": r.system}, "LDAP account")

	// navigate 只移動呼叫者自己的畫面(系統工作組織與自己的其他組織)
	navOf := func(account, org string) core.Nav {
		who := core.NewCaller(account, "", "", false, service.LDAPSettings{})
		app := r.app
		if org != r.system {
			scoped, release := r.app.Scoped(org, true)
			defer release()
			app = scoped
		}
		app.Mu.Lock()
		defer app.Mu.Unlock()
		return app.NavOf(who)
	}
	global, other := r.app.Nav, navOf("alice", r.system)
	r.expectOK(r.token("bob", "write"), "navigate", map[string]any{"org": r.system, "section": "settings"})
	if navOf("bob", r.system).Section != "settings" || !reflect.DeepEqual(navOf("alice", r.system), other) || r.app.Nav != global {
		t.Error("只該移動 bob:", navOf("bob", r.system), navOf("alice", r.system), r.app.Nav)
	}
	r.expectOK(r.token("carol", "write"), "navigate", map[string]any{"org": r.beta, "section": "settings"})
	if navOf("carol", r.beta).Section != "settings" || r.app.Nav != global {
		t.Error("carol 在 beta:", navOf("carol", r.beta), r.app.Nav)
	}
}

// 撤回待決定:核心資料,自己的任何組織都能用,以授權者的身分執行(不是建立者也不是管理員就擋)
func TestOAuthConsoleDecisionTools(t *testing.T) {
	r := newHostRig(t)
	carol := r.token("carol", "write")
	text := r.expectOK(carol, "issue_decision_add", map[string]any{"org": r.beta, "title": "選哪個", "kind": "help"})
	var content []map[string]any
	var added map[string]any
	if json.Unmarshal([]byte(text), &content) != nil || len(content) == 0 || json.Unmarshal([]byte(content[0]["text"].(string)), &added) != nil {
		t.Fatal(text)
	}
	id, _ := added["id"].(string)
	if id == "" {
		t.Fatal(text)
	}
	r.expectError(r.token("bob", "write"), "issue_decision_withdraw", map[string]any{"org": r.beta, "id": id}, "do not have access")
	r.expectError(carol, "issue_decision_reopen", map[string]any{"org": r.beta, "id": id}, "conflict") // 還沒處理過:核心照常擋
	out := r.expectOK(carol, "issue_decision_withdraw", map[string]any{"org": r.beta, "id": id})
	if !strings.Contains(out, "withdrawn") {
		t.Error(out)
	}
}

// work_item_create 帶 incident_id 等於改事件:OAuth 要 mcp:admin 而且是該組織管理員(和 incident_update 一樣)
func TestOAuthIncidentFollowUpGrant(t *testing.T) {
	r := newHostRig(t)
	call := func(user string, readOnly, admin bool) error {
		u, err := r.svc.Store.UserByID(user)
		if err != nil {
			t.Fatal(err)
		}
		p := &model.Principal{User: *u, Via: model.ViaOAuth, ReadOnly: readOnly, OAuthAdmin: admin}
		return oauthIncidentGrant(&Call{App: r.app, OAuth: &backend.Local{Svc: r.svc, P: p}})
	}
	for name, err := range map[string]error{"讀寫授權": call("u_chief", false, false), "不是組織管理員": call("u_member", false, true)} {
		if err == nil {
			t.Error(name, "要被拒")
		}
	}
	if err := call("u_chief", false, true); err != nil {
		t.Error(err)
	}
}
