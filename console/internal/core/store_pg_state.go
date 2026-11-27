package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/config"
)

// MARK: 動態牆與範例資料的變更

const activityKeep = 1000

// mockCollections 範例資料裡要存進 omixor_state 的集合(動態牆另外存)
var mockCollections = []string{"people", "services", "servers", "repos", "pull_requests", "work_items", "pipeline_runs",
	"deployments", "alerts", "alert_rules", "logs", "dashboards", "incidents", "on_call", "automations",
	"automation_runs", "approvals", "integrations"}

type pgState struct {
	mu    sync.Mutex
	dirty map[string]bool
}

var pgSt = &pgState{dirty: map[string]bool{}}

// loadPersisted 啟動時:有存過的範例資料與動態牆就用存的,沒有就把種子寫進去(呼叫時不用拿鎖,App 還沒開始用)
func (a *App) loadPersisted() {
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgQuery(ctx, `select name, data::text from omixor_state`)
	if err != nil {
		log.Printf("讀範例資料失敗:%v", err)
		return
	}
	found := map[string]bool{}
	for rows.Next() {
		var name string
		var b []byte
		if rows.Scan(&name, &b) != nil {
			continue
		}
		var v any
		if json.Unmarshal(b, &v) != nil {
			continue
		}
		found[name] = true
		if name == "sprint" {
			a.Sprint, _ = v.(map[string]any)
			continue
		}
		list, _ := v.([]any)
		items := make([]map[string]any, 0, len(list))
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				items = append(items, m)
			}
		}
		a.C[name] = items
	}
	rows.Close()
	for _, c := range append(mockCollections, "sprint") {
		if !found[c] {
			pgSt.dirty[c] = true
		}
	}

	// 動態牆:最新的 activityKeep 筆
	arows, err := pgQuery(ctx, `select item::text from omixor_activity where coalesce(item->>'org', '') = '' order by at desc, seq desc limit $1`, activityKeep)
	if err == nil {
		var items []map[string]any
		for arows.Next() {
			var b []byte
			var m map[string]any
			if arows.Scan(&b) == nil && json.Unmarshal(b, &m) == nil {
				items = append(items, m)
			}
		}
		arows.Close()
		if len(items) > 0 {
			a.C["activity"] = items
		} else {
			// 第一次:把種子的動態寫進去(舊的在前)
			seed := a.C["activity"]
			for i := len(seed) - 1; i >= 0; i-- {
				activityInsert(seed[i])
			}
		}
	}
	a.saveDirty()
}

func activityInsert(item map[string]any) {
	id, _ := item["id"].(string)
	at, err := ParseTime(fmt.Sprint(item["date"]))
	if err != nil {
		at = time.Now()
	}
	ctx, cancel := pgCtx()
	defer cancel()
	if _, err := pgExec(ctx, `insert into omixor_activity (id, at, item) values ($1, $2, $3::jsonb) on conflict (id) do nothing`,
		id, at, string(Marshal(item))); err != nil && !errors.Is(err, errPGClosed) {
		log.Printf("存動態失敗:%v", err)
	}
}

// markDirty Changed 的主題是範例資料集合時,標記要存
func markDirty(topics []string) {
	if pgPool() == nil {
		return
	}
	pgSt.mu.Lock()
	for _, t := range topics {
		if t == "*" {
			for _, c := range mockCollections {
				pgSt.dirty[c] = true
			}
			pgSt.dirty["sprint"] = true
			continue
		}
		for _, c := range append(mockCollections, "sprint") {
			if c == t {
				pgSt.dirty[c] = true
			}
		}
	}
	pgSt.mu.Unlock()
}

// saveDirty 把有變的範例資料集合存起來(拿鎖複製,放鎖再寫)。
// 先拿 a.Mu 再拿 pgSt.mu(和切換組織時的 flushMockAsync 同順序),名稱依目前組織加前綴。
func (a *App) saveDirty() {
	a.Mu.Lock()
	pgSt.mu.Lock()
	names := make([]string, 0, len(pgSt.dirty))
	for n := range pgSt.dirty {
		names = append(names, n)
	}
	pgSt.dirty = map[string]bool{}
	pgSt.mu.Unlock()
	org := a.mockOrg()
	data := map[string][]byte{}
	for _, n := range names {
		if n == "sprint" {
			data[mockStateKey(org, n)] = Marshal(a.Sprint)
		} else {
			data[mockStateKey(org, n)] = Marshal(a.C[n])
		}
	}
	a.Mu.Unlock()
	if err := pgWriteState(data); err != nil {
		// 寫失敗(連線暫時有問題…):把這些名稱標回 dirty,下一輪再試(已關閉的資料庫也標,無害)
		pgSt.mu.Lock()
		for _, n := range names {
			pgSt.dirty[n] = true
		}
		pgSt.mu.Unlock()
	}
}

