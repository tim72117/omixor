package auth

import (
	"testing"

	"omixor-core/httperr/errscan"
)

// 守門:登入、LDAP 的錯誤不能直接把 err.Error() 放進 UserError(未登入就能打;細節要用 diag 包起來,Login 換成固定句子)
func TestNoRawErrorInAuthErrors(t *testing.T) {
	bad, err := errscan.Scan(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把錯誤原文放進 UserError:%s", b)
	}
}
