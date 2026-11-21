package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// pg_actor_columns_batch.go 「誰」欄位引擎的批次更新(步驟 4)與空白 → NULL(步驟 7)。
//
// 批次取法:以 ctid 往前推進(keyset):每批 `where ctid > <上一批最後一列的 ctid> order by ctid limit N`,PostgreSQL 14 起走 TID Range Scan,
// 不會每批都從表頭重掃已經轉換的前綴(舊寫法是 `limit N` 無排序,每批從表頭掃起,總成本是列數的平方:60 萬列約 5 秒、500 萬列約 6 分鐘)。
// 被轉換更新出來的新版本列會落在表尾,往前推進的掃描最後會再經過它們,但它們已經是新值、不符合條件,只多掃一遍,成本仍是線性。
// 批次裡被並行修改的列(select 之後、update 之前被別的交易改過)update 會跳過,計入「並行修改」列數:由最後的驗證兜底,
// (update 的條件除了 ctid 還要 `舊值欄位 = 取到的舊值`:被鎖住的列等到對方 commit 後,PostgreSQL 會對「最新版本」重新檢查條件,
// 而 TID 掃描的重新檢查不比對 ctid,只比 ctid 的話會把對方剛寫的新值蓋掉)
// 驗證不過的欄位不寫完成標記,下次啟動重試。

// minTID 往前推進的起點(比任何 ctid 都小)
const minTID = "(0,0)"

// tidBatch 執行一批(一個交易、單一敘述):交易內關掉 seqscan 與平行掃描,讓 `ctid > <上一批的邊界>` 走 TID Range Scan(依實體順序往前掃、
// limit 取前 N 列)。TID Range Scan 不保證排序輸出,所以查詢不寫 order by(規劃器會改成「整表平行掃描 + 排序」,每批都掃全表,正是要避免的平方級);
// 實體順序是實作行為不是保證,所以呼叫端在掃到表尾之後還要再確認「沒有符合條件的列了」,有剩就從頭再掃一輪(見 actorBatchUpdate、actorMakeNullable)
func (p *PG) tidBatch(c context.Context, q string, args []any, dest ...any) error {
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `set local enable_seqscan = off; set local max_parallel_workers_per_gather = 0`); err != nil {
		return err
	}
	if err := tx.QueryRow(c, q, args...).Scan(dest...); err != nil {
		return err
	}
	return tx.Commit(c)
}

// actorBatchResult 一個欄位批次更新的結果
type actorBatchResult struct {
	Updated    int64 // 實際更新的列數
	Concurrent int64 // 取到了、但更新時已被並行修改而跳過的列數
}

