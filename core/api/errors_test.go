package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/client"
	"omixor-core/httperr"
	"omixor-core/httperr/errscan"
	"omixor-core/service"
	"omixor-core/store"
)

func captureErrLog(t *testing.T) *strings.Builder {
	var sb strings.Builder
	old := httperr.Logf
	httperr.Logf = func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	t.Cleanup(func() { httperr.Logf = old })
	return &sb
}

func doReply(err error) (int, string) {
	req := httptest.NewRequest("POST", "/api/agents/x", nil)
	req.Pattern = "POST /api/agents/{id}"
	rec := httptest.NewRecorder()
	reply(&opWriter{ResponseWriter: rec, r: req}, nil, err)
	return rec.Code, rec.Body.String()
}

// 資料庫錯誤、逾時:對外不含表名、欄位、SQL、SQLSTATE;原文與操作名稱進 log
func TestReplyMasksInternalErrors(t *testing.T) {
	pg := &pgconn.PgError{Code: "23503", TableName: "agent_tasks", ColumnName: "agent_id",
		Message: `insert or update on table "agent_tasks" violates foreign key constraint "agent_tasks_agent_id_fkey"`}
	for name, err := range map[string]error{
		"pg":       pg,
		"pg 包一層":   fmt.Errorf("存任務:%w", pg),
		"deadline": fmt.Errorf("SELECT id FROM agent_tasks: %w", context.DeadlineExceeded),
		"sql 文字":   errors.New(`INSERT INTO agent_tasks (agent_id) VALUES ($1) (SQLSTATE 23505)`),
	} {
		t.Run(name, func(t *testing.T) {
			lg := captureErrLog(t)
			code, body := doReply(err)
			if code != 500 {
				t.Errorf("狀態碼 %d", code)
			}
			for _, w := range []string{"agent_tasks", "agent_id", "SQLSTATE", "23505", "23503", "violates", "INSERT", "SELECT", "deadline"} {
				if strings.Contains(body, w) {
					t.Errorf("回應含 %q:%s", w, body)
				}
			}
			if !strings.Contains(lg.String(), "POST /api/agents/{id}") || !strings.Contains(lg.String(), err.Error()) {
				t.Errorf("log 要有操作名稱與原文:%s", lg.String())
			}
		})
	}
}

// 既有的對外行為不變:狀態碼、訊息、data
func TestReplyKeepsStatusMessageData(t *testing.T) {
	cases := []struct {
		err    error
		code   int
		substr []string
	}{
		{&service.Error{Code: 403, Msg: "不是成員"}, 403, []string{`"error":"不是成員"`}},
		{&service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": "v7"}}, 409,
			[]string{`"error":"結構已被別人更新,請重新載入"`, `"currentVersion":"v7"`}},
		{&service.Error{Code: 409, Msg: "名下還有任務", Data: map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}}, 409,
			[]string{`"code":"agentHasTasks"`, `"taskCounts":{"open":2}`}},
		{fmt.Errorf("x: %w", store.ErrInUse), 409, []string{httperr.MsgInUse}},
		{store.ErrBadRef, 400, []string{httperr.MsgBadRef}},
		{errors.New("要伺服器管理員"), 500, []string{`"error":"要伺服器管理員"`}},
	}
	for _, c := range cases {
		captureErrLog(t)
		code, body := doReply(c.err)
		if code != c.code {
			t.Errorf("%v:狀態碼 %d,要 %d", c.err, code, c.code)
		}
		for _, s := range c.substr {
			if !strings.Contains(body, s) {
				t.Errorf("%v:回應 %s 缺 %s", c.err, body, s)
			}
		}
	}
}

// 守門:出口不能直接把 err.Error() 寫進回應(要走 httperr)
func TestNoRawErrorInResponses(t *testing.T) {
	// 白名單: 只回解析器訊息(JSON 語法、欄位型別),沒有內部細節
	bad, err := errscan.Scan(".", map[string]bool{"api.go:bad": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把 err.Error() 寫進回應(請改用 reply / httperr):%s", b)
	}
}

var _ http.ResponseWriter = (*opWriter)(nil)

type noStatus struct{}

func (noStatus) Error() string                    { return "給人看的說明" }
func (noStatus) HTTPError() (int, map[string]any) { return 0, nil }

// Public 型別沒指定狀態碼(0)時不能 WriteHeader(0)(會 panic):用 500
func TestReplyPublicWithoutStatus(t *testing.T) {
	captureErrLog(t)
	for name, err := range map[string]error{
		"client.Error 連不上": &client.Error{Msg: "x"},
		"沒有狀態碼的 Public":    noStatus{},
		"包起來的 Public":      fmt.Errorf("外層:%w", noStatus{}),
	} {
		t.Run(name, func(t *testing.T) {
			code, body := doReply(err) // 修好之前:panic: invalid WriteHeader code 0
			if code != 500 || !strings.Contains(body, `"error"`) {
				t.Errorf("要 500 與 error 欄位:%d %s", code, body)
			}
		})
	}
	// 有狀態碼的照給
	if code, _ := doReply(&client.Error{Status: 404, Msg: "找不到"}); code != 404 {
		t.Errorf("狀態碼 %d", code)
	}
}

// 連不上後端:對外只有固定句子,後端位址與 dial tcp 資訊只進 log
func TestReplyMasksClientConnectError(t *testing.T) {
	lg := captureErrLog(t)
	httperr.ResetThrottle()
	c := client.New("http://10.9.8.7:8080")
	c.HTTP = &http.Client{Transport: failTransport{}}
	err := c.Do("GET", "/api/x", nil, nil)
	code, body := doReply(err)
	if code != 500 || strings.Contains(body, "10.9.8.7") || strings.Contains(body, "dial tcp") {
		t.Errorf("回應不能含後端位址:%d %s", code, body)
	}
	if !strings.Contains(lg.String(), "10.9.8.7") {
		t.Errorf("原文要進 log:%s", lg.String())
	}
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp 10.9.8.7:8080: connect: connection refused")
}

// ISS-34:Issue 版本衝突 409 的 Data(current、currentVersion)併進回應最上層
func TestReplyIssueVersionConflictData(t *testing.T) {
	captureErrLog(t)
	code, body := doReply(&service.Error{Code: 409, Msg: "Issue 已被別人更新", Data: map[string]any{
		"currentVersion": 7, "current": map[string]any{"id": "x", "title": "t"}}})
	if code != 409 || !strings.Contains(body, `"currentVersion":7`) || !strings.Contains(body, `"current":{"id":"x"`) {
		t.Errorf("%d %s", code, body)
	}
}
