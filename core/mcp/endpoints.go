package mcp

import (
	"net/http"
	"strings"

	"omixor-core/backend"
	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
)

// 可掛載的 MCP 端點(HTTP handler)。協定層共用 HTTPHandler(Origin 檢查、方法、body 限制),
// 路徑一律對應回 /mcp,所以可以掛在任何路徑底下。

func serve(w http.ResponseWriter, r *http.Request, handle func(body []byte) []byte) {
	r2 := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/mcp"
	r2.URL = &u
	HTTPHandler(handle).ServeHTTP(w, r2)
}

func unauthorized(w http.ResponseWriter, realm string) { unauthorizedWith(w, realm, "", false) }

// unauthorizedWith 401:resourceMetadata 非空時帶 resource_metadata(RFC 9728,讓 OAuth 用戶端找到授權伺服器);invalidToken 帶 error="invalid_token"(RFC 6750)
func unauthorizedWith(w http.ResponseWriter, realm, resourceMetadata string, invalidToken bool) {
	unauthorizedBody(w, realm, resourceMetadata, invalidToken, "Unauthorized")
}

// unauthorizedBody 同 unauthorizedWith,回應內容是 body
func unauthorizedBody(w http.ResponseWriter, realm, resourceMetadata string, invalidToken bool, body string) {
	ch := `Bearer realm="` + realm + `"`
	if resourceMetadata != "" {
		ch += `, resource_metadata="` + resourceMetadata + `"`
		w.Header().Set("Cache-Control", "no-store")
	}
	if invalidToken {
		ch += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", ch)
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(401)
	_, _ = w.Write([]byte(body))
}

// ================= 角色端點 =================

// AgentToolAllow 角色端點可能開放的核心工具(登記表 model.CoreTools 的 Agent;實際列哪些再依角色設定,見 service.AgentToolSet)
var AgentToolAllow = model.CoreToolSet(func(t model.CoreTool) bool { return t.Agent })

// agentToolsContext agent_context 的 tools:能用的工具(含額外掛上來的)與不能用的工具和原因
// (reason 是穩定代碼、reason_message 是英文短句,都給 LLM 看;怎麼處理 unavailable 寫在 agentInstructions,不每次重複)
func agentToolsContext(svc *service.Service, org string, g model.Agent, extra []string) map[string]any {
	can, cannot := []any{}, []any{}
	for _, t := range svc.AgentToolsOf(org, g) {
		if t.Available {
			can = append(can, map[string]any{"name": t.Name, "label": t.Label})
		} else {
			cannot = append(cannot, map[string]any{"name": t.Name, "label": t.Label, "reason": t.ReasonCode, "reason_message": t.ReasonEN})
		}
	}
	for _, n := range extra {
		can = append(can, map[string]any{"name": n})
	}
	return map[string]any{
		"available":   can,
		"unavailable": cannot,
	}
}

// AgentEndpointConfig 角色端點的設定
type AgentEndpointConfig struct {
	Svc *service.Service
	// Tools 額外的工具;掛上來的一律開放給角色端點,用 RoleOf(call) 取得是哪個角色,權限要自己檢查
	// (文件櫃已經是核心的工具,不用另外掛)
	Tools []Tool
	// Filter 每次工具呼叫前額外的限制(核心的工具與額外的工具都會經過);回傳錯誤就拒絕。
	// args 已經強制帶上憑證的角色(args["agent"])
	Filter func(role Role, tool string, args map[string]any) error
	// ContextExtra 附加到 agent_context 輸出的欄位(文件櫃的 cabinet、docs 核心已經附上)
	ContextExtra func(role Role, g model.Agent, includeBodies bool) map[string]any
	// Decorate 改寫角色的輸出(agents、agent_context 的 agent 欄位);docAccess 核心已經換成看得懂的文件櫃範圍
	Decorate func(role Role, g model.Agent, out map[string]any)
	// Instructions initialize 回傳的說明;空 = 預設
	Instructions string
	// Begin、Tool 同 Hooks:每個請求開始(回傳的函式在處理完後呼叫)、工具執行前(統計、處理完重新整理畫面用)
	Begin func() (end func())
	Tool  func(name string)
	// Done 每次工具呼叫處理完後(含被拒絕的):哪個角色、哪個任務(憑證綁定的)、呼叫哪個工具、成功(err 為 nil)或失敗。
	// 只給稽核紀錄用(例如自動執行的執行日誌),不帶參數與結果
	Done func(role Role, tool string, err error)
}

const agentInstructions = "This is omixor's agent endpoint: you are working as an agent, and your identity is determined by the credential. " +
	"First use agent_context to get the agent instructions and tasks, complete them following the task description, and report progress and the result with agent_task_report (done when finished, blocked when stuck; the credential is revoked immediately after you report). " +
	"When the organization chain has downstream agents, you can delegate work to them with agent_task_delegate (see chain.downstream of agent_context for who you can delegate to), and they report back to you automatically when done. " +
	"You can read issues (issues), create issues (issue_create), and comment on or change the status of issues (issue_update); to own the analysis and breakdown of an issue, accept it with issue_accept (release=true releases it). " +
	"The cabinet (cabinet, docs, doc_save) can be read and written only at locations within the agent's docAccess; when the cabinet is split into projects, use projects to see which projects exist, and pass project (the project id) to the tools to operate on that project; without it they operate on the no-project cabinet. " +
	"Message channels: mentions in agent_context are unread messages that mention you (only notifications, not tasks; you decide whether to reply); channels lists the channels you are in, channel_messages reads them (reading marks as read), and channel_post replies. The signature is always your agent. " +
	"Items (to-do list): for work that spans phases, should be split among other sub-agents, or needs a human decision, use issue_item_add to open an item under the issue (do not open overly small ones); issue_items shows the list (only unfinished ones by default). " +
	"If your task is an item (the task description states the item number, e.g. ISS-37.2), when finished use issue_item_report status=done (with evidence: commit, test results, anything not verified); " +
	"if stuck or a human decision is needed, use issue_item_report status=blocked and write block_reason; when a human decision is needed, set wait_for_kind=user_decision and do not guess the answer. " +
	"Tools: only the tools listed in tools.available of agent_context can be used. Tools in tools.unavailable are blocked by the role configuration (reason is a stable code, reason_message explains it) and only a human can change that; do not retry them or invent workarounds. " +
	"If the task needs one, report the task blocked (agent_task_report status=blocked) and say which tool or permission is missing." + protocolInstructions

// AgentEndpoint 角色端點的 http.Handler(掛在 /mcp/agent):
//
//	Authorization: Bearer <agentId>:<secret>  → svc.AuthenticateAgent 驗證(憑證綁定一筆進行中的任務)
//
// 驗證失敗一律 401(不洩漏角色或憑證存不存在),同一來源失敗太多次 429。
// 通過後每個工具呼叫都以該角色身分執行(Backend 是角色身分的 backend.Local,權限由 service 強制),只開放白名單工具。
func AgentEndpoint(cfg AgentEndpointConfig) http.Handler {
	ins := cfg.Instructions
	if ins == "" {
		ins = agentInstructions
	}
	srv := NewCoreServer(ins)
	allow := map[string]bool{}
	for k := range AgentToolAllow {
		allow[k] = true
	}
	extra := map[string]bool{}
	extraNames := []string{}
	for _, t := range cfg.Tools {
		srv.Add(t)
		n := t.Def["name"].(string)
		allow[n] = true
		extra[n] = true
		extraNames = append(extraNames, n)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := service.ClientIP(r)
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		var id, secret string
		var ok bool
		if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
			id, secret, ok = strings.Cut(strings.TrimSpace(auth[len(prefix):]), ":")
		}
		if !ok { // 格式不對也算一次失敗(同樣的 401)
			id, secret = "", ""
		}
		p, err := cfg.Svc.AuthenticateAgent(id, secret, ip)
		if err != nil {
			if service.Code(err) == 429 {
				w.Header().Set("Connection", "close")
				http.Error(w, "Too many failed attempts", 429)
				return
			}
			unauthorized(w, "omixor-agent")
			return
		}
		role := &Role{Org: p.Agent.Org, AgentID: p.Agent.AgentID, TaskID: p.Agent.TaskID}
		// 依角色目前的設定決定列哪些核心工具(沒有文件權限就不列文件櫃工具…);額外掛上來的工具一律列
		avail := cfg.Svc.AgentToolSet(role.Org, role.AgentID)
		sess := &Session{
			B: &backend.Local{Svc: cfg.Svc, P: p}, OrgID: role.Org, Identity: role, Diag: newConnDiag("agent", nil, nil, r),
			OrgNameFn: func() string {
				if o, err := cfg.Svc.Store.GetOrg(role.Org); err == nil {
					return o.Name
				}
				return role.Org
			},
			// 署名要能顯示其他角色的名稱:直接讀 store(角色身分的 backend 只看得到自己)
			AgentNameFn: func(id string) string {
				if g, err := cfg.Svc.Store.GetAgent(role.Org, id); err == nil {
					return g.Name
				}
				return id
			},
		}
		sess.Extra = func(g model.Agent, inc bool) map[string]any {
			out := map[string]any{}
			if cfg.ContextExtra != nil {
				for k, v := range cfg.ContextExtra(*role, g, inc) {
					out[k] = v
				}
			}
			out["tools"] = agentToolsContext(cfg.Svc, role.Org, g, extraNames)
			return out
		}
		if cfg.Decorate != nil {
			sess.Decorate = func(g model.Agent, out map[string]any) { cfg.Decorate(*role, g, out) }
		}
		hooks := Hooks{
			Ctx:   sess,
			Begin: cfg.Begin,
			Tool:  cfg.Tool,
			Done: func(name string, err error) {
				if cfg.Done != nil {
					cfg.Done(*role, name, err)
				}
			},
			Allow: func(name string) bool { return allow[name] && (extra[name] || avail[name]) },
			Guard: func(name string, args map[string]any) error {
				// 呼叫端自帶的 agent 參數不算數:不能指定其他角色,一律強制成憑證的角色
				if v, ok := args["agent"]; ok {
					s, _ := v.(string)
					if s = strings.TrimSpace(s); s != "" && !strings.EqualFold(s, role.AgentID) {
						return Errorf("The agent endpoint can only act as its own identity; another agent cannot be specified")
					}
				}
				args["agent"] = role.AgentID
				if name == "agent_task_delegate" { // 來源任務一律是憑證綁定的那一筆(service 也會強制)
					delete(args, "task")
				}
				if name == "agent_task_report" {
					id, _ := args["id"].(string)
					if strings.TrimSpace(id) == "" {
						args["id"] = role.TaskID
					} else if !strings.EqualFold(strings.TrimSpace(id), role.TaskID) {
						return Errorf("This credential can only report task %s", role.TaskID)
					}
				}
				if cfg.Filter != nil {
					return cfg.Filter(*role, name, args)
				}
				return nil
			},
		}
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}

// ================= 管理端點 =================

// AdminToolAllow 管理端點開放的核心工具(登記表 model.CoreTools 的 Admin)
var AdminToolAllow = model.CoreToolSet(func(t model.CoreTool) bool { return t.Admin })

// AdminEndpointConfig 管理端點的設定
type AdminEndpointConfig struct {
	// Resolve 認證這個請求,回傳這次連線的 Backend 與組織(例如雲端:用 omixor token 換 Principal,組織取自路徑)。
	// 錯誤是 *service.Error 時用它的狀態碼(401、403、404…),其他一律 401
	Resolve func(r *http.Request) (backend.Backend, string, error)
	// AgentEndpoint 角色端點的網址(領取任務時告訴呼叫端);空字串不附
	AgentEndpoint string
	// Tools 額外的工具;ContextExtra 同角色端點(管理端沒有角色限制)
	Tools        []Tool
	Instructions string
	// ResourceMetadata 這個請求對應的 OAuth Protected Resource Metadata 網址(RFC 9728);非空時 401 的 WWW-Authenticate 帶 resource_metadata。空 = 不帶
	ResourceMetadata func(r *http.Request) string
}

const adminInstructions = "This is omixor's MCP endpoint, operating the issues, agents and tasks of the current organization, with the same permissions as your signed-in omixor account. " +
	"Agents: agents shows agents, agent_task_claim claims a task (returning an agent credential bound to that task; use it to connect to the agent endpoint and work as that agent), " +
	"and agent_task_report reports. The assignee of issue_create / issue_update assigns an issue to an agent and automatically queues a task for it. issue_accept accepts an issue (you become the owner and the issue is in progress; release=true releases it), after which you break it down with items. " +
	"Cabinet: cabinet shows the structure, docs queries documents, doc_save creates or modifies them (signed by you; an agent identity cannot be specified; to work as an agent, claim a task with agent_task_claim to get a credential and connect to the agent endpoint /mcp/agent). " +
	"Projects: the cabinet and issues can be split into projects (channels, agents and so on still belong to the organization; an issue number inside a project is <project key>-<n>, at organization level it is ISS-<n>; always display the key); projects lists projects, project_save creates or modifies a project, and cabinet, docs, doc_save and cabinet_layout_save take project (the project id) to operate on that project, while without it they operate on the no-project cabinet; when doc_save modifies a document, giving project moves the document to that project. " +
	"Message channels: channels lists my channels (with unread counts), channel_create creates one, channel_messages reads (reading marks as read), and channel_post posts a message; mentioning an agent with @ only notifies it and does not queue a task. " +
	"Items (to-do list; the number is <issue key>.<sequence>, e.g. ISS-37.2): when a big idea needs phases or must be split among different sub-agents, use issue_item_add to open items under the issue (only for work that spans phases, spans sub-agents or needs a human decision), " +
	"issue_items shows the list, issue_item_claim claims (when the owner is an agent, a task is queued and a credential issued automatically), issue_item_report reports (done with evidence; when a human decision is needed use status=blocked and wait_for_kind=user_decision, and do not guess the answer), " +
	"issue_item_update modifies or cancels, and issue_item_promote promotes an item to an independent issue." + protocolInstructions

// AdminEndpoint 管理用 MCP 的 http.Handler(雲端掛在 /mcp/org/{org},用 omixor token 認證)
func AdminEndpoint(cfg AdminEndpointConfig) http.Handler {
	ins := cfg.Instructions
	if ins == "" {
		ins = adminInstructions
	}
	srv := NewCoreServer(ins)
	allow := map[string]bool{}
	for k := range AdminToolAllow {
		allow[k] = true
	}
	extra := map[string]bool{}
	extraNames := []string{}
	for _, t := range cfg.Tools {
		srv.Add(t)
		n := t.Def["name"].(string)
		allow[n] = true
		extra[n] = true
		extraNames = append(extraNames, n)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bk, org, err := cfg.Resolve(r)
		if err != nil {
			if c := service.Code(err); c == 403 || c == 404 || c == 429 {
				http.Error(w, httperr.Report("mcp 端點解析身分", err).Msg, c)
				return
			}
			rm := ""
			if cfg.ResourceMetadata != nil {
				rm = cfg.ResourceMetadata(r)
			}
			// 帶了 OAuth 的 access token(omo_)卻被拒(過期、撤銷、不是這個資源的):error="invalid_token",用戶端會去 refresh 或重新授權
			tok, _ := BearerToken(r)
			unauthorizedWith(w, "omixor", rm, strings.HasPrefix(tok, model.OAuthTokenPrefix))
			return
		}
		sess := &Session{B: bk, OrgID: org, Endpoint: cfg.AgentEndpoint, Diag: newConnDiag("admin", bk, nil, r)}
		// OAuth 授權:唯讀(mcp:read)只開放登記為 ReadOnly 的核心工具、其他一律「此連線是唯讀授權」(restrictReadOnly);讀寫(mcp:write)再多開 OAuthWriteTools(restrictOAuthWrite);額外掛上來的工具都不開放
		hooks := WithApps(Hooks{Ctx: sess,
			// 人類端點不列 HumanHiddenTools(agent_task_delegate)
			Allow: func(name string) bool {
				return allow[name] && !HumanHiddenTools[name]
			},
			// 人類端點不能指定角色身分(署名、文件權限):規格不列 agent 參數,帶 agent 回 400,不執行;flowchart_render、issue_items 帶 MCP Apps 畫面
			Defs: WithoutIdentityAgent,
			Guard: func(name string, args map[string]any) error {
				if err := rejectOtherOrg(args, org); err != nil {
					return err
				}
				return RejectIdentityAgent(name, args)
			},
		})
		hooks = restrictOAuth(hooks, bk, false)
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}

// rejectOtherOrg 舊端點(/mcp/org/{org})的組織固定是路徑上的 org:工具參數帶了不同的 org 就拒絕(相同或沒帶都可以;空字串視為沒帶)。
// 通過後把 org 參數拿掉,工具看到的參數和以前一樣
func rejectOtherOrg(args map[string]any, pathOrg string) error {
	v, has := args["org"]
	if !has {
		return nil
	}
	delete(args, "org")
	if v == nil {
		return nil
	}
	s, isStr := v.(string)
	if !isStr {
		return ToolError{Message: "The org parameter must be a string (organization id); this endpoint is fixed to organization " + pathOrg, Status: 400}
	}
	if s = strings.TrimSpace(s); s != "" && s != pathOrg {
		return ToolError{Message: "This endpoint is fixed to organization " + pathOrg + " (from the URL path), so the org parameter cannot change it to " + s + "; to work across organizations, connect to the unified entry point /mcp or to /mcp/org/" + s, Status: 400}
	}
	return nil
}