// actorBatchUpdate 步驟 4:依對照批次更新。每批一個交易(單一敘述),最多 actorColumnBatch 列;批間檢查預算,用完回 errActorPartial。
// 掃到表尾之後確認沒有符合條件的列了才結束(沒有就是常態,一次掃描;有剩——例如被並行修改而跳過、但仍符合對照的列——從頭再掃,最多 3 輪)
func (p *PG) actorBatchUpdate(c context.Context, col ActorColumn, cur string, maps []actorMap) (actorBatchResult, error) {
	orgs, kinds, olds, news := mapArrays(maps)
	var mdef, from, where, set, valCol, pre, joinOn string // m 的定義、from 子句(含 join)、額外條件、update 的 set、存舊值的欄位
	var args []any
	switch {
	case col.Mode == ActorColCopy && col.Form == IdentityActor:
		mdef = `select * from unnest($1::text[], $2::text[], $3::text[], $4::text[]) as t(org, kind, old, new)`
		from = fmt.Sprintf(`%s y join m on m.org = y.org and m.kind = y.%s and m.old = y.%s`, col.Table, col.KindColumn, col.Column)
		where = fmt.Sprintf(`y.%s is null`, col.Target)
		pre = fmt.Sprintf(`y.%s is null`, col.Target)
		joinOn = fmt.Sprintf(`m.org = y.org and m.kind = y.%s and m.old = y.%s`, col.KindColumn, col.Column)
		set, valCol = col.Target, col.Column
		args = []any{orgs, kinds, olds, news}
	case col.Mode == ActorColCopy:
		mdef = `select * from unnest($1::text[], $2::text[], $3::text[]) as t(org, old, new)`
		from = fmt.Sprintf(`%s y join m on m.org = y.org and m.old = y.%s`, col.Table, col.Column)
		where = fmt.Sprintf(`y.%s is null`, col.Target)
		pre = fmt.Sprintf(`y.%s is null`, col.Target)
		joinOn = fmt.Sprintf(`m.org = y.org and m.old = y.%s`, col.Column)
		set, valCol = col.Target, col.Column
		args = []any{orgs, olds, news}
	default:
		mdef = `select * from unnest($1::text[], $2::text[], $3::text[]) as t(org, old, new)`
		join := "m.org = y.org and "
		if !col.HasOrg {
			join = ""
		}
		from = fmt.Sprintf(`%s y join m on %sm.old = y.%s`, col.Table, join, cur)
		where = "true"
		pre = "true"
		joinOn = join + "m.old = y." + cur
		set, valCol = cur, cur
		args = []any{orgs, olds, news}
	}
	n := len(args)
	oldsIdx := 2
	if col.Mode == ActorColCopy && col.Form == IdentityActor {
		oldsIdx = 3
	}
	// c:單表的 TID Range Scan 加簡單過濾(舊值在對照裡),limit N,不 join(join 會讓規劃器自選連接順序,輸出順序不定);
	// b:用 ctid 回表、與對照 join 取新值;u:更新
	q := fmt.Sprintf(`with m as (%[1]s),
c as materialized (select y.ctid as cid from %[2]s y where y.ctid > $%[3]d::text::tid and y.%[4]s = any($%[5]d::text[]) and %[6]s limit $%[7]d),
b as materialized (select y.ctid as cid, y.%[8]s as old, m.new as new from c join %[2]s y on y.ctid = c.cid join m on %[9]s),
u as (update %[2]s x set %[10]s = b.new from b where x.ctid = b.cid and x.%[8]s = b.old returning 1)
select (select count(*) from c), (select count(*) from b), (select count(*) from u), (select cid::text from c order by cid desc limit 1)`,
		mdef, col.Table, n+2, valCol, oldsIdx, pre, n+1, valCol, joinOn, set)
	left := fmt.Sprintf(`with m as (%s) select exists (select 1 from %s where %s)`, mdef, from, where)
	var res actorBatchResult
	for pass := 0; pass < 3; pass++ {
		last := minTID
		for {
			if p.budgetExpired() || c.Err() != nil {
				return res, errActorPartial
			}
			var got, matched, upd int64
			var next *string
			if err := p.tidBatch(c, q, append(append([]any{}, args...), actorColumnBatch, last), &got, &matched, &upd, &next); err != nil {
				return res, err
			}
			res.Updated += upd
			res.Concurrent += matched - upd
			if actorColumnsBatchHook != nil {
				actorColumnsBatchHook()
			}
			if got < int64(actorColumnBatch) || next == nil {
				break // 往前推進的掃描走到表尾了
			}
			last = *next
		}
		var more bool
		if err := p.pool.QueryRow(c, left, args...).Scan(&more); err != nil {
			return res, err
		}
		if !more {
			break
		}
	}
	return res, nil
}

// actorMakeNullable 步驟 7(可為 NULL 的欄位):drop not null、drop default,再把空白值改成 NULL(批次,批間檢查預算;同樣以 ctid 往前推進)
func (p *PG) actorMakeNullable(c context.Context, col ActorColumn, cur string) error {
	var notNull, hasDefault bool
	if err := p.pool.QueryRow(c, `select is_nullable = 'NO', column_default is not null from information_schema.columns
where table_schema = current_schema() and table_name = $1 and column_name = $2`, col.Table, cur).Scan(&notNull, &hasDefault); err != nil {
		return err
	}
	if notNull || hasDefault {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, fmt.Sprintf(`alter table %[1]s alter column %[2]s drop not null, alter column %[2]s drop default`, col.Table, cur))
			return err
		}); err != nil {
			return fmt.Errorf("drop not null %s.%s:%w", col.Table, cur, err)
		}
	}
	q := fmt.Sprintf(`with b as materialized (select ctid as cid from %[1]s where ctid > $2::text::tid and %[2]s ~ %[3]s limit $1),
u as (update %[1]s x set %[2]s = null from b where x.ctid = b.cid and x.%[2]s ~ %[3]s returning 1)
select (select count(*) from b), (select cid::text from b order by cid desc limit 1)`, col.Table, cur, blankRe)
	more := fmt.Sprintf(`select exists (select 1 from %s where %s ~ %s)`, col.Table, cur, blankRe)
	for pass := 0; ; pass++ {
		last := minTID
		for {
			if p.budgetExpired() || c.Err() != nil {
				return errActorPartial
			}
			var got int64
			var next *string
			if err := p.tidBatch(c, q, []any{actorColumnBatch, last}, &got, &next); err != nil {
				return err
			}
			if actorColumnsBatchHook != nil {
				actorColumnsBatchHook()
			}
			if got < int64(actorColumnBatch) || next == nil {
				break
			}
			last = *next
		}
		// 結束條件是「已經沒有符合條件的列」:批次裡被併發修改的列會被跳過(但推進已經越過它們),再掃一輪確認;有剩就從頭再來
		var left bool
		if err := p.pool.QueryRow(c, more).Scan(&left); err != nil {
			return err
		}
		if !left {
			return nil
		}
		if pass >= 20 {
			return fmt.Errorf("%s.%s 的空白值一直有並行寫入,轉不完,下次啟動再試", col.Table, cur)
		}
	}
}
