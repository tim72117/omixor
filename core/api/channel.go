package api

import (
	"net/http"
	"strconv"

	"omixor-core/model"
	"omixor-core/service"
)

// channelRoutes 訊息頻道的路由(說明見 api.go 檔頭;規則在 service/channel.go)。
// 角色憑證(Bearer <agentId>:<secret>)也可以用:只看與發自己是成員的頻道。
// as(查詢參數或 body 欄位)只有伺服器管理員給的算數
func (a *API) channelRoutes(mux *http.ServeMux) {
	base := "/api/orgs/{org}/channels"
	mux.HandleFunc("GET "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		q := r.URL.Query()
		l, err := a.Svc.ListChannels(p, r.PathValue("org"), service.ChannelFilter{IssueID: q.Get("issue"), As: q.Get("as")})
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ChannelInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		c, err := a.Svc.CreateChannel(p, r.PathValue("org"), in)
		reply(w, c, err)
	}))
	mux.HandleFunc("GET "+base+"/inbox", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := a.Svc.ChannelInbox(p, r.PathValue("org"), r.URL.Query().Get("for"))
		reply(w, x, err)
	}))
	mux.HandleFunc("GET "+base+"/{id}", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		c, err := a.Svc.GetChannel(p, r.PathValue("org"), r.PathValue("id"), r.URL.Query().Get("as"))
		reply(w, c, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ChannelPatch
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		c, err := a.Svc.UpdateChannel(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, c, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/members", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ChannelMembersInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		c, err := a.Svc.ChangeChannelMembers(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, c, err)
	}))
	mux.HandleFunc("GET "+base+"/{id}/messages", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		q := r.URL.Query()
		in := service.MessageListInput{Since: q.Get("since"), Before: q.Get("before"), As: q.Get("as")}
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				bad(w, err)
				return
			}
			in.Limit = n
		}
		page, err := a.Svc.ListChannelMessages(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, page, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/messages", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.PostInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		m, err := a.Svc.PostChannelMessage(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, m, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/read", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ReadInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.MarkChannelRead(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, x, err)
	}))
}