// pgWriteState 寫入 omixor_state(key 已含組織前綴);資料庫已關閉回 errPGClosed,寫入失敗會記 log 並回最後一個錯誤
func pgWriteState(data map[string][]byte) error {
	if len(data) == 0 {
		return nil
	}
	pool := pgPool()
	if pool == nil {
		return errPGClosed
	}
	ctx, cancel := pgCtx()
	defer cancel()
	var last error
	for n, b := range data {
		if _, err := pool.Exec(ctx, `insert into omixor_state (name, data, updated_at) values ($1, $2::jsonb, now())
			on conflict (name) do update set data = excluded.data, updated_at = now()`, n, string(b)); err != nil {
			log.Printf("存範例資料 %s 失敗:%v", n, err)
			last = err
		}
	}
	return last
}

// ActivityHistory 動態牆歷史(before 之前、最多 limit 筆);沒有資料庫時從記憶體拿
func ActivityHistory(a *App, before time.Time, limit int) []map[string]any {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if pgPool() == nil {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var out []map[string]any
		for _, it := range a.C["activity"] {
			if t, err := ParseTime(fmt.Sprint(it["date"])); err == nil && t.Before(before) {
				out = append(out, it)
				if len(out) >= limit {
					break
				}
			}
		}
		return out
	}
	a.Mu.Lock()
	org := activityOrg(a.mockOrg())
	a.Mu.Unlock()
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgQuery(ctx, `select item::text from omixor_activity where at < $1 and coalesce(item->>'org', '') = $3 order by at desc, seq desc limit $2`, before, limit, org)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var b []byte
		var m map[string]any
		if rows.Scan(&b) == nil && json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// pgTimer 背景計時器(每個 App 一個):定時存有變的範例資料、清過期 session。
// CloseDB 會先停掉所有計時器並等它們結束,才關連線池,所以計時器不會碰到已關閉(nil)的連線池。
type pgTimer struct {
	stop chan struct{}
	done chan struct{}
}

var (
	pgTimersMu sync.Mutex
	pgTimers   []*pgTimer
)

// startPGTimer 啟動計時器;every 是間隔
func startPGTimer(a *App, every time.Duration) {
	t := &pgTimer{stop: make(chan struct{}), done: make(chan struct{})}
	pgTimersMu.Lock()
	pgTimers = append(pgTimers, t)
	pgTimersMu.Unlock()
	go func() {
		defer close(t.done)
		tk := time.NewTicker(every)
		defer tk.Stop()
		n := 0
		for {
			select {
			case <-t.stop:
				return
			case <-tk.C:
			}
			a.saveDirty()
			if n++; n%60 == 0 {
				sessionSweep()
			}
		}
	}()
}

// stopPGTimers 停掉所有計時器並等它們結束(正在跑的那一輪會先做完)
func stopPGTimers() {
	pgTimersMu.Lock()
	ts := pgTimers
	pgTimers = nil
	pgTimersMu.Unlock()
	for _, t := range ts {
		close(t.stop)
	}
	for _, t := range ts {
		<-t.done
	}
}

func init() {
	// 每 10 秒存一次有變的範例資料、清過期 session
	OnStart(func(a *App) {
		if pgPool() == nil || config.Current().Bool(config.NoTimers) {
			return
		}
		startPGTimer(a, 10*time.Second)
	})
	RegisterAction("activity_history", func(a *App, args map[string]any) (any, error) {
		before := time.Now().Add(time.Minute)
		if s, ok := args["before"].(string); ok && s != "" {
			if t, err := ParseTime(s); err == nil {
				before = t
			}
		}
		limit := 100
		if n, ok := args["limit"].(float64); ok {
			limit = int(n)
		}
		return visibleActivityHistory(a, HiddenOf(args), before, limit), nil
	})
}

// SaveNow 立刻存(結束程式前、測試用)
func (a *App) SaveNow() {
	if pgPool() != nil {
		a.saveDirty()
	}
}

// OpenIssueDB 只為了匯入 Issue 連資料庫(不換存放處、不需要 OMIXOR_SECRET_KEY);Issue 的表由本機核心建(localCore → store.NewPGFromPool)
func OpenIssueDB(url string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("資料庫網址格式不對:%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("連不到資料庫:%w", err)
	}
	if err := ensurePGSchema(ctx, pool); err != nil {
		pool.Close()
		return err
	}
	pgPoolPtr.Store(pool)
	return nil
}
