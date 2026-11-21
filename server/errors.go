package server

import (
	"net/http"

	"omixor-core/httperr"
)

// 本機管理端點(/local/…)的錯誤輸出:訊息與 data 取自 httperr(和 core/api 同一份規則),
// 資料庫錯誤等內部細節換成通用訊息,原文連同操作名稱進 log;狀態碼由呼叫端給(維持原樣)。

func opOf(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return r.Method + " " + r.URL.Path
}

// localFail 以 status 回錯誤:{"error": 訊息, ...data};extra 是一併回的欄位(例如匯入的 result)
func localFail(w http.ResponseWriter, r *http.Request, status int, err error, extra map[string]any) {
	body := httperr.Report(opOf(r), err).Body()
	for k, v := range extra {
		body[k] = v
	}
	writeLocal(w, status, body)
}

// localBadFormat 請求內容不是合法的 JSON:400,訊息是解析器的說明,沒有內部細節
func localBadFormat(w http.ResponseWriter, err error) {
	writeLocal(w, 400, map[string]any{"error": "格式錯誤:" + err.Error()})
}
