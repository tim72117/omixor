package oauth

import (
	"net/http"
	"net/url"
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

// 同意畫面「證明你是誰」的擴充點。OAuth 的其餘部分(授權請求驗證、CSRF、PKCE、授權碼、token)對所有宿主都一樣,
// 只有「怎麼證明身分」因宿主而異:雲端是 evoleap 登入與配對碼;別的宿主(例如本機 console)可以加自己的登入方式,實作 IdentityProvider 傳給 NewWith 即可。
// 身分證明之後的共同部分(讀寫授權檢查、成員資格、授權碼、稽核)一律是 service.ApproveOAuthIdentity,provider 不能自己簽發授權碼(evoleap 的回呼是唯一例外,見 EvoleapProvider)。

// Txn 一次已經驗證過的同意畫面送出:授權請求(Checked / Req)、CSRF 已通過、使用者選的授權等級(Grant)與整個表單
type Txn struct {
	Checked *service.OAuthAuthChecked
	Req     service.OAuthAuthRequest
	Grant   string // service.OAuthGrantRead / Write / Admin;偽造的等級在 provider 被呼叫之前就已經擋掉
	Form    url.Values
}

// Identity 已證明的身分:User 是對上的 omixor 使用者,Extra 寫進稽核 oauth.authorize 的 detail(例如 via、pair)
type Identity struct {
	User  *model.User
	Extra map[string]any
	// Hint 身分已證明、但授權被拒(使用者不是任何組織的管理員)而重新顯示同意畫面時,接在訊息後面的提示(憑證已消耗時,告訴使用者要重新取得);可為空
	Hint string
}

// Outcome Start 的結果,三擇一:
//   - Done:身分已證明,由 oauth 層用 ApproveOAuthIdentity 簽發授權碼並導回客戶端
//   - Redirect(可附 Cookie):303 導去外部登入頁;之後由 provider 的 Callback 接手
//   - Retry:這次沒過(例如配對碼輸錯),用 RetryStatus(0 = 403)與這句話重新顯示同意畫面(換新的 CSRF)
type Outcome struct {
	Done        *Identity
	Redirect    string
	Cookie      *http.Cookie
	Retry       string
	RetryStatus int
}

// Field 同意畫面上 provider 要使用者填的欄位;Kind 是 text 或 password
type Field struct {
	Name, Label, Kind string
	Placeholder       string
	MaxLen            int  // 0 = 不限
	Caps              bool // 一次性代碼:自動大寫、關掉輸入法修正
}

// Presentation provider 在同意畫面上的文字(畫面是繁體中文,由 provider 自己提供)。
// 沒有欄位的 provider 是一顆主要按鈕(Button + Note);有欄位的 provider 前面還有別的按鈕時收在 <details>(Summary、Button),否則直接顯示(SoloButton)
type Presentation struct {
	Button, SoloButton, Note, Summary string
	// FormSources 送出後會 303 導去的外部來源(scheme://host),同意畫面的 CSP form-action 要允許;沒有就空
	FormSources []string
}

// IdentityProvider 一種證明身分的方式
type IdentityProvider interface {
	// ID 穩定的短名字(小寫英數),也是回呼路徑 /oauth/<id>/callback 與按鈕值
	ID() string
	// Label 給人看的名稱(例如「配對碼」)
	Label() string
	// Fields 同意畫面要使用者填的欄位;沒有欄位 = 純按鈕(例如導去外部登入)
	Fields() []Field
	// FormActions 這個 provider 擁有的送出按鈕,格式 name=value(例如 via=evoleap、action=allow);第一個用來畫按鈕。
	// 表單送出時,先看哪個 provider 的欄位有填,再看按下的按鈕屬於誰
	FormActions() []string
	// Present 同意畫面上的文字
	Present() Presentation
	// Start 處理同意畫面的送出(授權請求、CSRF、授權等級都已驗證);錯誤用 *service.OAuthError 表達(狀態碼與訊息照它顯示)
	Start(w http.ResponseWriter, r *http.Request, t Txn) (Outcome, error)
	// Callback 外部登入完成後瀏覽器導回的處理器,掛在 GET /oauth/<ID>/callback;沒有就回 nil
	Callback() http.Handler
}

// confirmer provider 可以選擇性地實作:Callback 只顯示確認頁,使用者按下允許送到 POST /oauth/<ID>/confirm 才簽發授權碼
type confirmer interface{ Confirm() http.Handler }

// enabledToggle provider 可以選擇性地實作:回 false 時同意畫面不顯示它(例如 evoleap 沒設網址或被關掉);沒實作 = 永遠顯示
type enabledToggle interface{ Enabled() bool }

func providerEnabled(p IdentityProvider) bool {
	t, ok := p.(enabledToggle)
	return !ok || t.Enabled()
}

// callbackURL provider 的回呼網址(evoleap 是 <public>/oauth/evoleap/callback)
func callbackURL(public, id string) string { return public + "/oauth/" + id + "/callback" }

// retryOutcome 這次沒過、要重新顯示同意畫面:把原因記進觀測欄位(result 是 pairing_result 的值)
func retryOutcome(r *http.Request, oe *service.OAuthError, result string) Outcome {
	d := detail(r)
	d["error"], d["pairing_result"] = oe.Code, result
	return Outcome{Retry: oe.Desc, RetryStatus: oe.Status}
}

// pickProvider 這次送出屬於哪個 provider:先看誰的欄位有填(在配對碼欄按 Enter 時,瀏覽器會用第一個送出鈕,但填了碼就是要用碼),
// 再看按下的按鈕屬於誰;都沒有就用第一個有欄位的(舊行為:不是 evoleap 一律當配對碼)
func pickProvider(ps []IdentityProvider, f url.Values) IdentityProvider {
	for _, p := range ps {
		for _, fl := range p.Fields() {
			if strings.TrimSpace(f.Get(fl.Name)) != "" {
				return p
			}
		}
	}
	for _, p := range ps {
		for _, a := range p.FormActions() {
			if k, v, _ := strings.Cut(a, "="); f.Get(k) == v {
				return p
			}
		}
	}
	for _, p := range ps {
		if len(p.Fields()) > 0 {
			return p
		}
	}
	return nil
}
