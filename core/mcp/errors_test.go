package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/httperr"
	"omixor-core/httperr/errscan"
	"omixor-core/service"
	"omixor-core/store"
)

func captureToolLog(t *testing.T) *strings.Builder {
	var sb strings.Builder
	old := httperr.Logf
	httperr.Logf = func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	t.Cleanup(func() { httperr.Logf = old })
	return &sb
}

func resultText(res map[string]any) string {
	return res["content"].([]any)[0].(map[string]any)["text"].(string)
}

// 工具回給 agent 的資料庫錯誤、逾時:不含表名、SQL、SQLSTATE;原文連同工具名稱進 log(terr 與直接回的原始錯誤都一樣)
func TestToolFailureMasksInternalErrors(t *testing.T) {
	pg := &pgconn.PgError{Code: "23503", TableName: "agent_tasks", ColumnName: "agent_id",
		Message: `insert or update on table "agent_tasks" violates foreign key constraint "agent_tasks_agent_id_fkey"`}
	raws := map[string]error{
		"pg":       pg,
		"deadline": fmt.Errorf("SELECT * FROM agent_tasks: %w", context.DeadlineExceeded),
		"sql 文字":   fmt.Errorf(`INSERT INTO agent_tasks (agent_id) VALUES ($1) (SQLSTATE 23505)`),
	}
	for name, raw := range raws {
		for via, err := range map[string]error{"terr": terr(raw), "原始錯誤": raw} {
			t.Run(name+"/"+via, func(t *testing.T) {
				lg := captureToolLog(t)
				res := FailureResult("agent_task_report", err)
				if res["isError"] != true {
					t.Fatal("要是 isError")
				}
				b, _ := json.Marshal(res)
				for _, w := range []string{"agent_tasks", "agent_id", "SQLSTATE", "23505", "23503", "violates", "INSERT", "SELECT", "deadline"} {
					if strings.Contains(string(b), w) {
						t.Errorf("結果含 %q:%s", w, b)
					}
				}
				if !strings.Contains(lg.String(), "agent_task_report") || !strings.Contains(lg.String(), raw.Error()) {
					t.Errorf("log 要有工具名稱與原文:%s", lg.String())
				}
			})
		}
	}
}

// errOf 失敗結果的 error 物件(structuredContent 的 {"error": {...}});同時檢查 text 是同一份 JSON
func errOf(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	var sc struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(res["structuredContent"].(json.RawMessage), &sc); err != nil || sc.Error == nil {
		t.Fatalf("structuredContent 不是 {error:{}}:%v", res["structuredContent"])
	}
	// text 與 structuredContent 相同;唯一例外是 error.data 有大欄位(text 帶 truncated,見 result.go)
	if res["isError"] != true || resultText(res) != string(res["structuredContent"].(json.RawMessage)) && !strings.Contains(resultText(res), `"truncated":true`) {
		t.Fatalf("要是 isError,text 要與 structuredContent 相同:%v", res)
	}
	return sc.Error
}

// ToolError(給模型讀、要保留可操作的訊息)與 409 的 data
func TestToolFailureKeepsMessageAndData(t *testing.T) {
	captureToolLog(t)
	te := ToolError{Message: "GitHub 回應錯誤:Not Found(/repos/o/r)"}
	e := errOf(t, FailureResult("gh", terr(te)))
	if e["message"] != te.Message || e["status"] != float64(400) || e["code"] != "invalid_argument" || e["data"] != nil {
		t.Errorf("ToolError 的訊息要照給、沒狀態碼是 400:%v", e)
	}

	conflict := &service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": "v7"}}
	e = errOf(t, FailureResult("cabinet_layout_save", terr(conflict)))
	d, _ := e["data"].(map[string]any)
	if e["status"] != float64(409) || e["code"] != "conflict" || e["message"] != conflict.Msg || d["currentVersion"] != "v7" {
		t.Errorf("409 的 data 要放在 error.data:%v", e)
	}

	// 刪角色名下有任務:Data["code"] 是 error.code,其餘留在 data
	del := &service.Error{Code: 409, Msg: "名下還有任務", Data: map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}}
	e = errOf(t, FailureResult("agent_delete", terr(del)))
	d, _ = e["data"].(map[string]any)
	if e["code"] != "agentHasTasks" || d["taskCounts"] == nil || d["code"] != nil {
		t.Errorf("taskCounts:%v", e)
	}

	// 一般的 service 錯誤:訊息照給,有狀態碼(非 500)就帶
	forbid := terr(&service.Error{Code: 403, Msg: "不是成員"}).(ToolError)
	if forbid.Message != "不是成員" || forbid.Status != 403 {
		t.Errorf("%+v", forbid)
	}
	if e = errOf(t, FailureResult("x", forbid)); e["code"] != "forbidden" || e["data"] != nil {
		t.Errorf("%v", e)
	}
	// 外鍵錯誤:通用訊息
	if m := terr(store.ErrInUse).(ToolError).Message; m != httperr.MsgInUse {
		t.Errorf("ErrInUse:%q", m)
	}
}

