package errmask

import (
	"errors"
	"testing"
)

func TestMaskLeakPatterns(t *testing.T) {
	leaks := []string{
		`Get "https://x": dial tcp: lookup db.internal: no such host`,
		`lookup ldap.corp.local on 10.0.0.2:53: no such host`,
		`上游回應 401:Authorization: Bearer abc.def.ghi`,
		`header bearer   eyJhbGciOi`,
		`oauth 失敗:client_secret=s3cr3t&grant_type=x`,
		`refresh_token=abc 已過期`,
		`open \\fileserver\share\secrets\key.json: 找不到`,
		`讀取 "\\10.0.0.5\c$\data" 失敗`,
	}
	for _, s := range leaks {
		if _, ok := Mask(errors.New(s), true); !ok {
			t.Errorf("要遮蔽:%s", s)
		}
	}
	// 沒有內部細節的刻意訊息不誤殺
	for _, s := range []string{"要伺服器管理員", "名稱不能空白", "時間格式要像 2026-01-01", "權杖不對", "bearer"} {
		if _, ok := Mask(errors.New(s), true); ok {
			t.Errorf("不該遮蔽:%s", s)
		}
	}
	// 不啟用文字比對(管理員診斷)時不看這些樣式
	if _, ok := Mask(errors.New("lookup x: no such host"), false); ok {
		t.Error("heuristic=false 不用文字比對")
	}
}
