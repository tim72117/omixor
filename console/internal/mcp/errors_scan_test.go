package mcp

import (
	"testing"

	"omixor-core/httperr/errscan"
)

// 守門:工具不能直接把 err.Error() 放進 ToolError 或回應(要走 ToolErr / PrefixErr,資料庫等內部細節才會被遮蔽)。
// 白名單(「檔案:函式」)要寫理由;目前沒有。
func TestNoRawErrorInToolResults(t *testing.T) {
	bad, err := errscan.Scan(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("直接把錯誤原文放進工具結果(請改用 ToolErr / PrefixErr):%s", b)
	}
}
