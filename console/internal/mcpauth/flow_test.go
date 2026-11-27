package mcpauth

import "testing"

// 組織 id 可以是 mcp 開頭(或就叫 mcp),不能被當成端點路徑的 /mcp
func TestSplitResource(t *testing.T) {
	const pub = "https://o.example.com"
	for _, c := range []struct{ resource, public, suffix string }{
		{pub + "/mcp", pub, "/mcp"},
		{pub + "/mcp/org/mcp-team", pub, "/mcp/org/mcp-team"},
		{pub + "/mcp/worker/mcp-team", pub, "/mcp/worker/mcp-team"},
		{pub + "/mcp/worker/mcp", pub, "/mcp/worker/mcp"},
		{pub + "/mcp/mcp", pub + "/mcp", "/mcp"},
		{"http://127.0.0.1:5191/mcp/org/acme", "http://127.0.0.1:5191", "/mcp/org/acme"},
	} {
		public, suffix, err := splitResource(c.resource)
		if err != nil || public != c.public || suffix != c.suffix {
			t.Errorf("%s:%q %q %v", c.resource, public, suffix, err)
		}
	}
	if !IsWorkerResource(pub + "/mcp/worker/mcp-team") {
		t.Error("mcp 開頭的組織也是執行者端點")
	}
	for _, bad := range []string{pub, "https://mcp", pub + "/mcp/", pub + "/mcpx", pub + "/mcp/worker/", pub + "/mcp/worker/a/b",
		pub + "/mcp/other/a", pub + "/mcp/org/a%20b", pub + "/mcp?x=1", pub + "/mcp?", "http://o.example.com/mcp"} {
		if _, _, err := splitResource(bad); err == nil {
			t.Errorf("%s 要拒絕", bad)
		}
	}
}
