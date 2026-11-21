package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/configuration.md 由設定定義產生;定義改了、文件沒重產,這個測試就失敗。
// 重產:UPDATE_CONFIG_DOC=1 go test ./config -run TestDocFresh(在 core/ 底下)
func TestDocFresh(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "configuration.md")
	want := Markdown()
	if os.Getenv("UPDATE_CONFIG_DOC") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("讀不到 %s(%v)。請執行 UPDATE_CONFIG_DOC=1 go test ./config -run TestDocFresh 產生", path, err)
	}
	if got := strings.ReplaceAll(string(b), "\r\n", "\n"); got != want {
		t.Fatalf("%s 與 core/config 的設定定義不一致。請執行 UPDATE_CONFIG_DOC=1 go test ./config -run TestDocFresh 重新產生", path)
	}
}
