package mcp

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"

	"omixor-core/httperr"
)

// tools/call 結果的唯一組裝處(成功與失敗都走這裡,沒有別的組裝路徑;result_test.go 的靜態測試守住):
//   - structuredContent 一律是 JSON 物件;content[0].text 是同一個物件的 JSON 字串(規格建議的向下相容)
//   - 成功:工具回物件就原樣放;回陣列、字串、數字等非物件就包成 {"result": 值};nil(只回執)是 {}
//   - 失敗:isError = true,本體 {"error": {"code", "message", "status"[, "data"]}}
//   - 唯一例外:error.data 裡的大欄位(見 splitErrData)只放 structuredContent,text 的 error.data 只留小欄位與 "truncated": true,
//     避免 409 版本衝突的整份 Issue 在模型可見的文字裡重複一次
//   - _meta(omixor/org 等)照舊放在結果的 _meta

// 失敗結果裡 code 的預設值:ToolError.Data["code"] 沒給時,依狀態碼取(統一在這一張表)
var statusCodes = map[int]string{
	http.StatusBadRequest:            "invalid_argument",
	http.StatusUnauthorized:          "unauthorized",
	http.StatusForbidden:             "forbidden",
	http.StatusNotFound:              "not_found",
	http.StatusConflict:              "conflict",
	http.StatusRequestEntityTooLarge: "payload_too_large",
	http.StatusUnprocessableEntity:   "invalid_argument",
	http.StatusTooManyRequests:       "rate_limited",
	http.StatusInternalServerError:   "internal",
}

// StatusCode 狀態碼對應的預設錯誤代碼(穩定的機器碼);表裡沒有的:4xx 是 request_failed、其他是 internal
func StatusCode(status int) string {
	if c, ok := statusCodes[status]; ok {
		return c
	}
	if status >= 400 && status < 500 {
		return "request_failed"
	}
	return "internal"
}

// OKResult 成功的 tools/call 結果。marshal 是把值轉成 JSON 的函式(Server.Marshal);meta 非空時放進結果的 _meta。
// 物件原樣當 structuredContent,text 是同一份 JSON;非物件包成 {"result": v},nil 是 {}。
func OKResult(v any, marshal func(any) []byte, meta map[string]any) map[string]any {
	body := marshal(v)
	switch t := bytes.TrimSpace(body); {
	case string(t) == "null":
		body = []byte("{}")
	case len(t) == 0 || t[0] != '{':
		body = marshal(map[string]any{"result": v})
	}
	res := toolResult(body, body, false)
	if len(meta) > 0 {
		res["_meta"] = meta
	}
	return res
}

// errDataValueMax error.data 單一欄位的 JSON 超過這個位元組數,就不放進 text(只在 structuredContent)
const errDataValueMax = 300

// splitErrData 把 error.data 分成給 text 的小欄位(附 "truncated": true 標記)與完整的 data:
// key 是 current(409 版本衝突時對方的整份最新內容)一律算大欄位;沒有大欄位時 small 與 data 相同、不加標記。
func splitErrData(data map[string]any) (small map[string]any, large bool) {
	small = make(map[string]any, len(data)+1)
	for k, v := range data {
		if b, err := json.Marshal(v); k == "current" || err != nil || len(b) > errDataValueMax {
			large = true
			continue
		}
		small[k] = v
	}
	if large {
		small["truncated"] = true
	}
	return small, large
}

// ErrResult 失敗的 tools/call 結果:isError = true、{"error": {code, message, status[, data]}}。
// code 空就依 status 取預設;data 是額外欄位(例如 409 的 currentVersion),空就不放。
// data 有大欄位時 text 與 structuredContent 不同(見檔頭);data 序列化失敗就整個丟掉,只回 code、message、status。
func ErrResult(code, message string, status int, data map[string]any) map[string]any {
	if code == "" {
		code = StatusCode(status)
	}
	e := map[string]any{"code": code, "message": message, "status": status}
	if len(data) == 0 {
		return errBody(e, e)
	}
	full := map[string]any{"code": code, "message": message, "status": status, "data": data}
	small, large := splitErrData(data)
	if !large {
		return errBody(full, full)
	}
	text := map[string]any{"code": code, "message": message, "status": status, "data": small}
	return errBody(text, full)
}

// errBody 組失敗結果:text 與 structuredContent 各自是 {"error": e};任一個序列化失敗就退回不含 data 的最小本體
func errBody(text, structured map[string]any) map[string]any {
	tb, terr := json.Marshal(map[string]any{"error": text})
	sb, serr := json.Marshal(map[string]any{"error": structured})
	if terr != nil || serr != nil {
		bare := map[string]any{"code": text["code"], "message": text["message"], "status": text["status"]}
		tb, _ = json.Marshal(map[string]any{"error": bare}) // 只含字串與數字:不會失敗
		sb = tb
	}
	return toolResult(tb, sb, true)
}

// FailureResult 工具執行失敗的結果:訊息一律經過 httperr(資料庫錯誤等內部細節換成通用訊息,原文連同工具名稱 name 進 log);
// ToolError 是刻意給 agent 看的,訊息照給(它的 cause 進 log)。
// code 取 Data["code"](沒有就依狀態碼);沒狀態碼時,被遮蔽的內部錯誤是 500,其餘(參數錯誤)是 400。
func FailureResult(name string, err error) map[string]any {
	in := httperr.Report("mcp 工具 "+name, err)
	data := map[string]any{}
	maps.Copy(data, in.Data)
	delete(data, "error")
	delete(data, "status")
	code, _ := data["code"].(string)
	delete(data, "code")
	status := in.Status
	if status == 0 {
		status = http.StatusBadRequest
		if in.Masked {
			status = http.StatusInternalServerError
		}
	}
	return ErrResult(code, in.Msg, status, data)
}

// toolResult 結果本體:text 與 structuredContent 都必須是 JSON 物件;除了失敗結果的大欄位,兩者是同一份 JSON
func toolResult(text, structured []byte, isErr bool) map[string]any {
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"structuredContent": json.RawMessage(structured),
		"isError":           isErr,
	}
}
