package web

import (
	"net/http"
	"strings"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
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
// 限制(見 docs/authorization.md):這是輸出層的篩選,不是授權。由 Issue、文件衍生的聚合(badges 的數字、issue_repos 的候選、助理摘要、
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

// scrubRestricted 把 JSON 值(core.Object 轉出來的)裡屬於受限專案的物件拿掉。
// 回傳 (新的值, 這個值本身是不是受限專案的物件)。物件的判斷:project 欄位是受限專案 id(Issue、文件、頻道綁定…);
// 專案物件本身 restricted = true;unknown 時任何有非空 project 欄位的物件、restricted = true 的物件都算
func scrubRestricted(v any, set map[string]bool, unknown bool) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if p, ok := x["project"].(string); ok && p != "" && (unknown || set[p]) {
			return nil, true
		}
		if b, ok := x["restricted"].(bool); ok && b {
			if id, _ := x["id"].(string); unknown || set[id] {
				return nil, true
			}
		}
		for k, y := range x {
			ny, drop := scrubRestricted(y, set, unknown)
			if drop {
				delete(x, k) // 欄位值本身是受限的物件(例如 {"project": {...}}):整個欄位拿掉
				continue
			}
			x[k] = ny
		}
		return x, false
	case []any:
		out := make([]any, 0, len(x))
		for _, y := range x {
			ny, drop := scrubRestricted(y, set, unknown)
			if !drop {
				out = append(out, ny)
			}
		}
		return out, false
	}
	return v, false
}

// restrictedArg 引數指到受限專案(project、或專案 action 的 id)
func restrictedArg(args map[string]any, set map[string]bool, unknown bool) bool {
	for _, k := range []string{"project"} {
		if p, ok := args[k].(string); ok && strings.TrimSpace(p) != "" && (unknown || set[strings.ToUpper(strings.TrimSpace(p))]) {
			return true
		}
	}
	return false
}

// notFoundRestricted 受限專案的資料對這位使用者「不存在」:404,訊息固定
func notFoundRestricted(w http.ResponseWriter) {
	writeJSON(w, 404, map[string]any{"error": "找不到"})
}

// restrictedTarget 引數的 id 指到受限專案裡的 Issue 或文件(寫入前先讀一次,不能等寫了再把結果拿掉):
// 工具 issue_update、doc_save 與 action 名稱以 issue_、cabinet_doc_ 開頭、帶 id 的。讀不到(不存在、出錯)不擋,交給原本的處理回錯誤
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
	case short == "doc_save" || strings.HasPrefix(short, "cabinet_doc_"):
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

// issueAPIHidden 這張 Issue(內部 id 或 key)屬於呼叫者看不到的受限專案
func issueAPIHidden(r *http.Request, app *core.App, org, id string) bool {
	set, unk, active := issueAPIRestricted(r, app, org)
	if !active {
		return false
	}
	hidden := false
	app.Mu.Lock()
	app.WithOrgIssues(org, func() {
		for _, i := range app.Issues() {
			if strings.EqualFold(i.ID, id) || strings.EqualFold(i.Key, id) {
				hidden = i.Project != "" && (unk || set[i.Project])
				return
			}
		}
	})
	app.Mu.Unlock()
	return hidden
}

// issueAPIProjectHidden body 的 project 指到呼叫者看不到的受限專案
func issueAPIProjectHidden(r *http.Request, app *core.App, org string, in map[string]any) bool {
	set, unk, active := issueAPIRestricted(r, app, org)
	return active && restrictedArg(in, set, unk)
}
