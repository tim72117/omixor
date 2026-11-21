package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- jsonb 欄位拆表的啟動遷移:共用的批次驅動、寫入保護、未展開列統計 ----
//
// 這一輪(頻道成員、訊息提及、組織鏈的邊、文件標籤、角色權限與例行任務、任務備註與連結、事件、文件櫃結構)把 jsonb 欄位拆成資料表。
// 各表有 split_at 欄位:NULL = 還沒展開到新表。
//
// 遷移策略(啟動時,冪等):
//   - 整個遷移在一個 advisory lock 內(多個執行個體同時啟動時排隊,後到的發現沒東西要做就直接略過),
//     用獨立的長逾時 migrateTimeout(預設 30 分鐘;和一般查詢的 15 秒 ctx() 無關)。
//   - 先用 `select 1 … where split_at is null limit 1` 判斷還有沒有未展開列(有部分索引 <表>_unsplit,沒有就是瞬間),沒有就略過。
//   - 有就分批:每批 migrateBatchSize 列(依主鍵 keyset 分頁)一個交易。批內沒有子資料的列(例如空陣列)用一個語句標記 split_at;
//     其他列各用一個 savepoint 展開,單列資料壞(不是合格的 JSON 結構、外鍵不成立、資料例外)只回滾那一列,不影響同批其他列。
//   - 無損可修的資料遷移時直接修:成員、提及、標籤的重複去重(留第一個);例行任務 id 空白或重複補新 uuid(第一個保留原 id)。
//   - 真的無法遷移的列(壞 JSON、型別不符)略過、split_at 保持 NULL,記 `[遷移警告]` log;結束時彙總未展開列數。
//   - 逾時或未完成只記 `[遷移警告]` 不讓啟動失敗,下次啟動接著做(已標記的列不會再碰)。
//
// 未展開列(split_at 為 NULL)的處理:
//   - 讀取:退回讀舊的 jsonb 欄位(只讀,不寫新表);舊 jsonb 結構壞到解不出來的那一欄回空(文件櫃回明確錯誤)。
//   - 寫入:寫入前先在同一個交易裡嘗試展開那一列(guardUnsplit);展開不了就回 ErrUnmigratedRow,不靜默覆寫舊資料。
//
// 不支援回滾:新版不再寫舊 jsonb 欄位,回滾到舊版會看不到新版期間寫的資料;要回滾只能從備份還原。細節與 SQL 見 MIGRATIONS.md。

var (
	// migrateBatchSize 每批列數(變數,測試用小數字驗證分批)
	migrateBatchSize = 500
	// migrateTimeout 整個啟動遷移的逾時
	migrateTimeout = 30 * time.Minute
	// splitBatchRetries 一批遇到鎖逾時、死鎖時最多試幾次
	splitBatchRetries = 4
	// onSplitBatch 每處理完一批(交易 commit 前)呼叫一次;只有測試用來驗證分批
	onSplitBatch func(table string, rows int)
)

// ErrUnmigratedRow 寫入的那一列還沒展開到新表,而舊 jsonb 資料不合格、展開不了:拒絕寫入(寫了會蓋掉舊資料)。先修好那一列的舊資料再重試
var ErrUnmigratedRow = errors.New("這一列的舊格式資料不合格,尚未遷移到新表,不能寫入(請先修復舊資料,見 core/store/MIGRATIONS.md)")

// errSkip 遷移單列時回這個:這一列不合格,回滾那一列、繼續下一列
var errSkip = errors.New("skip")

func skipErr(err error) error { return fmt.Errorf("%w:%v", errSkip, err) }

// migState 各表是否還有未展開的列(每次啟動遷移結束時重算;全部沒有,讀取、寫入就都不必做退回與保護)
type migState struct {
	mu    sync.Mutex
	left  map[string]bool // 表 → 還有未展開的列
	total atomic.Bool     // left 裡有任何一個 true
	conv  convCache       // 「誰」欄位是否已轉成 actor id(見 pg_actor_bridge.go)
}

func (m *migState) set(table string, left bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.left == nil {
		m.left = map[string]bool{}
	}
	m.left[table] = left
	any := false
	for _, v := range m.left {
		any = any || v
	}
	m.total.Store(any)
}

