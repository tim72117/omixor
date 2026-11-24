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
func agentToolsContext(svc *service.Service, org string, g model.Agent, extra []string) map[string]any {
	can, cannot := []any{}, []any{}
	for _, t := range svc.AgentToolsOf(org, g) {
		if t.Available {
			can = append(can, map[string]any{"name": t.Name, "label": t.Label})
		} else {
			cannot = append(cannot, map[string]any{"name": t.Name, "label": t.Label, "reason": t.Reason})
		}
	}
	for _, n := range extra {
		can = append(can, map[string]any{"name": n})
	}
	return map[string]any{
		"available":   can,
		"unavailable": cannot,
		"note":        "只有 available 的工具能用;unavailable 要由人改角色設定才會開放,需要時回報 blocked 說明缺什麼",
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
}

const agentInstructions = "這是 omixor 的角色端點:你正以虛擬角色的身分工作,身分由憑證決定。" +
	"先用 agent_context 取得角色設定與任務,照任務說明完成,過程與結果用 agent_task_report 回報(做完 done、卡住 blocked,回報後憑證立刻失效)。" +
	"組織鏈上有下游角色時,可以用 agent_task_delegate 把工作委派出去(可以派給誰見 agent_context 的 chain.downstream),對方做完會自動回報給你。" +
	"可以讀 Issue(issues)、建立 Issue(issue_create)、對 Issue 留言與改狀態(issue_update);要負責一張 Issue 的分析與拆解用 issue_accept 接受(release=true 放回);" +
	"文件櫃(cabinet、docs、doc_save)只能讀寫角色 docAccess 範圍內的位置;文件櫃分專案時用 projects 看有哪些專案,工具帶 project(專案 id)操作那個專案,不帶是未分專案。" +
	"訊息頻道:agent_context 的 mentions 是提到你的未讀訊息(只是通知,不是任務,自己決定要不要回);channels 看你所在的頻道,channel_messages 讀(讀了算已讀),channel_post 回覆。署名一律是你的角色。" +
	"子項(待辦清單):遇到跨階段、要拆給別的子代理、或需要人決定的工作,用 issue_item_add 在 Issue 底下開子項(太細的不要開);issue_items 看清單(預設只回未完成)。" +
	"你的任務如果是子項(任務說明會寫子項編號,例如 ISS-37.2),做完用 issue_item_report status=done(附 evidence 證據:commit、測試結果、沒驗證到的項目);" +
	"卡住、或需要人決定就 issue_item_report status=blocked 並寫 block_reason,需要人決定時 wait_for_kind=user_decision,不要猜答案。"

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
			B: &backend.Local{Svc: cfg.Svc, P: p}, OrgID: role.Org, Identity: role,
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
			Allow: func(name string) bool { return allow[name] && (extra[name] || avail[name]) },
			Guard: func(name string, args map[string]any) error {
				// 呼叫端自帶的 agent 參數不算數:不能指定其他角色,一律強制成憑證的角色
				if v, ok := args["agent"]; ok {
					s, _ := v.(string)
					if s = strings.TrimSpace(s); s != "" && !strings.EqualFold(s, role.AgentID) {
						return Errorf("角色端點只能以自己的身分操作,不能指定其他角色")
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
						return Errorf("這組憑證只能回報任務 %s", role.TaskID)
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

const adminInstructions = "這是 omixor 的 MCP 端點,操作目前組織的 Issue、虛擬角色與任務,權限同你登入的 omixor 帳號。" +
	"虛擬角色:agents 看角色,agent_task_claim 領任務(回傳一組綁定該任務的角色憑證,用它連角色端點以該角色身分工作)," +
	"agent_task_report 回報。issue_create / issue_update 的 assignee 指派 Issue 給角色,會自動替它排任務。issue_accept 接受 Issue(你成為負責人、Issue 進行中;release=true 放回),之後再用子項拆解。" +
	"文件櫃:cabinet 看結構、docs 查文件、doc_save 新增或修改(署名是你;不能指定角色身分,要以角色身分工作請 agent_task_claim 領任務拿憑證、連角色端點 /mcp/agent)。" +
	"專案:文件櫃與 Issue 可以分專案(頻道、角色等仍屬於組織;專案裡的 Issue 編號是 <專案代號>-<n>,組織層級的是 ISS-<n>,顯示時用 key);projects 列專案、project_save 新增或修改專案,cabinet、docs、doc_save、cabinet_layout_save 帶 project(專案 id)操作那個專案,不帶是未分專案;doc_save 修改時給 project 就是把文件搬到那個專案。" +
	"訊息頻道:channels 列我的頻道(含未讀數)、channel_create 建立、channel_messages 讀(讀了算已讀)、channel_post 發訊息;@ 角色只通知、不排任務。" +
	"子項(待辦清單,編號是 <Issue key>.<序號>,例如 ISS-37.2):大構想要分階段、分給不同子代理時,用 issue_item_add 在 Issue 底下開子項(跨階段、跨子代理或要人決定的才開)," +
	"issue_items 看清單、issue_item_claim 領取(負責人是角色時自動排任務並發憑證)、issue_item_report 回報(done 附 evidence;需要人決定就 status=blocked、wait_for_kind=user_decision,不要猜答案)、" +
	"issue_item_update 改或取消、issue_item_promote 提升為獨立 Issue。"

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
	uiResources := UIResources()
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
		sess := &Session{B: bk, OrgID: org, Endpoint: cfg.AgentEndpoint}
		// OAuth 授權:唯讀(mcp:read)只開放登記為 ReadOnly 的核心工具、其他一律「此連線是唯讀授權」(restrictReadOnly);讀寫(mcp:write)再多開 OAuthWriteTools(restrictOAuthWrite);額外掛上來的工具都不開放
		hooks := Hooks{Ctx: sess,
			// 人類端點不列 HumanHiddenTools(agent_task_delegate)
			Allow: func(name string) bool {
				return allow[name] && !HumanHiddenTools[name]
			},
			// 人類端點不能指定角色身分(署名、文件權限):規格不列 agent 參數,帶 agent 回 400,不執行;flowchart_render、issue_items 帶 MCP Apps 畫面
			Defs: func(defs []any) []any { return WithUIMeta(WithoutIdentityAgent(defs)) },
			Guard: func(name string, args map[string]any) error {
				if err := rejectOtherOrg(args, org); err != nil {
					return err
				}
				return RejectIdentityAgent(name, args)
			},
			Resources: uiResources}
		hooks = restrictOAuth(hooks, bk)
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
		return ToolError{Message: "org 參數必須是字串(組織 id);這個端點的組織固定是 " + pathOrg, Status: 400}
	}
	if s = strings.TrimSpace(s); s != "" && s != pathOrg {
		return ToolError{Message: "這個端點的組織固定是 " + pathOrg + "(網址路徑),不能用 org 參數改成 " + s + ";要跨組織請改連統一入口 /mcp,或連 /mcp/org/" + s, Status: 400}
	}
	return nil
}
