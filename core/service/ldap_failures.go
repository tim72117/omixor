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
	// Identity 帳號 → 寫法鍵,給 Key 會把可能是不同帳號的寫法併在一起的宿主用:次數照樣合併計(換寫法不會多出次數),
	// 但密碼正確只清掉同一個寫法鍵記的次數。nil = 密碼正確清掉整個計數鍵
	Identity func(string) string

	c failCounter
}

func (l *LoginFailures) key(user string) string {
	if l.Key != nil {
		return l.Key(user)
	}
	return strings.ToLower(strings.TrimSpace(user))
}

func (l *LoginFailures) identity(user string) string {
	if l.Identity != nil {
		return l.Identity(user)
	}
	return ""
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
	return l.c.reserve(l.key(user), l.identity(user), now, win, max)
}

// Succeeded 密碼正確:清掉這個帳號的次數
func (l *LoginFailures) Succeeded(user string) {
	if l.Identity == nil {
		l.c.clear(l.key(user))
		return
	}
	l.c.clearIdentity(l.key(user), l.identity(user))
}

// Abort 這次沒有真的驗到密碼(目錄故障、設定問題):退回預扣
func (l *LoginFailures) Abort(user string) {
	if l.Identity == nil {
		l.c.release(l.key(user))
		return
	}
	l.c.releaseIdentity(l.key(user), l.identity(user))
}

// reserve 還沒到 max 就記一次並回 true;到了回剩下的時間。id 非空時另外記在這個寫法鍵名下
func (c *failCounter) reserve(key, id string, now time.Time, win time.Duration, max int) (time.Duration, bool) {
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
	if id != "" {
		if e.by == nil {
			e.by = map[string]int{}
		}
		e.by[id]++
	}
	c.m[key] = e
	return 0, true
}

// clearIdentity 清掉 id 名下的次數,別的寫法記的留著
func (c *failCounter) clearIdentity(key, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok {
		e.n -= e.by[id]
		delete(e.by, id)
		c.put(key, e)
	}
}

// releaseIdentity 退回 id 名下的一次;已經被清掉就不退(不能扣到別人的)
func (c *failCounter) releaseIdentity(key, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[key]; ok && e.by[id] > 0 {
		if e.by[id]--; e.by[id] == 0 {
			delete(e.by, id)
		}
		e.n--
		c.put(key, e)
	}
}

// put 在鎖裡呼叫:寫回,沒有次數了就刪掉
func (c *failCounter) put(key string, e failEntry) {
	if e.n <= 0 {
		delete(c.m, key)
		return
	}
	c.m[key] = e
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
