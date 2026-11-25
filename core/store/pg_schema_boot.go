package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/model"
)

// pg_schema_boot.go 啟動時的結構建立與版本化的結構異動(bootstrapSchema、runSteps)。步驟清單與新增程序見 schema_steps.go。
//
// 三種情況(空或非空只看核心表,規則見 schema_core.go;宿主程式自己的 omixor_ 表不算):
//   - 空資料庫:一個交易建完整的最終結構(pgSchema、fkSpecs 的外鍵、固定的系統 actor),並寫入版本記錄:
//     baseline(版本 1)與 schemaSteps 的所有版本(全新安裝已經是最終結構,不執行步驟)。
//   - 已有核心表、沒有版本表(導入版本表之前的資料庫):建版本表、蓋上 baseline(版本 1),再依版本執行步驟。
//   - 有版本表:讀出目前最大版本,依序執行版本更大的步驟。每個步驟自己一個交易,成功才寫版本記錄。
//
// 並行:所有會寫結構的交易先拿 advisory lock(schemaLockKey),拿到後重新讀狀態,所以多個實例同時啟動只會有一個動手。
// 步驟交易裡 set local lock_timeout(預設 10 秒),鎖逾時或死鎖(55P03、40P01、40001)隨機退避後重試幾次。
// 啟動整段用較長的 context(bootstrapTimeout),不受 NewPG 查詢用的 15 秒限制。

const (
	// schemaLockKey 建表與步驟的交易層級 advisory lock(多個實例同時啟動時排隊)
	schemaLockKey = "omixor-schema-create"
	// bootstrapTimeout 啟動時建表與套用步驟的整段上限(空資料庫一個大交易要建 48 張表與約 60 條外鍵)
	bootstrapTimeout = 2 * time.Minute
)

// 變數是為了測試能縮短
var (
	schemaLockWait   = 2 * time.Minute  // 等 advisory lock 的上限(別的實例的步驟可能正在跑)
	stepLockTimeout  = 10 * time.Second // 步驟自己的 DDL 等表鎖的上限
	stepAttempts     = 5                // 鎖逾時、死鎖時,一個步驟最多嘗試幾次
	stepRetryBackoff = time.Second      // 重試的退避基數(乘以第幾次,再加隨機)
)

// bootstrapCtx 啟動建表與套用步驟用的 context
func bootstrapCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), bootstrapTimeout)
}

type schemaQ interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type rowQ interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// currentSchemaVersion 目前資料庫記錄的最大版本;exists = 有沒有版本表(沒有表或表是空的,版本是 0)
func currentSchemaVersion(c context.Context, q rowQ) (version int, exists bool, err error) {
	if err := q.QueryRow(c, `select to_regclass($1) is not null`, schemaVersionTable).Scan(&exists); err != nil {
		return 0, false, fmt.Errorf("查版本表失敗:%w", err)
	}
	if !exists {
		return 0, false, nil
	}
	if err := q.QueryRow(c, `select coalesce(max(version), 0) from `+schemaVersionTable).Scan(&version); err != nil {
		return 0, true, fmt.Errorf("讀結構版本失敗:%w", err)
	}
	return version, true, nil
}

// bootstrapSchema 見檔頭
func bootstrapSchema(parent context.Context, pool *pgxpool.Pool) error {
	empty, err := coreSchemaEmpty(parent, pool)
	if err != nil {
		return err
	}
	if empty {
		created, err := createFreshSchema(parent, pool)
		if err != nil {
			return err
		}
		if !created { // 另一個實例剛建完(可能是還沒有版本表的舊版程式):補版本記錄
			err = stampBaseline(parent, pool)
		}
		if err != nil {
			return err
		}
		// 建好了:版本記錄已經是最新,runSteps 會發現沒有要做的
	} else if err := stampBaseline(parent, pool); err != nil {
		return err
	}
	if err := runSteps(parent, pool, schemaSteps); err != nil {
		return err
	}
	return syncSystemActors(parent, pool)
}

