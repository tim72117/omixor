package oauth

import (
	"errors"
	"net/http"
	"strings"

	"omixor-core/service"
)

// LDAPProvider 用目錄(LDAP / AD)帳號密碼證明身分。密碼驗證、每來源頻率限制、帳號失敗鎖定、稽核與對應核心使用者
// (ldap:<帳號>)和 POST /api/auth/ldap 是同一套(service.ProveLDAP);通過後先顯示確認頁,按允許才簽發授權碼
// (POST /oauth/ldap/confirm)。同意畫面的 CSRF 綁定與跨站拒絕由 oauth 層處理。密碼只傳給目錄,不寫進任何紀錄
type LDAPProvider struct {
	Svc    *service.Service
	Public string // 對外網址(不含結尾斜線)
	// Settings 目前的目錄設定與是否啟用;nil = 用 service 啟動時的設定(伺服器)。宿主的設定可以在執行中改(console)
	Settings func() (service.LDAPSettings, bool)
}

const (
	ldapUserField = "ldap_username"
	ldapPassField = "ldap_password"
	ldapKind      = "ldap-confirm"
)

func (p *LDAPProvider) settings() (service.LDAPSettings, bool) {
	if p.Settings != nil {
		set, ok := p.Settings()
		return set, ok && set.Dir != nil
	}
	set := p.Svc.LDAPSettings()
	return set, set.Dir != nil
}

func (p *LDAPProvider) ID() string    { return "ldap" }
func (p *LDAPProvider) Label() string { return "LDAP" }
func (p *LDAPProvider) Fields() []Field {
	return []Field{
		{Name: ldapUserField, Kind: "text", Label: "LDAP 帳號", MaxLen: 256},
		{Name: ldapPassField, Kind: "password", Label: "密碼", MaxLen: 1024},
	}
}
func (p *LDAPProvider) FormActions() []string { return []string{"via=ldap"} }
func (p *LDAPProvider) Present() Presentation {
	return Presentation{Button: "用 LDAP 帳號登入", SoloButton: "登入", Summary: "用 LDAP 帳號登入"}
}
func (p *LDAPProvider) Callback() http.Handler { return nil }

// Enabled 目錄登入有啟用才在同意畫面顯示
func (p *LDAPProvider) Enabled() bool {
	_, ok := p.settings()
	return ok
}

// hiddenWhenDisabled 沒啟用時 pickProvider 也要當它不存在(沒有 LDAP 的伺服器行為和以前完全一樣)
func (p *LDAPProvider) hiddenWhenDisabled() {}

// Start 驗證帳號密碼;通過回確認頁,帳密或帳號的問題重新顯示同意畫面
func (p *LDAPProvider) Start(w http.ResponseWriter, r *http.Request, t Txn) (Outcome, error) {
	d := detail(r)
	d["via"] = "ldap"
	set, ok := p.settings()
	if !ok {
		return Outcome{}, &service.OAuthError{Status: 400, Code: "invalid_request", Desc: "這個伺服器沒有啟用 LDAP 登入"}
	}
	username := strings.TrimSpace(t.Form.Get(ldapUserField))
	password := t.Form.Get(ldapPassField)
	if username == "" || password == "" || len(username) > 256 || len(password) > 1024 || strings.ContainsRune(username, 0) {
		d["pairing_result"] = "ldap_missing"
		return Outcome{Retry: "請輸入 LDAP 帳號與密碼", RetryStatus: 400}, nil
	}
	u, err := p.Svc.ProveLDAP(set, username, password, service.ClientIP(r))
	if err != nil {
		var e *service.Error
		if !errors.As(err, &e) {
			return Outcome{}, err
		}
		d["pairing_result"] = "ldap_failed"
		if e.Code == 429 {
			return Outcome{}, &service.OAuthError{Status: 429, Code: "temporarily_unavailable", Desc: e.Msg, Reason: "locked"}
		}
		return Outcome{Retry: e.Msg, RetryStatus: e.Code}, nil
	}
	page, err := p.Svc.StartOAuthConfirm(ldapKind, t.Checked, t.Req.CodeChallenge, t.Req.State, t.Grant, browserSecret(r), u)
	if err != nil {
		return Outcome{}, err
	}
	d["pairing_result"] = "ldap_confirm"
	return Outcome{Confirm: page}, nil
}

// Confirm 確認頁送出(POST /oauth/ldap/confirm);沒啟用時 404
func (p *LDAPProvider) Confirm() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.Enabled() {
			http.NotFound(w, r)
			return
		}
		detail(r)["via"] = "ldap"
		token, allow, ok := confirmForm(w, r, p.Public)
		if !ok {
			return
		}
		res, err := p.Svc.FinishOAuthConfirm(ldapKind, token, browserSecret(r), allow, map[string]any{"via": "ldap"})
		if err != nil {
			confirmFailed(w, r, res, err)
			return
		}
		detail(r)["pairing_result"] = "ok"
		redirectTo(w, res.RedirectURI, "code", res.Code, "state", res.State)
	})
}
