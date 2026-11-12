// Package api 是核心的 HTTP 路由。回應:成功 {"result": …}(登入與 /api/auth/me 例外,欄位在最上層),失敗 {"error": "…"} 加狀態碼。
//
//	POST /api/auth/evoleap                     {token, device} → {token, tokenId, expiresAt, user}(不用登入,有頻率限制)
//	GET  /api/auth/me                          {user, token}
//	POST /api/auth/logout                      撤銷目前這個 token
//
//	GET  /api/orgs                             我能用的組織(伺服器管理員是全部;myRole 是我的角色)
//	POST /api/orgs                             {id, name} 建立組織(伺服器管理員)
//	GET  /api/orgs/{org}/members               成員
//	POST /api/orgs/{org}/members               {email, role} 加入或改角色(組織管理員)
//	POST /api/orgs/{org}/members/remove        {email}
//	GET  /api/orgs/{org}/issues                全部 Issue(新的在前)
//	POST /api/orgs/{org}/issues                {title, body, priority, labels, source, link, author} 建立
//	GET  /api/orgs/{org}/issues/{id}
//	POST /api/orgs/{org}/issues/{id}           {title?, body?, status?, priority?, labels?, source?, link?, comments?, author?} 修改
//	POST /api/orgs/{org}/issues/{id}/delete
//	GET  /api/orgs/{org}/issues/export         整份(組織管理員)
//	POST /api/orgs/{org}/issues/import         {issues} 第一次搬資料用(組織管理員;已經有 Issue 就拒絕)
//
//	GET  /api/admin/users | POST {email, role?, disabled?}   (伺服器管理員,以下同)
//	GET  /api/admin/tokens | POST /api/admin/tokens/{id}/revoke
//	GET  /api/admin/audit?limit=
//
// 舊路徑(現有的 app 與 CLI 還在用,行為相同):/api/issues/{org}…、/api/admin/issues/{org}/export|import
package api

import (
	"encoding/json"
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
		var in struct{ ID, Name string }
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		o, err := a.Svc.CreateOrg(p, in.ID, in.Name)
		reply(w, o, err)
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
