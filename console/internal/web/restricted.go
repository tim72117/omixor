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
// 誰看得到只有核心的一條規則(專案成員、組織管理員、伺服器管理員;omixor-core 的 service/project_access.go)。畫面用登入者本人的身分
// 問核心他看不到哪些受限專案(core/restricted_visibility.go),再把那些專案的資料從系統身分讀的快取裡拿掉:
//   - 不用篩:沒啟用登入的本機使用者、web 管理員(管理員帳號名單,對核心是伺服器管理員)、API token
//   - 登入者:看不到的受限專案,view、action、tool 的輸出把屬於它的物件(帶 project 欄位的 Issue、文件,專案本身)拿掉;
//     單筆回和不存在一樣的 404。寫入以登入者本人執行,能不能寫由核心決定
//   - 分不出是哪位使用者(防呆):受限專案全部擋掉,引數指到受限專案的在執行前就回 404
//   - 查不出來(核心連不上、對不到身分):當作「專案都不能看」,帶 project 的物件全部拿掉,指到專案的引數回 404
//
// 沒有 project 欄位的衍生資料(動態牆的文字、徽章與清單的 total、counts、看板標籤、角色任務、待決定、用過的 repo)
// 在算出來之前用 core.Hidden 篩掉(core/restricted_hidden.go):view 經 core.ViewFor、action 經 args[core.HiddenKey]、
// 工具經 mcp.RunHidden。新增會彙整 Issue、文件內容的 view、action、工具時要一併接上;用系統身分讀單筆的 action
// 也要用 core.HiddenOf 擋(或改用呼叫者身分讀)。
//
// 附件是整個組織共用、不分專案(核心 service/attachments.go),拿得到網址(內容雜湊)的組織成員都讀得到。

// restrictedSetFor 這個請求看不到的受限專案 id;active = false 不必篩(管理員、沒有看不到的專案)。
// unknown = 查不出來(要當作所有專案都擋)
func restrictedSetFor(r *http.Request, app *core.App, au *auth.Service, org string) (set map[string]bool, unknown, active bool) {
	if isAdmin(r, app, au) {
		return nil, false, false
	}
	if org == "" {
		org = app.CurrentOrgNow()
	}
	set, unknown = app.HiddenProjects(org, callerOf(r, au))
	return set, unknown, unknown || len(set) > 0
}

// notFoundRestricted 受限專案的資料對這位使用者「不存在」:404,訊息固定
func notFoundRestricted(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]any{"error": "找不到"})
}

// gateRestricted 執行前就擋掉指到受限專案的引數與目標:只在核心擋不了的時候(分不出呼叫者、查不出看得到哪些)。
// 分得出呼叫者時交給核心,回的錯誤才會和「不存在」一模一樣
func gateRestricted(app *core.App, name string, args map[string]any, set map[string]bool, unknown bool, who *core.Caller) bool {
	if who != nil && !unknown {
		return false
	}
	return core.RestrictedArg(args, set, unknown) || restrictedTarget(app, name, args, set, unknown)
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
	_, drop := core.ScrubRestricted(core.Object(out), set, unknown)
	return drop
}

// issueAPIRestricted /api/issues/… 的篩選:有登入而且不是 web 管理員才篩(API token、本機沒有登入者視同管理員)
func issueAPIRestricted(r *http.Request, app *core.App, au *auth.Service, org string) (set map[string]bool, unknown, active bool) {
	u, ok := ctxUser(r)
	if !ok || u.Admin {
		return nil, false, false
	}
	set, unknown = app.HiddenProjects(org, callerOf(r, au))
	return set, unknown, unknown || len(set) > 0
}

// issueAPITarget 把路徑上的編號(內部 id、目前或舊的 key)解析成內部 id,並判斷呼叫者能不能看。
// 一律向核心查(快取只認得目前的 key,也可能還沒看到剛搬進受限專案);之後的動作要用回傳的 id。
// hidden = 屬於看不到的受限專案;err = 查不出來(不能當作看得到)。找不到的照原本的編號交給後面回 404
func issueAPITarget(r *http.Request, app *core.App, au *auth.Service, org, ref string) (id string, hidden bool, err error) {
	set, unk, active := issueAPIRestricted(r, app, au, org)
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
func issueAPIResultHidden(r *http.Request, app *core.App, au *auth.Service, org string, i model.Issue) bool {
	set, unk, active := issueAPIRestricted(r, app, au, org)
	return active && i.Project != "" && (unk || set[i.Project])
}

// issueAPIProjectGate body 的 project 指到受限專案,而且核心擋不了(同 gateRestricted)
func issueAPIProjectGate(r *http.Request, app *core.App, au *auth.Service, org string, in map[string]any) bool {
	set, unk, active := issueAPIRestricted(r, app, au, org)
	return active && (unk || callerOf(r, au) == nil) && core.RestrictedArg(in, set, unk)
}