// lockSchema 在交易裡取 advisory lock(交易結束才放),最多等 schemaLockWait;拿到後把 lock_timeout 設成 after(0 = 不限制)
func lockSchema(c context.Context, tx pgx.Tx, after string) error {
	if _, err := tx.Exec(c, fmt.Sprintf(`set local lock_timeout = '%dms'`, schemaLockWait.Milliseconds())); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext($1))`, schemaLockKey); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "55P03" {
			return fmt.Errorf("等結構鎖逾時(%v;別的實例還在建表或做結構異動?):%w", schemaLockWait, err)
		}
		return fmt.Errorf("等結構鎖失敗:%w", err)
	}
	_, err := tx.Exec(c, fmt.Sprintf(`set local lock_timeout = '%s'`, after))
	return err
}

// createFreshSchema 空資料庫:一個交易建完最終結構並寫版本記錄。拿到鎖後重查一次,另一個實例剛建完就不做(created = false)
func createFreshSchema(c context.Context, pool *pgxpool.Pool) (created bool, err error) {
	tx, err := pool.Begin(c)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(c)
	if err := lockSchema(c, tx, "0"); err != nil {
		return false, err
	}
	empty, err := coreSchemaEmpty(c, tx)
	if err != nil || !empty {
		return false, err
	}
	if v, _, err := currentSchemaVersion(c, tx); err != nil {
		return false, err
	} else if v > 0 {
		warnf("版本表記錄版本 %d,但沒有任何核心資料表(先前啟動失敗留下的?),建立完整結構", v)
	}
	if _, err := tx.Exec(c, pgSchema); err != nil {
		return false, fmt.Errorf("建表失敗:%w", err)
	}
	for _, f := range fkSpecs() {
		if _, err := tx.Exec(c, fmt.Sprintf(`alter table %s add constraint %s %s`, f.table, f.name, f.def)); err != nil {
			return false, fmt.Errorf("建外鍵 %s 失敗:%w", f.name, err)
		}
	}
	if err := syncSystemActors(c, tx); err != nil {
		return false, err
	}
	if err := recordVersion(c, tx, baselineVersion, "baseline"); err != nil {
		return false, err
	}
	for _, s := range schemaSteps {
		if err := recordVersion(c, tx, s.Version, s.Name); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(c)
}

func recordVersion(c context.Context, q execQ, version int, name string) error {
	if _, err := q.Exec(c, `insert into omixor_schema_version (version, name) values ($1, $2) on conflict (version) do nothing`, version, name); err != nil {
		return fmt.Errorf("寫結構版本 %d(%s)失敗:%w", version, name, err)
	}
	return nil
}

// stampBaseline 已有核心表、但還沒有版本記錄(導入版本表之前的資料庫):在鎖下補建版本表並蓋上 baseline(版本 1)
func stampBaseline(c context.Context, pool *pgxpool.Pool) error {
	if v, exists, err := currentSchemaVersion(c, pool); err != nil || (exists && v > 0) {
		return err
	}
	return retryLock(c, stepAttempts, stepRetryBackoff, func() error {
		tx, err := pool.Begin(c)
		if err != nil {
			return err
		}
		defer tx.Rollback(c)
		if err := lockSchema(c, tx, stepLockTimeout.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(c, schemaVersionDDL); err != nil {
			return fmt.Errorf("建版本表失敗:%w", err)
		}
		if _, err := tx.Exec(c, `insert into omixor_schema_version (version, name) select $1, 'baseline' where not exists (select 1 from omixor_schema_version)`, baselineVersion); err != nil {
			return fmt.Errorf("蓋 baseline 版本失敗:%w", err)
		}
		return tx.Commit(c)
	})
}

// runSteps 依序執行版本大於資料庫目前最大版本的步驟(steps 要依版本遞增)。
// 資料庫版本比程式已知的新(部署回滾到舊版):寫警告、不動資料庫(步驟向後相容,舊程式照常可用)
func runSteps(c context.Context, pool *pgxpool.Pool, steps []schemaStep) error {
	cur, exists, err := currentSchemaVersion(c, pool)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("資料庫沒有版本表 %s", schemaVersionTable)
	}
	if known := knownSchemaVersion(steps); cur > known {
		warnf("資料庫結構版本 %d 比這版程式已知的 %d 新(可能是部署回滾),略過結構異動、照常啟動", cur, known)
		return nil
	}
	for _, s := range steps {
		if s.Version <= cur {
			continue
		}
		if err := retryLock(c, stepAttempts, stepRetryBackoff, func() error { return applyStep(c, pool, s) }); err != nil {
			return fmt.Errorf("結構異動步驟 %d(%s)失敗:%w", s.Version, s.Name, err)
		}
	}
	return nil
}

// applyStep 一個步驟一個交易:拿鎖、重新確認這個版本還沒做(別的實例剛做完就略過)、執行 SQL、寫版本記錄、commit
func applyStep(c context.Context, pool *pgxpool.Pool, s schemaStep) error {
	tx, err := pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := lockSchema(c, tx, stepLockTimeout.String()); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(c, `select exists (select 1 from omixor_schema_version where version = $1)`, s.Version).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	if _, err := tx.Exec(c, s.SQL); err != nil {
		return err
	}
	if err := recordVersion(c, tx, s.Version, s.Name); err != nil {
		return err
	}
	return tx.Commit(c)
}

// isLockErr 鎖等待逾時(55P03)、死鎖(40P01)、序列化失敗(40001):可以退避後重試
func isLockErr(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "55P03" || pe.Code == "40P01" || pe.Code == "40001")
}

// jitter 退避時間加隨機(0.5 到 1.5 倍),多個實例同時重試不會同步撞在一起
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int63n(int64(d)))
}

// retryLock 遇到鎖逾時、死鎖、序列化失敗就退避後重試,最多 attempts 次;其他錯誤立刻回傳
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
		warnf("結構異動遇到鎖等待逾時或死鎖(第 %d/%d 次),稍後重試:%v", i+1, attempts, err)
	}
	return err
}

type execQ interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// syncSystemActors 寫入固定的系統 actor(model.SystemActors;名稱、handle 由程式固定)。每次啟動都做,冪等:已經是目前的值就不寫(不產生新的列版本)
func syncSystemActors(c context.Context, q execQ) error {
	for _, s := range model.SystemActors {
		if _, err := q.Exec(c, `insert into omixor_actors (id, kind, name, handle) values ($1, 'system', $2, $3)
on conflict (id) do update set name = excluded.name, handle = excluded.handle, disabled_at = null
where (omixor_actors.name, omixor_actors.handle, omixor_actors.disabled_at) is distinct from (excluded.name, excluded.handle, null::timestamptz)`,
			s.ID, s.Name, s.Handle); err != nil {
			return fmt.Errorf("寫入系統 actor %s:%w", s.ID, err)
		}
	}
	return nil
}
