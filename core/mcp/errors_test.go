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
				res := toolFailure("agent_task_report", err)
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

// ToolError(給模型讀、要保留可操作的訊息)與 409 的 data
func TestToolFailureKeepsMessageAndData(t *testing.T) {
	captureToolLog(t)
	te := ToolError{Message: "GitHub 回應錯誤:Not Found(/repos/o/r)"}
	res := toolFailure("gh", terr(te))
	if resultText(res) != te.Message {
		t.Errorf("ToolError 的訊息要照給:%q", resultText(res))
	}
	if _, ok := res["structuredContent"]; ok {
		t.Error("沒有狀態碼與 data 就不帶 structuredContent")
	}

	conflict := &service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": "v7"}}
	res = toolFailure("cabinet_layout_save", terr(conflict))
	if !strings.HasPrefix(resultText(res), conflict.Msg+"\n") || !strings.Contains(resultText(res), `"currentVersion":"v7"`) {
		t.Errorf("訊息 %q", resultText(res))
	}
	sc, _ := res["structuredContent"].(map[string]any)
	if sc["status"] != 409 || sc["currentVersion"] != "v7" || sc["error"] != conflict.Msg {
		t.Errorf("409 的 data 要帶出去:%v", res["structuredContent"])
	}

	// 刪角色名下有任務
	del := &service.Error{Code: 409, Msg: "名下還有任務", Data: map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}}
	sc, _ = toolFailure("agent_delete", terr(del))["structuredContent"].(map[string]any)
	if sc["code"] != "agentHasTasks" || sc["taskCounts"] == nil {
		t.Errorf("taskCounts:%v", sc)
	}

	// 一般的 service 錯誤:訊息照給,有狀態碼(非 500)就帶
	forbid := terr(&service.Error{Code: 403, Msg: "不是成員"}).(ToolError)
	if forbid.Message != "不是成員" || forbid.Status != 403 {
		t.Errorf("%+v", forbid)
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

// terr 與 toolFailure 對 %w 包過的 ToolError 一致(都用 errors.As):訊息、狀態碼、data 不因斷言方式不同而丟掉
func TestTerrWrappedToolError(t *testing.T) {
	captureToolLog(t)
	inner := ToolError{Message: "要給 name", Status: 400, Data: map[string]any{"field": "name"}}
	wrapped := fmt.Errorf("外層脈絡:%w", inner)
	te, ok := terr(wrapped).(ToolError)
	if !ok || te.Message != inner.Message || te.Status != 400 || te.Data["field"] != "name" {
		t.Fatalf("terr 要取最內層的 ToolError:%+v", te)
	}
	a, b := resultText(toolFailure("t", terr(wrapped))), resultText(toolFailure("t", wrapped))
	if a != b {
		t.Errorf("terr 包過與直接給的輸出要一致:%q vs %q", a, b)
	}
	// 沒有狀態碼的 ToolError:structuredContent 不帶 status(不會變成 0)
	res := toolFailure("t", terr(ToolError{Message: "x"}))
	if _, ok := res["structuredContent"]; ok {
		t.Errorf("沒有狀態碼與 data:%v", res["structuredContent"])
	}
}

// Data(currentVersion、taskCounts)也附在 text 末尾(Claude Code 只顯示 content 的 text),structuredContent 保留
func TestToolFailureTextCarriesData(t *testing.T) {
	captureToolLog(t)
	res := toolFailure("cabinet_layout_save", terr(&service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": 3}}))
	text := resultText(res)
	if !strings.HasPrefix(text, "結構已被別人更新,請重新載入\n") || !strings.Contains(text, `{"currentVersion":3}`) {
		t.Errorf("text 要含 currentVersion:%q", text)
	}
	sc := res["structuredContent"].(map[string]any)
	if sc["currentVersion"] != 3 || sc["status"] != 409 {
		t.Errorf("structuredContent 要保留:%v", sc)
	}
	// 沒有 data 的錯誤 text 不變
	if got := resultText(toolFailure("x", terr(&service.Error{Code: 403, Msg: "不是成員"}))); got != "不是成員" {
		t.Errorf("%q", got)
	}
}

// ListIssues、ListOrgs 的資料庫錯誤不再原文回給 agent(以前是 ToolError{Message: err.Error()})
func TestToolDBErrorsMasked(t *testing.T) {
	lg := captureToolLog(t)
	pg := &pgconn.PgError{Code: "42P01", Message: `relation "omixor_orgs" does not exist`}
	res := toolFailure("orgs", terr(fmt.Errorf("list orgs: %w", pg)))
	if text := resultText(res); strings.Contains(text, "omixor_orgs") || strings.Contains(text, "42P01") || text != httperr.MsgDB {
		t.Errorf("要換成通用訊息:%q", text)
	}
	if !strings.Contains(lg.String(), "omixor_orgs") {
		t.Errorf("原文要進 log:%s", lg.String())
	}
}

// ISS-34:Issue 版本衝突的 409(Data 有 currentVersion 與整個 current):text 只附純量(太長時),structuredContent 完整
func TestToolFailureIssueVersionConflict(t *testing.T) {
	captureToolLog(t)
	cur := map[string]any{"id": "x", "body": "短短短短"}
	e := &service.Error{Code: 409, Msg: "Issue 已被別人更新,請重新載入", Data: map[string]any{"currentVersion": 7, "current": cur}}
	res := toolFailure("issue_update", terr(e))
	text := resultText(res)
	if !strings.Contains(text, `"currentVersion":7`) || strings.Contains(text, "短短短短") {
		t.Errorf("text 要有 currentVersion、不要塞整個 Issue:%.120q", text)
	}
	sc := res["structuredContent"].(map[string]any)
	if sc["status"] != 409 || sc["currentVersion"] != 7 || sc["current"] == nil {
		t.Errorf("structuredContent 要完整:%v", sc)
	}
}
