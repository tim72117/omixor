package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// OAuth 的 evoleap 登入(service 層):假的 evoleap(httptest),不連真的服務

type evoStub struct {
	srv                       *httptest.Server
	starts, exchanges, logout atomic.Int32
	me                        atomic.Value // map[string]any
}

func newEvoStub(t *testing.T) *evoStub {
	s := &evoStub{}
	s.me.Store(map[string]any{"id": "ev-1", "email": "boss@x", "google": true})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cli-auth/start", func(w http.ResponseWriter, r *http.Request) {
		n := s.starts.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "id-" + string(rune('a'+n))})
	})
	mux.HandleFunc("POST /api/cli-auth/exchange", func(w http.ResponseWriter, r *http.Request) {
		s.exchanges.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "evotok"})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user": s.me.Load()})
	})
	mux.HandleFunc("POST /api/cli-auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer evotok" {
			s.logout.Add(1)
		}
		_, _ = w.Write([]byte("{}"))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

const evoCB = "https://omixor.test/oauth/evoleap/callback"

func TestOAuthEvoleapService(t *testing.T) {
	e := newOAuthEnv(t)
	evo := newEvoStub(t)
	e.svc.Evoleap = evo.srv.URL
	cl := e.client()

	// 沒啟用 / 被關掉
	e.svc.Evoleap = ""
	if _, _, err := e.svc.StartOAuthEvoleap(e.check(cl, "o"), oaChallenge(oaVerifier), "s", evoCB, false); err == nil {
		t.Fatal("沒設 evoleap 不能開始")
	}
	e.svc.Evoleap = evo.srv.URL
	e.svc.OAuthEvoleapOff = true
	if e.svc.OAuthEvoleapEnabled() {
		t.Fatal("關掉了")
	}
	e.svc.OAuthEvoleapOff = false

	// 沒有 cookie(沒帶綁定密鑰):說明是瀏覽器端的原因;沒啟用 evoleap 也說明
	if _, err := e.svc.CompleteOAuthEvoleap("id-x", "c", ""); !evoInvalid(err, "evoleap_no_cookie", false) || !strings.Contains(err.Error(), "cookie") {
		t.Fatal("沒有 cookie:", err)
	}
	e.svc.OAuthEvoleapOff = true
	if _, err := e.svc.CompleteOAuthEvoleap("id-x", "c", "b"); !evoInvalid(err, "evoleap_disabled", false) {
		t.Fatal("沒啟用:", err)
	}
	e.svc.OAuthEvoleapOff = false

	// 客戶端沒要求 write 卻選讀寫:invalid_scope,不呼叫 evoleap
	_, _, err := e.svc.StartOAuthEvoleap(e.check(cl, "o"), oaChallenge(oaVerifier), "s", evoCB, true)
	var oe *service.OAuthError
	if !asOAuthErr(err, &oe) || oe.Code != "invalid_scope" || evo.starts.Load() != 0 {
		t.Fatal(err, evo.starts.Load())
	}

	// 完整流程,授權碼換得到 token
	loginURL, binding, err := e.svc.StartOAuthEvoleap(e.check(cl, "o"), oaChallenge(oaVerifier), "st", evoCB, false)
	if err != nil || binding == "" || !strings.HasSuffix(loginURL, "/login/cli?id=id-b") {
		t.Fatal(loginURL, binding, err)
	}
	if _, err := e.svc.CompleteOAuthEvoleap("id-b", "c", "wrong"); !evoInvalid(err, "evoleap_binding_mismatch", true) {
		t.Fatal("cookie 不對:", err)
	}
	res, err := e.svc.CompleteOAuthEvoleap("id-b", "c", binding)
	if err != nil || res.Code == "" || res.State != "st" || res.RedirectURI != oaCallback {
		t.Fatal(res, err)
	}
	if evo.logout.Load() != 1 {
		t.Fatal("要撤銷 evoleap token")
	}
	if _, err := e.exchange(cl, res.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteOAuthEvoleap("id-b", "c", binding); !evoInvalid(err, "evoleap_used", false) {
		t.Fatal("重放:", err)
	}

	// 逾時
	_, binding, _ = e.svc.StartOAuthEvoleap(e.check(cl, "o"), oaChallenge(oaVerifier), "st", evoCB, false)
	*e.now = e.now.Add(service.OAuthEvoleapTTL + time.Second)
	if _, err := e.svc.CompleteOAuthEvoleap("id-c", "c", binding); !evoInvalid(err, "evoleap_expired", false) {
		t.Fatal("逾時:", err)
	}
	if evo.exchanges.Load() != 1 {
		t.Fatal("逾時不能換 token")
	}

	// 停用的帳號:導回錯誤(res 有轉址),evoleap token 撤銷
	_, binding, _ = e.svc.StartOAuthEvoleap(e.check(cl, "o"), oaChallenge(oaVerifier), "st", evoCB, false)
	_ = e.svc.Store.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", EvoleapID: "ev-1", Disabled: true, CreatedAt: model.Now()})
	res, err = e.svc.CompleteOAuthEvoleap("id-d", "c", binding)
	if !asOAuthErr(err, &oe) || oe.Code != "access_denied" || oe.NoRedirect || res == nil || res.RedirectURI != oaCallback || res.Code != "" {
		t.Fatal(res, err)
	}
	if evo.logout.Load() != 2 {
		t.Fatal("停用也要撤銷")
	}
}

// evoInvalid 是 evoleap 回呼不被接受的錯誤(400 invalid_request、不導回),細分原因是 reason;
// generic = 還不能證明是同一個瀏覽器(cookie 不對、不認得的 id)時,給使用者的說明必須和通用那句一樣(不洩漏是哪一種)
func evoInvalid(err error, reason string, generic bool) bool {
	oe, ok := err.(*service.OAuthError)
	return ok && oe.Status == 400 && oe.Code == "invalid_request" && oe.NoRedirect && oe.Reason == reason &&
		(!generic || oe.Desc == service.ErrOAuthEvoleapInvalid.Desc)
}

func asOAuthErr(err error, target **service.OAuthError) bool {
	if oe, ok := err.(*service.OAuthError); ok {
		*target = oe
		return true
	}
	return false
}
