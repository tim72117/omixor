// Package httperr 把 error 轉成對外的「狀態碼 + 訊息 + 可選 data」,API、console 網頁、本機管理端點、MCP 共用同一份規則。
//
// 規則(由上往下,第一個符合的):
//
//	*service.Error                     → e.Code、e.Msg、e.Data(刻意給使用者看的,照給)
//	實作 Public 的型別                 → 型別自己的狀態碼(0 = 沒指定,由呼叫端決定)、自己的訊息、Data
//	                                     (console 的 StatusError / UserError、mcp.ToolError、client.Error、flowchart / wireframe 的 Error)
//	store.ErrInUse                     → 409,通用訊息(不夾帶外鍵錯誤的表名、欄位、值)
//	store.ErrBadRef                    → 400,通用訊息
//	store 的哨兵錯誤 → 固定句子(MsgNotFound 等),被 fmt.Errorf("…%w") 包在後面的原文(位置、鍵值、舊資料內容)不對外,只進 log:
//	                                     ErrNotFound → 404;ErrVersionConflict / ErrLocked → 409(版本衝突帶 Data.currentVersion);ErrUnmigratedRow → 503;ErrProjectNoKey → 400
//	                                     要刻意給人看特定文字(例如「找不到這個專案」),用 httperr.Human(err, 訊息),或在 service 層轉成 *service.Error,不要靠 fmt.Errorf 包 %w 夾帶文字
//	資料庫錯誤(*pgconn.PgError、連線錯誤、pgx.ErrNoRows)→ 500,通用訊息,原文進 log
//	逾時(context.DeadlineExceeded、net timeout)、檔案系統錯誤(*fs.PathError)、其他網路錯誤 → 500,通用訊息,原文進 log
//	其他未分類錯誤                     → 500;訊息看起來含內部細節(檔案路徑、SQL、SQLSTATE、dial tcp…)換成通用訊息,否則照 err.Error()
//
// 「原文進 log」:Report(op, err) 在訊息被換掉時,把原文連同操作名稱 op 寫進 log。
package httperr

import (
	"errors"
	"fmt"
	"log"

	"omixor-core/errmask"
	"omixor-core/service"
	"omixor-core/store"
)

// Public 刻意給使用者(或給 agent 看)的錯誤:訊息照 Error() 對外,不遮蔽。
// status 是它自己的 HTTP 狀態碼(0 = 沒指定,由呼叫端決定);data 是要一併回給呼叫端的欄位(沒有是 nil)。
type Public interface {
	error
	HTTPError() (status int, data map[string]any)
}

// Info 一個錯誤對外的樣子
type Info struct {
	Status int            // HTTP 狀態碼;0 = 錯誤型別沒有指定(只有 Public 會是 0)
	Msg    string         // 對外訊息
	Data   map[string]any // 要併進回應最上層的欄位(例如 409 的 currentVersion、taskCounts);沒有是 nil
	Masked bool           // 原文被換成通用訊息(原文只應進 log)
	// Cause Public 型別自己帶的內部原文(Masked 時 log 寫它,不寫 Public 的 Error();見 Causer);沒有是 nil
	Cause error
}

// Causer 實作 Public 的型別,對外訊息是固定句子、但另外帶著內部原文(連不到後端的位址、資料庫錯誤等):
// Cause() 不是 nil 時,Classify 回 Masked = true,原文只進 log。
type Causer interface {
	Cause() error
}

// 被換掉原文時對外的通用訊息
const (
	MsgDB       = errmask.MsgDB
	MsgTimeout  = errmask.MsgTimeout
	MsgInternal = errmask.MsgInternal
	MsgInUse    = "這筆資料還被其他資料使用中,不能刪除或修改(請先處理關聯的資料)"
	MsgBadRef   = "關聯的對象不存在(例如專案、角色、Issue 或任務已被刪除),請重新整理後再試"

	// 哨兵錯誤的固定句子(不論有沒有被包在別的錯誤後面,對外都是這一句)
	MsgNotFound        = "找不到指定的資料"
	MsgVersionConflict = "這筆資料已經被別人改過,請重新載入後再試"
	MsgLocked          = "這筆資料目前被鎖定(例如任務已被別人領取),請稍後再試"
	MsgUnmigratedRow   = "這筆資料的舊格式尚未遷移完成,暫時不能寫入,請洽管理員"
	MsgProjectNoKey    = "這個專案還沒設定代號,請先設定專案代號"
)

// humanError 刻意給人看的訊息,狀態碼與 data 沿用被包住的錯誤(例如 ErrNotFound 的 404)
type humanError struct {
	err error
	msg string
}

func (h *humanError) Error() string { return h.msg }
func (h *humanError) Unwrap() error { return h.err }
func (h *humanError) HTTPError() (int, map[string]any) {
	in := Classify(h.err)
	return in.Status, in.Data
}

// Human 包一個刻意給人看的訊息(對外照 msg 給,不遮蔽);狀態碼與 data 沿用 err(例如 store.ErrNotFound → 404、版本衝突的 currentVersion)。
// err 仍可用 errors.Is / errors.As 辨認;err 本身不會對外(要記 log 的話呼叫端自己記)。
func Human(err error, msg string) error { return &humanError{err: err, msg: msg} }

