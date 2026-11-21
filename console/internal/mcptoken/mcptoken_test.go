package mcptoken

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewAndValid(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New()
	if a == b {
		t.Fatal("每次產生要不一樣")
	}
	if !strings.HasPrefix(a, Prefix) || len(a) != len(Prefix)+43 { // 32 位元組 base64url 無填充 = 43 字元
		t.Fatalf("格式不對(長度 %d)", len(a))
	}
	if !Valid(a) {
		t.Fatal("產生的要合格")
	}
	for _, bad := range []string{"", "short", "has space in the middle 1234567", "tab\tinside-the-token-12345", strings.Repeat("x", 513)} {
		if Valid(bad) {
			t.Errorf("%q 不該合格", bad)
		}
	}
}

func TestHintNeverFull(t *testing.T) {
	tok, _ := New()
	h := Hint(tok)
	if h != Prefix+"…"+tok[len(tok)-4:] || strings.Contains(h, tok[len(Prefix):len(tok)-4]) {
		t.Fatalf("遮罩不對:%s", h)
	}
	if Hint("short") != "…" {
		t.Fatal("太短只顯示 …")
	}
	if got := Hint("0123456789abcdefghij0123456789abcdefghij"); got != "…ghij" {
		t.Fatalf("沒有前綴的:%s", got)
	}
}

func TestFileRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	if _, err := ReadFile(dir); !os.IsNotExist(err) {
		t.Fatalf("不存在要回 ErrNotExist:%v", err)
	}
	tok, _ := New()
	if err := WriteFile(dir, tok); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir)
	if err != nil || got != tok {
		t.Fatalf("讀回:%q %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(Path(dir))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("權限要 0600:%v", fi.Mode().Perm())
		}
	}
	tok2, _ := New()
	if err := WriteFile(dir, tok2); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadFile(dir); got != tok2 {
		t.Fatal("覆蓋後要讀到新的")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("不留暫存檔:%v", ents)
	}
	// 內容壞掉:錯誤訊息不含內容
	_ = os.WriteFile(Path(dir), []byte("bad token"), 0o600)
	if _, err := ReadFile(dir); err == nil || strings.Contains(err.Error(), "bad token") {
		t.Fatalf("壞內容要報錯且不洩漏:%v", err)
	}
	if err := WriteFile(dir, "short"); err == nil {
		t.Fatal("不合格的權杖不寫入")
	}
}

func TestFromEnv(t *testing.T) {
	good := Prefix + strings.Repeat("a", 27) // 32 字元
	for raw, want := range map[string]string{good: good, good + "\n": good, good + "\r\n": good, good + " \t\r\n\n": good} {
		tok, set, err := FromEnv(raw)
		if err != nil || !set || tok != want {
			t.Errorf("%q:(%q,%v,%v)", raw, tok, set, err)
		}
	}
	for _, raw := range []string{"", "\n", "  \n"} {
		if tok, set, err := FromEnv(raw); set || err != nil || tok != "" {
			t.Errorf("%q 視為沒設:(%q,%v,%v)", raw, tok, set, err)
		}
	}
	for _, raw := range []string{good[:31], "a b" + good, " " + good, "x\ny" + good, strings.Repeat("x", 513)} {
		_, set, err := FromEnv(raw)
		if err == nil || !set {
			t.Errorf("%q 要回錯誤", raw)
		} else if strings.Contains(err.Error(), raw) {
			t.Errorf("錯誤不能含權杖:%v", err)
		}
	}
}
