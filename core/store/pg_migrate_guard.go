package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---- 啟動遷移的共用保護:整體預算、大表保護、鎖等待上限、session 鎖釋放、啟動彙總 ----
//
// 啟動遷移在 listen 之前同步執行(Cloud Run 等平台有啟動探測時限),所以:
//   - 整體預算 migrateStartupBudget(預設 10 分鐘)由所有階段共用:每個階段自己的逾時(拆表 30、外鍵 15、Issue 編號 10、序號 10 分鐘)
//     與「剩餘預算」取較短,用完就剩下的階段全部只警告略過(不讓啟動失敗),下次啟動接著做。
//   - 會重寫整張表的 ALTER(stored 產生欄位)先估表大小,超過門檻就不自動做,只警告、等維護窗口手動腳本(sql/premigrate.sql)。
//   - 等任何遷移鎖(advisory lock)最多 migrateLockWait,超過就警告略過這一輪,不把預算全花在等鎖。

var (
	// migrateStartupBudget 啟動遷移的整體預算(所有階段共用)。變數是為了測試與設定覆寫
	migrateStartupBudget = 10 * time.Minute
	// migrateLockWait 等遷移用 advisory lock 的上限
	migrateLockWait = 2 * time.Minute
	// maxAutoRewriteBytes 會重寫整張表的 ALTER,表(含索引與 TOAST,pg_total_relation_size)超過這個大小就不自動做;0 = 不限制。
	// 預設 1 GiB:重寫期間該表被 ACCESS EXCLUSIVE 鎖住,讀寫全擋;雲端託管 PG(磁碟與 WAL 都有限速)重寫連同索引重建
	// 實務上約 10–50 MB/s,1 GiB 約 20 秒到 2 分鐘,仍遠小於單步上限(migrateStepTimeout 10 分鐘),
	// 失敗(逾時回滾)的風險小;再大就可能被擋數分鐘甚至逾時回滾、下次又重來。
	maxAutoRewriteBytes int64 = 1024 << 20
	// fkBackgroundRetryEvery 背景補外鍵、重試被鎖擋住的 validate 的間隔
	fkBackgroundRetryEvery = 5 * time.Minute
)

// SetMigrationLimits 由程式啟動時依設定呼叫(NewPG 之前):maxAutoRewriteMB(0 = 不限制)、budgetMinutes(<= 0 = 維持預設)
func SetMigrationLimits(maxAutoRewriteMB, budgetMinutes int) {
	if maxAutoRewriteMB >= 0 {
		maxAutoRewriteBytes = int64(maxAutoRewriteMB) << 20
	}
	if budgetMinutes > 0 {
		migrateStartupBudget = time.Duration(budgetMinutes) * time.Minute
	}
}

// migNotes 啟動遷移期間的共用狀態(整體預算的截止時間)
type migNotes struct {
	mu       sync.Mutex
	deadline time.Time
	skips    map[string]string // 這次啟動被略過、沒做完的步驟 → 原因(後面的步驟需要它時,失敗訊息要指到真因)
}

// setSkip 記下一個步驟被略過(why 空 = 清掉)
func (n *migNotes) setSkip(step, why string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if why == "" {
		delete(n.skips, step)
		return
	}
	if n.skips == nil {
		n.skips = map[string]string{}
	}
	n.skips[step] = why
}

func (n *migNotes) skipOf(step string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.skips[step]
}

// skipList 全部被略過的步驟(依名稱排序,「步驟:原因」)
func (n *migNotes) skipList() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for k, v := range n.skips {
		out = append(out, k+":"+v)
	}
	sort.Strings(out)
	return out
}

func (n *migNotes) begin(budget time.Duration) {
	n.mu.Lock()
	n.deadline = time.Now().Add(budget)
	n.mu.Unlock()
}

func (n *migNotes) end() {
	n.mu.Lock()
	n.deadline = time.Time{}
	n.mu.Unlock()
}

func (n *migNotes) remaining() (time.Duration, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.deadline.IsZero() {
		return 0, false
	}
	return time.Until(n.deadline), true
}

// budgetExpired 啟動整體預算是否已用完(只在啟動期間有意義)
func (p *PG) budgetExpired() bool {
	rem, ok := p.note.remaining()
	return ok && rem <= 0
}