func (p *PG) hasUnsplit() bool { return p.mig != nil && p.mig.total.Load() }

// splitSpec 一張有 split_at 的父表怎麼遷移
type splitSpec struct {
	name   string
	table  string
	keys   []string // 主鍵欄位(都是 text)
	empty  string   // SQL 條件:這一列沒有子資料,只要標記 split_at(空字串 = 沒有這種快速路徑)
	expand func(p *PG, c context.Context, tx pgx.Tx, key []string) error
	// expand:鎖住那一列(for update)、確認 split_at 仍為 NULL、展開並把 split_at 設成 now();
	// 已被別人展開回 nil;不合格回 errSkip(用 skipErr)
}

// allSplitSpecs 全部要遷移的表(順序無關;統計用)
func allSplitSpecs() []*splitSpec {
	return []*splitSpec{splitCabinet, splitChannels, splitMessages, splitChains, splitDocs, splitAgents, splitTasks, splitIncidents}
}

// isSkippable 這個錯誤是「這一列資料本身的問題」(略過那一列):errSkip、SQLSTATE 22 資料例外、23 完整性(外鍵、重複)
func isSkippable(err error) bool {
	if errors.Is(err, errSkip) {
		return true
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && len(pe.Code) >= 2 && (pe.Code[:2] == "22" || pe.Code[:2] == "23")
}

func isFKViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

func isTimeout(c context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || (c != nil && c.Err() != nil)
}

func warnf(format string, args ...any) { log.Printf("[遷移警告] "+format, args...) }

// migrationLock 取得啟動遷移的 advisory lock(等到拿到或逾時);回傳解鎖函式
func (p *PG) migrationLock(c context.Context) (func(), error) {
	tx, err := p.pool.Begin(c)
	if err != nil {
		return nil, err
	}
	if err := xactLock(c, tx, "omixor-split-migrate"); err != nil { // 等鎖有獨立上限(migrateLockWait)
		rc, cancel := ctx()
		defer cancel()
		_ = tx.Rollback(rc)
		return nil, err
	}
	return func() {
		rc, cancel := ctx()
		defer cancel()
		_ = tx.Rollback(rc)
	}, nil
}

// splitRun 在 advisory lock 內(先跑 pre,例如建欄位與子表)依序遷移 specs;逾時只告警
func (p *PG) splitRun(pre func(c context.Context) error, specs ...*splitSpec) error {
	c, cancel := p.migCtx(migrateTimeout) // 自己的逾時與啟動整體預算(剩餘)取較短
	defer cancel()
	defer p.refreshUnsplit(specs...)
	skipKey := "拆表遷移 " + specs[0].name
	p.note.setSkip(skipKey, "")
	unlock, err := p.migrationLock(c)
	if err != nil {
		if isTimeout(c, err) || errors.Is(err, errLockWait) {
			warnf("等不到遷移鎖,略過這次遷移(下次啟動再做):%v", err)
			p.note.setSkip(skipKey, "等不到遷移鎖或啟動預算用完")
			return nil
		}
		return err
	}
	defer unlock()
	if pre != nil {
		if err := pre(c); err != nil {
			if isTimeout(c, err) {
				warnf("遷移前置步驟逾時,略過這次遷移(下次啟動再做):%v", err)
				p.note.setSkip(skipKey, "前置步驟逾時或啟動預算用完")
				return nil
			}
			return err
		}
	}
	for _, s := range specs {
		if err := p.ensureUnsplitIndex(c, s); err != nil {
			if isTimeout(c, err) {
				warnf("%s:逾時,剩下的下次啟動再做:%v", s.name, err)
				p.note.setSkip(skipKey, "逾時或啟動預算用完")
				return nil
			}
			return fmt.Errorf("%s:%w", s.name, err)
		}
	}
	for _, s := range specs {
		n, err := p.runSplit(c, s)
		if err != nil {
			if isTimeout(c, err) {
				warnf("%s:逾時(%v),剩下的下次啟動再做:%v", s.name, migrateTimeout, err)
				p.note.setSkip(skipKey, "逾時或啟動預算用完,有的列還沒展開")
				return nil
			}
			return fmt.Errorf("%s:%w", s.name, err)
		}
		if n > 0 {
			warnf("%s:有 %d 列的舊資料不合格,略過(split_at 保持 NULL;讀取退回舊 jsonb、寫入會被拒絕)", s.name, n)
		}
	}
	return nil
}

// ensureUnsplitIndex 部分索引 <表>_unsplit(只含 split_at 為 NULL 的列):「還有沒有未展開列」是瞬間的,不必全表掃描。先查有沒有再建(避免白拿鎖)
func (p *PG) ensureUnsplitIndex(c context.Context, s *splitSpec) error {
	name := s.table + "_unsplit"
	var one int
	err := p.pool.QueryRow(c, `select 1 from pg_indexes where schemaname = current_schema() and indexname = $1`, name).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = p.pool.Exec(c, fmt.Sprintf(`create index if not exists %s on %s (%s) where split_at is null`, name, s.table, strings.Join(s.keys, ", ")))
	return err
}

func queryKeys(c context.Context, q queryer, sql string, args ...any) ([][]string, error) {
	rows, err := q.Query(c, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var k []string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// runSplit 分批展開一張表,回傳略過的列數
func (p *PG) runSplit(c context.Context, s *splitSpec) (int, error) {
	var one int
	err := p.pool.QueryRow(c, fmt.Sprintf(`select 1 from %s where split_at is null limit 1`, s.table)).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cols := strings.Join(s.keys, ", ")
	arr := "array[" + cols + "]::text[]"
	skipped := 0
	var last []string
	for {
		sql := fmt.Sprintf(`select %s from %s where split_at is null order by %s limit %d`, arr, s.table, cols, migrateBatchSize)
		var args []any
		if last != nil {
			ph := make([]string, len(last))
			for i := range last {
				ph[i] = fmt.Sprintf("$%d", i+1)
				args = append(args, last[i])
			}
			sql = fmt.Sprintf(`select %s from %s where split_at is null and (%s) > (%s) order by %s limit %d`,
				arr, s.table, cols, strings.Join(ph, ", "), cols, migrateBatchSize)
		}
		keys, err := queryKeys(c, p.pool, sql, args...)
		if err != nil {
			return skipped, err
		}
		if len(keys) == 0 {
			return skipped, nil
		}
		last = keys[len(keys)-1]
		// 多個實例同時啟動時,批次(寫子表)與別的實例補外鍵(add constraint 對子表與被參照表的 SHARE ROW EXCLUSIVE)
		// 鎖順序可能相反而被 PG 判死鎖(40P01):整批已回滾,重試即可,不要讓整個遷移(啟動)失敗
		var n int
		err = retryLock(c, splitBatchRetries, time.Second, func() error {
			var err error
			n, err = p.splitBatch(c, s, keys)
			return err
		})
		skipped += n
		if err != nil {
			return skipped, err
		}
	}
}

// splitBatch 一批(一個交易):沒有子資料的列一個語句標記,其他列各一個 savepoint 展開
func (p *PG) splitBatch(c context.Context, s *splitSpec, keys [][]string) (int, error) {
	cols := strings.Join(s.keys, ", ")
	colArrs := make([][]string, len(s.keys))
	casts := make([]string, len(s.keys))
	args := make([]any, len(s.keys))
	for i := range s.keys {
		for _, k := range keys {
			colArrs[i] = append(colArrs[i], k[i])
		}
		casts[i] = fmt.Sprintf("$%d::text[]", i+1)
		args[i] = colArrs[i]
	}
	inBatch := fmt.Sprintf(`(%s) in (select * from unnest(%s))`, cols, strings.Join(casts, ", "))
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if s.empty != "" {
		if _, err := tx.Exec(c, fmt.Sprintf(`update %s set split_at = now() where split_at is null and %s and (%s)`, s.table, inBatch, s.empty), args...); err != nil {
			return 0, err
		}
	}
	rest, err := queryKeys(c, tx, fmt.Sprintf(`select array[%s]::text[] from %s where split_at is null and %s`, cols, s.table, inBatch), args...)
	if err != nil {
		return 0, err
	}
	if onSplitBatch != nil {
		onSplitBatch(s.table, len(keys))
	}
	skipped := 0
	for _, k := range rest {
		sp, err := tx.Begin(c)
		if err != nil {
			return skipped, err
		}
		err = s.expand(p, c, sp, k)
		if err == nil {
			if err := sp.Commit(c); err != nil {
				return skipped, err
			}
			continue
		}
		_ = sp.Rollback(c)
		if !isSkippable(err) {
			return skipped, fmt.Errorf("%s:%w", strings.Join(k, "/"), err)
		}
		skipped++
		warnf("%s:略過 %s(%v)", s.name, strings.Join(k, "/"), err)
	}
	return skipped, tx.Commit(c)
}

// guardUnsplit 寫入前的保護:那一列若還沒展開(split_at 為 NULL),先在這個交易裡嘗試展開;展開不了回 ErrUnmigratedRow。
// 沒有未展開列(絕大多數情況)時零成本。key 順序同 spec.keys
func (p *PG) guardUnsplit(c context.Context, tx pgx.Tx, s *splitSpec, key ...string) error {
	if !p.hasUnsplit() {
		return nil
	}
	conds := make([]string, len(s.keys))
	args := make([]any, len(s.keys))
	for i, k := range s.keys {
		conds[i] = fmt.Sprintf("%s = $%d", k, i+1)
		args[i] = key[i]
	}
	var one int
	err := tx.QueryRow(c, fmt.Sprintf(`select 1 from %s where %s and split_at is null`, s.table, strings.Join(conds, " and ")), args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	sp, err := tx.Begin(c)
	if err != nil {
		return err
	}
	err = s.expand(p, c, sp, key)
	if err == nil {
		return sp.Commit(c)
	}
	_ = sp.Rollback(c)
	if isSkippable(err) {
		return fmt.Errorf("%w:%s %s(%v)", ErrUnmigratedRow, s.name, strings.Join(key, "/"), err)
	}
	return err
}

// UnmigratedRowsByTable 各表還沒展開(split_at 為 NULL)的列數;全部為 0 才可以進下個版本(drop 舊欄位)。健康檢查、測試用
func (p *PG) UnmigratedRowsByTable() (map[string]int, error) { return p.unmigrated(allSplitSpecs()) }

func (p *PG) unmigrated(specs []*splitSpec) (map[string]int, error) {
	c, cancel := ctx()
	defer cancel()
	out := map[string]int{}
	for _, s := range specs {
		var n int
		if err := p.pool.QueryRow(c, fmt.Sprintf(`select count(*) from %s where split_at is null`, s.table)).Scan(&n); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "42703" { // 欄位還沒建(這張表的遷移還沒跑到):沒有展開過的列可言
				out[s.table] = 0
				continue
			}
			return nil, fmt.Errorf("%s:%w", s.table, err)
		}
		out[s.table] = n
	}
	return out, nil
}

// UnmigratedRows 還沒展開的列總數(0 = 遷移完成)
func (p *PG) UnmigratedRows() (int, error) {
	m, err := p.UnmigratedRowsByTable()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, n := range m {
		total += n
	}
	return total, nil
}

// refreshUnsplit 這一輪遷移結束時重算 specs 各表「還有沒有未展開列」,並彙總告警
func (p *PG) refreshUnsplit(specs ...*splitSpec) {
	if p.mig == nil {
		return
	}
	m, err := p.unmigrated(specs)
	if err != nil {
		// 查不出來就保守地當作還有(讀取退回與寫入保護多做一點事,不會錯)
		warnf("統計未展開列數失敗:%v", err)
		for _, s := range specs {
			p.mig.set(s.table, true)
		}
		return
	}
	var parts []string
	total := 0
	for _, s := range specs {
		n := m[s.table]
		p.mig.set(s.table, n > 0)
		if n > 0 {
			total += n
			parts = append(parts, fmt.Sprintf("%s=%d", s.table, n))
		}
	}
	if total > 0 {
		warnf("共 %d 列尚未展開(%s);讀取退回舊 jsonb、寫入會被拒絕,修好舊資料後重啟即可", total, strings.Join(parts, ", "))
	}
}

// newRoutineID 補例行任務 id 用(格式同 service 的 uuid)
func newRoutineID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
