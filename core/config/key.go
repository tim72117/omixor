package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// 金鑰型設定(OMIXOR_SECRET_KEY、OMIXOR_SECRET_KEYS_OLD)的解碼。
// 值是 32 bytes 的金鑰,可寫成 64 個 hex 字元,或 base64(標準或 URL 安全,有無補 = 都可以);
// 錯誤訊息一律不含金鑰內容。

// KeyLen 金鑰的 byte 數(AES-256)
const KeyLen = 32

// errKeyFormat 金鑰格式不對;訊息短且不含值
var errKeyFormat = errors.New("must be 32 bytes, as 64 hex chars or base64")

// DecodeKey 把 hex 或 base64 的字串解成 32 bytes 的金鑰;前後空白會去掉
func DecodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 2*KeyLen {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == KeyLen {
			return b, nil
		}
	}
	return nil, errKeyFormat
}

// splitKeys 逗號分隔的金鑰清單(忽略空項與前後空白)
func splitKeys(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// checkKeys 檢查金鑰型設定:Key 要剛好一把,KeyList 可以多把(逗號分隔);錯誤只點名變數與第幾把,不印值
func (v *Var) checkKeys(raw string) error {
	parts := splitKeys(raw)
	if v.Kind == Key && len(parts) > 1 { // 沒設(0 把)不是錯誤
		return errors.New(v.Name + " 只能有一把金鑰(32 bytes,64 個 hex 字元或 base64)(值已隱藏)")
	}
	for i, p := range parts {
		if _, err := DecodeKey(p); err != nil {
			return errors.New(v.Name + " 第 " + strconv.Itoa(i+1) + " 把金鑰要是 32 bytes(64 個 hex 字元或 base64)(值已隱藏)")
		}
	}
	return nil
}

// Keys 解碼金鑰型設定的所有金鑰(依設定順序);沒設回 nil;任何一把格式不對就回錯誤(同 Validate 的訊息,不含值)
func (c *Config) Keys(v *Var) ([][]byte, error) {
	c.known(v)
	if err := v.checkKeys(c.raw[v.Name]); err != nil {
		return nil, err
	}
	var out [][]byte
	for _, p := range splitKeys(c.raw[v.Name]) {
		b, _ := DecodeKey(p) // checkKeys 已確認每把都解得開
		out = append(out, b)
	}
	return out, nil
}
