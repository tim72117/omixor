package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-web/internal/core"
)

type gateResp struct {
	ID     float64 `json:"id"`
	Result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func gateCall(t *testing.T, h *Handler, msg any) gateResp {
	t.Helper()
	var r gateResp
	if err := json.Unmarshal(h.Handle(core.Marshal(msg)), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func gateList(t *testing.T, h *Handler) []string {
	t.Helper()
	r := gateCall(t, h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	names := []string{}
	for _, tl := range r.Result.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// 對應的功能 key 都要在核心登記過、工具都要在規格裡
func TestFeatureToolsRegistered(t *testing.T) {
	for tool, key := range featureTools {
		if _, ok := model.FeatureByKey(key); !ok {
			t.Errorf("%s 對應的功能 %s 核心沒登記", tool, key)
		}
		if !srv.InSpec(tool) {
			t.Errorf("%s 不在工具規格裡", tool)
		}
	}
}

// 範例資料工具:對應的功能關掉時 tools/list 不列、tools/call 回「這個功能已關閉」;開著時照常。核心工具不受影響
func TestMCPSampleToolsFollowFeatures(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir()) // 測試用記憶體核心,每個資料夾一份
	t.Setenv("OMIXOR_NO_TIMERS", "1")
	a := core.New()
	h := &Handler{App: a}

	// 預設:實驗功能(告警、待核准、執行紀錄…)系統層不開放
	names := gateList(t, h)
	for _, n := range []string{"alert_create", "alert_update", "log_append", "work_item_create", "work_item_move", "pipeline_update", "request_approval"} {
		if slices.Contains(names, n) {
			t.Errorf("功能關著,tools/list 不應該列 %s", n)
		}
	}
	for _, n := range []string{"overview", "activity_post", "list", "get", "navigate", "issue_create", "issues", "agents", "channels", "orgs"} {
		if !slices.Contains(names, n) {
			t.Errorf("tools/list 應該列 %s", n)
		}
	}

	r := gateCall(t, h, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "alert_create", "arguments": map[string]any{"service": "api-server"}}})
	if r.Error != nil || !r.Result.IsError || len(r.Result.Content) == 0 ||
		!strings.Contains(r.Result.Content[0].Text, "這個功能已關閉") || !strings.Contains(r.Result.Content[0].Text, "告警") {
		t.Fatalf("關掉的功能要回「這個功能已關閉」:%+v", r)
	}
	alerts := len(a.C["alerts"])

	// 批次:tools/list 和 tools/call 一起送,各自照規則
	var batch []gateResp
	if err := json.Unmarshal(h.Handle(core.Marshal([]any{
		map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"},
		map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "request_approval", "arguments": map[string]any{}}},
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
	})), &batch); err != nil || len(batch) != 2 {
		t.Fatalf("批次:%v %+v", err, batch)
	}
	for _, tl := range batch[0].Result.Tools {
		if tl.Name == "request_approval" {
			t.Fatal("批次的 tools/list 也不應該列 request_approval")
		}
	}
	if !batch[1].Result.IsError || !strings.Contains(batch[1].Result.Content[0].Text, "這個功能已關閉") {
		t.Fatalf("批次的 tools/call:%+v", batch[1])
	}

	// 畫面(/api/tool → Run)不經過這個過濾:事件的後續工作等開著的功能也會用到
	if _, err := Run(a, "alert_update", map[string]any{"alert_id": "ALT-1"}, core.MCPActor); err == nil || strings.Contains(err.Error(), "已關閉") {
		t.Fatalf("Run 不應該被功能開關擋:%v", err)
	}

	// 系統層開放、組織層打開告警:列出來,也能呼叫
	if _, err := a.ServerFeaturesSave(map[string]bool{"monitoring.alerts": true}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SystemSave(map[string]bool{"monitoring.alerts": true}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(gateList(t, h), "alert_create") {
		t.Fatal("告警開了,tools/list 應該列 alert_create")
	}
	r = gateCall(t, h, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "alert_create", "arguments": map[string]any{"service": "nope"}}})
	if len(r.Result.Content) == 0 || strings.Contains(r.Result.Content[0].Text, "已關閉") || !strings.Contains(r.Result.Content[0].Text, "找不到服務 nope") {
		t.Fatalf("告警開了要照常執行:%+v", r)
	}
	if len(a.C["alerts"]) != alerts {
		t.Fatal("關掉時的呼叫不應該建立告警")
	}

	// 頁面整個關掉:分頁跟著關
	if _, err := a.SystemSave(map[string]bool{"monitoring": false}, nil); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(gateList(t, h), "alert_create") {
		t.Fatal("監控頁關了,tools/list 不應該列 alert_create")
	}
}
