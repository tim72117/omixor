package mcpauth

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 非 Windows:0600
func TestRestrictToOwnerUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 看下一個測試")
	}
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RestrictToOwner(p); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("要 0600:%v", st.Mode().Perm())
	}
}

// Windows:呼叫 icacls /inheritance:r /grant:r "<使用者>:F"(用假的 icacls 驗參數;失敗要回傳錯誤、警告)
func TestRestrictToOwnerWindowsArgs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("只在 Windows")
	}
	oldI, oldW, oldWarn := icacls, windowsAccount, Warnf
	t.Cleanup(func() { icacls, windowsAccount, Warnf = oldI, oldW, oldWarn })
	windowsAccount = func() (string, error) { return `PC\tester`, nil }
	var got []string
	icacls = func(path string, args ...string) ([]byte, error) {
		got = append([]string{path}, args...)
		return nil, nil
	}
	if err := RestrictToOwner(`C:\x\mcp-cred.json`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != `C:\x\mcp-cred.json|/inheritance:r|/grant:r|PC\tester:F` {
		t.Fatalf("icacls 參數:%q", got)
	}
	icacls = func(string, ...string) ([]byte, error) { return []byte("拒絕存取"), errors.New("exit 5") }
	var warned string
	Warnf = func(f string, a ...any) { warned = f }
	if RestrictOrWarn(`C:\x\mcp-cred.json`) || warned == "" {
		t.Fatal("失敗要警告並回傳 false")
	}
	windowsAccount = func() (string, error) { return "", errors.New("沒有使用者") }
	if err := RestrictToOwner(`C:\x`); err == nil {
		t.Fatal("找不到使用者要回錯誤")
	}
}

// Windows 真的跑 icacls:收緊後 ACL 只剩本人(沒有 Users、Everyone、Authenticated Users),檔案本人仍讀寫得了
func TestRestrictToOwnerWindowsReal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("只在 Windows")
	}
	if _, err := exec.LookPath("icacls"); err != nil {
		t.Skip("沒有 icacls")
	}
	path := filepath.Join(t.TempDir(), "cred.json")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestrictToOwner(path); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls:%v %s", err, out)
	}
	for _, bad := range []string{"Everyone", `BUILTIN\Users`, "Authenticated Users", "(I)"} {
		if strings.Contains(string(out), bad) {
			t.Errorf("ACL 還含 %q:%s", bad, out)
		}
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "secret" {
		t.Fatalf("本人仍要讀得到:%v", err)
	}
	// 已存在的檔案再收緊一次也成功
	if err := RestrictToOwner(path); err != nil {
		t.Fatal(err)
	}
}
