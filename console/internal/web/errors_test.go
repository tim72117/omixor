package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/httperr"
	"omixor-core/httperr/errscan"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-web/internal/auth"
	"omixor-web/internal/core"
)

func captureWebLog(t *testing.T) *strings.Builder {
	var sb strings.Builder
	old := httperr.Logf
	httperr.Logf = func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	t.Cleanup(func() { httperr.Logf = old })
	return &sb
}

var webLeakWords = []string{"agent_tasks", "agent_id", "SQLSTATE", "23503", "23505", "violates", "INSERT", "SELECT", "deadline", "C:\\", "/var/lib"}

func init() {
	pg := &pgconn.PgError{Code: "23503", TableName: "agent_tasks", ColumnName: "agent_id",
		Message: `insert or update on table "agent_tasks" violates foreign key constraint "agent_tasks_agent_id_fkey"`}
	reg := func(name string, err error) {
		core.RegisterAction(name, func(a *core.App, args map[string]any) (any, error) { return nil, err })
	}
	reg("zz_err_pg", pg)
	reg("zz_err_deadline", fmt.Errorf("SELECT * FROM agent_tasks: %w", context.DeadlineExceeded))
	reg("zz_err_sql", errors.New("INSERT INTO agent_tasks (agent_id) VALUES ($1) (SQLSTATE 23505)"))
	reg("zz_err_path", errors.New(`讀不出 C:\omixor\data\x.json`))
	reg("zz_err_user", core.UserError{Message: "找不到任務 t_1"})
	reg("zz_err_status", core.StatusError{Status: 409, Message: "畫面顯示的組織已經不是目前的組織", Data: map[string]any{"code": "orgChanged", "currentOrg": "o2"}})
	reg("zz_err_wrapped_user", fmt.Errorf("外層:%w", core.UserError{Message: "內層訊息"}))
	reg("zz_err_wrapped_service", &service.Error{Code: 409, Msg: "名下還有任務", Data: map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}})
}

// 動作的錯誤出口:資料庫錯誤、逾時、路徑不外露,原文與操作名稱進 log;內部錯誤是 500(內容 {error} 不變,畫面看 error 欄位)
func TestActionErrorMasksInternalDetails(t *testing.T) {
	c, _ := newServer(t, nil)
	for _, name := range []string{"zz_err_pg", "zz_err_deadline", "zz_err_sql", "zz_err_path"} {
		lg := captureWebLog(t)
		code, body := c.do("POST", "/api/action/"+name, map[string]any{}, nil)
		if code != 500 {
			t.Errorf("%s:內部錯誤要 500,得到 %d", name, code)
		}
		msg, _ := body["error"].(string)
		if msg == "" {
			t.Fatalf("%s:沒有 error:%v", name, body)
		}
		for _, w := range webLeakWords {
			if strings.Contains(msg, w) {
				t.Errorf("%s:對外訊息含 %q:%s", name, w, msg)
			}
		}
		if !strings.Contains(lg.String(), "POST /api/action/{name}") {
			t.Errorf("%s:log 要有操作名稱:%s", name, lg.String())
		}
	}
	lg := captureWebLog(t)
	c.do("POST", "/api/action/zz_err_pg", map[string]any{}, nil)
	if !strings.Contains(lg.String(), "agent_tasks") {
		t.Errorf("log 要有原文:%s", lg.String())
	}
}

// 既有行為不變:UserError、StatusError(狀態碼與 data)
func TestActionErrorKeepsStatusMessageData(t *testing.T) {
	captureWebLog(t)
	c, _ := newServer(t, nil)
	code, b := c.do("POST", "/api/action/zz_err_user", map[string]any{}, nil)
	if code != 400 || b["error"] != "找不到任務 t_1" {
		t.Errorf("UserError:%d %v", code, b)
	}
	code, b = c.do("POST", "/api/action/zz_err_wrapped_user", map[string]any{}, nil)
	if code != 400 || b["error"] != "內層訊息" {
		t.Errorf("包起來的 UserError:%d %v", code, b)
	}
	code, b = c.do("POST", "/api/action/zz_err_status", map[string]any{}, nil)
	if code != 409 || b["error"] != "畫面顯示的組織已經不是目前的組織" || b["code"] != "orgChanged" || b["currentOrg"] != "o2" {
		t.Errorf("StatusError:%d %v", code, b)
	}
	// 核心的 409(service.Error):狀態碼是它的 Code,訊息與 data 照給
	code, b = c.do("POST", "/api/action/zz_err_wrapped_service", map[string]any{}, nil)
	if code != 409 || b["error"] != "名下還有任務" || b["code"] != "agentHasTasks" {
		t.Errorf("service.Error:%d %v", code, b)
	}
}

