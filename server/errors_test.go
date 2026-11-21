package server

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/httperr"
	"omixor-core/httperr/errscan"
	"omixor-core/service"
)

func TestLocalFailMasksInternalDetails(t *testing.T) {
	pg := &pgconn.PgError{Code: "23505", TableName: "omixor_orgs", ConstraintName: "omixor_orgs_pkey",
		Message: `duplicate key value violates unique constraint "omixor_orgs_pkey"`}
	for name, err := range map[string]error{
		"pg":       pg,
		"pg 包一層":   fmt.Errorf("建立組織:%w", pg),
		"deadline": fmt.Errorf("SELECT * FROM omixor_orgs: %w", context.DeadlineExceeded),
		"sql 文字":   errors.New(`INSERT INTO omixor_orgs (id) VALUES ($1) (SQLSTATE 23505)`),
	} {
		t.Run(name, func(t *testing.T) {
			var lg strings.Builder
			old := httperr.Logf
			httperr.Logf = func(f string, a ...any) { fmt.Fprintf(&lg, f, a...) }
			defer func() { httperr.Logf = old }()
			req := httptest.NewRequest("DELETE", "/local/orgs/o1", nil)
			req.Pattern = "DELETE /local/orgs/{org}"
			rec := httptest.NewRecorder()
			localFail(rec, req, 500, err, map[string]any{"result": map[string]any{"k": "v"}})
			body := rec.Body.String()
			if rec.Code != 500 || !strings.Contains(body, `"result"`) {
				t.Errorf("狀態碼與 extra 欄位:%d %s", rec.Code, body)
			}
			for _, w := range []string{"omixor_orgs", "SQLSTATE", "23505", "violates", "duplicate", "INSERT", "SELECT", "deadline"} {
				if strings.Contains(body, w) {
					t.Errorf("回應含 %q:%s", w, body)
				}
			}
			if !strings.Contains(lg.String(), "DELETE /local/orgs/{org}") || !strings.Contains(lg.String(), err.Error()) {
				t.Errorf("log 要有操作名稱與原文:%s", lg.String())
			}
		})
	}
}

// 既有行為不變:狀態碼由呼叫端給;service.Error 的訊息與 data 照給
func TestLocalFailKeepsMessageAndData(t *testing.T) {
	req := httptest.NewRequest("POST", "/local/orgs", nil)
	rec := httptest.NewRecorder()
	localFail(rec, req, 401, errors.New("要伺服器管理員"), nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"error":"要伺服器管理員"`) {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	localFail(rec, req, 409, &service.Error{Code: 409, Msg: "衝突", Data: map[string]any{"currentVersion": "v7"}}, nil)
	if !strings.Contains(rec.Body.String(), `"currentVersion":"v7"`) || !strings.Contains(rec.Body.String(), `"error":"衝突"`) {
		t.Errorf("%s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	localBadFormat(rec, errors.New("unexpected EOF"))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "格式錯誤:unexpected EOF") {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

// 守門:出口不能直接把 err.Error() 寫進回應(要走 localFail / httperr)
func TestNoRawErrorInResponses(t *testing.T) {
	// 白名單(檔案:函式,附理由):localBadFormat 只回解析器的說明;其餘是伺服器啟動 / 命令列的訊息(印在管理者的終端機,不經過 HTTP 回應)
	bad, err := errscan.Scan(".", map[string]bool{
		"errors.go:localBadFormat":      true,
		"embeddedpg.go:checkPGDataDir":  true,
		"embeddedpg.go:repairDatabase":  true,
		"embeddedpg.go:StartEmbeddedPG": true,
		"local_run.go:runLocal":         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把 err.Error() 寫進回應(請改用 localFail / httperr):%s", b)
	}
}