// migCtx 一個遷移階段的 context:自己的逾時與啟動整體預算(剩餘)取較短
func (p *PG) migCtx(own time.Duration) (context.Context, context.CancelFunc) {
	d := own
	if rem, ok := p.note.remaining(); ok && rem < d {
		d = rem
	}
	if d < 0 {
		d = 0
	}
	return context.WithTimeout(context.Background(), d)
}

// startupMigrations NewPG / NewPGFromPool 共用的遷移序列。各階段只有在「整體預算已用完」時
// 才把錯誤降為警告(預算用完時各階段的查詢會逾時),否則維持原本的行為(結構性錯誤讓啟動失敗)
func (p *PG) startupMigrations() error {
	p.note.begin(migrateStartupBudget)
	defer p.note.end()
	steps := []struct {
		msg  string
		soft bool
		fn   func() error
	}{
		{"文件櫃結構遷移失敗", true, p.migrateCabinetLayouts},
		{"頻道、組織鏈拆表遷移失敗", true, p.migrateChannelSplit},
		{"文件標籤、角色權限與例行任務拆表遷移失敗", true, p.migrateSplitDocsAgents},
		{"事件、任務拆表遷移失敗", true, p.migrateSplitIncidentsTasks},
		{"actor 表遷移失敗", true, p.migrateActors},        // ISS-36 B-2a:拆表之後、外鍵之前;只建 omixor_actors 與系統 actor,不碰既有表
		{"使用者 actor 遷移失敗", true, p.migrateActorUsers}, // ISS-36 B-2b:omixor_users.actor_id、補建 usr_ actor、複合外鍵;使用者 id 不改寫(D11)
		{"角色 actor 遷移失敗", true, p.migrateAgentActors}, // ISS-36 B-2c:omixor_agents.actor_id、為既有角色建 actor、唯一約束與複合外鍵
		{"actor 別名表遷移失敗", true, p.migrateActorAliases}, // ISS-36 C-1:只建一張新的空表 omixor_actor_aliases,不碰既有表
		{"外鍵遷移失敗", true, p.migrateForeignKeys},
		{"Issue 編號遷移失敗", true, p.migrateIssueKeys},
		{"編號序號遷移失敗", true, p.migrateSeq},
		// ISS-36:22 個「誰」的欄位轉成 actor id 並加外鍵。舊結構的資料庫在這裡原地轉換;全新資料庫的表本來就是最終結構,這裡只補完成標記與外鍵(空表,瞬間)。
		// 放在最後(三個大動作不爭同一張表),外鍵要趕在 finishStartup 開背景 validate 之前加。
		// 不是 soft:程式只認轉換後的結構,沒轉完就不能服務(migrateActorColumnsStep 回錯誤,進度保留、重啟接著做)
		{"actor 欄位遷移失敗", false, p.migrateActorColumnsStep},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			if s.soft && p.budgetExpired() {
				p.note.setSkip(s.msg, err.Error())
				warnf("%s(啟動遷移整體預算 %v 已用完,略過這一段、服務照常啟動,下次啟動再做):%v", s.msg, migrateStartupBudget, err)
				continue
			}
			return fmt.Errorf("%s:%w", s.msg, err)
		}
	}
	p.logMigrationSummary()
	p.finishStartup()
	return nil
}

// ---- 大表保護 ----

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}

// tableBytes 表的大小(含索引、TOAST);表不存在回 0
func tableBytes(c context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table string) (int64, error) {
	var n int64
	err := q.QueryRow(c, `select coalesce(pg_total_relation_size(to_regclass($1)), 0)`, table).Scan(&n)
	return n, err
}

// oversized 這張表是否大到不該自動重寫(門檻 0 = 不限制)
func oversized(size int64) bool { return maxAutoRewriteBytes > 0 && size > maxAutoRewriteBytes }

