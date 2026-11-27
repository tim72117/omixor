package core

import (
	"net/http"
	"sync"
	"time"
)

// 系統工作組織的讀寫鎖:
//   - 讀鎖:登入者(網頁、OAuth)用原本 App 處理的呼叫,期間讀寫的就是檢查過的那個組織
//   - 寫鎖:換系統工作組織。只在真正換的那一小段拿(在 a.Mu 之前),不跨讀請求內容、等人操作、外部服務登入
// 和 sync.RWMutex 不同:換組織最多等 SystemSwitchWait,等不到就放棄(回 409),不會因為某個很慢的請求,
// 讓排隊的寫鎖把之後所有請求一直擋住。要長時間等待(開瀏覽器登入…)的動作用 away 先放掉讀鎖。

// SystemSwitchWait 換系統工作組織最多等進行中的請求多久;這段期間新的請求也跟著等
var SystemSwitchWait = 5 * time.Second

// SystemGate 見檔頭
type SystemGate struct {
	mu      sync.Mutex
	readers int
	writing bool
	waiting int
	// changed 狀態變了就關掉(喚醒等待的人)
	changed chan struct{}
}

func (g *SystemGate) waitCh() chan struct{} {
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	return g.changed
}

func (g *SystemGate) wake() {
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
}

// RLock 有人在換、或在等著換時先等(最多 SystemSwitchWait 加上換的時間)
func (g *SystemGate) RLock() {
	for {
		g.mu.Lock()
		if !g.writing && g.waiting == 0 {
			g.readers++
			g.mu.Unlock()
			return
		}
		ch := g.waitCh()
		g.mu.Unlock()
		<-ch
	}
}

func (g *SystemGate) RUnlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.readers--
	g.wake()
}

// LockWithin 拿寫鎖,最多等 d;false = 等不到(沒有拿到)
func (g *SystemGate) LockWithin(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	g.mu.Lock()
	g.waiting++
	for g.writing || g.readers > 0 {
		ch := g.waitCh()
		g.mu.Unlock()
		select {
		case <-ch:
			g.mu.Lock()
		case <-t.C:
			g.mu.Lock()
			g.waiting--
			g.wake()
			g.mu.Unlock()
			return false
		}
	}
	g.waiting--
	g.writing = true
	g.mu.Unlock()
	return true
}

func (g *SystemGate) Unlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writing = false
	g.wake()
}

var errSystemBusy = StatusError{Status: http.StatusConflict, Message: "還有其他請求在處理中,暫時不能換系統工作組織,請稍後再試"}

// switching 拿著寫鎖執行 f(換系統工作組織的那一段)。不要拿著 a.Mu 呼叫(f 裡自己拿)
func (a *App) switching(f func() error) error {
	g := a.SystemOrgLock()
	if !g.LockWithin(SystemSwitchWait) {
		return errSystemBusy
	}
	defer g.Unlock()
	return f()
}

// SystemPin 拿著系統工作組織讀鎖的請求(web 放在 args[PinKey])。
// Away 放掉讀鎖執行 wait、再拿回來;回傳系統工作組織是不是還是同一個(false 時不能再讀寫組織資料)
type SystemPin interface{ Away(wait func()) (same bool) }

// PinKey 見 SystemPin
const PinKey = "_pin"

// away 執行要等很久的 wait(等人在瀏覽器操作…);沒有拿著讀鎖(沒有登入者)時直接執行
func away(args map[string]any, wait func()) bool {
	if p, ok := args[PinKey].(SystemPin); ok {
		return p.Away(wait)
	}
	wait()
	return true
}

var errSystemSwitched = StatusError{Status: http.StatusConflict, Message: "等待期間系統工作組織換了,結果沒有存;請重新整理後再做一次"}
