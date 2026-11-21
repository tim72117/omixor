// pg_actor_columns.go 「誰」欄位遷移引擎(ISS-36 階段 C-1;規格見雲端 omixor DOC-13 §1.4、§1.5、§2.1、§2.2、§2.5、§7)。
//
// 把 22 個「誰」的欄位(identity_fields.go:ActorColumns)從舊字串(email、user:<email>、agent:<id>、mcp…)換成 omixor_actors 的 actor id。
// 表驅動:每個欄位依序做
//
//	步驟 0 前置檢查(整個引擎一次,pg_actor_columns_precheck.go):omixor_actors、omixor_actor_aliases 存在;omixor_agents.actor_id、omixor_users.actor_id 在且前一步沒被略過;
//	       UnmigratedRows() == 0(還有沒展開的 jsonb 列,「誰」還在舊 jsonb 裡,轉不到);偵測開發機殘留的身分登記層(只警告)。不過 = 引擎整個沒做,啟動失敗(訊息指到真因)
//	步驟 1 建對照:去重的舊值 select org, 欄位值, count(*) … group by(只掃還不是 actor id 的值);
//	步驟 2 建缺的 actor:寬鬆解析器 ResolveLegacy(pg_actor_legacy.go)對每個舊值;缺的使用者 → 未註冊使用者 actor,對不上 → sys_legacy_*(寫 omixor_actor_aliases 與 log)
//	步驟 5 主鍵含 member 的表(#10、#12、#13)先刪「轉換後會撞主鍵」的重複列(留 pos 小的、已讀留 last_read 最大的)
//	步驟 4 批次原地更新(pg_actor_columns_batch.go):以 ctid 往前推進取一批(不重掃已轉換的前綴)、每批一個交易、批間檢查啟動預算(用完就停、下次續做);只有列鎖,不重寫整張表
//	步驟 6 驗證:轉換後指向不存在的 actor 的列數必須 0;非 0 → 這一欄略過後面的步驟、記 [遷移警告]、列出前 10 個值,不加外鍵(最後啟動失敗並列出)
//	步驟 7 結構:可為 NULL 的欄位 drop not null / drop default、空字串 → NULL;欄位改名(from_node → from_actor);
//	       最終驗證通過後(複製欄位 drop 舊來源欄位)寫完成標記、加外鍵(references omixor_actors(id) on delete restrict,not valid,validate 交給背景)
//
// 事件時間軸(actor_kind + actor_id)與稽核(user_id)是「複製」:結果寫進新欄位 actor,驗證通過後 drop 舊來源欄位。
// 其餘欄位原地更新;原本 NOT NULL 的維持 NOT NULL(空字串 → sys_legacy_unknown),所以不需要 DOC-13 §2.1 步驟 7 的
// 「check not valid + validate」(那是為了新加 NOT NULL;這裡沒有)。
//
// 完成標記:欄位轉換完、驗證通過、結構改完之後,在欄位上寫註解 'omixor:actor-converted'(catalog 操作,不掃表)。
// 之後每次啟動只查 catalog 就跳過,不會為了確認而掃大表。DOC-13 §2.1 步驟 1 建議用「外鍵已 validate」當標記,但外鍵預設不加(§2.5 (a)),所以另用這個。
//
// 觸發方式(C-2 共用地基之後):不再有開關。全新資料庫(以及 storetest 範本庫)的建表語句(pg.go:pgSchema、pg_split_*.go)直接就是「最終結構」,
// 這個引擎在同一次啟動的最後一步對空表補完成標記與外鍵(瞬間);舊結構的資料庫由它原地轉換成同一個結構(用結構指紋測試證明兩者一致,
// 見 pg_actor_final_test.go)。程式碼(服務層、store)只認最終結構,所以轉換不是選項:
//   - 只要還有欄位沒轉完(預算用完、表超過 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB 的自動遷移門檻、驗證失敗、等不到鎖、還有壞掉沒展開的舊 jsonb 列),啟動就失敗並列出欄位、原因與處理方式;
//     已轉換的進度保留(每批一個交易),重啟會接著做,不會留下「半轉換卻照常服務」的狀態。
//   - 舊欄位(事件的 actor_kind、actor_id、稽核的 user_id)轉換驗證通過後 drop(只改 catalog,不重寫表);不支援回滾(只能還原升級前備份)。見 MIGRATIONS.md §13。

