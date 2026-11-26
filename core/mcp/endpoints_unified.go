package mcp

import (
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"omixor-core/backend"
	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/version"
)

// 統一入口(雲端 /mcp):一個連線操作帳號下所有組織。沒有預設組織:除了 OrgFreeTools,每次工具呼叫都要帶 org 參數(必填),
// 組織取自 args["org"],每次呼叫各自驗證「呼叫者是該組織成員」,並產生各自的 Call.Ctx(Hooks.CallCtx),
// 所以同一批 JSON-RPC 請求裡不同呼叫可以帶不同組織,不會互相影響。

// OrgFreeTools 統一入口上不需要 org 的工具(帳號層級):orgs 列可用組織、schema_check 檢核資料庫結構(伺服器管理員)
var OrgFreeTools = map[string]bool{"orgs": true, "schema_check": true}

// UnifiedUnauthorizedMessage 統一入口 401 的說明(給 LLM / 用戶端看,英文)
const UnifiedUnauthorizedMessage = "A bearer token is required: an omx_ user token or an OAuth access token."

// UnifiedEndpointConfig 統一入口的設定
type UnifiedEndpointConfig struct {
	// Resolve 認證這個請求,回傳這次連線的 Backend(沒有組織:組織每次呼叫自己帶)。錯誤處理同 AdminEndpointConfig.Resolve
	// 帶 omx_ 使用者 token 或 OAuth access token(omo_,帳號層級;唯讀 = Backend 的 ReadOnly() 為 true,讀寫 = OAuthWrite() 為 true)都由它認證。
	// 錯誤是 *service.Error 且狀態碼 403、404、429 時照該狀態碼回應,其他一律 401(帶 WWW-Authenticate)
	Resolve func(r *http.Request) (backend.Backend, error)
	// ResourceMetadata 統一入口的 OAuth Protected Resource Metadata 網址(RFC 9728);非空時 401 的 WWW-Authenticate 帶 resource_metadata。空 = 不帶(沒有啟用 OAuth)
	ResourceMetadata string
	// AgentEndpoint 角色端點的網址(領取任務時告訴呼叫端);空字串不附
	AgentEndpoint string
	// PublicURL 對外網址(OMIXOR_PUBLIC_URL);非空時 omixor/server 的 name、origin 取自它,否則取自請求的 Host(只用來顯示)
	PublicURL string
	// Cloud 這個伺服器是雲端部署(例如有 K_SERVICE);true 時 omixor/server 的 env 為 "cloud"。
	// false 時,對外網址是非 loopback 的 https 也視為 cloud;其餘是 "local"
	Cloud bool
	// Tools 額外的工具(一律視為需要 org)
	Tools        []Tool
	Instructions string
	// Begin 同 Hooks.Begin:每個帶 id 的請求開始時呼叫,回傳的函式在處理完後呼叫(宿主統計、處理完重新整理畫面用);nil = 不呼叫
	Begin func() (end func())
	// Tool 同 Hooks.Tool:每次工具執行前呼叫(哪個工具),回傳的函式在該次處理完後呼叫;nil = 不呼叫
	Tool func(name string) (done func())
}

var unifiedInstructions = "This is omixor's unified MCP entry point: one connection can operate on all organizations under your account, and there is no default organization. " +
	"Except for orgs, every tool call must carry the org parameter (the organization id), for reads and writes alike; use orgs first to see the organizations you can use. " +
	"The result carries the organization actually used in its _meta field, under the key omixor/org. " +
	"Every result also carries _meta[\"omixor/server\"] ({name, origin, version, env}) identifying which server answered. " +
	"When connected with an OAuth grant, the connection is account-level and each call is checked against your current organization membership. " +
	"By default an OAuth grant is read-only (scope mcp:read): only read tools are available. If you explicitly granted read-write (scope mcp:write), a limited set of collaboration write tools is also available " +
	"(issue_create, report_problem, issue_update, issue_accept, issue_decision_add, issue_decision_answer, issue_decision_to_issue, issue_item_add, issue_item_update, issue_item_report, issue_item_promote, doc_save, channel_create, channel_post, agent_task_report), " +
	"with your own organization permissions and never server-administrator powers. " +
	"If you explicitly granted the organization-admin option (scope mcp:admin, which includes read-write; only organization admins can grant it), the organization-structure tools are also available " +
	"(agent_upsert, chain_save, project_save, cabinet_layout_save, incident_declare, incident_update); each call is checked against your current role in the organization named by org, so it fails unless you are an admin of that organization right now. " +
	"Still unavailable over OAuth under every grant: claiming tasks or credentials, scheduling or transferring agent tasks, commit writes and a few other tools; those require an omx_ user token. " +
	"issue_create, issue_update and issue_accept return a compact receipt by default (key, id, status, version, updatedAt, changed, comment_id when a comment was added); pass return=\"full\" to get the whole issue with body and comments. " +
	"issues(id) returns only the 5 most recent comments by default (use comments_limit / comments_before to page)." +
	" " + strings.Replace(adminInstructions, "of the current organization", "of the organization named by the org parameter", 1)