// rewriteAllowed 在會重寫整張表的 ALTER 之前呼叫:表太大就寫一行警告並回 false(呼叫端略過那張表、記為「需手動」)
func (p *PG) rewriteAllowed(c context.Context, table, what string) bool {
	if maxAutoRewriteBytes <= 0 {
		return true
	}
	size, err := tableBytes(c, p.pool, table)
	if err != nil {
		warnf("%s:查不出表 %s 的大小,為了安全不自動重寫(請在維護窗口先跑 sql/premigrate.sql 再重啟):%v", what, table, err)
		return false
	}
	if oversized(size) {
		warnf("%s:%s 約 %s(門檻 %s),自動重寫可能鎖表很久;請在維護窗口先跑 sql/premigrate.sql(或同等的 alter table 指令)再重啟;"+
			"要強制自動做,把 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB 調大或設 0", what, table, humanBytes(size), humanBytes(maxAutoRewriteBytes))
		return false
	}
	return true
}

// ---- 鎖等待、重試 ----

// jitter 退避時間加隨機(0.5 到 1.5 倍),多個實例同時重試不會同步撞在一起
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int63n(int64(d)))
}

// retryLock 遇到鎖逾時、死鎖、序列化失敗(isLockErr)就退避後重試,最多 attempts 次
func retryLock(c context.Context, attempts int, wait time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-c.Done():
				return c.Err()
			case <-time.After(jitter(wait * time.Duration(i))):
			}
		}
		if err = fn(); err == nil || !isLockErr(err) {
			return err
		}
	}
	return err
}

// errLockWait 等遷移鎖超過上限
var errLockWait = errors.New("等不到遷移鎖")

// xactLock 在交易裡取 advisory lock(交易結束才放),最多等 migrateLockWait,逾時回 errLockWait。拿到後 lock_timeout 還原成不限制
func xactLock(c context.Context, tx pgx.Tx, key string) error {
	if _, err := tx.Exec(c, fmt.Sprintf(`set local lock_timeout = '%dms'`, migrateLockWait.Milliseconds())); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "55P03" {
			return fmt.Errorf("%w(%s,等了 %v)", errLockWait, key, migrateLockWait)
		}
		return err
	}
	_, err := tx.Exec(c, `set local lock_timeout = 0`)
	return err
}

// closeConn 把一條連線從池裡拿出來關掉(session 層級的鎖隨連線結束一定釋放)
func closeConn(conn *pgxpool.Conn) {
	cc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Hijack().Close(cc)
}

// sessionLock 取 session 層級 advisory lock(獨立一條連線;只保護「同時只有一個實例在跑」,不包住任何交易)。
// 等鎖最多 migrateLockWait(輪詢 pg_try_advisory_lock,有隨機間隔),也受 c 限制;等不到回 ok=false 並已寫警告,
// 呼叫端略過這一輪、下次啟動再做。釋放(unlock)失敗或回 false 時整條連線關掉(鎖不會殘留在被池回收的連線上)。
// 沒有連線池本身(批次裡)回一個空的 unlock
func (p *PG) sessionLock(c context.Context, key, what string) (unlock func(), ok bool, err error) {
	if p.root == nil {
		return func() {}, true, nil
	}
	conn, err := p.root.Acquire(c)
	if err != nil {
		if isTimeout(c, err) {
			warnf("%s:等不到連線,略過這次(下次啟動再做):%v", what, err)
			return nil, false, nil
		}
		return nil, false, err
	}
	wc, cancel := context.WithTimeout(c, migrateLockWait)
	defer cancel()
	for {
		var got bool
		if err := conn.QueryRow(wc, `select pg_try_advisory_lock(hashtext($1))`, key).Scan(&got); err != nil {
			closeConn(conn) // 查詢被取消時鎖有沒有拿到不確定:連線關掉最保險
			if isTimeout(wc, err) {
				warnf("%s:等不到遷移鎖(%s,等了 %v),略過這次(下次啟動再做)", what, key, migrateLockWait)
				return nil, false, nil
			}
			return nil, false, err
		}
		if got {
			break
		}
		select {
		case <-wc.Done():
			conn.Release()
			warnf("%s:等不到遷移鎖(%s,等了 %v;別的實例還在遷移?),略過這次(下次啟動再做)", what, key, migrateLockWait)
			return nil, false, nil
		case <-time.After(jitter(400 * time.Millisecond)):
		}
	}
	return func() {
		uc, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ucancel()
		var released bool
		if err := conn.QueryRow(uc, `select pg_advisory_unlock(hashtext($1))`, key).Scan(&released); err != nil || !released {
			warnf("%s:釋放遷移鎖失敗(%v),關閉那條連線以確保鎖釋放", what, err)
			closeConn(conn)
			return
		}
		conn.Release()
	}, true, nil
}

