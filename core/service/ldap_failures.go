package service

import (
	"strings"
	"time"
)

// LoginFailures 目錄帳號的失敗鎖定(不依賴資料庫):送目錄之前先預扣一次,通過就清掉,不是帳密問題(目錄故障)就退回。
// 先扣再送,並行請求送到目錄的次數也不會超過上限(AD 的鎖定是整個網域的)。
// 同一個目錄的所有登入途徑要共用同一個實例(console:網頁登入與 MCP 授權)。零值可用
type LoginFailures struct {
	// Max、Window 一輪內最多幾次(0 = 5 次、10 分鐘)
	Max    int
	Window time.Duration
	// Key 帳號 → 計數鍵;nil = 轉小寫去空白
	Key func(string) string

	c failCounter
}

func (l *LoginFailures) key(user string) string {
	if l.Key != nil {
		return l.Key(user)
	}
	return strings.ToLower(strings.TrimSpace(user))
}

// Reserve 預扣一次;這一輪已經滿了回 ok=false 與還要等多久(不扣)
func (l *LoginFailures) Reserve(user string, now time.Time) (wait time.Duration, ok bool) {
	return l.reserve(user, now, l.Max, l.Window)
}

func (l *LoginFailures) reserve(user string, now time.Time, max int, win time.Duration) (time.Duration, bool) {
	if max <= 0 {
		max = 5
	}
	if win <= 0 {
		win = 10 * time.Minute
	}
	return l.c.reserve(l.key(user), now, win, max)
}

// Succeeded 密碼正確:清掉這個帳號的次數
func (l *LoginFailures) Succeeded(user string) { l.c.clear(l.key(user)) }

// Abort 這次沒有真的驗到密碼(目錄故障、設定問題):退回預扣
func (l *LoginFailures) Abort(user string) { l.c.release(l.key(user)) }

// reserve 還沒到 max 就記一次並回 true;到了回剩下的時間
func (c *failCounter) reserve(key string, now time.Time, win time.Duration, max int) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]failEntry{}
	}
	e, ok := c.m[key]
	if !ok {
		c.makeRoom(now, win)
	}
	if !ok || now.Sub(e.since) >= win {
		e = failEntry{since: now}
	}
	if e.n >= max {
		return win - now.Sub(e.since), false
	}
	e.n++
	c.m[key] = e
	return 0, true
}

func (c *failCounter) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		if e.n--; e.n <= 0 {
			delete(c.m, key)
		} else {
			c.m[key] = e
		}
	}
}