// Logf 寫 log 的函式(測試可以換掉)
var Logf = log.Printf

// Classify 錯誤對外的狀態碼、訊息、data(沒有 log;要 log 用 Report)。err 是 nil 回零值。
func Classify(err error) Info { return classify(err, true) }

// ClassifyDetail 同 Classify,但未分類錯誤不用文字比對遮蔽(給管理員診斷用,例如測試 LDAP 連線要看到 dial tcp 的原因);
// 型別明確的資料庫錯誤、逾時、檔案系統錯誤仍然遮蔽。
func ClassifyDetail(err error) Info { return classify(err, false) }

// Report 同 Classify,訊息被換掉時把原文連同操作名稱 op 寫進 log
func Report(op string, err error) Info { return report(op, err, Classify(err)) }

// ReportDetail 同 ClassifyDetail,訊息被換掉時寫 log
func ReportDetail(op string, err error) Info { return report(op, err, ClassifyDetail(err)) }

func report(op string, err error, in Info) Info {
	if in.Masked {
		LogMasked(op, causeOr(in, err))
	}
	return in
}

// LogMasked 寫「訊息被換掉,原文只進 log」的那一行:原文去掉換行與控制字元(防 log 注入)、同一種錯誤節流。
// 各出口自己遮蔽訊息(不走 Report)時也用它。
func LogMasked(op string, err error) {
	text := err.Error()
	if len(text) > 4*maxLineLen { // 超大的訊息先砍,不讓雜湊與換行處理吃到整份
		text = text[:4*maxLineLen]
	}
	line := truncateRunes(oneLine(text), maxLineLen)
	op = truncateRunes(oneLine(op), maxOpLen)
	ok, n, skipped := allow(throttleKey(op, line))
	if !ok {
		return
	}
	tail := ""
	if n > 0 {
		tail += fmt.Sprintf("(前一段時間另有 %d 次相同錯誤未重複記錄)", n)
	}
	if skipped > 0 {
		tail += fmt.Sprintf("(log 太密,已略過 %d 筆)", skipped)
	}
	Logf("錯誤原文(對外已改成通用訊息)[%s]:%s%s", op, line, tail)
}

func causeOr(in Info, err error) error {
	if in.Cause != nil {
		return in.Cause
	}
	return err
}

// Body 回應的 JSON:{"error": 訊息, ...Data};Data 裡的 error、result 不會蓋掉
func (in Info) Body() map[string]any {
	body := map[string]any{"error": in.Msg}
	for k, v := range in.Data {
		if k != "error" && k != "result" {
			body[k] = v
		}
	}
	return body
}

// StatusOr 錯誤型別沒指定狀態碼(0)時用 def
func (in Info) StatusOr(def int) int {
	if in.Status > 0 {
		return in.Status
	}
	return def
}

func classify(err error, heuristic bool) Info {
	if err == nil {
		return Info{}
	}
	var se *service.Error
	if errors.As(err, &se) {
		return Info{Status: se.Code, Msg: se.Msg, Data: se.Data}
	}
	var pe Public
	if errors.As(err, &pe) {
		st, data := pe.HTTPError()
		in := Info{Status: st, Msg: pe.Error(), Data: data}
		if c, ok := pe.(Causer); ok && c.Cause() != nil {
			in.Masked, in.Cause = true, c.Cause()
		}
		return in
	}
	switch {
	case errors.Is(err, store.ErrInUse):
		return Info{Status: 409, Msg: MsgInUse, Masked: true}
	case errors.Is(err, store.ErrBadRef):
		return Info{Status: 400, Msg: MsgBadRef, Masked: true}
	case errors.Is(err, store.ErrProjectNoKey):
		return sentinel(err, store.ErrProjectNoKey, 400, MsgProjectNoKey, nil)
	case errors.Is(err, store.ErrNotFound):
		return sentinel(err, store.ErrNotFound, 404, MsgNotFound, nil)
	case errors.Is(err, store.ErrVersionConflict):
		var data map[string]any
		var vc *store.VersionConflictError
		if errors.As(err, &vc) {
			data = map[string]any{"currentVersion": vc.Current}
		}
		return sentinel(err, store.ErrVersionConflict, 409, MsgVersionConflict, data)
	case errors.Is(err, store.ErrLocked):
		return sentinel(err, store.ErrLocked, 409, MsgLocked, nil)
	case errors.Is(err, store.ErrUnmigratedRow):
		return sentinel(err, store.ErrUnmigratedRow, 503, MsgUnmigratedRow, nil)
	}
	if msg, ok := errmask.Mask(err, heuristic); ok {
		return Info{Status: 500, Msg: msg, Masked: true}
	}
	return Info{Status: 500, Msg: err.Error()}
}

// sentinel 哨兵錯誤一律回固定句子;err 比哨兵本身多了文字(被 %w 包住、夾帶位置或內部原文)時標記 Masked,原文只進 log
func sentinel(err, base error, status int, msg string, data map[string]any) Info {
	var vc *store.VersionConflictError
	bare := err.Error() == base.Error() || (errors.As(err, &vc) && err.Error() == vc.Error())
	return Info{Status: status, Msg: msg, Data: data, Masked: !bare}
}