// 管理員診斷(測試 LDAP 連線)要看得到原因;一般登入看不到內部位址
func TestAuthErrorWriters(t *testing.T) {
	captureWebLog(t)
	err := errors.New("dial tcp 10.0.0.5:389: connect: connection refused")
	rec := newRec()
	writeErr(rec.w, rec.r, 200, err)
	if strings.Contains(rec.body(), "10.0.0.5") {
		t.Errorf("一般錯誤不能外露連線位址:%s", rec.body())
	}
	rec = newRec()
	writeErrPrefix(rec.w, rec.r, 200, "用測試帳號登入失敗,設定沒有存:", err, true)
	if !strings.Contains(rec.body(), "10.0.0.5") || !strings.Contains(rec.body(), "用測試帳號登入失敗") {
		t.Errorf("管理員診斷要看得到原因:%s", rec.body())
	}
}

// 守門:出口不能直接把 err.Error() 寫進回應(要走 errors.go 的 writeErr 等)
func TestNoRawErrorInResponses(t *testing.T) {
	// 白名單: 只回解析器 / 驗證的說明,沒有內部細節
	bad, err := errscan.Scan(".", map[string]bool{"errors.go:badRequest": true, "errors.go:badFormat": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把 err.Error() 寫進回應(請改用 writeErr / httperr):%s", b)
	}
}

type rec struct {
	w *httptest.ResponseRecorder
	r *http.Request
}

func newRec() rec {
	return rec{httptest.NewRecorder(), httptest.NewRequest("POST", "/api/auth/test", nil)}
}

func (x rec) body() string { return x.w.Body.String() }

// ISS-34:Issue 版本衝突 409 的 Data(current、currentVersion)從 writeErr 帶出去
func TestWriteErrCarriesVersionConflictData(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "/api/issues/x", nil)
	writeErr(rec, r, 409, &service.Error{Code: 409, Msg: "Issue 已被別人更新", Data: map[string]any{
		"currentVersion": 7, "current": map[string]any{"id": "x"}}})
	b := rec.Body.String()
	if rec.Code != 409 || !strings.Contains(b, `"currentVersion":7`) || !strings.Contains(b, `"current":{"id":"x"}`) {
		t.Errorf("%d %s", rec.Code, b)
	}
}

// 失敗要回真的 HTTP 狀態碼(內容 {error, …data} 不變):每個出口對每種錯誤型別的狀態碼
func TestFailureStatusTable(t *testing.T) {
	captureWebLog(t)
	cases := []struct {
		err  error
		want int
	}{
		{&service.Error{Code: 403, Msg: "x"}, 403},
		{&service.Error{Code: 409, Msg: "x", Data: map[string]any{"currentVersion": 3}}, 409},
		{core.StatusError{Status: 413, Message: "x"}, 413},
		{core.UserError{Message: "x"}, 400},
		{fmt.Errorf("包:%w", core.UserError{Message: "x"}), 400},
		{errors.New("一般錯誤"), 500},
		{&pgconn.PgError{Code: "23505"}, 500},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), 500},
		{store.ErrNotFound, 404},
		{store.ErrVersionConflict, 409},
		{store.ErrLocked, 409},
		{store.ErrInUse, 409},
		{store.ErrBadRef, 400},
	}
	for _, c := range cases {
		if got := failStatus(c.err); got != c.want {
			t.Errorf("%v:要 %d,得到 %d", c.err, c.want, got)
		}
	}
	if got := authLoginStatus(core.UserError{Message: "帳號或密碼錯誤"}); got != 401 {
		t.Errorf("登入失敗要 401:%d", got)
	}
	if got := authLoginStatus(&pgconn.PgError{Code: "23505"}); got != 500 {
		t.Errorf("登入時的內部錯誤要 500:%d", got)
	}
	if got := auth.StatusOf(core.UserError{Message: "x"}); got != 400 {
		t.Errorf("輸入問題要 400:%d", got)
	}
	if got := auth.StatusOf(errors.New("boom")); got != 500 {
		t.Errorf("內部錯誤要 500:%d", got)
	}
}
