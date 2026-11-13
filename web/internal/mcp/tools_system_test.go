package mcp

import (
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// navigate 去系統設定關掉的頁面:改去總覽,回傳 notice(同 App 的 MCPHandler.navigate)
func TestNavigateHonorsFeatureFlags(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir()) // 測試用記憶體核心,每個資料夾一份
	t.Setenv("OPS_NO_TIMERS", "1")
	a := core.New()
	if _, err := a.SystemSave(map[string]bool{"cabinet": false}, nil); err != nil {
		t.Fatal(err)
	}
	out := mockRun(t, a, "navigate", map[string]any{"section": "cabinet", "focus_id": "x"})
	if out["section"] != "overview" || out["focusId"] != nil || !strings.Contains(out["notice"].(string), "文件櫃") {
		t.Fatalf("%+v", out)
	}
	out = mockRun(t, a, "navigate", map[string]any{"section": "incidents"})
	if out["section"] != "incidents" || out["notice"] != nil {
		t.Fatalf("%+v", out)
	}
}
