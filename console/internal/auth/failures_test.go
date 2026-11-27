package auth

import (
	"strings"
	"testing"
	"time"

	"omixor-core/service"
)

func TestFailureKeyVariants(t *testing.T) {
	for _, v := range []string{"bob", " BOB ", `CORP\bob`, `CORP\ bob`, "bob@a.com", "bob @b.com", "ｂｏｂ", "Ｂｏｂ＠a.com", `CORP＼bob`, "\u3000bob\u3000"} {
		if got := failureKey(v); got != "bob" {
			t.Errorf("failureKey(%q) = %q", v, got)
		}
	}
}

// 次數跨寫法合併;密碼正確只清同一個寫法的,清不掉別的網域(可能是別人)的
func TestFailuresNotClearedByOtherIdentity(t *testing.T) {
	l := &service.LoginFailures{Key: failureKey, Identity: failureIdentity}
	now := time.Now()
	reserve := func(u string) bool { _, ok := l.Reserve(u, now); return ok }
	fill := func(u string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if !reserve(u) {
				t.Fatal(u, i)
			}
		}
	}
	fill("bob@a.com", 4)
	fill("bob@b.com", 1) // 攻擊者自己的帳號,這次密碼正確
	l.Succeeded("Bob@B.com")
	fill("BOB@A.COM", 1)
	for _, v := range []string{"bob@a.com", "bob", `A\bob`, "bob@b.com", strings.ToUpper("ｂｏｂ")} {
		if reserve(v) {
			t.Fatal("換寫法不能多出次數:", v)
		}
	}
	// 目錄故障的退回只退自己名下的
	l.Abort("bob")
	if reserve("bob") {
		t.Fatal("沒有記過的寫法退不到次數")
	}
	l.Abort("bob@a.com")
	fill("bob", 1)
}

func TestFailureClearedBySameIdentity(t *testing.T) {
	l := &service.LoginFailures{Key: failureKey, Identity: failureIdentity}
	now := time.Now()
	for i := 0; i < 20; i++ {
		for j := 0; j < 5; j++ {
			if _, ok := l.Reserve("Alice", now); !ok {
				t.Fatal(i, j)
			}
		}
		l.Succeeded(" alice ")
	}
}

func TestLockedErrorMessages(t *testing.T) {
	rate := loginError(&service.Error{Code: 429, Data: map[string]any{"retryAfter": 60, service.DataRateLimited: true}})
	if got := rate.Error(); got != "登入嘗試太頻繁,請 1 分鐘後再試" {
		t.Fatal(got)
	}
	locked := loginError(&service.Error{Code: 429, Data: map[string]any{"retryAfter": 125}})
	if got := locked.Error(); got != "登入失敗太多次,請 3 分鐘後再試" {
		t.Fatal(got)
	}
}