package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	actorColumnsLockKey = "omixor-actor-columns-migrate"
	// actorConvertedMarker 欄位已轉換完成的標記(欄位註解)
	actorConvertedMarker = "omixor:actor-converted"
)

var (
	// actorColumnBatch 批次原地更新每批最多幾列(測試會調小)
	actorColumnBatch = 5000
	// actorColumnsBatchHook 每批更新完呼叫一次(測試用:模擬預算用完、檢查批次)
	actorColumnsBatchHook func()
)

// migrateActorColumnsStep startupMigrations 的一步:執行引擎;做完還有欄位沒轉換完成就回錯誤(程式只認最終結構,不能照常服務)
func (p *PG) migrateActorColumnsStep() error {
	rep, err := p.runActorColumns()
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pending, err := p.actorColumnsPending(c)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	var why []string
	for _, k := range pending {
		if w := rep.Skipped[k]; w != "" {
			why = append(why, k+"("+w+")")
		} else {
			why = append(why, k)
		}
	}
	if w := rep.Skipped["*"]; w != "" {
		why = append(why, "整體:"+w)
	}
	sort.Strings(why)
	if rep.Partial {
		why = append(why, "啟動遷移預算用完或等不到鎖:調大 OMIXOR_MIGRATE_BUDGET_MINUTES 或直接重啟,已轉換的進度保留、會接著做")
	}
	return fmt.Errorf("還有 %d 個「誰」欄位沒轉成 actor id,程式只認轉換後的結構所以不能啟動:%s;已轉換的進度保留,修好後重啟會接著做"+
		"(表太大被略過的,調大 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB 或設 0;孤兒資料看上面的 [遷移警告])", len(pending), strings.Join(why, "、"))
}

// ActorColumnsReport 一次執行的結果(測試與日誌用)
type ActorColumnsReport struct {
	Converted []string          // 已轉換完成的欄位(含先前就完成的)
	Skipped   map[string]string // 欄位 → 略過的原因(孤兒、表太大、錯誤…);key "*" = 整個引擎略過
	Updated   map[string]int64  // 欄位 → 本輪更新(或刪除重複)的列數
	Partial   bool              // 啟動預算用完或等不到鎖,有欄位留到下次
}

func newActorColumnsReport() *ActorColumnsReport {
	return &ActorColumnsReport{Skipped: map[string]string{}, Updated: map[string]int64{}}
}

func (r *ActorColumnsReport) skip(key, why string) { r.Skipped[key] = why }

// migrateActorColumns 執行引擎。冪等、可重跑、多實例同時啟動只有一個在做
func (p *PG) migrateActorColumns() error {
	_, err := p.runActorColumns()
	return err
}