// orgDenied 缺 org、org 不合法、不存在、不是成員都用同一句(不洩漏組織存不存在),並列出呼叫者可用的組織
func orgDenied(why string, avail []model.Org, bk any) error {
	parts := make([]string, 0, len(avail))
	for _, o := range avail {
		if o.Name != "" && o.Name != o.ID {
			parts = append(parts, o.ID+"("+o.Name+")")
		} else {
			parts = append(parts, o.ID)
		}
	}
	list := "There are no organizations available"
	if h := noOrgsHint(bk, avail); h != "" {
		list = h
	}
	if len(parts) > 0 {
		list = "Available organizations: " + strings.Join(parts, ", ")
	}
	return ToolError{Message: why + ". " + list + ".", Status: 403}
}

// UnifiedOrg 統一入口解析這次呼叫的組織:args["org"] 必填,而且是呼叫者能用的組織(成員;伺服器管理員視同全部)。
// 缺、空白、不是字串回 400 並列出可用組織;格式不對、不存在、非成員一律同一句 403(不洩漏存在與否)。
// 成員資格以 Backend.ListOrgs 為準(service 層本來就會再檢查一次;這裡是第一道、也不依賴每個方法都有檢查)
func UnifiedOrg(b backend.Backend, args map[string]any) (string, error) {
	avail, err := b.ListOrgs()
	if err != nil {
		return "", err
	}
	return unifiedOrgIn(b, avail, args)
}

// unifiedOrgIn 同 UnifiedOrg,但用已查好的可用組織清單(bk 只用來判斷授權種類,決定「沒有組織」時的說明)
func unifiedOrgIn(bk any, avail []model.Org, args map[string]any) (string, error) {
	v, has := args["org"]
	s, isStr := v.(string)
	s = strings.TrimSpace(s)
	if !has || !isStr || s == "" {
		e := orgDenied("The unified entry point requires the org parameter (organization id) on every call", avail, bk).(ToolError)
		e.Status = 400
		return "", e
	}
	if model.ValidOrgID(s) {
		for _, o := range avail {
			if o.ID == s {
				return o.ID, nil
			}
		}
	}
	return "", orgDenied("You do not have access to organization "+s+" (or it does not exist)", avail, bk)
}

// WithOrgParam 統一入口的規格:需要組織的工具加上必填的 org 屬性(複製,不改共用的規格);
// orgs 的說明改成統一入口的版本(沒有目前組織)
func WithOrgParam(defs []any) []any {
	out := make([]any, 0, len(defs))
	for _, d := range defs {
		m, ok := d.(map[string]any)
		name, _ := m["name"].(string)
		if !ok {
			out = append(out, d)
			continue
		}
		cp := maps.Clone(m)
		if name == "orgs" {
			cp["description"] = "Lists the organizations you can use (id, name). The unified entry point has no current organization: every other tool must carry org (the organization id) on each call."
			out = append(out, cp)
			continue
		}
		if OrgFreeTools[name] {
			out = append(out, d)
			continue
		}
		if desc, ok := m["description"].(string); ok {
			cp["description"] = desc + " (Unified entry point: org is required to select the organization, see orgs.)"
		}
		sch, _ := m["inputSchema"].(map[string]any)
		s2 := maps.Clone(sch)
		if s2 == nil {
			s2 = map[string]any{"type": "object", "additionalProperties": false}
		}
		props := maps.Clone(func() map[string]any { p, _ := s2["properties"].(map[string]any); return p }())
		if props == nil {
			props = map[string]any{}
		}
		props["org"] = pStr("Organization id (required; use orgs to see the organizations you can use)")
		s2["properties"] = props
		req := []string{}
		switch r := s2["required"].(type) {
		case []string:
			req = slices.Clone(r)
		case []any:
			for _, x := range r {
				if str, ok := x.(string); ok {
					req = append(req, str)
				}
			}
		}
		if !slices.Contains(req, "org") {
			req = append([]string{"org"}, req...)
		}
		s2["required"] = req
		cp["inputSchema"] = s2
		out = append(out, cp)
	}
	return out
}

