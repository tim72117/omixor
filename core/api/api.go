// Package api 是核心的 HTTP 路由。回應:成功 {"result": …}(登入與 /api/auth/me 例外,欄位在最上層),失敗 {"error": "…"} 加狀態碼。
//
//	POST /api/auth/evoleap                     {token, device} → {token, tokenId, expiresAt, user}(不用登入,有頻率限制)
//	GET  /api/auth/me                          {user, token}
//	POST /api/auth/logout                      撤銷目前這個 token
//	POST /api/auth/pair                        已登入的使用者產生手機配對碼 → {code, expiresAt}(一次性、5 分鐘;見 service/pair.go)
//	POST /api/auth/pair/claim                  {code, device} → 同 /api/auth/evoleap(不用登入,跟登入共用頻率限制;失敗一律 401 同一句話)
//
//	GET  /api/orgs                             我能用的組織(伺服器管理員是全部;myRole 是我的角色)
//	POST /api/orgs                             {id, name, symbol?, color?, note?} 建立組織(伺服器管理員)
//	PUT  /api/orgs/{org}                       {name, symbol?, color?, note?} 修改組織(伺服器管理員)
//	GET  /api/orgs/{org}/members               成員
//	POST /api/orgs/{org}/members               {email, role} 加入或改角色(組織管理員)
//	POST /api/orgs/{org}/members/remove        {email}
//	GET  /api/orgs/{org}/issues?repo=          全部 Issue(新的在前);repo 有給只回標記該倉庫的(不分大小寫,github:/gitlab: 前綴一邊沒寫就忽略)
//	POST /api/orgs/{org}/issues                {title, body, priority, labels, source, link, repo?, author} 建立
//	GET  /api/orgs/{org}/issues/{id}
//	POST /api/orgs/{org}/attachments           {data: base64} 上傳圖片(PNG、JPEG、GIF、WebP,10 MB)→ {id}
//	GET  /api/orgs/{org}/attachments/{id}      → {type, data: base64}
//	POST /api/orgs/{org}/issues/{id}           {title?, body?, status?, priority?, labels?, source?, link?, repo?, comments?, author?} 修改
//	POST /api/orgs/{org}/issues/{id}/delete
//	GET  /api/orgs/{org}/issues/export         整份(組織管理員)
//	POST /api/orgs/{org}/issues/import         {issues} 第一次搬資料用(組織管理員;已經有 Issue 就拒絕)
//
//	GET  /api/orgs/{org}/agents                虛擬角色(成員;角色憑證只看到自己)
//	POST /api/orgs/{org}/agents                {id?, name, role, instructions, active, symbol, color, docAccess, routines} 新增或修改(組織管理員;沒給 active 視為 true)
//	GET  /api/orgs/{org}/agents/{id}
//	POST /api/orgs/{org}/agents/{id}/delete    刪除(未結束的任務一併取消、憑證作廢)
//	GET  /api/orgs/{org}/agent-tasks?agent=&status=   任務(status:open、all 或單一狀態;預設 all)
//	POST /api/orgs/{org}/agent-tasks           {agentId, title, instructions, priority, links} 派任務(組織管理員)
//	GET  /api/orgs/{org}/agent-tasks/{id}
//	POST /api/orgs/{org}/agent-tasks/claim     {agentId, taskId?} 領取 → {agent, task, credential};沒任務時 task 是 null(組織成員)
//	POST /api/orgs/{org}/agent-tasks/{id}/claim   {agentId} 領取指定任務
//	POST /api/orgs/{org}/agent-tasks/claim-next   {agentId?} 從全部啟用角色的待領取任務挑最優先的一筆領走(可限定角色)→ 同 claim
//	POST /api/orgs/{org}/agent-tasks/{id}/release {reason?} 放棄領取,退回待領取(領取者本人或組織管理員)
//	POST /api/orgs/{org}/agent-tasks/{id}/report  {status?, result?, note?, links?} 回報(成員;角色憑證只能回報綁定的任務)
//	POST /api/orgs/{org}/agent-tasks/{id}/cancel | /requeue   取消、重新排隊(組織管理員)
//	GET  /api/orgs/{org}/agent-tasks?reportTo=me|user:<email>|agent:<id>   依組織鏈回報給這個節點的任務
//
//	GET  /api/orgs/{org}/chain                 組織鏈 {edges: [{from, to, label}], updatedAt}(成員;角色憑證也可以)
//	POST /api/orgs/{org}/chain                 {edges} 整份取代(組織管理員;節點不存在、自環、重複、循環回 400)
//	POST /api/orgs/{org}/agent-tasks/delegate  {to, title, instructions?, priority?} 依組織鏈委派(角色憑證;伺服器管理員可帶 from、taskId)
//	POST /api/orgs/{org}/agent-tasks/{id}/worker/delegate {code, to, title, instructions?, priority?} 執行者以任務角色委派
//	POST /api/orgs/{org}/agent-tasks 的一般成員:鏈上要有 user:<自己> → agent:<目標>(管理員不受限)
//	  credential = {agentId, taskId, secret, expiresAt}:密鑰只在領取時給一次,伺服器只存雜湊;
//	  連角色端點 /mcp/agent 用 Authorization: Bearer <agentId>:<secret>(見 core/mcp、server)
//	Issue 的 POST(建立、修改)多一個 assignee(角色 id):指向存在的角色(不存在 400)且啟用時自動替它排任務
//	Issue 的 repo:所屬程式碼倉庫,owner/name(GitHub)或 group/sub/project(GitLab),可加 github:、gitlab: 前綴;
//	  只允許英數與 - _ . / :、最長 200,不合格 400;修改時給 "" = 清除;沒標記時 JSON 不帶 repo
//
//	GET  /api/orgs/{org}/cabinet               文件櫃:{layout, docs, layoutSaved}(成員;角色憑證只看到 docAccess 能讀的文件)
//	POST /api/orgs/{org}/cabinet/layout        {layout} 改結構(組織管理員;被拿掉的區塊、頁籤裡不能還有文件)
//	POST /api/orgs/{org}/cabinet/import        {layout?, docs?} 第一次搬資料用(組織管理員;已經有文件就拒絕,已經有結構就略過結構)
//	GET  /api/orgs/{org}/docs                  文件(新的在前;角色憑證只看到能讀的)
//	POST /api/orgs/{org}/docs                  {title, body, section, tab?, tags, author?} 新增 → DOC-<n>(依範本檢查必填欄位)
//	GET  /api/orgs/{org}/docs/{id}
//	POST /api/orgs/{org}/docs/{id}             {title?, body?, appendBody?, section?, tab?, tags?} 修改;給 section 就是搬移
//	POST /api/orgs/{org}/docs/{id}/delete      刪除(成員;角色憑證不行)
//
//	GET  /api/orgs/{org}/channels?issue=       訊息頻道:我看得到的(成員;組織管理員是全部;角色憑證只看到自己是成員的),附 unread、lastMessageAt
//	POST /api/orgs/{org}/channels              {name, topic?, issueId?, members?} 建立(組織成員;建立者自動是成員;Issue 已有頻道 → 回傳那個或 409)
//	GET  /api/orgs/{org}/channels/inbox?for=   提到 me|agent:<id>|user:<email> 的未讀訊息(最多 20 則)與所在頻道的未讀數
//	GET  /api/orgs/{org}/channels/{id}
//	POST /api/orgs/{org}/channels/{id}         {name?, topic?} 修改(頻道成員或組織管理員)
//	POST /api/orgs/{org}/channels/{id}/members {add?, remove?} 加減成員(頻道成員或組織管理員;角色憑證不行)
//	GET  /api/orgs/{org}/channels/{id}/messages?since=&before=&limit=   {messages, hasMore}(由舊到新)
//	POST /api/orgs/{org}/channels/{id}/messages {text} 發訊息(頻道成員;@ 只算成員)
//	POST /api/orgs/{org}/channels/{id}/read    {upTo?} 標已讀(沒給 = 最新)
//	  伺服器管理員可以用 as(或 messages 的 author、read 的 member)以角色或別人的身分操作
//
//	GET  /api/orgs/{org}/incidents             事件(新的在前;成員,角色憑證也可以)
//	POST /api/orgs/{org}/incidents             {title, sev, services?, commander?, commanderName?, impact?, startedAt?, aiSummary?, alertIds?, alertTitles?, timeline?} 宣告 → INC-<n>
//	GET  /api/orgs/{org}/incidents/{id}
//	POST /api/orgs/{org}/incidents/{id}        {status?, note?, finding?, aiSummary?, impact?, title?, sev?, services?, commander?, events?, addFollowUps?, addAlertIds?} 更新
//	POST /api/orgs/{org}/incidents/import      {incidents} 第一次搬資料用(組織管理員;已經有事件就拒絕)
//	  伺服器管理員可以用 actor({kind, id})或 author 指定執行者(見 service/incident.go)
//
//	GET  /api/orgs/{org}/integrations/github   GitHub 設定 {repos, saved, updatedAt?, updatedBy?}(成員;角色憑證也可以;沒存過 saved = false)
//	POST /api/orgs/{org}/integrations/github   {repos?, add?, remove?} 修改(組織管理員;repos 整份取代,再套用加減;格式不對 400)
//	POST /api/orgs/{org}/integrations/github/import {repos} 第一次匯入裝置的本機設定(組織管理員;核心存過就不動)→ {imported, skipped, settings}
//	GET  /api/orgs/{org}/integrations/gitlab   GitLab 設定 {baseUrl, projects, saved, updatedAt?, updatedBy?}
//	POST /api/orgs/{org}/integrations/gitlab   {baseUrl?, projects?, add?, remove?} 修改(組織管理員)
//	POST /api/orgs/{org}/integrations/gitlab/import {baseUrl, projects} 第一次匯入(同 GitHub)
//
//	外部 MCP 閘道的登記(功能開關 mcpGateway;沒開時 GET 回 enabled = false 與空清單、寫入回 403;規則在 service/mcp.go):
//	GET  /api/orgs/{org}/mcp-servers           {enabled, note?, servers, grants, canEdit}(成員;角色憑證只看到自己的權限)
//	POST /api/orgs/{org}/mcp-servers           {id, name?, description?, transport?, command?, args?, cwd?, envNames?, url?, headerNames?, enabled?, toolDefault?}
//	                                           新增或修改(組織管理員;帶 env、headers、secrets、token 這類密鑰值一律 400:core 只記名稱)
//	POST /api/orgs/{org}/mcp-servers/{id}/delete   刪除(角色權限裡的這台一併拿掉)
//	POST /api/orgs/{org}/mcp-servers/{id}/tools    {sync?: [{name, title?, description?}], prune?, rules?: [{name, enabled?, confirm?}]} 同步工具清單、改規則
//	POST /api/orgs/{org}/mcp-grants            {agentId, grants: [{server, tools}]} 整份取代角色的權限(tools 空 = 這台開著的全部工具)
//	POST /api/orgs/{org}/mcp-tools/resolve     {agentId?, tools: [{server, name}]} → {enabled, tools: [{server, name, qualified, allowed, confirm, reason?}]}
//	                                           (成員;App 的閘道列工具、呼叫前用;agentId 空 = 一般成員)
//	  只存非機密設定;token、CA 憑證檔、不驗證憑證、用哪個本機連線都留在各裝置(見 model/integration.go)
//
//	GET  /api/orgs/{org}/system                系統設定(只列這次編譯的版本有的功能) {features: [{key, name, description, parent?, edition, experimental, experimentalNote?, default, required?, enabled, on, changed}], saved, updatedAt?, updatedBy?, edition, canEdit}
//	                                           (成員;角色憑證也可以;功能清單在 model/features.go,App 與 web 不另外寫)
//	POST /api/orgs/{org}/system                {features?: {key: bool}, reset?: [key]} 修改功能開關(組織管理員;沒登記的 key、這次編譯沒有的功能、關必要功能 400)
//
//	GET  /api/orgs/{org}/templates             可用的組織樣板 [{id, name, description, feature, agents, channels, docs, edges}](成員;功能開關關閉的不列)
//	GET  /api/orgs/{org}/templates/{id}?scenarios=1 預覽 {template, items: [{kind, name, status: create|skip, detail?}], created, skipped, suggestAutoReply, canApply}
//	POST /api/orgs/{org}/templates/{id}/apply  {scenarios?: bool} 套用(組織管理員;scenarios = 帶入模擬情境;已存在的略過)→ 同上,status 是 created|skipped|failed;功能開關關閉 403;這次編譯沒有該功能 404
//
//	GET  /api/admin/users | POST {email, role?, disabled?}   (伺服器管理員,以下同)
//	GET  /api/admin/tokens | POST /api/admin/tokens/{id}/revoke
//	GET  /api/admin/audit?limit=
//
// 舊路徑(現有的 app 與 CLI 還在用,行為相同):/api/issues/{org}…、/api/admin/issues/{org}/export|import
package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