func (p *PG) runActorColumns() (*ActorColumnsReport, error) {
	rep := newActorColumnsReport()
	if p.budgetExpired() { // 前面的步驟已把整體預算用完:不去等鎖(查詢都會立刻逾時),直接回報留到下次
		rep.Partial = true
		rep.skip("*", "啟動遷移整體預算在這一步之前已用完")
		return rep, nil
	}
	c, cancel := p.migCtx(30 * time.Minute)
	defer cancel()
	unlock, ok, err := p.sessionLock(c, actorColumnsLockKey, "actor 欄位遷移")
	if err != nil {
		return rep, err
	}
	if !ok {
		rep.Partial = true
		rep.skip("*", "等不到遷移鎖")
		return rep, nil
	}
	defer unlock()
	if why := p.actorColumnsPrecheck(c); why != "" {
		warnf("actor 欄位遷移略過:%s", why)
		rep.skip("*", why)
		return rep, nil
	}
	lk := p.newLegacyLookup(c)
	for _, col := range ActorColumns {
		if p.budgetExpired() || c.Err() != nil {
			rep.Partial = true
			warnf("actor 欄位遷移:啟動遷移整體預算已用完,剩下的欄位留到下次啟動繼續(從 %s 起)", col.Key())
			break
		}
		if err := p.convertActorColumn(c, lk, col, rep); err != nil {
			if errors.Is(err, errActorPartial) || p.budgetExpired() || isTimeout(c, err) {
				rep.Partial = true
				warnf("actor 欄位遷移:%s 做到一半預算用完,下次啟動接著做", col.Key())
				break
			}
			warnf("actor 欄位遷移:%s 失敗,略過這一欄(下次啟動再試):%v", col.Key(), err)
			rep.skip(col.Key(), err.Error())
		}
	}
	log.Printf("[遷移] actor 欄位:完成 %d / %d 欄,略過 %d 欄%s", len(rep.Converted), len(ActorColumns), len(rep.Skipped), map[bool]string{true: ",預算用完、留到下次", false: ""}[rep.Partial])
	return rep, nil
}

// actorColumnConverted 欄位是否已有完成標記
func (p *PG) actorColumnConverted(c context.Context, table, col string) bool {
	var d *string
	err := p.pool.QueryRow(c, `select col_description(a.attrelid, a.attnum) from pg_attribute a
where a.attrelid = to_regclass($1) and a.attname = $2 and not a.attisdropped`, table, col).Scan(&d)
	return err == nil && d != nil && *d == actorConvertedMarker
}

// actorValue 一個要轉換的舊值(去重後):org、(事件時間軸再加 kind)、值、筆數
type actorValue struct {
	org, kind, id string
	n             int64
}

// actorMap 一筆對照:舊值 → actor id
type actorMap struct {
	actorValue
	actor string
}

func (col ActorColumn) orgExpr(alias string) string {
	if col.HasOrg {
		return alias + ".org"
	}
	return "''::text"
}

// blankRe 「整個值都是空白」:空白的定義與 Go 的 strings.TrimSpace(unicode.IsSpace)完全一致。
// 不用 PostgreSQL 的 \s:它依資料庫 locale,對 NBSP(U+00A0)、全形空白等不一定判為空白,
// 而 Go 端的解析器(ResolveLegacy)用 TrimSpace 判斷「空值」,兩邊不一致會讓驗證永遠失敗
const blankRe = `'^[ \t\n\v\f\r\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$'`

// distinctSQL 步驟 1:還沒轉換的去重舊值。cur 是目前存舊值的欄位名(Rename 欄位改名後是 Target)
func (col ActorColumn) distinctSQL(cur string) string {
	switch {
	case col.Mode == ActorColCopy && col.Form == IdentityActor:
		return fmt.Sprintf(`select x.org, x.%s, x.%s, count(*) from %s x
where x.%s is null and (x.%s = 'system' or x.%s !~ %s) group by 1, 2, 3`,
			col.KindColumn, col.Column, col.Table, col.Target, col.KindColumn, col.Column, blankRe)
	case col.Mode == ActorColCopy:
		return fmt.Sprintf(`select x.org, ''::text, x.%s, count(*) from %s x where x.%s is null and x.%s !~ %s group by 1, 2, 3`,
			col.Column, col.Table, col.Target, col.Column, blankRe)
	}
	cond := fmt.Sprintf(`x.%[1]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[1]s)`, cur)
	if col.Nullable { // 空白值留給步驟 7 改成 NULL
		cond += fmt.Sprintf(` and x.%s !~ %s`, cur, blankRe)
	}
	return fmt.Sprintf(`select %s, ''::text, x.%s, count(*) from %s x where %s group by 1, 2, 3`, col.orgExpr("x"), cur, col.Table, cond)
}

