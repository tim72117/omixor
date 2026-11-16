// Package integrations 是選用模組「程式碼倉庫(GitHub、GitLab)」:組織的 GitHub、GitLab 設定(追蹤的 repo、GitLab 網址與專案)。
//
// 用法:入口程式 `import _ "omixor-core/modules/integrations"` 才會編進去(見 core/module);沒 import 時
// /api/orgs/{org}/integrations/github|gitlab 不存在(404),功能清單沒有 development.repos(開發進度不顯示 Repositories 分頁)。
//
// GitHub、GitLab 做成一個模組(不拆兩個):兩者共用設定頁(設定 › 程式碼倉庫)、同一組加減清單的規則與鎖、同一張 PG 表,
// web 的同步層(本機舊設定匯入、背景讀核心)也是同一份;拆開沒有實際好處,只會多一倍的註冊與轉接。
//
// 內容:
//
//	model.go    設定的資料格式與正規化
//	service.go  規則(權限、加減清單、匯入);存在核心 store 的通用組織設定文件(kind = github、gitlab)
//	module.go   註冊(功能、API 路由)
//	backend.go  給 web 這類用戶端:Local(同一個程式裡的核心)、Remote(連線上的 server)
//	client.go   HTTP client
package integrations

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/module"
)

// ModuleID 模組代號
const ModuleID = "integrations"

// Features 模組擁有的功能(登記到 model.Features)。
//
// development.repos:開發進度 › Repositories,組織追蹤的 GitHub repo 與 GitLab 專案(用標籤標平台),點進去看 PR/MR 與
// Actions/Pipeline(唯讀,用各裝置的 token)。原本的 GitHub 分頁(development.github)與 web 獨有的 GitLab 分頁併進來;
// 舊的 development.github 設定讀的時候當作 development.repos 的值(見 model.LegacyFeatureKeys)
var Features = []model.Feature{
	{Key: "development.repos", Parent: "development", Name: "Repositories", Description: "追蹤中的 GitHub repo 與 GitLab 專案:PR/MR 與 Actions/Pipeline(唯讀,用各裝置的 token)", Default: true},
}

func init() {
	module.Register(module.Module{
		ID:          ModuleID,
		Name:        "程式碼倉庫(GitHub、GitLab)",
		Description: "組織追蹤的 GitHub repo、GitLab 網址與專案(只有非機密設定;token 留在各裝置)",
		Features:    Features,
		Routes:      routes,
	})
}

// routes 組織的 GitHub、GitLab 設定(原本在 core/api/integration.go)。
// 只有非機密設定:token 留在各裝置,讀 PR、Actions(GitLab 的 MR、Pipeline)也在各裝置,不經過這裡
//
//	GET  /api/orgs/{org}/integrations/github   GitHub 設定 {repos, saved, updatedAt?, updatedBy?}(成員;角色憑證也可以;沒存過 saved = false)
//	POST /api/orgs/{org}/integrations/github   {repos?, add?, remove?} 修改(組織管理員;repos 整份取代,再套用加減;格式不對 400)
//	POST /api/orgs/{org}/integrations/github/import {repos} 第一次匯入裝置的本機設定(組織管理員;核心存過就不動)→ {imported, skipped, settings}
//	GET  /api/orgs/{org}/integrations/gitlab   GitLab 設定 {baseUrl, projects, saved, updatedAt?, updatedBy?}
//	POST /api/orgs/{org}/integrations/gitlab   {baseUrl?, projects?, add?, remove?} 修改(組織管理員)
//	POST /api/orgs/{org}/integrations/gitlab/import {baseUrl, projects} 第一次匯入(同 GitHub)
func routes(mux *http.ServeMux, a module.API) {
	svc := For(a.Svc)
	gh := "/api/orgs/{org}/integrations/github"
	mux.HandleFunc("GET "+gh, a.AuthedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := svc.GetGitHubSettings(p, r.PathValue("org"))
		a.Reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gh, a.Authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in GitHubSettingsInput
		if err := a.Read(r, &in); err != nil {
			a.Bad(w, err)
			return
		}
		x, err := svc.SaveGitHubSettings(p, r.PathValue("org"), in)
		a.Reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gh+"/import", a.Authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in GitHubImport
		if err := a.Read(r, &in); err != nil {
			a.Bad(w, err)
			return
		}
		x, err := svc.ImportGitHubSettings(p, r.PathValue("org"), in)
		a.Reply(w, x, err)
	}))

	gl := "/api/orgs/{org}/integrations/gitlab"
	mux.HandleFunc("GET "+gl, a.AuthedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := svc.GetGitLabSettings(p, r.PathValue("org"))
		a.Reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gl, a.Authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in GitLabSettingsInput
		if err := a.Read(r, &in); err != nil {
			a.Bad(w, err)
			return
		}
		x, err := svc.SaveGitLabSettings(p, r.PathValue("org"), in)
		a.Reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gl+"/import", a.Authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in GitLabImport
		if err := a.Read(r, &in); err != nil {
			a.Bad(w, err)
			return
		}
		x, err := svc.ImportGitLabSettings(p, r.PathValue("org"), in)
		a.Reply(w, x, err)
	}))
}
