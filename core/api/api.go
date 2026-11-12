// Package api 是核心的 HTTP 路由。回應:成功 {"result": …}(登入與 /api/auth/me 例外,欄位在最上層),失敗 {"error": "…"} 加狀態碼。
//
//	POST /api/auth/evoleap                     {token, device} → {token, tokenId, expiresAt, user}(不用登入,有頻率限制)
//	GET  /api/auth/me                          {user, token}
//	POST /api/auth/logout                      撤銷目前這個 token
//
//	GET  /api/orgs                             我能用的組織(伺服器管理員是全部;myRole 是我的角色)
//	POST /api/orgs                             {id, name, symbol?, color?, note?} 建立組織(伺服器管理員)
//	PUT  /api/orgs/{org}                       {name, symbol?, color?, note?} 修改組織(伺服器管理員)
//	GET  /api/orgs/{org}/members               成員
//	POST /api/orgs/{org}/members               {email, role} 加入或改角色(組織管理員)
//	POST /api/orgs/{org}/members/remove        {email}
//	GET  /api/orgs/{org}/issues                全部 Issue(新的在前)
//	POST /api/orgs/{org}/issues                {title, body, priority, labels, source, link, author} 建立
//	GET  /api/orgs/{org}/issues/{id}
//	POST /api/orgs/{org}/attachments           {data: base64} 上傳圖片(PNG、JPEG、GIF、WebP,10 MB)→ {id}
//	GET  /api/orgs/{org}/attachments/{id}      → {type, data: base64}
//	POST /api/orgs/{org}/issues/{id}           {title?, body?, status?, priority?, labels?, source?, link?, comments?, author?} 修改
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
//	  credential = {agentId, taskId, secret, expiresAt}:密鑰只在領取時給一次,伺服器只存雜湊;
//	  連角色端點 /mcp/agent 用 Authorization: Bearer <agentId>:<secret>(見 core/mcp、server)
//	Issue 的 POST(建立、修改)多一個 assignee(角色 id):指向存在的角色(不存在 400)且啟用時自動替它排任務
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