// unresolvedWhere 轉換後還有問題的列的條件(沒轉成、或指向不存在的 actor)。
// strict = false(步驟 6,結構步驟之前):可為 NULL 的欄位的空白值留給步驟 7 改成 NULL,不算未轉換;
// strict = true(開外鍵前的重驗證、寫完成標記前的最終驗證):非 NULL 而且不是有效 actor id 的值(含空字串)都算未轉換
func (col ActorColumn) unresolvedWhere(cur string, strict bool) string {
	switch {
	case col.Mode == ActorColCopy && col.Form == IdentityActor:
		return fmt.Sprintf(`(x.%[1]s is null and (x.%[2]s = 'system' or x.%[3]s !~ %[4]s)) or (x.%[1]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[1]s))`,
			col.Target, col.KindColumn, col.Column, blankRe)
	case col.Mode == ActorColCopy:
		return fmt.Sprintf(`(x.%[1]s is null and x.%[2]s !~ %[3]s) or (x.%[1]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[1]s))`,
			col.Target, col.Column, blankRe)
	}
	cond := fmt.Sprintf(`x.%[1]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[1]s)`, cur)
	if col.Nullable && !strict {
		cond += fmt.Sprintf(` and x.%s !~ %s`, cur, blankRe)
	}
	return cond
}

// sampleExpr 驗證失敗時列出的值(前 10 個去重)
func (col ActorColumn) sampleExpr(cur string) string {
	if col.Mode == ActorColCopy && col.Form == IdentityActor {
		return fmt.Sprintf(`x.%s || '/' || x.%s`, col.KindColumn, col.Column)
	}
	if col.Mode == ActorColCopy {
		return `x.` + col.Column
	}
	return `x.` + cur
}

func (col ActorColumn) opts() LegacyOpts {
	return LegacyOpts{Form: col.Form, NotNull: !col.Nullable}
}

