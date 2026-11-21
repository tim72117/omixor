package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --init-worker 寫的 .mcp.json(含權杖):非 Windows 是 0600;Windows 用 icacls 只留本人(沒有繼承、Users、Everyone)
func TestInitWorkerMcpJSONPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "worker")
	ts := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": goodTok}, t.TempDir())
	if err := initWorker("http://127.0.0.1:3765/mcp", ts, []string{dir}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".mcp.json")
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf(".mcp.json 要 0600:%v", st.Mode().Perm())
		}
		return
	}
	out, err := exec.Command("icacls", p).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls:%v %s", err, out)
	}
	for _, bad := range []string{"Everyone", `BUILTIN\Users`, "Authenticated Users", "(I)"} {
		if strings.Contains(string(out), bad) {
			t.Errorf(".mcp.json 的 ACL 還含 %q:\n%s", bad, out)
		}
	}
	// --force 覆蓋已存在的檔案也要收緊
	if err := initWorker("http://127.0.0.1:3765/mcp", ts, []string{dir, "--force"}); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command("icacls", p).CombinedOutput()
	if strings.Contains(string(out), "(I)") {
		t.Errorf("--force 後 ACL 又出現繼承:\n%s", out)
	}
}
