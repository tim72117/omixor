package httperr

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func throttleHarness(t *testing.T) (lines *[]string, setNow func(time.Time)) {
	t.Helper()
	var mu sync.Mutex
	var out []string
	cur := time.Unix(10_000, 0)
	oldLog, oldNow, oldG := Logf, now, globalPerSecond
	Logf = func(f string, a ...any) { mu.Lock(); out = append(out, fmt.Sprintf(f, a...)); mu.Unlock() }
	now = func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
	t.Cleanup(func() { Logf, now, globalPerSecond = oldLog, oldNow, oldG; ResetThrottle() })
	ResetThrottle()
	return &out, func(x time.Time) { mu.Lock(); cur = x; mu.Unlock() }
}

// 大量不同的訊息:每秒寫出的行數有全域上限,超過的下一次輸出補報「已略過 M 筆」
func TestGlobalRateLimitOnUniqueMessages(t *testing.T) {
	lines, setNow := throttleHarness(t)
	globalPerSecond = 20
	for i := 0; i < 5000; i++ {
		LogMasked("POST /x", fmt.Errorf("SQLSTATE 23505 key=%d", i))
	}
	if len(*lines) != 20 {
		t.Fatalf("同一秒內最多 20 行,得到 %d", len(*lines))
	}
	// 下一秒:第一行補報略過 4980 筆
	setNow(time.Unix(10_001, 0))
	LogMasked("POST /x", errors.New("SQLSTATE 23505 key=next"))
	if len(*lines) != 21 || !strings.Contains((*lines)[20], "已略過 4980 筆") {
		t.Fatalf("要補報略過筆數:%d 行,最後一行 %q", len(*lines), (*lines)[len(*lines)-1])
	}
	// 補報只一次
	LogMasked("POST /x", errors.New("SQLSTATE 23505 key=next2"))
	if strings.Contains((*lines)[len(*lines)-1], "已略過") {
		t.Fatal("補報不能重複")
	}
}

// 超大訊息:log 行有長度上限、key 長度固定;同一則超大訊息仍受每種錯誤的限制
func TestHugeMessageBounded(t *testing.T) {
	lines, _ := throttleHarness(t)
	huge := strings.Repeat("字SQLSTATE", 2_000_000) // 約 20MB 文字
	for i := 0; i < 100; i++ {
		LogMasked(strings.Repeat("op", 5000), errors.New(huge))
	}
	if len(*lines) != throttleBurst {
		t.Fatalf("同一則超大訊息要受每種錯誤限制:%d 行", len(*lines))
	}
	for _, l := range *lines {
		if len([]rune(l)) > maxLineLen+maxOpLen+200 {
			t.Fatalf("log 行太長:%d 字", len([]rune(l)))
		}
		if !strings.Contains(l, "[opop") {
			t.Fatalf("op 要被截斷但保留:%q", l[:50])
		}
	}
	// key 與原文長度無關
	k1, k2 := throttleKey("op", "a"), throttleKey("op", huge[:100000])
	if len(k1) != len(k2) {
		t.Fatalf("key 長度要固定:%d %d", len(k1), len(k2))
	}
	if len(throttleKey(strings.Repeat("o", 100000), "a")) > 16+maxOpLen {
		t.Fatal("op 也要截斷")
	}
}

// 大量唯一訊息:表的大小有上限(超過清掉重來),不會無限成長
func TestThrottleTableBounded(t *testing.T) {
	throttleHarness(t)
	globalPerSecond = 1 << 30
	for i := 0; i < throttleMaxKey*3; i++ {
		LogMasked("op", fmt.Errorf("SQLSTATE x%d", i))
	}
	n := 0
	throttle.Range(func(_, _ any) bool { n++; return true })
	if n > throttleMaxKey {
		t.Fatalf("表有 %d 筆,超過上限 %d", n, throttleMaxKey)
	}
}

// 記憶體上限:灌 20 萬種各 10KB 的訊息,堆積的記憶體不隨訊息大小成長(只留固定大小的 key)
func TestThrottleMemoryBounded(t *testing.T) {
	throttleHarness(t)
	globalPerSecond = 1 << 30
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	pad := strings.Repeat("x", 10*1024)
	for i := 0; i < 20000; i++ {
		LogMasked("op", errors.New(pad+fmt.Sprint(i)))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	// throttleMaxKey 個 key,每個約 100 位元組;給 16MB 的餘裕(若保存原文會是 4096 x 10KB = 40MB 以上)
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 16<<20 {
		t.Fatalf("節流表占用 %d 位元組,太多", grown)
	}
}