// convertActorColumn 一個欄位的步驟 1~7。回傳 errActorPartial 表示預算用完中斷
func (p *PG) convertActorColumn(c context.Context, lk *pgLegacyLookup, col ActorColumn, rep *ActorColumnsReport) error {
	key := col.Key()
	hasSrc, err := p.columnExists(c, col.Table, col.Column)
	if err != nil {
		return err
	}
	hasTgt, err := p.columnExists(c, col.Table, col.Target)
	if err != nil {
		return err
	}
	cur := col.Column  // 目前存值的欄位(Rename 改名之後是 Target)
	finalOnly := false // 沒有舊來源欄位:全新資料庫的最終結構(或舊欄位已 drop),只剩完成標記與外鍵
	switch col.Mode {
	case ActorColRename:
		if !hasSrc && hasTgt {
			cur = col.Target
		} else if !hasSrc {
			rep.skip(key, "欄位不存在")
			return nil
		}
	case ActorColCopy:
		if !hasSrc && hasTgt {
			finalOnly = true
		} else if !hasSrc {
			rep.skip(key, "欄位不存在")
			return nil
		}
	default:
		if !hasSrc {
			rep.skip(key, "欄位不存在")
			return nil
		}
	}
	// 已完成(有完成標記):不掃表。外鍵還沒加時,加之前再驗證一次:滾動部署時舊版本在轉換之後仍可能寫舊字串,
	// 外鍵不能在這種資料上加;有漏網的舊字串就作廢標記、這一輪重新轉換
	if p.actorColumnConverted(c, col.Table, col.Target) {
		if ex, _, err := fkExists(c, p.pool, col.Table, col.FK); err != nil {
			return err
		} else if ex {
			rep.Converted = append(rep.Converted, key)
			if col.Mode == ActorColCopy && !finalOnly { // C-1 開關開啟時轉換過的資料庫:舊來源欄位還在,現在 drop(結構要和全新建立的一致)
				return p.dropCopySource(c, col)
			}
			return nil
		}
		var bad int64
		if err := p.pool.QueryRow(c, fmt.Sprintf(`select count(*) from %s x where %s`, col.Table, col.unresolvedFinal(cur, finalOnly))).Scan(&bad); err != nil {
			return err
		}
		if bad == 0 {
			rep.Converted = append(rep.Converted, key)
			if col.Mode == ActorColCopy && !finalOnly {
				if err := p.dropCopySource(c, col); err != nil {
					return err
				}
			}
			return p.ensureActorFK(c, col)
		}
		log.Printf("[遷移] %s:加外鍵前驗證發現 %d 筆轉換之後才寫入的舊字串,作廢完成標記、重新轉換", key, bad)
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, fmt.Sprintf(`comment on column %s.%s is null`, col.Table, col.Target))
			return err
		}); err != nil {
			return fmt.Errorf("作廢完成標記:%w", err)
		}
	}
	if finalOnly { // 最終結構、沒有舊欄位:驗證目標欄位後寫標記、加外鍵
		return p.finishActorColumn(c, col, cur, true, rep)
	}
	// 大表不自動轉
	size, err := tableBytes(c, p.pool, col.Table)
	if err != nil {
		return err
	}
	if oversized(size) && col.Mode != ActorColCopy {
		// 全空(沒有任何非 NULL 值)的欄位沒有東西要轉、不會產生舊 tuple 與 WAL(例如舊庫剛加的 ops_issues.deleted_by):不受大表門檻限制。
		// 用 exists 判斷:只要找到一列有值就立刻停(全空時是一次唯讀掃描,不重寫)
		allNull, err := p.columnAllNull(c, col.Table, cur)
		if err != nil {
			return err
		}
		if allNull {
			log.Printf("[遷移] %s:欄位全空,不受大表門檻(%s)限制,直接轉換", key, humanBytes(maxAutoRewriteBytes))
			size = 0
		}
	}
	if oversized(size) {
		warnf("actor 欄位遷移:%s 所在的表 %s 約 %s,超過自動遷移的門檻 %s,不自動轉換(批次更新會產生大量舊 tuple 與 WAL);"+
			"請在維護窗口處理,或把 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB 調大或設 0", key, col.Table, humanBytes(size), humanBytes(maxAutoRewriteBytes))
		rep.skip(key, fmt.Sprintf("表 %s 約 %s,超過自動遷移的門檻", col.Table, humanBytes(size)))
		return nil
	}
	if col.Mode == ActorColCopy && !hasTgt {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, fmt.Sprintf(`alter table %s add column if not exists %s text`, col.Table, col.Target))
			return err
		}); err != nil {
			return fmt.Errorf("加欄位 %s.%s:%w", col.Table, col.Target, err)
		}
	}
	// 步驟 1:去重的舊值
	vals, err := p.actorDistinctValues(c, col, cur)
	if err != nil {
		return err
	}
	// 步驟 2:解析、建缺的 actor
	var maps []actorMap
	for _, v := range vals {
		var res LegacyResult
		var rerr error
		if col.Form == IdentityActor {
			res, rerr = ResolveLegacyEvent(lk, v.org, v.kind, v.id, col.opts())
		} else {
			res, rerr = ResolveLegacy(lk, v.org, v.id, col.opts())
		}
		if rerr != nil {
			return fmt.Errorf("解析舊值 %q:%w", clipLegacy(v.id), rerr)
		}
		where := fmt.Sprintf("%s 組織 %q 舊值 %q 筆數 %d", key, v.org, clipLegacy(strings.TrimSpace(v.kind+"/"+v.id)), v.n)
		if col.Form != IdentityActor {
			where = fmt.Sprintf("%s 組織 %q 舊值 %q 筆數 %d", key, v.org, clipLegacy(v.id), v.n)
		}
		aid, aerr := lk.applyLegacy(res, where)
		if aerr != nil {
			return aerr
		}
		if aid == "" || (col.Mode != ActorColCopy && aid == v.id) {
			continue
		}
		maps = append(maps, actorMap{v, aid})
	}
	// 步驟 5:主鍵衝突去重
	if len(maps) > 0 && len(col.KeyCols) > 0 {
		n, err := p.actorDedupKeys(c, col, cur, maps)
		if err != nil {
			return err
		}
		if n > 0 {
			rep.Updated[key+"(去重刪除)"] += n
			log.Printf("[遷移] %s:同一人以不同寫法同時出現在同一個主鍵,刪掉 %d 筆重複列(保留規則 %s)", key, n, col.Keep)
		}
	}
	// 步驟 4:批次原地更新
	var concurrent int64 // 批次裡被並行修改而跳過的列
	if len(maps) > 0 {
		br, err := p.actorBatchUpdate(c, col, cur, maps)
		rep.Updated[key] += br.Updated
		concurrent = br.Concurrent
		if err != nil {
			return err
		}
	}
	// 步驟 6:驗證
	var bad int64
	if err := p.pool.QueryRow(c, fmt.Sprintf(`select count(*) from %s x where %s`, col.Table, col.unresolvedWhere(cur, false))).Scan(&bad); err != nil {
		return err
	}
	if bad > 0 {
		samples, _ := p.actorSamples(c, col, cur, false)
		if concurrent > 0 && bad <= concurrent {
			// 不是孤兒:這些列在轉換期間被別的交易改過(舊程式仍在寫舊字串),批次更新跳過了它們
			warnf("actor 欄位遷移:%s 有 %d 列在轉換期間被並行修改,將於下次啟動重試(前 10 個值:%s);這一欄略過後續步驟(不加外鍵、不做結構改動)",
				key, bad, strings.Join(samples, "、"))
			rep.skip(key, fmt.Sprintf("有 %d 列在轉換期間被並行修改,將於下次啟動重試", bad))
			return nil
		}
		extra := ""
		if concurrent > 0 {
			extra = fmt.Sprintf("(其中至少 %d 列是轉換期間被並行修改,將於下次啟動重試)", concurrent)
		}
		warnf("actor 欄位遷移:%s 轉換後還有 %d 筆對不上 actor%s(前 10 個值:%s);這一欄略過後續步驟(不加外鍵、不做結構改動)",
			key, bad, extra, strings.Join(samples, "、"))
		rep.skip(key, fmt.Sprintf("驗證失敗:%d 筆孤兒%s", bad, extra))
		return nil
	}
	// 步驟 7:結構
	if col.Mode != ActorColCopy && col.Nullable {
		if err := p.actorMakeNullable(c, col, cur); err != nil {
			return err
		}
	}
	if col.Mode == ActorColRename && cur == col.Column {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, fmt.Sprintf(`alter table %s rename column %s to %s`, col.Table, col.Column, col.Target))
			return err
		}); err != nil {
			return fmt.Errorf("欄位改名 %s → %s:%w", col.Column, col.Target, err)
		}
	}
	return p.finishActorColumn(c, col, cur, false, rep)
}

