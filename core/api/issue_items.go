package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// issueItemRoutes Issue 子項(issue_item,ISS-37)的路由。規則(授權、可見性繼承、領取、回寫任務)在 service/issue_items.go。
// 看、新增、回報(只限自己綁定任務的子項)角色憑證也可以;其餘(改、領取、放棄、解除卡住、取消、提升、刪除)只有人(角色憑證 403)。
// 署名一律是呼叫者本人,請求裡沒有「誰」的欄位(負責人除外:assignee 是被指派的對象)。{id} 是 Issue 編號,{itemId} 是子項 key、序號或 id。
//
//	GET    /api/orgs/{org}/issues/{id}/items?includeDone=1&status=      列(預設只回未完成)
//	POST   /api/orgs/{org}/issues/{id}/items                            新增 {title, files?, acceptance?, assignee?, dependsOn?}
//	GET    /api/orgs/{org}/issues/{id}/items/{itemId}
//	PATCH  /api/orgs/{org}/issues/{id}/items/{itemId}                   {title?, files?, acceptance?, assignee?, dependsOn?}
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/claim             {note?} → {item, task?, credential?}(負責人是角色時帶任務與一次性憑證)
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/release
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/report            {status: todo|doing|blocked|done, evidence?, note?, blockReason?, waitForKind?, waitFor?}
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/block             {blockReason, waitForKind?, waitFor?}
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/unblock
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/cancel
//	POST   /api/orgs/{org}/issues/{id}/items/{itemId}/promote           {priority?, labels?, project?} → {issue, item}
//	DELETE /api/orgs/{org}/issues/{id}/items/{itemId}
func (a *API) issueItemRoutes(mux *http.ServeMux) {
	base := "/api/orgs/{org}/issues/{id}/items"
	one := base + "/{itemId}"
	type handler func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, item string)
	route := func(pattern string, h handler) {
		mux.HandleFunc(pattern, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
			h(w, r, p, r.PathValue("org"), r.PathValue("id"), r.PathValue("itemId"))
		}))
	}
	// post 讀 JSON 請求(body 可以是空的)再呼叫 f
	post := func(action string, in func() any, f func(p *model.Principal, org, issue, item string, in any) (any, error)) {
		route("POST "+one+"/"+action, func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, item string) {
			v := in()
			if v != nil {
				if err := read(r, v); err != nil {
					bad(w, err)
					return
				}
			}
			out, err := f(p, org, issue, item, v)
			reply(w, out, err)
		})
	}

	route("GET "+base, func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, _ string) {
		q := r.URL.Query()
		l, err := a.Svc.ListItems(p, org, issue, service.ItemListQuery{IncludeDone: q.Get("includeDone") == "1" || q.Get("includeDone") == "true", Status: q.Get("status")})
		reply(w, l, err)
	})
	route("POST "+base, func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, _ string) {
		var in service.ItemInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		it, err := a.Svc.AddItem(p, org, issue, in)
		reply(w, it, err)
	})
	route("GET "+one, func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, item string) {
		it, err := a.Svc.GetItem(p, org, issue, item)
		reply(w, it, err)
	})
	route("PATCH "+one, func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, item string) {
		var in service.ItemPatch
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		it, err := a.Svc.UpdateItem(p, org, issue, item, in)
		reply(w, it, err)
	})
	route("DELETE "+one, func(w http.ResponseWriter, r *http.Request, p *model.Principal, org, issue, item string) {
		err := a.Svc.DeleteItem(p, org, issue, item)
		reply(w, map[string]bool{"ok": err == nil}, err)
	})

	post("claim", func() any { return &service.ItemClaimInput{} }, func(p *model.Principal, org, issue, item string, in any) (any, error) {
		return a.Svc.ClaimItem(p, org, issue, item, *in.(*service.ItemClaimInput))
	})
	post("release", func() any { return nil }, func(p *model.Principal, org, issue, item string, _ any) (any, error) {
		return a.Svc.ReleaseItem(p, org, issue, item)
	})
	post("report", func() any { return &service.ItemReport{} }, func(p *model.Principal, org, issue, item string, in any) (any, error) {
		return a.Svc.ReportItem(p, org, issue, item, *in.(*service.ItemReport))
	})
	post("block", func() any { return &service.ItemReport{} }, func(p *model.Principal, org, issue, item string, in any) (any, error) {
		return a.Svc.BlockItem(p, org, issue, item, *in.(*service.ItemReport))
	})
	post("unblock", func() any { return nil }, func(p *model.Principal, org, issue, item string, _ any) (any, error) {
		return a.Svc.UnblockItem(p, org, issue, item)
	})
	post("cancel", func() any { return nil }, func(p *model.Principal, org, issue, item string, _ any) (any, error) {
		return a.Svc.CancelItem(p, org, issue, item)
	})
	post("promote", func() any { return &service.ItemPromoteInput{} }, func(p *model.Principal, org, issue, item string, in any) (any, error) {
		return a.Svc.PromoteItem(p, org, issue, item, *in.(*service.ItemPromoteInput))
	})
}