// ---- 啟動彙總 ----

// migProgress 遷移完成度(輕量:只查 information_schema 與 pg_constraint,不掃資料表)
type migProgress struct {
	MissingProjectRef []string // 還沒建 project_ref 的表
	MissingFKs        []string // 還沒加的外鍵(fkSpecs)
	UnvalidatedFKs    []string // 外鍵已加、還沒 validate
	ManualTables      []string // 缺 project_ref、而且表超過大小門檻 → 要在維護窗口跑 sql/premigrate.sql("表(約 N)")
	// ActorColumnsPending 還沒轉成 actor id 的「誰」欄位(表.欄位)
	ActorColumnsPending []string
}

func (p *PG) migrationProgress(c context.Context) (migProgress, error) {
	var pr migProgress
	missingTables := map[string]bool{}
	for _, t := range projectRefTables {
		ok, err := p.columnExists(c, t, "project_ref")
		if err != nil {
			return pr, err
		}
		if !ok {
			pr.MissingProjectRef = append(pr.MissingProjectRef, t)
			missingTables[t] = true
		}
	}
	cons := map[string]bool{} // 外鍵名稱 → 已驗證
	rows, err := p.pool.Query(c, `select conname, convalidated from pg_constraint where contype = 'f' and conname like 'fk\_%'`)
	if err != nil {
		return pr, err
	}
	for rows.Next() {
		var n string
		var v bool
		if err := rows.Scan(&n, &v); err != nil {
			rows.Close()
			return pr, err
		}
		cons[n] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return pr, err
	}
	for _, f := range fkSpecs() {
		if !p.fkSpecReady(c, f) { // actor 欄位還沒轉換完成:外鍵本來就還不能加,不算缺
			continue
		}
		switch v, ex := cons[f.name]; {
		case !ex:
			pr.MissingFKs = append(pr.MissingFKs, f.name)
		case !v:
			pr.UnvalidatedFKs = append(pr.UnvalidatedFKs, f.name)
		}
	}
	{
		l, err := p.actorColumnsPending(c)
		if err != nil {
			return pr, err
		}
		pr.ActorColumnsPending = l
	}
	if maxAutoRewriteBytes > 0 {
		tabs := make([]string, 0, len(missingTables))
		for t := range missingTables {
			tabs = append(tabs, t)
		}
		sort.Strings(tabs)
		for _, t := range tabs {
			if size, err := tableBytes(c, p.pool, t); err == nil && oversized(size) {
				pr.ManualTables = append(pr.ManualTables, fmt.Sprintf("%s(約 %s)", t, humanBytes(size)))
			}
		}
	}
	return pr, nil
}

// logMigrationSummary 啟動結尾彙總一行:還有什麼沒做完(缺 project_ref、缺外鍵、需手動的大表)。
// 全部完成(只剩背景驗證中)就只寫一行一般 log。查詢失敗只略過,不影響啟動
func (p *PG) logMigrationSummary() {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pr, err := p.migrationProgress(c)
	if err != nil {
		log.Printf("[遷移] 彙總略過(查不出完成度):%v", err)
		return
	}
	var parts []string
	add := func(label string, l []string) {
		if len(l) > 0 {
			parts = append(parts, fmt.Sprintf("%s %d(%s)", label, len(l), strings.Join(l, " ")))
		}
	}
	add("缺 project_ref", pr.MissingProjectRef)
	add("需在維護窗口手動處理的大表", pr.ManualTables)
	add("缺外鍵", pr.MissingFKs)
	add("尚未轉成 actor id 的欄位", pr.ActorColumnsPending)
	if len(parts) > 0 {
		warnf("啟動遷移尚未完成(服務照常啟動;缺外鍵會在背景每 %v 重試,缺欄位要重啟或先跑維護窗口腳本):%s", fkBackgroundRetryEvery, strings.Join(parts, ";"))
		return
	}
	if n := len(pr.UnvalidatedFKs); n > 0 {
		log.Printf("[遷移] 欄位與外鍵都已就位,%d 條外鍵正在背景驗證", n)
		return
	}
	log.Printf("[遷移] 欄位與外鍵都已就位且已驗證")
}