// finishActorColumn 最後一段:最終驗證(嚴格:空字串也算未轉換;步驟 6 之後、結構步驟期間舊程式仍可能寫進舊值)→ 複製欄位的舊來源欄位 drop →
// 完成標記 → 外鍵。finalOnly = 沒有舊來源欄位(全新資料庫的最終結構),只驗證目標欄位
func (p *PG) finishActorColumn(c context.Context, col ActorColumn, cur string, finalOnly bool, rep *ActorColumnsReport) error {
	key := col.Key()
	curNow := cur
	if col.Mode == ActorColRename {
		curNow = col.Target
	}
	var bad2 int64
	if err := p.pool.QueryRow(c, fmt.Sprintf(`select count(*) from %s x where %s`, col.Table, col.unresolvedFinal(curNow, finalOnly))).Scan(&bad2); err != nil {
		return err
	}
	if bad2 > 0 {
		samples, _ := p.actorSamples(c, col, curNow, true, finalOnly)
		warnf("actor 欄位遷移:%s 結構步驟完成後驗證發現 %d 筆新寫入的舊值(前 10 個值:%s);不寫完成標記、不加外鍵,下次啟動再轉",
			key, bad2, strings.Join(samples, "、"))
		rep.skip(key, fmt.Sprintf("最終驗證失敗:%d 筆未轉換", bad2))
		return nil
	}
	if col.Mode == ActorColCopy && !finalOnly { // 複製欄位:結果已驗證,舊來源欄位 drop(只改 catalog)
		if err := p.dropCopySource(c, col); err != nil {
			return err
		}
	}
	if err := p.fkStep(c, func(tx pgx.Tx) error {
		_, err := tx.Exec(c, fmt.Sprintf(`comment on column %s.%s is '%s'`, col.Table, col.Target, actorConvertedMarker))
		return err
	}); err != nil {
		return fmt.Errorf("寫完成標記:%w", err)
	}
	rep.Converted = append(rep.Converted, key)
	if !finalOnly {
		log.Printf("[遷移] actor 欄位:%s 轉換完成(更新 %d 列)", key, rep.Updated[key])
	}
	return p.ensureActorFK(c, col)
}

