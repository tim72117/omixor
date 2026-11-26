package web

import (
	"net/http"
	"strings"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"

	"omixor-core/httperr"
	"omixor-core/model"
)

// 受限專案(ISS-34 第 3 階段)在 web 層的篩選。
//
// 核心依「呼叫者」篩選受限專案(只有專案成員、組織管理員看得到),但 web 對核心用同一個身分(本機是伺服器管理員、遠端是 web 共用的權杖),
// 分不出是哪位 web 使用者,核心的規則在這裡擋不住 web 的一般使用者(和 admin.go 說的是同一個問題)。所以:
//   - web 管理員(沒啟用登入時的本機使用者、auth.User.Admin)、API token:和核心的身分一樣,看得到全部
//   - 其他登入者:受限專案的資料整個擋掉 —— view、action、tool 的輸出把屬於受限專案的物件(帶 project 欄位的 Issue、文件,
//     restricted 的專案本身)拿掉;單筆就是受限專案的回 404;引數指到受限專案(project 參數)回 404,和不存在一樣
//   - 問不到哪些專案受限(核心連不上)時,對這些人一律當作「專案都不能看」:帶 project 的物件全部拿掉
//
// 限制(見 omixor 文件櫃 DOC-35《docs/authorization.md》):這是輸出層的篩選,不是授權。由 Issue、文件衍生的聚合(badges 的數字、issue_repos 的候選、助理摘要、
// 動態牆、頻道與任務的畫面)沒有逐一篩選;要真正依成員篩選,console 需要用每位使用者自己的身分呼叫核心(還沒做)。

// restrictedSetFor 這個請求要擋掉的受限專案 id;nil, false = 不必擋(管理員、沒有受限專案)。
// unknown = 問不到(要當作所有專案都擋)
func restrictedSetFor(r *http.Request, app *core.App, au *auth.Service, org string) (set map[string]bool, unknown, active bool) {
	if isAdmin(r, app, au) {
		return nil, false, false
	}
	var err error
	if org == "" {
		set, err = app.RestrictedProjectIDs()
	} else {
		set, err = app.RestrictedProjectIDsOf(org)
	}
	if err != nil {
		return nil, true, true
	}
	return set, false, len(set) > 0
}

// scrubRestricted、restrictedArg 規則在 core/console_policy.go(MCP 入口的專有工具共用同一套)
func scrubRestricted(v any, set map[string]bool, unknown bool) (any, bool) {
	return core.ScrubRestricted(v, set, unknown)
}

func restrictedArg(args map[string]any, set map[string]bool, unknown bool) bool {
	return core.RestrictedArg(args, set, unknown)
}

// notFoundRestricted 受限專案的資料對這位使用者「不存在」:404,訊息固定
func notFoundRestricted(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]any{"error": "找不到"})
}

// restrictedTarget 引數的 id 指到受限專案裡的 Issue 或文件(寫入前先讀一次,不能等寫了再把結果拿掉):
// 工具 issue_update、doc_save、docs 與 action 名稱以 issue_、cabinet_doc_ 開頭、帶 id 的。讀不到(不存在、出錯)不擋,交給原本的處理回錯誤
func restrictedTarget(app *core.App, name string, args map[string]any, set map[string]bool, unknown bool) bool {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return false
	}
	short := core.ShortName(name)
	read := ""
	switch {
	case short == "issue_update" || strings.HasPrefix(short, "issue_"):
		read = "issues"
	case short == "doc_save" || short == "docs" || strings.HasPrefix(short, "cabinet_doc_"): // docs 的分段讀取結果沒有 project 欄位
		read = "docs"
	default:
		return false
	}
	out, err := mcp.Run(app, read, map[string]any{"id": id}, core.Person(app.Me))
	if err != nil {
		return false
	}
	_, drop := scrubRestricted(core.Object(out), set, unknown)
	return drop
}

// issueAPIRestricted /api/issues/… 的篩選:有登入而且不是 web 管理員才篩(API token、本機沒有登入者視同管理員)
func issueAPIRestricted(r *http.Request, app *core.App, org string) (set map[string]bool, unknown, active bool) {
	u, ok := ctxUser(r)
	if !ok || u.Admin {
		return nil, false, false
	}
	var err error
	if set, err = app.RestrictedProjectIDsOf(org); err != nil {
		return nil, true, true
	}
	return set, false, len(set) > 0
}

// issueAPITarget 把路徑上的編號(內部 id、目前或舊的 key)解析成內部 id,並判斷呼叫者能不能看。
// 一律向核心查(快取只認得目前的 key,也可能還沒看到剛搬進受限專案);之後的動作要用回傳的 id。
// hidden = 屬於看不到的受限專案;err = 查不出來(不能當作看得到)。找不到的照原本的編號交給後面回 404
func issueAPITarget(r *http.Request, app *core.App, org, ref string) (id string, hidden bool, err error) {
	set, unk, active := issueAPIRestricted(r, app, org)
	if !active {
		return ref, false, nil
	}
	i, err := app.LookupIssue(org, ref)
	if err != nil {
		if httperr.Classify(err).Status == http.StatusNotFound {
			return ref, false, nil
		}
		return "", false, err
	}
	return i.ID, i.Project != "" && (unk || set[i.Project]), nil
}

// issueAPIResultHidden 要回給呼叫者的 Issue 屬於看不到的受限專案(寫入期間被別人搬進去)
func issueAPIResultHidden(r *http.Request, app *core.App, org string, i model.Issue) bool {
	set, unk, active := issueAPIRestricted(r, app, org)
	return active && i.Project != "" && (unk || set[i.Project])
}

// issueAPIProjectHidden body 的 project 指到呼叫者看不到的受限專案
func issueAPIProjectHidden(r *http.Request, app *core.App, org string, in map[string]any) bool {
	set, unk, active := issueAPIRestricted(r, app, org)
	return active && restrictedArg(in, set, unk)
}