// 守門:MCP 端點不能直接把 err.Error() 寫進回應
func TestNoRawErrorInMCPResponses(t *testing.T) {
	bad, err := errscan.Scan(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把 err.Error() 寫進回應(請改用 httperr):%s", b)
	}
}

// terr 與 FailureResult 對 %w 包過的 ToolError 一致(都用 errors.As):訊息、狀態碼、data 不因斷言方式不同而丟掉
func TestTerrWrappedToolError(t *testing.T) {
	captureToolLog(t)
	inner := ToolError{Message: "要給 name", Status: 400, Data: map[string]any{"field": "name"}}
	wrapped := fmt.Errorf("外層脈絡:%w", inner)
	te, ok := terr(wrapped).(ToolError)
	if !ok || te.Message != inner.Message || te.Status != 400 || te.Data["field"] != "name" {
		t.Fatalf("terr 要取最內層的 ToolError:%+v", te)
	}
	a, b := resultText(FailureResult("t", terr(wrapped))), resultText(FailureResult("t", wrapped))
	if a != b {
		t.Errorf("terr 包過與直接給的輸出要一致:%q vs %q", a, b)
	}
	// 沒有狀態碼的 ToolError:400(參數錯誤);被遮蔽的內部錯誤:500
	if e := errOf(t, FailureResult("t", terr(ToolError{Message: "x"}))); e["status"] != float64(400) {
		t.Errorf("%v", e)
	}
	if e := errOf(t, FailureResult("t", terr(context.DeadlineExceeded))); e["status"] != float64(500) || e["code"] != "internal" {
		t.Errorf("%v", e)
	}
}

// 失敗結果的 text 是同一份 JSON,Data(currentVersion、taskCounts)在 error.data
func TestToolFailureTextIsStructuredJSON(t *testing.T) {
	captureToolLog(t)
	res := FailureResult("cabinet_layout_save", terr(&service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": 3}}))
	if e := errOf(t, res); e["data"].(map[string]any)["currentVersion"] != float64(3) {
		t.Errorf("%v", e)
	}
	if !strings.Contains(resultText(res), `"currentVersion":3`) {
		t.Errorf("text:%q", resultText(res))
	}
}

// ListIssues、ListOrgs 的資料庫錯誤不再原文回給 agent(以前是 ToolError{Message: err.Error()})
func TestToolDBErrorsMasked(t *testing.T) {
	lg := captureToolLog(t)
	pg := &pgconn.PgError{Code: "42P01", Message: `relation "omixor_orgs" does not exist`}
	res := FailureResult("orgs", terr(fmt.Errorf("list orgs: %w", pg)))
	if text := resultText(res); strings.Contains(text, "omixor_orgs") || strings.Contains(text, "42P01") || errOf(t, res)["message"] != httperr.MsgDB {
		t.Errorf("要換成通用訊息:%q", text)
	}
	if !strings.Contains(lg.String(), "omixor_orgs") {
		t.Errorf("原文要進 log:%s", lg.String())
	}
}

// ISS-34:Issue 版本衝突的 409(Data 有 currentVersion 與整個 current):structuredContent 的 error.data 完整,
// text 只留小欄位(currentVersion)與 truncated,整份 Issue 不在模型可見的文字裡重複
func TestToolFailureIssueVersionConflict(t *testing.T) {
	captureToolLog(t)
	cur := map[string]any{"id": "x", "body": "短短短短"}
	e := &service.Error{Code: 409, Msg: "Issue 已被別人更新,請重新載入", Data: map[string]any{"currentVersion": 7, "current": cur}}
	res := FailureResult("issue_update", terr(e))
	got := errOf(t, res)
	d := got["data"].(map[string]any)
	if got["status"] != float64(409) || d["currentVersion"] != float64(7) || d["current"] == nil {
		t.Errorf("structuredContent 的 error.data 要完整:%v", got)
	}
	if text := resultText(res); strings.Contains(text, "短短短短") || !strings.Contains(text, `"currentVersion":7`) || !strings.Contains(text, `"truncated":true`) {
		t.Errorf("text 要只有小欄位與 truncated:%q", text)
	}
}
