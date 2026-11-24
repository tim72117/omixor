package mcp

import (
	"testing"

	"omixor-web/internal/core"
)

// 並行請求:A(寫入)還在跑時,B(唯讀)先結束,不能洗掉 A 的寫入標記;A 結束時仍要重讀快取
func TestActivityInterleavedRequests(t *testing.T) {
	finished := 0
	act := &activity{a: core.New(), writes: func(tool string) bool { return tool == "w" }, finished: func() { finished++ }}
	endA := act.Begin()
	doneA := act.Tool("w")
	endB := act.Begin()
	if doneB := act.Tool("r"); doneB != nil {
		t.Fatal("唯讀工具不該有重讀收尾")
	}
	endB()
	if finished != 0 {
		t.Fatalf("B 結束時不該重讀:%d", finished)
	}
	doneA()
	endA()
	if finished != 1 {
		t.Fatalf("A 結束後要重讀一次,實際 %d", finished)
	}
}

// writes 為 nil(角色端點):每個工具都算寫入
func TestActivityEveryToolWritesByDefault(t *testing.T) {
	finished := 0
	act := &activity{a: core.New(), finished: func() { finished++ }}
	act.Tool("anything")()
	if finished != 1 {
		t.Fatal(finished)
	}
}
