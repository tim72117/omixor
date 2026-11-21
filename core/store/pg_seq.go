package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 編號序號表:每個 (org, scope) 一列,記「最後發出去的號碼」,刪掉的號碼不再使用。
// scope:issue(組織層級 Issue 的 ISS-<n>)、issue:<專案 id>(專案裡 Issue 的 <代號>-<n>)、task、doc、channel、incident、decision(DEC-<n>)、
// msg:<channel_id>(訊息編號每個頻道各自一組)。
// (專案的 omixor_project_seq 維持原樣。)
//
// 已知限制(只列不改):
//   - 刪頻道:Store 目前沒有 DeleteChannel,所以不會留下 msg:<channel_id> 的序號列;將來加刪頻道要順手
//     delete from omixor_seq where scope = 'msg:' || id。
//   - Batch 最長可持有序號列的行鎖 10 分鐘;兩個批次若以相反順序建立不同類型(A 先 Issue 再任務、B 先任務再 Issue)
//     可能互等而死鎖,資料庫會偵測並中止其中一個(整批還原)。呼叫端同一批次內請固定建立順序。
const pgSeqSchema = `
create table if not exists omixor_seq (
  org text not null,
  scope text not null,
  last integer not null,
  primary key (org, scope)
);
`

// seqScopeMax 該 scope 的「現有資料最大號碼」子查詢(用 $1 = org;訊息多用一個 $4 = 頻道 id)。只看 number > 0,
// 對得上 (org, number) 的部分唯一索引;非數字 id 的資料 number 是 0,不算
func seqScopeMax(scope string) (sql string, extra []any) {
	const f = `(select max(number) from %s where org = $1 and number > 0%s)`
	switch scope {
	case "issue":
		return fmt.Sprintf(f, "ops_issues", " and project = ''"), nil
	case "task":
		return fmt.Sprintf(f, "omixor_agent_tasks", ""), nil
	case "doc":
		return fmt.Sprintf(f, "omixor_docs", ""), nil
	case "channel":
		return fmt.Sprintf(f, "omixor_channels", ""), nil
	case "incident":
		return fmt.Sprintf(f, "omixor_incidents", ""), nil
	case "decision":
		return fmt.Sprintf(f, "omixor_decisions", ""), nil
	}
	if is, ok := strings.CutPrefix(scope, "item:"); ok { // 子項的序號欄位叫 seq(不是 number),每個 Issue 一組
		return `(select max(seq) from omixor_issue_items where org = $1 and issue_id = $4)`, []any{is}
	}
	if pr, ok := strings.CutPrefix(scope, "issue:"); ok {
		return fmt.Sprintf(f, "ops_issues", " and project = $4"), []any{pr}
	}
	if ch, ok := strings.CutPrefix(scope, "msg:"); ok {
		return fmt.Sprintf(f, "omixor_channel_messages", " and channel_id = $4"), []any{ch}
	}
	return "", nil
}

// seqNext 取下一個號碼:單句 upsert,同一個 (org, scope) 的並發建立在這一列的行鎖上排隊
// (鎖到交易結束,所以同一個交易裡接著讀 max(pos) 等也被這把鎖保護)。floor 是起始下限
// (事件的號碼從 model.IncidentNumberBase 往上),不需要時給 0。
// 自癒:新號碼 = max(記錄的最後號碼, 現有資料最大號碼, floor) + 1。滾動升級時舊版實例用 max+1 先發出的號碼、
// 手動 SQL、還原的資料,都不會讓之後的建立永遠撞唯一索引(max 走 (org, number) 索引)
func seqNext(c context.Context, tx pgx.Tx, org, scope string, floor int) (int, error) {
	mx, extra := seqScopeMax(scope)
	if mx == "" {
		mx = "0"
	}
	var n int
	args := append([]any{org, scope, floor}, extra...)
	err := tx.QueryRow(c, fmt.Sprintf(`insert into omixor_seq (org, scope, last) values ($1, $2, greatest($3::integer, coalesce(%[1]s, 0)) + 1)
		on conflict (org, scope) do update set last = greatest(omixor_seq.last, $3::integer, coalesce(%[1]s, 0)) + 1
		returning last`, mx), args...).Scan(&n)
	return n, err
}

// seqBump 匯入帶著指定號碼的資料時,把記錄的最後號碼拉到至少 n(不會調低);順便鎖住這一列
func seqBump(c context.Context, tx pgx.Tx, org, scope string, n int) error {
	_, err := tx.Exec(c, `insert into omixor_seq (org, scope, last) values ($1, $2, $3::integer)
		on conflict (org, scope) do update set last = greatest(omixor_seq.last, $3::integer)`, org, scope, n)
	return err
}

// 要做唯一性保險的表:表、索引名、(org, number) 之外的鍵欄位(訊息是 channel_id)
var seqTables = []struct{ table, index, extra string }{
	{"ops_issues", "ops_issues_org_number", "project"}, // Issue 的號碼在每個範圍(組織層級 / 各專案)內唯一
	{"omixor_agent_tasks", "omixor_agent_tasks_org_number", ""},
	{"omixor_docs", "omixor_docs_org_number", ""},
	{"omixor_channels", "omixor_channels_org_number", ""},
	{"omixor_channel_messages", "omixor_channel_messages_org_chan_number", "channel_id"},
	{"omixor_incidents", "omixor_incidents_org_number", ""},
	{"omixor_decisions", "omixor_decisions_org_number", ""},
}

