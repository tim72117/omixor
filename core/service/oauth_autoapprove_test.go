package service_test

import (
	"strings"
	"testing"

	"omixor-core/service"
)

// 自動核准(略過同意畫面)的 service 層:假的 evoleap,不連真的服務
func TestStartOAuthEvoleapAuto(t *testing.T) {
	e := newOAuthEnv(t)
	evo := newEvoStub(t)
	e.svc.Evoleap = evo.srv.URL
	cl := e.client()
	chk := e.check(cl, "o")

	e.svc.OAuthAutoApproveOff = true
	if _, _, err := e.svc.StartOAuthEvoleapAuto(chk, oaChallenge(oaVerifier), "s", evoCB); err == nil || evo.starts.Load() != 0 {
		t.Fatal("開關關掉不能自動開始")
	}
	e.svc.OAuthAutoApproveOff = false
	wr := *chk
	wr.WriteRequested = true
	if _, _, err := e.svc.StartOAuthEvoleapAuto(&wr, oaChallenge(oaVerifier), "s", evoCB); err == nil || evo.starts.Load() != 0 {
		t.Fatal("要求 mcp:write 不能自動開始")
	}
	loginURL, binding, err := e.svc.StartOAuthEvoleapAuto(chk, oaChallenge(oaVerifier), "st", evoCB)
	if err != nil || binding == "" || !strings.Contains(loginURL, "/login/cli?id=") {
		t.Fatal(loginURL, binding, err)
	}
	if _, err := e.svc.CompleteOAuthEvoleap("id-b", "c", "wrong"); err != service.ErrOAuthEvoleapInvalid {
		t.Fatal(err)
	}
	res, err := e.svc.CompleteOAuthEvoleap("id-b", "c", binding)
	if err != nil || res.Code == "" || res.State != "st" {
		t.Fatal(res, err)
	}
	if _, err := e.exchange(cl, res.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteOAuthEvoleap("id-b", "c", binding); err != service.ErrOAuthEvoleapInvalid {
		t.Fatal("重放:", err)
	}
}
