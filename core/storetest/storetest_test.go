package storetest

import (
	"testing"
	"time"
)

func TestMain(m *testing.M) { Main(m) }

func TestNewIsolated(t *testing.T) {
	a := NewWithOrg(t, "o")
	b := New(t)
	if _, err := b.GetOrg("o"); err == nil {
		t.Fatal("另一個 store 不該看到 o")
	}
	if _, err := a.GetOrg("o"); err != nil {
		t.Fatal(err)
	}
}

// 量測單次 New 的耗時,含測完的還原、回收(go test -run NewTiming -v 看輸出;每次用子測試,資料庫才會被回收)
func TestNewTiming(t *testing.T) {
	t.Run("warm", func(t *testing.T) { New(t) }) // 暖機:建範本、第一個資料庫
	const n = 10
	t0 := time.Now()
	for range n {
		t.Run("x", func(t *testing.T) { New(t) })
	}
	t.Logf("New 平均 %v(%d 次,含連線池、測完還原回收)", time.Since(t0)/n, n)
}

func TestParallel(t *testing.T) {
	for range 8 {
		t.Run("p", func(t *testing.T) {
			t.Parallel()
			s := NewWithOrg(t)
			if _, err := s.GetOrg("o"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