// migrateSeq 建序號表,並對每個 (org, scope) 以「現有最大號碼」與「已存在的序號列」取較大者初始化
// (冪等,不降低已經發出去的號碼);再補 (org, number) 的唯一索引。唯一索引是「部分索引 where number > 0」:
// 非數字 id(gh-1 這類,number = 0)不參與唯一性。索引那一段慢或失敗只寫 log,不讓啟動失敗
func (p *PG) migrateSeq() error {
	c, cancel := p.migCtx(10 * time.Minute) // 自己的逾時與啟動整體預算(剩餘)取較短
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := xactLock(c, tx, "omixor-seq-migrate"); err != nil {
		if errors.Is(err, errLockWait) || isTimeout(c, err) {
			warnf("編號序號遷移:%v,略過這次(下次啟動再做)", err)
			return nil
		}
		return err
	}
	if _, err := tx.Exec(c, pgSeqSchema); err != nil {
		return err
	}
	for _, q := range []string{
		`insert into omixor_seq (org, scope, last) select org, 'issue', max(number) from ops_issues where project = '' group by org
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'issue:' || project, max(number) from ops_issues where project <> '' group by org, project
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'task', max(number) from omixor_agent_tasks group by org
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'doc', max(number) from omixor_docs group by org
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'channel', max(number) from omixor_channels group by org
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'incident', max(number) from omixor_incidents group by org
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'decision', max(number) from omixor_decisions group by org
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
		`insert into omixor_seq (org, scope, last) select org, 'msg:' || channel_id, max(number) from omixor_channel_messages group by org, channel_id
			on conflict (org, scope) do update set last = greatest(omixor_seq.last, excluded.last)`,
	} {
		if _, err := tx.Exec(c, q); err != nil {
			return err
		}
	}
	if err := tx.Commit(c); err != nil {
		return err
	}
	if p.root == nil {
		return nil
	}
	// 唯一索引:用 create unique index concurrently(不擋寫入,不能在交易裡),獨立一條連線並持有 session 層級的 advisory lock,
	// 多個實例同時啟動只有一個在建(避免把別人正在建的 invalid 索引當殘留刪掉)。各表各自獨立,失敗只告警
	unlock, ok, err := p.sessionLock(c, "omixor-seq-index", "序號唯一索引")
	if err != nil {
		log.Printf("警告:序號唯一索引的鎖取不到:%v", err)
		return nil
	}
	if !ok {
		return nil
	}
	defer unlock()
	for _, t := range seqTables {
		if err := ensureSeqIndex(c, p.pool, t.table, t.index, t.extra); err != nil {
			log.Printf("警告:序號唯一索引 %s 處理失敗(服務照常啟動,這條約束暫時沒有):%v", t.index, err)
		}
	}
	return nil
}

type seqConn interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// ensureSeqIndex 保證 (org[, extra], number) where number > 0 的有效唯一索引:欄位、唯一、部分索引條件都要對。
// 欄位對不上的(舊版 ops_issues_org_number 只有 (org, number))、非部分索引,先在別的名字下建好新的,建成功才拿掉舊的再改名,
// 所以換索引的過程中唯一性一直有保障;concurrently 失敗留下的 invalid 索引先 drop。現有資料有重複就只告警、不建(舊的留著)
func ensureSeqIndex(c context.Context, q seqConn, table, index, extra string) error {
	cols := "org, number"
	want := []string{"org", "number"}
	if extra != "" {
		cols = "org, " + extra + ", number"
		want = []string{"org", extra, "number"}
	}
	var valid, partial, unique bool
	var have []string
	err := q.QueryRow(c, `select i.indisvalid, i.indpred is not null, i.indisunique,
			array(select pg_get_indexdef(i.indexrelid, k, true) from generate_series(1, i.indnkeyatts::int) k order by k)
		from pg_index i where i.indexrelid = to_regclass($1)`, index).Scan(&valid, &partial, &unique, &have)
	exists := true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		exists = false
	case err != nil:
		return err
	case valid && partial && unique && strings.Join(have, ",") == strings.Join(want, ","):
		return nil
	case !valid: // 殘留的 invalid:直接拿掉重建
		if _, err := q.Exec(c, fmt.Sprintf(`drop index concurrently if exists %s`, index)); err != nil {
			return err
		}
		exists = false
	}
	var dup int
	if err := q.QueryRow(c, fmt.Sprintf(`select count(*) from (select 1 from %s where number > 0 group by %s having count(*) > 1) d`, table, cols)).Scan(&dup); err != nil {
		return err
	}
	if dup > 0 {
		log.Printf("警告:%s 有 %d 組重複的 (%s),略過唯一索引 %s,請先清掉重複的資料", table, dup, cols, index)
		return nil
	}
	build := index
	if exists {
		build = index + "_new" // 舊的(對不上的)還在:先在別的名字下建,成功才換
		if _, err := q.Exec(c, fmt.Sprintf(`drop index concurrently if exists %s`, build)); err != nil {
			return err
		}
	}
	if _, err := q.Exec(c, fmt.Sprintf(`create unique index concurrently if not exists %s on %s (%s) where number > 0`, build, table, cols)); err != nil {
		_, _ = q.Exec(context.Background(), fmt.Sprintf(`drop index concurrently if exists %s`, build)) // 留下的 invalid 索引
		return err
	}
	if exists {
		if _, err := q.Exec(c, fmt.Sprintf(`drop index concurrently if exists %s`, index)); err != nil {
			return err
		}
		if _, err := q.Exec(c, fmt.Sprintf(`alter index %s rename to %s`, build, index)); err != nil {
			return err
		}
	}
	return nil
}

func msgScope(channelID string) string { return fmt.Sprintf("msg:%s", channelID) }