type API struct{ Svc *service.Service }

func New(svc *service.Service) *API { return &API{Svc: svc} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeJSON(w, service.Code(err), map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"result": v})
}

func read(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return err
	}
	return json.Unmarshal(b, v)
}

func bad(w http.ResponseWriter, err error) {
	writeJSON(w, 400, map[string]any{"error": "格式錯誤:" + err.Error()})
}

type handler func(w http.ResponseWriter, r *http.Request, p *model.Principal)

func (a *API) authed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		p, err := a.Svc.Authenticate(tok)
		if err != nil {
			reply(w, nil, err)
			return
		}
		h(w, r, p)
	}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/evoleap", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token  string `json:"token"`
			Device string `json:"device"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.LoginWithEvoleap(in.Token, in.Device, service.ClientIP(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		writeJSON(w, 200, res)
	})
	mux.HandleFunc("POST /api/auth/pair", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		c, err := a.Svc.CreatePairCode(p)
		reply(w, c, err)
	}))
	mux.HandleFunc("POST /api/auth/pair/claim", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code   string `json:"code"`
			Device string `json:"device"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.ClaimPairCode(in.Code, in.Device, service.ClientIP(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		writeJSON(w, 200, res)
	})
	mux.HandleFunc("GET /api/auth/me", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		writeJSON(w, 200, map[string]any{"user": p.User, "token": p.Token})
	}))
	mux.HandleFunc("POST /api/auth/logout", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.Logout(p))
	}))

	// 組織與成員
	mux.HandleFunc("GET /api/orgs", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListOrgs(p)
		reply(w, l, err)
	}))
	mux.HandleFunc("POST /api/orgs", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in model.Org
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		o, err := a.Svc.CreateOrgWith(p, in)
		reply(w, o, err)
	}))
	mux.HandleFunc("PUT /api/orgs/{org}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in model.Org
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		in.ID = r.PathValue("org")
		if err := a.Svc.UpdateOrg(p, in); err != nil {
			reply(w, nil, err)
			return
		}
		o, err := a.Svc.Store.GetOrg(in.ID)
		reply(w, o, err)
	}))
	mux.HandleFunc("POST /api/orgs/{org}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.DeleteOrg(p, r.PathValue("org")))
	}))
	mux.HandleFunc("GET /api/orgs/{org}/members", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListMembers(p, r.PathValue("org"))
		reply(w, l, err)
	}))
	mux.HandleFunc("POST /api/orgs/{org}/members", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct{ Email, Role string }
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		m, err := a.Svc.SaveMember(p, r.PathValue("org"), in.Email, in.Role)
		reply(w, m, err)
	}))
	mux.HandleFunc("POST /api/orgs/{org}/members/remove", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct{ Email string }
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		reply(w, true, a.Svc.RemoveMember(p, r.PathValue("org"), in.Email))
	}))

	// Issue(新路徑與舊路徑)
	for _, base := range []string{"/api/orgs/{org}/issues", "/api/issues/{org}"} {
		mux.HandleFunc("GET "+base, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			l, err := a.Svc.ListIssues(p, r.PathValue("org"))
			if err == nil {
				l = service.FilterIssuesByRepo(l, r.URL.Query().Get("repo"))
			}
			reply(w, l, err)
		}))
		mux.HandleFunc("POST "+base, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			var in service.IssueInput
			if err := read(r, &in); err != nil {
				bad(w, err)
				return
			}
			i, err := a.Svc.CreateIssue(p, r.PathValue("org"), in)
			reply(w, i, err)
		}))
		mux.HandleFunc("GET "+base+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			i, err := a.Svc.GetIssue(p, r.PathValue("org"), r.PathValue("id"))
			reply(w, i, err)
		}))
		mux.HandleFunc("POST "+base+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			var in service.IssuePatch
			if err := read(r, &in); err != nil {
				bad(w, err)
				return
			}
			i, err := a.Svc.UpdateIssue(p, r.PathValue("org"), r.PathValue("id"), in)
			reply(w, i, err)
		}))
		mux.HandleFunc("POST "+base+"/{id}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			reply(w, true, a.Svc.DeleteIssue(p, r.PathValue("org"), r.PathValue("id")))
		}))
	}
	mux.HandleFunc("POST /api/orgs/{org}/attachments", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Data string `json:"data"` // base64
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		data, err := base64.StdEncoding.DecodeString(in.Data)
		if err != nil {
			bad(w, errors.New("data 不是 base64"))
			return
		}
		id, err := a.Svc.SaveAttachment(p, r.PathValue("org"), data)
		reply(w, map[string]string{"id": id}, err)
	}))
	mux.HandleFunc("GET /api/orgs/{org}/attachments/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		data, mime, err := a.Svc.GetAttachment(p, r.PathValue("org"), r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, map[string]string{"type": mime, "data": base64.StdEncoding.EncodeToString(data)}, nil)
	}))
	for _, base := range []string{"/api/orgs/{org}/issues", "/api/admin/issues/{org}"} {
		mux.HandleFunc("GET "+base+"/export", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			l, err := a.Svc.ExportIssues(p, r.PathValue("org"))
			reply(w, l, err)
		}))
		mux.HandleFunc("POST "+base+"/import", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			var in struct {
				Issues []model.Issue `json:"issues"`
			}
			if err := read(r, &in); err != nil {
				bad(w, err)
				return
			}
			res, err := a.Svc.ImportIssues(p, r.PathValue("org"), in.Issues)
			reply(w, res, err)
		}))
	}

	// 虛擬角色與任務(agents.go)
	a.agentRoutes(mux)
	a.cabinetRoutes(mux)
	a.chainRoutes(mux)
	a.channelRoutes(mux)
	a.incidentRoutes(mux)
	a.integrationRoutes(mux)
	a.mcpRoutes(mux)
	a.systemRoutes(mux)
	a.templateRoutes(mux)

	// 伺服器管理
	mux.HandleFunc("GET /api/admin/users", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListUsers(p)
		reply(w, l, err)
	}))
	mux.HandleFunc("POST /api/admin/users", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Email    string  `json:"email"`
			Role     *string `json:"role"`
			Disabled *bool   `json:"disabled"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		u, err := a.Svc.SaveUser(p, in.Email, in.Role, in.Disabled)
		reply(w, u, err)
	}))
	mux.HandleFunc("GET /api/admin/tokens", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListTokens(p)
		reply(w, l, err)
	}))
	mux.HandleFunc("POST /api/admin/tokens/{id}/revoke", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.RevokeToken(p, r.PathValue("id")))
	}))
	mux.HandleFunc("GET /api/admin/audit", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		l, err := a.Svc.ListAudit(p, n)
		reply(w, l, err)
	}))
	return mux
}
