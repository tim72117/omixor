package mcp

import (
	"net/http"
	"strings"

	"omixor-core/backend"
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

func unauthorized(w http.ResponseWriter, realm string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`"`)
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(401)
	_, _ = w.Write([]byte("Unauthorized"))
}

// ================= 角色端點 =================

// AgentToolAllow 角色端點開放的核心工具(tools/list 也只列這些;額外掛上來的工具另外算)
var AgentToolAllow = map[string]bool{
	"ops_agent_context":     true,
	"ops_agent_tasks":       true,
	"ops_agent_task_report": true,
	"ops_issues":            true,
	"ops_issue_create":      true,
	"ops_issue_update":      true,
	"ops_cabinet":           true,
	"ops_docs":              true,
	"ops_doc_save":          true,
	// 依組織鏈委派給下游角色(service 依憑證身分強制)
	"ops_agent_task_delegate": true,
	// 訊息頻道:只看與發自己是成員的頻道(service 依憑證身分強制;不能建頻道、不能改成員)
	"ops_channels":         true,
	"ops_channel_messages": true,
	"ops_channel_post":     true,
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
	// ContextExtra 附加到 ops_agent_context 輸出的欄位(文件櫃的 cabinet、docs 核心已經附上)
	ContextExtra func(role Role, g model.Agent, includeBodies bool) map[string]any
	// Decorate 改寫角色的輸出(ops_agents、ops_agent_context 的 agent 欄位);docAccess 核心已經換成看得懂的文件櫃範圍
	Decorate func(role Role, g model.Agent, out map[string]any)
	// Instructions initialize 回傳的說明;空 = 預設
	Instructions string
	// Begin、Tool 同 Hooks:每個請求開始(回傳的函式在處理完後呼叫)、工具執行前(統計、處理完重新整理畫面用)
	Begin func() (end func())
	Tool  func(name string)
}

const agentInstructions = "這是 omixor 的角色端點:你正以虛擬角色的身分工作,身分由憑證決定。" +
	"先用 ops_agent_context 取得角色設定與任務,照任務說明完成,過程與結果用 ops_agent_task_report 回報(做完 done、卡住 blocked,回報後憑證立刻失效)。" +
	"組織鏈上有下游角色時,可以用 ops_agent_task_delegate 把工作委派出去(可以派給誰見 ops_agent_context 的 chain.downstream),對方做完會自動回報給你。" +
	"可以讀 Issue(ops_issues)、建立 Issue(ops_issue_create)、對 Issue 留言與改狀態(ops_issue_update);" +
	"文件櫃(ops_cabinet、ops_docs、ops_doc_save)只能讀寫角色 docAccess 範圍內的位置。" +
	"訊息頻道:ops_agent_context 的 mentions 是提到你的未讀訊息(只是通知,不是任務,自己決定要不要回);ops_channels 看你所在的頻道,ops_channel_messages 讀(讀了算已讀),ops_channel_post 回覆。署名一律是你的角色。"

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
	for _, t := range cfg.Tools {
		srv.Add(t)
		allow[t.Def["name"].(string)] = true
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
		if cfg.ContextExtra != nil {
			sess.Extra = func(g model.Agent, inc bool) map[string]any { return cfg.ContextExtra(*role, g, inc) }
		}
		if cfg.Decorate != nil {
			sess.Decorate = func(g model.Agent, out map[string]any) { cfg.Decorate(*role, g, out) }
		}
		hooks := Hooks{
			Ctx:   sess,
			Begin: cfg.Begin,
			Tool:  cfg.Tool,
			Allow: func(name string) bool { return allow[name] },
			Guard: func(name string, args map[string]any) error {
				// 呼叫端自帶的 agent 參數不算數:不能指定其他角色,一律強制成憑證的角色
				if v, ok := args["agent"]; ok {
					s, _ := v.(string)
					if s = strings.TrimSpace(s); s != "" && !strings.EqualFold(s, role.AgentID) {
						return Errorf("角色端點只能以自己的身分操作,不能指定其他角色")
					}
				}
				args["agent"] = role.AgentID
				if name == "ops_agent_task_delegate" { // 來源任務一律是憑證綁定的那一筆(service 也會強制)
					delete(args, "task")
				}
				if name == "ops_agent_task_report" {
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

// AdminToolAllow 管理端點開放的核心工具
var AdminToolAllow = map[string]bool{
	"ops_orgs":              true,
	"ops_issues":            true,
	"ops_issue_create":      true,
	"ops_issue_update":      true,
	"ops_agents":            true,
	"ops_agent_context":     true,
	"ops_agent_tasks":       true,
	"ops_agent_task_claim":  true,
	"ops_agent_task_report": true,
	"ops_agent_upsert":      true,
	"ops_agent_task_create": true,
	"ops_cabinet":           true,
	"ops_docs":              true,
	"ops_doc_save":          true,
	// 組織鏈:讀(成員)、改(組織管理員)
	"ops_chain":      true,
	"ops_chain_save": true,
	// 訊息頻道
	"ops_channels":         true,
	"ops_channel_create":   true,
	"ops_channel_messages": true,
	"ops_channel_post":     true,
}

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
}

const adminInstructions = "這是 omixor 的 MCP 端點,操作目前組織的 Issue、虛擬角色與任務,權限同你登入的 omixor 帳號。" +
	"虛擬角色:ops_agents 看角色,ops_agent_task_claim 領任務(回傳一組綁定該任務的角色憑證,用它連角色端點以該角色身分工作)," +
	"ops_agent_task_report 回報。ops_issue_create / ops_issue_update 的 assignee 指派 Issue 給角色,會自動替它排任務。" +
	"文件櫃:ops_cabinet 看結構、ops_docs 查文件、ops_doc_save 新增或修改(以角色身分工作時帶 agent,只能讀寫該角色 docAccess 範圍內的文件)。" +
	"訊息頻道:ops_channels 列我的頻道(含未讀數)、ops_channel_create 建立、ops_channel_messages 讀(讀了算已讀)、ops_channel_post 發訊息;@ 角色只通知、不排任務。"

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
	for _, t := range cfg.Tools {
		srv.Add(t)
		allow[t.Def["name"].(string)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bk, org, err := cfg.Resolve(r)
		if err != nil {
			if c := service.Code(err); c == 403 || c == 404 || c == 429 {
				http.Error(w, err.Error(), c)
				return
			}
			unauthorized(w, "omixor")
			return
		}
		sess := &Session{B: bk, OrgID: org, Endpoint: cfg.AgentEndpoint}
		hooks := Hooks{Ctx: sess, Allow: func(name string) bool { return allow[name] }}
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}
