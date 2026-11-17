package mcp

import (
	"slices"
	"strings"
	"testing"
)

// 沒編進選用模組 integrations(這個測試套件不 import 模組):規格(spec/tools.json,web 自己的工具)照舊有 github* 工具,
// 但 tools/list 不列;呼叫回「這台伺服器沒有編進這個工具」。有模組的情況在 internal/modules/integrations 測
func TestGitHubToolsHiddenWithoutModule(t *testing.T) {
	a := extTestApp(t)
	for _, n := range []string{"github", "github_runs", "github_run", "github_pull"} {
		if slices.Contains(ToolNames(), n) {
			t.Fatal("tools/list 不該有", n)
		}
		if !srv.InSpec(n) {
			t.Fatal("規格照舊有", n)
		}
		msg := extErr(t, a, n, map[string]any{"repo": "a/b"})
		if !strings.Contains(msg, "沒有編進") {
			t.Fatal(n, msg)
		}
	}
	if m := Missing(); len(m) != 4 {
		t.Fatalf("沒實作的應該只有 GitHub 模組的 4 個工具:%v", m)
	}
}