// dropCopySource 複製欄位轉換完成後 drop 舊來源欄位(事件的 actor_kind + actor_id、稽核的 user_id);只改 catalog,不重寫表
func (p *PG) dropCopySource(c context.Context, col ActorColumn) error {
	drop := `alter table ` + col.Table + ` drop column if exists ` + col.Column
	if col.KindColumn != "" {
		drop += `, drop column if exists ` + col.KindColumn
	}
	if err := p.fkStep(c, func(tx pgx.Tx) error {
		_, err := tx.Exec(c, drop)
		return err
	}); err != nil {
		return fmt.Errorf("drop 舊欄位 %s:%w", col.Column, err)
	}
	return nil
}

// unresolvedFinal 最終驗證的條件:finalOnly(沒有舊來源欄位)只看目標欄位 —— 非 NULL 而且不是有效 actor id;否則同 unresolvedWhere(strict)
func (col ActorColumn) unresolvedFinal(cur string, finalOnly bool) string {
	if finalOnly {
		return fmt.Sprintf(`x.%[1]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[1]s)`, col.Target)
	}
	return col.unresolvedWhere(cur, true)
}

func (p *PG) actorDistinctValues(c context.Context, col ActorColumn, cur string) ([]actorValue, error) {
	rows, err := p.pool.Query(c, col.distinctSQL(cur))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []actorValue
	for rows.Next() {
		var v actorValue
		var org, kind, id string
		if err := rows.Scan(&org, &kind, &id, &v.n); err != nil {
			return nil, err
		}
		v.org, v.kind, v.id = org, kind, id
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *PG) actorSamples(c context.Context, col ActorColumn, cur string, strict bool, finalOnly ...bool) ([]string, error) {
	where, sample := col.unresolvedWhere(cur, strict), col.sampleExpr(cur)
	if len(finalOnly) > 0 && finalOnly[0] {
		where, sample = col.unresolvedFinal(cur, true), `x.`+col.Target
	}
	rows, err := p.pool.Query(c, fmt.Sprintf(`select distinct %s from %s x where %s limit 10`, sample, col.Table, where))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%q", clipLegacy(s)))
	}
	return out, rows.Err()
}

// mapArrays 對照拆成四個平行陣列(org、kind、舊值、新 actor id)給 unnest
func mapArrays(maps []actorMap) (orgs, kinds, olds, news []string) {
	for _, m := range maps {
		orgs, kinds, olds, news = append(orgs, m.org), append(kinds, m.kind), append(olds, m.id), append(news, m.actor)
	}
	return
}

