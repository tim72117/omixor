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
	if _, err := e.svc.CompleteOAuthEvoleap("id-b", "c", "wrong"); err != service.ErrOAuthEvoleapInvalid {
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
	if _, err := e.svc.CompleteOAuthEvoleap("id-b", "c", binding); err != service.ErrOAuthEvoleapInvalid {
		t.Fatal("重放:", err)
	}

	// 逾時
	_, binding, _ = e.svc.StartOAuthEvoleap(e.check(cl, "o"), oaChallenge(oaVerifier), "st", evoCB, false)
	*e.now = e.now.Add(service.OAuthEvoleapTTL + time.Second)
	if _, err := e.svc.CompleteOAuthEvoleap("id-c", "c", binding); err != service.ErrOAuthEvoleapInvalid {
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

func asOAuthErr(err error, target **service.OAuthError) bool {
	if oe, ok := err.(*service.OAuthError); ok {
		*target = oe
		return true
	}
	return false
}
