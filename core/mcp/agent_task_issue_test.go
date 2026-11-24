package mcp_test

import (
	"strings"
	"testing"
)

// agent_task_create 的 issue 參數:key 或內部 id 都連得上,結果的 links 帶 Issue 內部 id;
// 不存在的 Issue 回英文錯誤與機器碼 issueNotFound,而且不建立任務
func TestAgentTaskCreateIssueParam(t *testing.T) {
	_, _, _, admin, _ := identityEnv(t)
	iss := ok(t, admin, "", "issue_create", map[string]any{"title": "要處理的", "return": "full"})
	id, _ := iss["id"].(string)
	if id == "" {
		t.Fatalf("issue_create 沒回 id:%v", iss)
	}
	links := func(r map[string]any) string {
		var out []string
		for _, l := range r["links"].([]any) {
			out = append(out, l.(string))
		}
		return strings.Join(out, ",")
	}
	r := ok(t, admin, "", "agent_task_create", map[string]any{"assign_to": "cto", "title": "手動任務", "issue": "iss-1"})
	if links(r) != id || r["title"] != "手動任務" {
		t.Fatalf("用 key 連:%v", r)
	}
	r = ok(t, admin, "", "agent_task_create", map[string]any{"assign_to": "cto", "title": "用 id", "issue": id})
	if links(r) != id {
		t.Fatalf("用內部 id 連:%v", r)
	}
	r = ok(t, admin, "", "agent_task_create", map[string]any{"assign_to": "cto", "title": "不連"})
	if len(r["links"].([]any)) != 0 {
		t.Fatalf("沒帶 issue 不連:%v", r)
	}

	_, e := call(t, admin, "", "agent_task_create", map[string]any{"assign_to": "cto", "title": "x", "issue": "ISS-99"})
	if !strings.HasPrefix(e, "Not found: issue ISS-99") || !strings.Contains(e, "issueNotFound") {
		t.Fatalf("要回英文錯誤與機器碼:%q", e)
	}
	if l := ok(t, admin, "", "agent_tasks", map[string]any{"status": "all"}); l["total"].(float64) != 3 {
		t.Fatalf("失敗時不能建立任務:%v", l["total"])
	}
}
