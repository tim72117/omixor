package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/httperr"
	"omixor-core/service"
)

// agentUserErr / agentConflictErr 和 API、MCP 走同一份規則(httperr):409 的 data 帶得出去、資料庫錯誤遮蔽且原文進 log
func TestAgentErrHelpersUseHTTPErr(t *testing.T) {
	var lg strings.Builder
	old := httperr.Logf
	httperr.Logf = func(f string, a ...any) { fmt.Fprintf(&lg, f, a...) }
	defer func() { httperr.Logf = old }()

	conflict := &service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": "v7"}}
	se, ok := agentConflictErr(conflict).(StatusError)
	if !ok || se.Status != 409 || se.Data["currentVersion"] != "v7" || se.Message != conflict.Msg {
		t.Errorf("409 要帶 currentVersion:%#v", agentConflictErr(conflict))
	}
	del := &service.Error{Code: 409, Msg: "名下還有任務", Data: map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}}
	if se, ok := agentConflictErr(del).(StatusError); !ok || se.Data["taskCounts"] == nil {
		t.Errorf("taskCounts:%#v", agentConflictErr(del))
	}
	// 沒有 data 的 409、其他狀態碼:UserError(同以前)
	if ue, ok := agentConflictErr(&service.Error{Code: 409, Msg: "重複"}).(UserError); !ok || ue.Message != "重複" {
		t.Errorf("沒有 data 的 409 是 UserError")
	}
	if ue, ok := agentUserErr(conflict).(UserError); !ok || ue.Message != conflict.Msg {
		t.Errorf("agentUserErr 訊息照給")
	}

	pg := &pgconn.PgError{Code: "23503", TableName: "agent_tasks", Message: `violates foreign key constraint "agent_tasks_agent_id_fkey"`}
	for _, e := range []error{agentUserErr(pg), agentConflictErr(pg)} {
		if strings.Contains(e.Error(), "agent_tasks") || strings.Contains(e.Error(), "violates") {
			t.Errorf("資料庫錯誤要遮蔽:%s", e)
		}
	}
	if !strings.Contains(lg.String(), "agent_tasks") || !strings.Contains(lg.String(), "agent_err_test.go") {
		t.Errorf("log 要有原文與呼叫位置:%s", lg.String())
	}
	if agentUserErr(nil) != nil || agentConflictErr(nil) != nil {
		t.Error("nil 原樣回傳")
	}
}
