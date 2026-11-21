package core

import (
	"strings"
	"testing"

	"omixor-core/store"
	"omixor-core/storetest"
)

// 測試程序結束時 storetest.Main 會 drop 資料庫,測試留下的背景 goroutine 這時呼叫核心:
// get 回 nil 就是沒有核心(不 panic);連線失敗但 get 之後回 nil(結束中)也不 panic;其他原因的失敗照樣 panic
func TestUseTestStoresShutdownRace(t *testing.T) {
	old := newTestCore
	t.Cleanup(func() { newTestCore = old })

	UseTestStores(func(string) *store.PG { return nil })
	if l := newTestCore(); l != nil {
		t.Fatal("get 回 nil 要沒有核心", l)
	}

	dead := storetest.New(t)
	dead.Close() // 連線池已關:ensureLocalPrincipal 會失敗
	calls := 0
	UseTestStores(func(string) *store.PG {
		calls++
		if calls == 1 {
			return dead
		}
		return nil // 第二次問:已經在結束
	})
	if l := newTestCore(); l != nil {
		t.Fatal("結束中的失敗要當沒有核心", l)
	}

	UseTestStores(func(string) *store.PG { return dead })
	func() {
		defer func() {
			if r, _ := recover().(string); !strings.Contains(r, "測試核心的本機使用者建立失敗") {
				t.Fatal("不是結束中的失敗要 panic", r)
			}
		}()
		newTestCore()
	}()
}
