package httperr

import (
	"crypto/sha256"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// log 的節流:
//   - 每一種錯誤(操作 + 原文)每個視窗(throttleWindow)最多寫 throttleBurst 行,超過的只累計次數,下一個視窗的第一行補上「另外有 N 次」。
//     key 是 op(截斷)加原文的雜湊(固定長度):原文再長、再多樣,每個 key 的記憶體都是固定的,表最多 throttleMaxKey 個。
//   - 全域速率上限:整體每秒最多 globalPerSecond 行,不論錯誤種類;超過的只累計,下一次輸出的那一行補上「已略過 M 筆」。
//     (攻擊者造一堆不同訊息繞過每種錯誤的限制時,log 的寫入量仍有上限。)
//
// 用 sync.Map + atomic,熱路徑只有每個 key 一把小鎖;全域計數是一把很短的鎖。測試可以換 now。
const (
	throttleWindow = 10 * time.Second
	throttleBurst  = 5
	throttleMaxKey = 4096 // 超過就整張表清掉重來(攻擊者造不同訊息灌記憶體;有全域速率上限兜底)
	maxOpLen       = 100  // key 與 log 裡的 op 最多這麼多字元
	maxLineLen     = 1000 // log 裡的原文最多這麼多字元(超過截斷),再大的訊息也不會寫出大行或吃記憶體
)

// globalPerSecond 整體每秒最多寫幾行(測試可以調)
var globalPerSecond = 50

var (
	now        = time.Now
	throttle   sync.Map // key([sha256 前 16 位元組] 加 op)→ *throttleEntry
	throttleN  atomic.Int64
	resetMutex sync.Mutex

	globalMu      sync.Mutex
	globalSecond  int64 // 目前這一秒(Unix 秒)
	globalCount   int   // 這一秒已寫幾行
	globalSkipped int   // 因為全域上限略過、還沒補報的筆數
)

type throttleEntry struct {
	mu         sync.Mutex // 只鎖這個 key
	windowFrom time.Time
	count      int
	suppressed int
}

// throttleKey 節流用的 key:op(截斷)加原文的雜湊;長度固定,和原文長度無關
func throttleKey(op, line string) string {
	h := sha256.Sum256([]byte(line))
	return string(h[:16]) + truncateRunes(op, maxOpLen)
}

// allow 這一行要不要寫;回傳同一種錯誤要補報的被省略次數、全域要補報的略過筆數
func allow(key string) (ok bool, suppressed, skipped int) {
	v, loaded := throttle.Load(key)
	if !loaded {
		if throttleN.Add(1) > throttleMaxKey {
			resetThrottle()
			throttleN.Add(1)
		}
		v, _ = throttle.LoadOrStore(key, &throttleEntry{windowFrom: now()})
	}
	e := v.(*throttleEntry)
	e.mu.Lock()
	t := now()
	if t.Sub(e.windowFrom) >= throttleWindow {
		suppressed = e.suppressed
		e.windowFrom, e.count, e.suppressed = t, 0, 0
	}
	if e.count >= throttleBurst {
		e.suppressed++
		e.mu.Unlock()
		return false, 0, 0
	}
	e.mu.Unlock()
	// 通過每種錯誤的限制後,再看全域上限(沒通過全域的不算進這種錯誤的 count,下個視窗不會誤報)
	globalMu.Lock()
	defer globalMu.Unlock()
	if sec := t.Unix(); sec != globalSecond {
		globalSecond, globalCount = sec, 0
	}
	if globalCount >= globalPerSecond {
		globalSkipped++ // 這一筆只計在全域
		return false, 0, 0
	}
	globalCount++
	e.mu.Lock()
	e.count++
	e.mu.Unlock()
	skipped, globalSkipped = globalSkipped, 0
	return true, suppressed, skipped
}

func resetThrottle() {
	resetMutex.Lock()
	defer resetMutex.Unlock()
	throttle.Range(func(k, _ any) bool { throttle.Delete(k); return true })
	throttleN.Store(0)
}

// ResetThrottle 清掉節流狀態(測試用)
func ResetThrottle() {
	resetThrottle()
	globalMu.Lock()
	globalSecond, globalCount, globalSkipped = 0, 0, 0
	globalMu.Unlock()
}

// truncateRunes 最多 n 個字元(不切壞 UTF-8)
func truncateRunes(s string, n int) string {
	if len(s) <= n { // 位元組數不到 n,字元數一定不到
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// oneLine 把換行與其他控制字元換成可見的跳脫字,避免請求內容夾帶換行偽造 log 行
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.NewReplacer("\r", `\r`, "\n", `\n`, "\t", `\t`).Replace(s))
}