// UnifiedEndpoint 統一入口的 http.Handler(雲端掛在 /mcp:omixor 使用者 token(omx_)認證,或 OAuth access token(omo_,帳號層級;預設唯讀只開放 ReadOnlyTools,使用者明確授權 mcp:write 才再開放 OAuthWriteTools))
func UnifiedEndpoint(cfg UnifiedEndpointConfig) http.Handler {
	ins := cfg.Instructions
	if ins == "" {
		ins = unifiedInstructions
	}
	srv := NewCoreServer(ins)
	allow := map[string]bool{}
	for k := range AdminToolAllow {
		allow[k] = true
	}
	extra := map[string]bool{}
	for _, t := range cfg.Tools {
		srv.Add(t)
		n := t.Def["name"].(string)
		allow[n] = true
		extra[n] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bk, err := cfg.Resolve(r)
		if err != nil {
			if c := service.Code(err); c == 403 || c == 404 || c == 429 {
				http.Error(w, httperr.Report("mcp 統一入口解析身分", err).Msg, c)
				return
			}
			// 帶了 OAuth 的 access token(omo_)卻被拒(過期、撤銷、綁的不是 /mcp):error="invalid_token",用戶端會去 refresh 或重新授權
			tok, _ := BearerToken(r)
			unauthorizedBody(w, "omixor", cfg.ResourceMetadata, strings.HasPrefix(tok, model.OAuthTokenPrefix), UnifiedUnauthorizedMessage)
			return
		}
		// ListOrgs is looked up at most once per HTTP request (this bk) and dropped with it; never shared across requests or users.
		oc := &orgCache{b: bk}
		srvID := serverIdentity(cfg.PublicURL, cfg.Cloud, r)
		diag := newConnDiag("unified", bk, srvID, r)
		hooks := WithApps(Hooks{
			Begin: cfg.Begin,
			Tool:  cfg.Tool,
			// 預設的上下文沒有組織(空字串):就算漏了 CallCtx,工具也會因組織 id 不合法而失敗,不會落到某個組織
			Ctx: &Session{B: bk, Endpoint: cfg.AgentEndpoint, Diag: diag},
			Allow: func(name string) bool {
				return allow[name] && !HumanHiddenTools[name]
			},
			Defs: func(defs []any) []any { return WithOrgParam(WithoutIdentityAgent(defs)) },
			// 每次呼叫各自的上下文:組織取自 args["org"],驗證成員資格後建立專屬的 Session(不共用、不改寫)
			CallCtx: func(name string, args map[string]any) (any, map[string]any, error) {
				sess := &Session{B: bk, Endpoint: cfg.AgentEndpoint, Diag: diag}
				if OrgFreeTools[name] && !extra[name] {
					delete(args, "org")
					return sess, nil, nil
				}
				avail, err := oc.list()
				if err != nil {
					return nil, nil, err
				}
				org, err := unifiedOrgIn(bk, avail, args)
				if err != nil {
					return nil, nil, err
				}
				// mcp:admin 授權的組織管理員工具:每次呼叫即時確認呼叫者目前是這個 org 的管理員(不是授權當下;被降級或移出組織後立刻失效)
				if OAuthAdminTools[name] && !extra[name] && isOAuthAdminBackend(bk) {
					if err := orgAdminCheck(avail, org); err != nil {
						return nil, nil, err
					}
				}
				delete(args, "org") // 工具看到的參數和舊端點一樣
				sess.OrgID = org
				sess.OrgNameFn = func() string { return oc.name(org) }
				return sess, map[string]any{"omixor/org": org}, nil
			},
			Guard: RejectIdentityAgent,
		})
		// OAuth(唯讀):和 AdminEndpoint 同一套限制;組織仍由每次呼叫的 org 參數與即時成員資格決定(CallCtx),token 本身不帶組織
		hooks = restrictOAuth(hooks, bk, true)
		// 每個結果的 _meta 都帶 omixor/server(含 orgs、schema_check 這類沒有 org 的工具);omixor/org 的行為不變
		callCtx := hooks.CallCtx
		hooks.CallCtx = func(name string, args map[string]any) (any, map[string]any, error) {
			c, meta, err := callCtx(name, args)
			if err != nil {
				return c, meta, err
			}
			out := make(map[string]any, len(meta)+1)
			maps.Copy(out, meta)
			out[ServerMetaKey] = srvID
			return c, out, nil
		}
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}

// orgAdminCheck 組織管理員授權(mcp:admin)的每次呼叫檢查:avail 是這個請求即時查到的「呼叫者能用的組織」(帶 MyRole,OAuth 身分不是伺服器管理員,所以是真正的成員角色);
// 呼叫的 org 的角色不是 admin = 403,機器碼 org_admin_required(service 層的授權表之後仍照舊檢查,這裡是明確的第一道)
func orgAdminCheck(avail []model.Org, org string) error {
	for _, o := range avail {
		if o.ID == org && o.MyRole == "admin" {
			return nil
		}
	}
	return ToolError{Status: 403, Data: map[string]any{"code": OAuthCodeOrgAdminRequired},
		Message: "This tool requires you to be an administrator of organization " + org + ", and you are not one right now (an OAuth admin grant is checked against your current role on every call)."}
}

// orgCache caches Backend.ListOrgs for the lifetime of one HTTP request (batch calls share it).
type orgCache struct {
	b    backend.Backend
	mu   sync.Mutex
	done bool
	l    []model.Org
	err  error
}

func (c *orgCache) list() ([]model.Org, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.done {
		c.l, c.err = c.b.ListOrgs()
		c.done = true
	}
	return c.l, c.err
}

// name returns the organization's name, or its id when unknown.
func (c *orgCache) name(id string) string {
	if l, err := c.list(); err == nil {
		for _, o := range l {
			if o.ID == id {
				return o.Name
			}
		}
	}
	return id
}

// ServerMetaKey is the _meta key that identifies which server answered a unified-endpoint call.
const ServerMetaKey = "omixor/server"

// serverIdentity builds the {name, origin, version, env} value of _meta["omixor/server"] once per HTTP request.
// Display only: it never contains tokens, database URLs or environment values. Without a configured public URL the
// origin comes from r.Host alone (X-Forwarded-* headers are ignored), length-limited and stripped of control characters.
func serverIdentity(publicURL string, cloud bool, r *http.Request) map[string]any {
	name, origin := "", ""
	https, loopback := false, false
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && publicURL != "" && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		name = cleanHost(u.Host)
		origin = u.Scheme + "://" + name
		https = u.Scheme == "https"
		loopback = isLoopbackHost(u.Hostname())
	} else if r != nil {
		name = cleanHost(r.Host)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		origin = scheme + "://" + name
		// without a configured public URL the request itself is not proof of a cloud deployment: env stays "local" unless cfg.Cloud
	}
	env := "local"
	if cloud || (https && !loopback) {
		env = "cloud"
	}
	v := version.Version
	if v == "" {
		v = "unknown"
	}
	return map[string]any{"name": name, "origin": origin, "version": v, "env": env}
}

// cleanHost drops control characters and whitespace and limits the length to 255 characters.
func cleanHost(h string) string {
	h = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return -1
		}
		return r
	}, h)
	if len(h) > 255 {
		h = h[:255]
	}
	return h
}

// isLoopbackHost reports whether h is "localhost", a *.localhost name or a loopback IP literal (127.0.0.0/8, ::1).
// A hostname that merely starts with "127." (for example 127.example.com) is not loopback.
func isLoopbackHost(h string) bool {
	h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
