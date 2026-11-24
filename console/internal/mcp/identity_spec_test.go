package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	coremcp "omixor-core/mcp"
)

// 本機 /mcp 是人類端點:身分假冒型工具(coremcp.IdentityAgentTools)的 tools/list 不能再教 LLM 帶 agent
// (帶了會 400)。實際走 tools/list,所以規格(spec/tools.json)改句子時,這裡會擋下沒被換掉的描述。
func TestToolsListHidesIdentityAgentGuidance(t *testing.T) {
	h := &Handler{App: extTestApp(t)}
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)), &resp); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range resp.Result.Tools {
		name, _ := d["name"].(string)
		if !coremcp.IdentityAgentTools[name] {
			continue
		}
		seen++
		desc, _ := d["description"].(string)
		low := strings.ToLower(desc)
		if strings.Contains(low, "pass agent") || strings.Contains(low, "attributed to that role") {
			t.Errorf("%s 的描述還在教帶 agent:%s", name, desc)
		}
		sch, _ := d["inputSchema"].(map[string]any)
		props, _ := sch["properties"].(map[string]any)
		if _, has := props["agent"]; has {
			t.Errorf("%s 的 inputSchema 還有 agent 參數", name)
		}
	}
	if seen == 0 {
		t.Fatal("tools/list 沒有任何身分假冒型工具,測試沒有檢查到東西")
	}
}

// initialize 的 instructions 也不能教 LLM 對身分假冒型工具帶 agent(fb_draft_save 的括號說明要被換掉)
func TestInitializeInstructionsHideIdentityAgentGuidance(t *testing.T) {
	h := &Handler{App: extTestApp(t)}
	var resp struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)), &resp); err != nil {
		t.Fatal(err)
	}
	ins := resp.Result.Instructions
	if ins == "" {
		t.Fatal("initialize 沒有 instructions")
	}
	if strings.Contains(strings.ToLower(ins), "pass agent") {
		t.Errorf("instructions 還在教帶 agent:%s", ins)
	}
	if !strings.Contains(ins, "fb_draft_save") {
		t.Error("instructions 沒有提到 fb_draft_save,測試沒有檢查到東西")
	}
}
