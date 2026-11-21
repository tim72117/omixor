package server

import (
	"os"
	"testing"
	"time"

	"omixor-core/config"
)

// 同一行程第二次啟動,不能把第一次自己設的 PGCTLTIMEOUT 當成使用者設的
func TestApplyPGCtlTimeoutSecondCall(t *testing.T) {
	os.Unsetenv("PGCTLTIMEOUT")
	pgctlSetByUs = ""
	t.Cleanup(func() { os.Unsetenv("PGCTLTIMEOUT"); pgctlSetByUs = "" })
	live := func() *config.Config { return config.LoadFrom(os.Getenv) } // 沒走 Load 時 Current() 就是即時讀環境

	applyPGCtlTimeout(live(), 10*time.Minute)
	if got := os.Getenv("PGCTLTIMEOUT"); got != "540" {
		t.Fatalf("第一次:%q", got)
	}
	applyPGCtlTimeout(live(), 20*time.Minute) // 第二次要能更新成新的逾時
	if got := os.Getenv("PGCTLTIMEOUT"); got != "1140" {
		t.Fatalf("第二次沒更新(把自己設的當成使用者設的):%q", got)
	}
	// 使用者自己設的值要保留
	os.Setenv("PGCTLTIMEOUT", "77")
	applyPGCtlTimeout(live(), 30*time.Minute)
	if got := os.Getenv("PGCTLTIMEOUT"); got != "77" {
		t.Fatalf("使用者的值被蓋掉:%q", got)
	}
}
