package web

import (
	"net/http"

	"omixor-core/httperr"
)

// 錯誤的對外輸出集中在這裡:狀態碼、訊息、data 取自 httperr(和 core/api、MCP 同一份規則);
// 資料庫錯誤等內部細節換成通用訊息,原文連同操作名稱(路由樣式)進 log。
// 失敗一律回真的 HTTP 狀態碼(failStatus;內容 {error, …data} 不變);掃描測試(errors_scan_test.go)不讓其他地方直接把 err.Error() 寫進回應。

// opOf 這次請求的操作名稱(log 用)
func opOf(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return r.Method + " " + r.URL.Path
}

// writeErr 以 status 回錯誤:{"error": 訊息, ...data}
func writeErr(w http.ResponseWriter, r *http.Request, status int, err error) {
	writeJSON(w, status, httperr.Report(opOf(r), err).Body())
}

// writeErrPrefix 同 writeErr,訊息前面加一段說明;detail = true 時不用文字比對遮蔽(管理員診斷用,例如測試 LDAP 連線)
func writeErrPrefix(w http.ResponseWriter, r *http.Request, status int, prefix string, err error, detail bool) {
	in := httperr.Report(opOf(r), err)
	if detail {
		in = httperr.ReportDetail(opOf(r), err)
	}
	body := in.Body()
	body["error"] = prefix + in.Msg
	writeJSON(w, status, body)
}

// writeErrDetail 同 writeErr,但未分類錯誤照原訊息(管理員診斷用)
func writeErrDetail(w http.ResponseWriter, r *http.Request, status int, err error) {
	writeJSON(w, status, httperr.ReportDetail(opOf(r), err).Body())
}

// badRequest 請求內容讀不出來(JSON 語法、欄位型別、參數驗證):400,訊息就是解析 / 驗證的說明,沒有內部細節
func badRequest(w http.ResponseWriter, err error) {
	writeJSON(w, 400, map[string]any{"error": err.Error()})
}

// badFormat 同 badRequest,訊息前面加「格式錯誤:」
func badFormat(w http.ResponseWriter, err error) {
	writeJSON(w, 400, map[string]any{"error": "格式錯誤:" + err.Error()})
}

// failStatus 失敗的 HTTP 狀態碼(動作、工具、附件等出口;內容 {error, …data} 不變):
// 型別指定的用它(service.Error 的 Code、StatusError 的 Status);沒指定的 Public(UserError 等給人看的說明,例如輸入有問題)400;
// 其餘(內部錯誤、資料庫錯誤)500。MCP 的 JSON-RPC 協定層(/mcp)的錯誤另外在協定內容裡回 200,不走這裡。
func failStatus(err error) int { return httperr.Classify(err).StatusOr(400) }

// writeFail 以 failStatus 的狀態碼回錯誤
func writeFail(w http.ResponseWriter, r *http.Request, err error) {
	writeErr(w, r, failStatus(err), err)
}

// officeFail 虛擬辦公室的錯誤:驗證不過是 400(訊息是給呼叫者看的驗證說明),其他(找不到資料夾、places.json 讀不出來)是 503,
// 訊息經過 httperr(路徑等內部細節換成通用訊息、原文進 log)
func officeFail(w http.ResponseWriter, r *http.Request, err error) {
	if officeErrStatus(err) == 400 {
		badRequest(w, err)
		return
	}
	writeErr(w, r, 503, err)
}

// authLoginStatus 登入失敗的狀態碼:型別有指定的用它(被鎖 429 帶 Retry-After、LDAP 連不上 / 服務帳號錯誤等診斷類 503、存 session 失敗 500);
// 帳號不存在與密碼錯誤是同一個 ErrBadLogin(沒指定 → 401,同一句話);沒有啟用等其他給人看的說明 401;其餘內部錯誤照 httperr 的 500
func authLoginStatus(err error) int { return httperr.Classify(err).StatusOr(401) }
