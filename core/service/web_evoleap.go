package service

// web_evoleap.go 瀏覽器畫面(omixor server 的 /console)用 evoleap 登入,換一個 web session(websession.go)。
// 身分規則和 CLI 的 evoleap 登入完全相同(resolveEvoleapUser:要在使用者名單、不悄悄建立帳號、停用不能登入)。
//
// 登入交易:開始時伺服器產生瀏覽器綁定密鑰(呼叫端放進 HttpOnly 的綁定 cookie),只記它的雜湊與 evoleap 登入 id;
// 回呼要 id 與密鑰都對上才收,用一次(防登入 CSRF:網址上的 id 不能拿來當 cookie 值)。換到的 evoleap token 用完就撤銷。
// 交易只在記憶體(server 只支援單一實例,見 server/README.md);重開伺服器時進行中的登入要重來。
//
// 限額:開始與回呼分開計(LoginRate)。開始之後沒有完成登入的來源算失敗過,不能用保留給正常來源的名額。

import (
	"crypto/subtle"
	"strings"
	"sync"
	"time"

	"omixor-core/evoleap"
)

// webEvoleapName 登記在 evoleap 的名稱(核准的人看得到自己在核准什麼)
const webEvoleapName = "omixor 網頁版登入"

const (
	webLoginTxTTL = 10 * time.Minute
	webLoginTxMax = 10000 // 開始有限額,正常到不了;滿了淘汰最舊的
)

var errWebEvoleapInvalid = &Error{Code: 400, Msg: "登入已失效(逾時、重複使用,或不是在同一個瀏覽器開始的),請重新登入"}

type webLoginTx struct {
	bindHash string // 瀏覽器綁定密鑰的雜湊
	source   string // 開始登入的來源(限額的鍵)
	expires  time.Time
}

// webLoginTxs evoleap 登入 id → 進行中的交易
type webLoginTxs struct {
	mu sync.Mutex
	m  map[string]webLoginTx
}

func (w *webLoginTxs) put(now time.Time, id string, tx webLoginTx) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.m == nil {
		w.m = map[string]webLoginTx{}
	}
	if len(w.m) >= webLoginTxMax {
		oldest, at := "", time.Time{}
		for k, v := range w.m {
			if !now.Before(v.expires) {
				delete(w.m, k)
			} else if oldest == "" || v.expires.Before(at) {
				oldest, at = k, v.expires
			}
		}
		if len(w.m) >= webLoginTxMax {
			delete(w.m, oldest)
		}
	}
	w.m[id] = tx
}

// match 沒過期而且密鑰對得上的交易;use = true 時一併用掉。密鑰不對不動交易(不能讓別人弄壞進行中的登入)
func (w *webLoginTxs) match(now time.Time, id, binding string, use bool) (webLoginTx, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tx, ok := w.m[id]
	if !ok || !now.Before(tx.expires) || subtle.ConstantTimeCompare([]byte(tx.bindHash), []byte(oauthBindHash(binding))) != 1 {
		return webLoginTx{}, false
	}
	if use {
		delete(w.m, id)
	}
	return tx, true
}

// StartWebEvoleapLogin 開始網頁登入:回傳瀏覽器要導去的核准頁網址,與要放進綁定 cookie 的密鑰
func (s *Service) StartWebEvoleapLogin(callbackURL, ip string) (loginURL, binding string, err error) {
	if !s.OAuthEvoleapEnabled() {
		return "", "", fail(404, "這個伺服器沒有啟用 evoleap 登入")
	}
	source := limitKey(ip)
	per, total := s.loginMax()
	if _, ok := s.logins.webStart.take(s.now(), source, per, total); !ok {
		return "", "", fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	id, err := evoleap.Start(s.Evoleap, callbackURL, webEvoleapName)
	if err != nil {
		return "", "", fail(502, "暫時無法連到 evoleap:%v", err)
	}
	binding = RandID("", 32)
	now := s.now()
	s.webTx.put(now, id, webLoginTx{bindHash: oauthBindHash(binding), source: source, expires: now.Add(webLoginTxTTL)})
	return evoleap.ConsoleLoginURL(s.Evoleap, s.EvoleapConsole, id), binding, nil
}

// CompleteWebEvoleapLogin evoleap 核准後的回呼:binding 是綁定 cookie 的值。成功回傳 web session(有效 ttl);
// used = 這次登入的交易已經用掉(成功失敗都算,呼叫端要清掉綁定 cookie)
func (s *Service) CompleteWebEvoleapLogin(evoID, evoCode, binding, ip string, ttl time.Duration) (res *LoginResult, used bool, err error) {
	if !s.OAuthEvoleapEnabled() {
		return nil, false, fail(404, "這個伺服器沒有啟用 evoleap 登入")
	}
	if evoID == "" || evoCode == "" || binding == "" || len(evoID) > 200 || len(evoCode) > 2000 {
		return nil, false, errWebEvoleapInvalid
	}
	// 對不上的回呼在計限額之前就擋掉,灌這個網址佔不到名額
	if _, ok := s.webTx.match(s.now(), evoID, binding, false); !ok {
		return nil, false, errWebEvoleapInvalid
	}
	source := limitKey(ip)
	per, total := s.loginMax()
	window, ok := s.logins.webCallback.take(s.now(), source, per, total)
	if !ok {
		return nil, false, fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	tx, ok := s.webTx.match(s.now(), evoID, binding, true)
	if !ok { // 並行的另一個回呼先用掉了
		return nil, false, errWebEvoleapInvalid
	}
	tok, err := evoleap.Exchange(s.Evoleap, evoID, evoCode)
	if err != nil {
		return nil, true, errWebEvoleapInvalid
	}
	defer evoleap.Logout(s.Evoleap, tok) // 成功失敗都撤銷:這個 token 只用來問「你是誰」
	u, _, err := s.resolveEvoleapUser(tok)
	if err != nil {
		if err == errEvoleapDisabled {
			return nil, true, fail(403, "這個帳號已停用")
		}
		return nil, true, err
	}
	res, err = s.IssueWebSession(u.ID, strings.ToLower(u.Email), ttl)
	if err == nil {
		s.logins.webCallback.refund(window, source)
		s.logins.webStart.forgive(tx.source)
	}
	return res, true, err
}
