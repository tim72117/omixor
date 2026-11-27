// ratelimit.go 頻率限制與失敗計數:都在記憶體裡、有上限。
//   - rateWindow:固定視窗(1 分鐘)的「每個來源、全部」兩層上限;各個入口(配對、LDAP 登入、evoleap 登入、OAuth …)各用一個,互不影響
//   - failCounter:每個鍵(IP 或帳號)一輪內的失敗次數;表有上限,滿了只淘汰最不重要的項目,不會整張清空

package service

import (
	"sort"
	"sync"
	"time"
)

// 固定視窗的頻率限制(每個來源、整個入口各一個上限,視窗 1 分鐘;只有放行的請求才佔名額,所以 map 不會超過 totalMax 個)
type rateWindow struct {
	mu     sync.Mutex
	window time.Time
	per    map[string]int
	all    int
}

func (r *rateWindow) allow(now time.Time, key string, perMax, totalMax int) bool {
	_, ok := r.take(now, key, perMax, totalMax)
	return ok
}

// take 同 allow,另回這次記在哪個視窗(refund 用)
func (r *rateWindow) take(now time.Time, key string, perMax, totalMax int) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.per == nil || now.Sub(r.window) >= time.Minute {
		r.window, r.per, r.all = now, map[string]int{}, 0
	}
	if r.per[key] >= perMax || r.all >= totalMax {
		return r.window, false
	}
	r.per[key]++
	r.all++
	return r.window, true
}

// refund 退回 take 佔的那一個名額;視窗已經換掉就不用退
func (r *rateWindow) refund(window time.Time, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.window.Equal(window) || r.per[key] <= 0 {
		return
	}
	if r.per[key]--; r.per[key] == 0 {
		delete(r.per, key)
	}
	r.all--
}

// LoginRate 宿主自己的登入入口用的一份限額(上限同 allowLogin,和其他入口分開計)。
// 密碼驗證通過的那次會退回名額,只有沒通過的(含進行中的)佔名額
type LoginRate struct{ w rateWindow }

// loginLimits 各入口各自的限額,一個被灌爆不影響別的
type loginLimits struct {
	pair, ldap, evoleap, gitlab, gitlabToken rateWindow
}

// allowLogin 登入類入口的限額(每個來源 LoginPerIP、每個入口 LoginTotal,預設 10 與 60,每分鐘);w 是該入口自己的視窗
func (s *Service) allowLogin(w *rateWindow, ip string) bool {
	_, ok := s.takeLogin(w, ip)
	return ok
}

// takeLogin 同 allowLogin,另回記在哪個視窗(refund 用)
func (s *Service) takeLogin(w *rateWindow, ip string) (time.Time, bool) {
	per, total := s.LoginPerIP, s.LoginTotal
	if per == 0 {
		per = 10
	}
	if total == 0 {
		total = 60
	}
	return w.take(s.now(), limitKey(ip), per, total)
}

// quotaWindow 固定視窗的配額(每個鍵自己的視窗,從第一次使用起算 win);用在每個使用者每小時的上限。
// 表有上限:滿了先丟過期的,還是滿就淘汰最舊的一成(灌一堆不同的使用者鍵洗不掉別人的計數:使用者 id 要先通過登入驗證才會有鍵)
type quotaWindow struct {
	mu sync.Mutex
	m  map[string]failEntry
}

// quotaReq 一項要扣的配額:key 的這一輪用量加 n 不能超過 max
type quotaReq struct {
	key    string
	n, max int
}

// take 同時檢查並扣掉所有項目(有任何一項超過就一項都不扣)。超過回 (false, 最早要等多久)
func (q *quotaWindow) take(now time.Time, win time.Duration, reqs ...quotaReq) (bool, time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.m == nil {
		q.m = map[string]failEntry{}
	}
	cur := func(k string) failEntry {
		e, ok := q.m[k]
		if !ok || now.Sub(e.since) >= win {
			return failEntry{since: now}
		}
		return e
	}
	for _, r := range reqs {
		if e := cur(r.key); e.n+r.n > r.max {
			return false, e.since.Add(win).Sub(now)
		}
	}
	for _, r := range reqs {
		if _, ok := q.m[r.key]; !ok {
			q.makeRoom(now, win)
		}
		e := cur(r.key)
		e.n += r.n
		q.m[r.key] = e
	}
	return true, 0
}

func (q *quotaWindow) makeRoom(now time.Time, win time.Duration) {
	if len(q.m) < defaultFailCap {
		return
	}
	for k, e := range q.m {
		if now.Sub(e.since) >= win {
			delete(q.m, k)
		}
	}
	if len(q.m) < defaultFailCap {
		return
	}
	type kv struct {
		k string
		t time.Time
	}
	all := make([]kv, 0, len(q.m))
	for k, e := range q.m {
		all = append(all, kv{k, e.since})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	for _, x := range all[:defaultFailCap/10] {
		delete(q.m, x.k)
	}
}

const defaultFailCap = 10000

// failEntry 一個鍵這一輪的失敗次數;since 是這一輪開始的時間
type failEntry struct {
	n     int
	since time.Time
}

// failCounter 每個鍵一輪(win)內的失敗次數,最多 max 個鍵(0 = 10000)。
// 滿了先丟掉過期的;還是滿就淘汰失敗次數最少、同樣少時最舊的一成。已經失敗很多次(被限制)的鍵最後才會被淘汰,
// 所以灌一堆不同的鍵,洗不掉已被限制的人
type failCounter struct {
	mu  sync.Mutex
	m   map[string]failEntry
	max int
}

// add 記一次失敗,回傳這一輪第幾次
func (c *failCounter) add(key string, now time.Time, win time.Duration) int {
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
	e.n++
	c.m[key] = e
	return e.n
}

// get 這個鍵這一輪的失敗次數與起點(沒有或已過期回 0)
func (c *failCounter) get(key string, now time.Time, win time.Duration) (int, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.since) >= win {
		return 0, time.Time{}
	}
	return e.n, e.since
}

func (c *failCounter) clear(key string) {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}

func (c *failCounter) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// makeRoom 在鎖裡呼叫:新增一個鍵之前確保不超過上限
func (c *failCounter) makeRoom(now time.Time, win time.Duration) {
	max := c.max
	if max <= 0 {
		max = defaultFailCap
	}
	if len(c.m) < max {
		return
	}
	for k, e := range c.m {
		if now.Sub(e.since) >= win {
			delete(c.m, k)
		}
	}
	if len(c.m) < max {
		return
	}
	type kv struct {
		k string
		e failEntry
	}
	all := make([]kv, 0, len(c.m))
	for k, e := range c.m {
		all = append(all, kv{k, e})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].e.n != all[j].e.n {
			return all[i].e.n < all[j].e.n
		}
		return all[i].e.since.Before(all[j].e.since)
	})
	drop := max / 10
	if drop < 1 {
		drop = 1
	}
	for _, x := range all[:drop] {
		delete(c.m, x.k)
	}
}
