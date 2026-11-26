package core

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// OpenDB 收的金鑰格式要和 config 驗證的一樣(hex 或 base64)。用解析不了的資料庫網址:金鑰過了才會走到網址,不需要資料庫
func TestOpenDBKeyFormats(t *testing.T) {
	const badURL = "postgres://%zz"
	raw := []byte("0123456789abcdef0123456789abcde\xff")
	for name, key := range map[string]string{
		"hex":        hex.EncodeToString(raw),
		"std base64": base64.StdEncoding.EncodeToString(raw),
		"url base64": base64.RawURLEncoding.EncodeToString(raw),
	} {
		if err := OpenDB(badURL, key); err == nil || !strings.Contains(err.Error(), "資料庫網址") {
			t.Errorf("%s 的金鑰要收:%v", name, err)
		}
	}
	const bad = "not-a-key-SECRETVALUE"
	err := OpenDB(badURL, bad)
	if err == nil || !strings.Contains(err.Error(), "OMIXOR_SECRET_KEY") || strings.Contains(err.Error(), bad) {
		t.Fatal("格式不對要點名變數、不印值:", err)
	}
}
