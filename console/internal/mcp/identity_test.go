package mcp

import (
	"testing"

	coremcp "omixor-core/mcp"
)

// rejectIdentity 與核心的 RejectIdentityAgent 對齊:agent 是非字串、或字串但不是空白,一律 400;空字串與沒給放行
func TestRejectIdentityNonStringAgent(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"數字": {"agent": 5}, "布林": {"agent": true}, "物件": {"agent": map[string]any{"id": "x"}}, "非空字串": {"agent": "pm"}, "空白以外": {"agent": " pm "},
	} {
		for _, tool := range []string{"", "issue_create", "doc_save"} {
			err := rejectIdentity(tool, args)
			te, ok := err.(ToolError)
			if !ok || te.Status != 400 {
				t.Errorf("%s(tool=%q)要 400:%v", name, tool, err)
			}
		}
	}
	for name, args := range map[string]map[string]any{"沒給": {}, "空字串": {"agent": ""}, "空白": {"agent": "  "}} {
		for _, tool := range []string{"", "issue_create"} {
			if err := rejectIdentity(tool, args); err != nil {
				t.Errorf("%s(tool=%q)要放行:%v", name, tool, err)
			}
		}
	}
}

// tools/list(本機 /mcp)不列 agent_task_delegate 與身分假冒型的 agent 參數
func TestListHidesHumanOnly(t *testing.T) {
	for _, d := range ToolDefs() {
		m := d.(map[string]any)
		name, _ := m["name"].(string)
		if !coremcp.IdentityAgentTools[name] {
			continue
		}
		props, _ := m["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if _, has := props["agent"]; has {
			t.Errorf("%s 還列著 agent 參數", name)
		}
	}
	if !coremcp.HumanHiddenTools["agent_task_delegate"] {
		t.Error("agent_task_delegate 要在人類端點隱藏清單")
	}
}
