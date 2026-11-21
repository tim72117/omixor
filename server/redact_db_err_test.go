package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRedactDBErr(t *testing.T) {
	base := errors.New("連不上")
	for _, c := range []struct{ dsn, pw string }{
		{"postgres://u:URLPW-123@h/db", "URLPW-123"},
		{"host=/cloudsql/p:r:i user=u password=KVPW-456 dbname=d", "KVPW-456"},
		{"postgres://h/db?user=u&password=QPW-789", "QPW-789"},
	} {
		err := redactDBErr(c.dsn, fmt.Errorf("failed to connect to `%s`: password authentication failed (%s): %w", c.dsn, c.pw, base))
		if strings.Contains(err.Error(), c.pw) {
			t.Errorf("錯誤洩漏密碼:%s", err)
		}
		if !errors.Is(err, base) {
			t.Error("要保留 errors.Is")
		}
	}
	if redactDBErr("x=1", nil) != nil {
		t.Error("nil 要回 nil")
	}
}
