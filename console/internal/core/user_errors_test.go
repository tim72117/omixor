package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"omixor-core/httperr"
	"omixor-core/service"
)

func captureHttperrLog(t *testing.T) *strings.Builder {
	t.Helper()
	var sb strings.Builder
	old := httperr.Logf
	httperr.Logf = func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	httperr.ResetThrottle()
	t.Cleanup(func() { httperr.Logf = old; httperr.ResetThrottle() })
	return &sb
}

// 內部錯誤(資料庫、路徑、連線)不能被包成 UserError 原文給畫面;原文進 log
func TestFacingDoesNotWrapInternalErrors(t *testing.T) {
	lg := captureHttperrLog(t)
	for _, raw := range []string{
		`ERROR: duplicate key value violates unique constraint "ops_incidents_pkey" (SQLSTATE 23505)`,
		`open C:\Users\ops\data\incidents.json: Access is denied.`,
		`Post "https://graph.facebook.com/v21.0/me?access_token=EAAB": dial tcp 31.13.1.1:443: i/o timeout`,
	} {
		err := facing(errors.New(raw))
		var ue UserError
		if errors.As(err, &ue) {
			t.Errorf("內部錯誤不能包成 UserError:%s", raw)
		}
		in := httperr.Report("POST /api/action/x", err)
		if !in.Masked || strings.Contains(in.Msg, "SQLSTATE") || strings.Contains(in.Msg, "Users") || strings.Contains(in.Msg, "access_token") {
			t.Errorf("對外訊息要遮蔽:%+v", in)
		}
	}
	if !strings.Contains(lg.String(), "SQLSTATE 23505") || !strings.Contains(lg.String(), "Access is denied") {
		t.Errorf("原文要進 log:%s", lg.String())
	}
}

// 已知「給人看」的型別保留(service.Error 的狀態碼與訊息、UserError、FBError 的 Meta 說明)
func TestFacingKeepsKnownUserErrors(t *testing.T) {
	se := &service.Error{Code: 409, Msg: "這張事件已經被別人改過"}
	if got := facing(fmt.Errorf("外層:%w", se)); !errors.Is(got, se) {
		t.Errorf("service.Error 要保留:%v", got)
	}
	if in := httperr.Classify(facing(se)); in.Status != 409 || in.Msg != se.Msg {
		t.Errorf("%+v", in)
	}
	if got := facing(UserError{"註記不能是空的"}); got.Error() != "註記不能是空的" {
		t.Errorf("%v", got)
	}
	fb := FBError{Message: "Invalid OAuth access token.", Code: 190}
	got := facing(fb)
	if in := httperr.Classify(got); in.Masked || !strings.Contains(in.Msg, "Facebook") {
		t.Errorf("FBError 的說明要給人看:%+v", in)
	}
	// FBError 的文字若含內部細節(連線位址)仍遮蔽
	bad := FBError{Message: "HTTP 502:dial tcp 10.0.0.1:443: connection refused", Status: 502}
	if in := httperr.Classify(facing(bad)); !in.Masked {
		t.Errorf("含內部細節的 FBError 要遮蔽:%+v", in)
	}
	if facing(nil) != nil || fbUserErr(nil) != nil {
		t.Error("nil 要回 nil")
	}
}

func TestFacingPrefix(t *testing.T) {
	lg := captureHttperrLog(t)
	err := facingPrefix("jira_import", "Issue 沒有存起來:", errors.New(`pq: relation "ops_issues" does not exist (SQLSTATE 42P01)`))
	in := httperr.Classify(err)
	if !strings.HasPrefix(in.Msg, "Issue 沒有存起來:") || strings.Contains(in.Msg, "ops_issues") || strings.Contains(in.Msg, "SQLSTATE") {
		t.Fatalf("%+v", in)
	}
	if !strings.Contains(lg.String(), "ops_issues") {
		t.Error("原文要進 log")
	}
	// 驗證類(service.Error)保留狀態碼
	in = httperr.Classify(facingPrefix("jira_import", "Issue 沒有存起來:", &service.Error{Code: 409, Msg: "版本衝突"}))
	if in.Status != 409 || in.Msg != "Issue 沒有存起來:版本衝突" {
		t.Fatalf("%+v", in)
	}
}