// actorDedupKeys 步驟 5:先刪「轉換後會撞主鍵」的重複列。同一個 (org, 其他主鍵欄位, 轉換後的 actor id) 只留排序第一的(col.Keep)。
// 已經是目標 actor id 的列也一起排序(部分已轉換的資料庫)。單一敘述,原子
func (p *PG) actorDedupKeys(c context.Context, col ActorColumn, cur string, maps []actorMap) (int64, error) {
	orgs, _, olds, news := mapArrays(maps)
	part := "x.org"
	for _, k := range col.KeyCols {
		part += ", x." + k
	}
	ct, err := p.pool.Exec(c, fmt.Sprintf(`with m as (select * from unnest($1::text[], $2::text[], $3::text[]) as t(org, old, new)),
r as (select x.ctid as cid, row_number() over (partition by %[1]s, coalesce(m.new, x.%[2]s) order by %[3]s) as rn
  from %[4]s x left join m on m.org = x.org and m.old = x.%[2]s)
delete from %[4]s where ctid in (select cid from r where rn > 1)`, part, cur, col.Keep, col.Table), orgs, olds, news)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// ---- 外鍵 ----

func (col ActorColumn) fkSpec() fkSpec {
	return fkSpec{
		name:   col.FK,
		table:  col.Table,
		def:    fmt.Sprintf(`foreign key (%s) references omixor_actors (id) on delete restrict`, col.Target),
		orphan: fmt.Sprintf(`select count(*) from %[1]s x where x.%[2]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[2]s)`, col.Table, col.Target),
		col:    col.Target,
	}
}

// actorFKSpecs 22 條 actor 欄位的外鍵(還沒轉換完成的欄位 migrateForeignKeys 不會加,由引擎轉換完成後加)
func actorFKSpecs() []fkSpec {
	out := make([]fkSpec, 0, len(ActorColumns))
	for _, col := range ActorColumns {
		out = append(out, col.fkSpec())
	}
	return out
}

// actorFKNames actor 欄位外鍵的名稱集合(fkSpec 是位置式的結構,不加欄位;用名稱判斷是不是 actor 外鍵)
var actorFKNames = func() map[string]bool {
	m := map[string]bool{}
	for _, col := range ActorColumns {
		m[col.FK] = true
	}
	return m
}()

func isActorFK(name string) bool { return actorFKNames[name] }

// fkSpecReady 這條外鍵現在能不能加:一般外鍵永遠可以;actor 欄位的外鍵要等欄位轉換完成(完成標記),否則舊字串會讓外鍵擋掉所有新寫入
func (p *PG) fkSpecReady(c context.Context, f fkSpec) bool {
	if !isActorFK(f.name) {
		return true
	}
	return p.actorColumnConverted(c, f.table, f.col)
}

// ensureActorFK 欄位轉換完成後加外鍵(not valid;validate 由 finishStartup 的背景工作做)。已存在就不動
func (p *PG) ensureActorFK(c context.Context, col ActorColumn) error {
	f := col.fkSpec()
	ex, _, err := fkExists(c, p.pool, f.table, f.name)
	if err != nil {
		return err
	}
	if ex {
		return nil
	}
	if err := p.fkStep(c, func(tx pgx.Tx) error {
		_, err := tx.Exec(c, fmt.Sprintf(`alter table %s add constraint %s %s not valid`, f.table, f.name, f.def))
		return err
	}); err != nil {
		return fmt.Errorf("加外鍵 %s:%w", f.name, err)
	}
	log.Printf("[遷移] 外鍵 %s 已加上(not valid,背景驗證)", f.name)
	return nil
}

// actorColumnsPending 還沒轉換完成的欄位(查 catalog,不掃表);啟動彙總用
func (p *PG) actorColumnsPending(c context.Context) ([]string, error) {
	var out []string
	for _, col := range ActorColumns {
		hasSrc, err := p.columnExists(c, col.Table, col.Column)
		if err != nil {
			return nil, err
		}
		hasTgt, err := p.columnExists(c, col.Table, col.Target)
		if err != nil {
			return nil, err
		}
		if !hasSrc && !hasTgt {
			continue // 表或欄位不存在(別的遷移還沒做完):不算待轉換
		}
		if !p.actorColumnConverted(c, col.Table, col.Target) {
			out = append(out, col.Key())
		}
	}
	return out, nil
}

// columnAllNull 欄位全是 NULL(沒有任何非 NULL 值)。用 exists:找到一列有值就停
func (p *PG) columnAllNull(c context.Context, table, column string) (bool, error) {
	var any bool
	if err := p.pool.QueryRow(c, fmt.Sprintf(`select exists (select 1 from %s where %s is not null)`, table, column)).Scan(&any); err != nil {
		return false, err
	}
	return !any, nil
}
