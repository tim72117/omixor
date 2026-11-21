package integrations

import (
	"testing"

	"omixor-core/httperr/errscan"
)

// 守門:GitHub / GitLab 的工具與畫面狀態不能直接放 err.Error()(ConnError 是 httperr.Public 照給,其他走 httperr.Report)
func TestNoRawErrorInIntegrations(t *testing.T) {
	bad, err := errscan.Scan(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把錯誤原文放進輸出(請改用 httperr.Report / mcp.PrefixErr):%s", b)
	}
}
