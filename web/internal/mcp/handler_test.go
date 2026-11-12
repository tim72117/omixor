package mcp

import (
	"testing"
)

// 每個工具都要有實作(spec/tools.json 有幾個,Go 版就要有幾個)
func TestAllToolsImplemented(t *testing.T) {
	if m := Missing(); len(m) > 0 {
		t.Logf("還沒實作的工具(%d 個):%v", len(m), m)
		if testingStrict() {
			t.Fail()
		}
	}
}
