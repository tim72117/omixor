package store

// ISS-36 資料層與遷移的獨立審查修正(H1、M1~M4、L1、L2、L5)的測試:
//   H1 壞 jsonb 列使啟動失敗(訊息可操作)、修好後重啟成功;M1 已讀合併保留最大 last_read;M2 結構完整的資料庫建表不被表鎖擋;
//   M3 前面步驟被略過時引擎給明確訊息;M4 批次更新耗時近線性;L1 並行修改的訊息;L2 user:<使用者 id>;L5 全空欄位不受大表門檻限制

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---- H1 ----

// 壞掉的舊 jsonb 列(組織鏈 edges 是字串、頻道 members 是物件):啟動失敗,訊息指出表、主鍵、舊欄位與修法;
// 修掉那些列後重啟成功,22 欄全部轉完
func TestActorColumnsBrokenJSONBFailsStartup(t *testing.T) {
	url := testPGBlank(t)
	p := fkPG(t, url)
	mkOrgs(t, p, "o")
	legacyActorStructure(t, p)
	now := time.Now()
	pgExec(t, p, `insert into omixor_org_chains (org, edges) values ('o', '"str"')`)
	pgExec(t, p, `insert into omixor_channels (org, id, number, name, members, created_at, updated_at, created_by) values ('o', 'CH-9', 9, 'bad', '{"a":1}', $1, $1, 'a@x')`, now)
	p.Close()

	captureLog(t)
	if n, err := NewPG(url); err == nil {
		n.Close()
		t.Fatal("有壞掉的舊 jsonb 列,啟動要失敗")
	} else {
		msg := err.Error()
		for _, want := range []string{"omixor_org_chains", "(o)", "edges", "omixor_channels", "(o/CH-9)", "members", "修復", "刪除", "delete from", "重啟", "UnmigratedRows"} {
			if !strings.Contains(msg, want) {
				t.Errorf("失敗訊息缺 %q:\n%s", want, msg)
			}
		}
	}

	raw, err := pgxpool.New(context.Background(), url)
	chk(t, err)
	defer raw.Close()
	for _, q := range []string{`delete from omixor_org_chains where org = 'o'`, `delete from omixor_channels where org = 'o'`} {
		if _, err := raw.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	n, err := NewPG(url)
	if err != nil {
		t.Fatalf("修掉壞列後重啟要成功:%v", err)
	}
	n.WaitFKValidate()
	t.Cleanup(n.Close)
	pend, err := n.actorColumnsPending(context.Background())
	chk(t, err)
	if len(pend) != 0 {
		t.Fatal("還有欄位沒轉完:", pend)
	}
}

// 拆表遷移預算用完而沒展開的列:失敗訊息提示調大 OMIXOR_MIGRATE_BUDGET_MINUTES 或重啟接續
func TestActorColumnsSplitSkippedMessageActionable(t *testing.T) {
	fx := newActorFx(t)
	fx.seedValue(t, ActorColumns[0], "o", 0, "a@x", "")
	pgExec(t, fx.p, `insert into omixor_channels (org, id, number, name, created_at, updated_at) values ('o', 'CH-1', 1, 'raw', now(), now())`)
	fx.p.note.setSkip("拆表遷移 頻道", "逾時或啟動預算用完")
	err := fx.p.migrateActorColumnsStep()
	if err == nil {
		t.Fatal("還有未展開的列,不能啟動")
	}
	for _, want := range []string{"OMIXOR_MIGRATE_BUDGET_MINUTES", "重啟", "omixor_channels", "拆表遷移 頻道"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("缺 %q:%v", want, err)
		}
	}
	pgExec(t, fx.p, `update omixor_channels set split_at = now()`)
	fx.p.note.setSkip("拆表遷移 頻道", "")
	chk(t, fx.p.migrateActorColumnsStep())
}

// ---- M1 ----

// 已讀去重:同一人三種寫法、last_read = 2、4、1,at 遞增(最新的 last_read 最小):保留最大的 last_read,已讀位置不倒退
func TestActorColumnsReadsKeepMaxLastRead(t *testing.T) {
	fx := newActorFx(t)
	now := time.Now()
	t0 := now.Add(-time.Hour)
	fx.insert(t, "omixor_channels", "org", "o", "id", "c1", "number", 1, "name", "c1", "created_at", now, "updated_at", now, "created_by", "sys_omixor")
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c1", "member", "a@x", "last_read", 2, "at", t0)
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c1", "member", "user:a@x", "last_read", 4, "at", t0.Add(time.Minute))
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c1", "member", "A@X", "last_read", 1, "at", t0.Add(2*time.Minute))
	for pos, m := range []string{"a@x", "user:a@x", "A@X"} { // 成員保留 pos 小的(不變)
		fx.insert(t, "omixor_channel_members", "org", "o", "channel_id", "c1", "member", m, "pos", pos)
	}
	wantAllConverted(t, runCols(t, fx.p))
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_reads where channel_id = 'c1' and member = $1`, fx.userA); n != 1 {
		t.Fatal("已讀剩一筆", n)
	}
	if lr := pgCount(t, fx.p, `select last_read from omixor_channel_reads where channel_id = 'c1' and member = $1`, fx.userA); lr != 4 {
		t.Fatal("已讀要保留最大的 last_read(4),不能倒退", lr)
	}
	if pos := pgCount(t, fx.p, `select pos from omixor_channel_members where channel_id = 'c1' and member = $1`, fx.userA); pos != 0 {
		t.Fatal("成員保留 pos 小的", pos)
	}
}

// ---- M2 ----

// 結構完整的資料庫:另一個連線持有熱表的 ACCESS EXCLUSIVE 鎖時,建表(補欄位、索引檢查)不會被擋住
func TestBootstrapSchemaNotBlockedByTableLock(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	c := context.Background()
	conn, err := p.root.Acquire(c)
	chk(t, err)
	defer conn.Release()
	tx, err := conn.Begin(c)
	chk(t, err)
	defer tx.Rollback(c)
	tables := map[string]bool{}
	for _, col := range pgSchemaColumns {
		tables[col.table] = true
	}
	for _, ix := range pgSchemaIndexes {
		var tb string
		chk(t, p.pool.QueryRow(c, `select tablename from pg_indexes where schemaname = current_schema() and indexname = $1`, ix.name).Scan(&tb))
		tables[tb] = true
	}
	for tb := range tables {
		if _, err := tx.Exec(c, `lock table `+tb+` in access exclusive mode`); err != nil {
			t.Fatal(tb, err)
		}
	}
	done := make(chan error, 1)
	go func() {
		bc, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		done <- bootstrapSchema(bc, p.root)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("欄位、索引都已存在,建表不該被別的連線持有的 ACCESS EXCLUSIVE 鎖擋住")
	}
}

// 舊結構(缺欄位、缺索引)補得回來:結構與原本一致
func TestBootstrapSchemaAddsMissingColumnsAndIndexes(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	want := structureFP(t, p)
	pgExec(t, p, `drop index ops_issues_org_key`)
	pgExec(t, p, `drop index omixor_audit_at`)
	pgExec(t, p, `alter table omixor_orgs drop column note`)
	pgExec(t, p, `alter table omixor_audit drop column source`)
	chk(t, bootstrapSchema(context.Background(), p.root))
	if d := diffFP(structureFP(t, p), want); len(d) > 0 {
		t.Fatalf("補回之後結構要一致:\n%s", strings.Join(d, "\n"))
	}
}

// ---- M3 ----

// 角色 actor 遷移因等不到鎖被略過、omixor_agents.actor_id 不存在:引擎給明確訊息(哪個步驟、為什麼、怎麼處理),不是 17 段 SQL 錯誤
func TestActorColumnsExplainsSkippedAgentActors(t *testing.T) {
	fx := newActorFx(t)
	fx.seedValue(t, ActorColumns[0], "o", 0, "agent:dev", "")
	pgExec(t, fx.p, `alter table omixor_agents drop column actor_id cascade`)

	old := migrateLockWait
	migrateLockWait = 300 * time.Millisecond
	t.Cleanup(func() { migrateLockWait = old })
	holder, err := fx.p.root.Acquire(context.Background())
	chk(t, err)
	var got bool
	chk(t, holder.QueryRow(context.Background(), `select pg_try_advisory_lock(hashtext('omixor-agent-actors-migrate'))`).Scan(&got))
	if !got {
		t.Fatal("拿不到鎖")
	}
	chk(t, fx.p.migrateAgentActors()) // 等不到鎖 → 略過、不是錯誤
	if _, err := holder.Exec(context.Background(), `select pg_advisory_unlock(hashtext('omixor-agent-actors-migrate'))`); err != nil {
		t.Fatal(err)
	}
	holder.Release()

	buf := captureLog(t)
	err = fx.p.migrateActorColumnsStep()
	if err == nil {
		t.Fatal("角色 actor 遷移被略過,引擎不能照常做")
	}
	msg := err.Error()
	for _, want := range []string{"角色 actor 遷移", "omixor_agents.actor_id", "等不到遷移鎖", "重啟"} {
		if !strings.Contains(msg, want) {
			t.Errorf("訊息缺 %q:%s", want, msg)
		}
	}
	if strings.Contains(msg, "does not exist") || strings.Contains(buf.String(), "does not exist") {
		t.Errorf("不該出現 SQL 欄位不存在的錯誤:%s\n%s", msg, buf.String())
	}
	// 鎖放掉、重做角色步驟之後引擎可以做完
	chk(t, fx.p.migrateAgentActors())
	wantAllConverted(t, runCols(t, fx.p))
	chk(t, fx.p.migrateActorColumnsStep())
}

// ---- M4 ----

// 批次更新的耗時隨列數近線性(舊寫法每批從表頭重掃,N 變 2N 約 4 倍):比較 N 與 2N 的耗時比,不寫死秒數
func TestActorBatchUpdateScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	withActorBatch(t, 500)
	col := ActorColumns[0] // ops_issues.created_by
	run := func(n int) time.Duration {
		fx := newActorFx(t)
		pgExec(t, fx.p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at)
select 'o', 'i' || g, g, g, 't', 'open', 'normal', 'a@x', now(), now() from generate_series(1, $1::int) g`, n)
		pgExec(t, fx.p, `analyze ops_issues`)
		start := time.Now()
		res, err := fx.p.actorBatchUpdate(context.Background(), col, "created_by", []actorMap{{actorValue{org: "o", id: "a@x"}, "sys_omixor"}})
		d := time.Since(start)
		chk(t, err)
		if res.Updated != int64(n) || res.Concurrent != 0 {
			t.Fatalf("n=%d 更新了 %+v", n, res)
		}
		if c := pgCount(t, fx.p, `select count(*) from ops_issues where created_by = 'sys_omixor'`); c != n {
			t.Fatalf("n=%d 轉了 %d 列", n, c)
		}
		return d
	}
	// 取多次中的最小值(負載造成的變慢只會讓某一次變大,最小值最接近真實成本)
	minOf := func(n int) time.Duration {
		best := run(n)
		for i := 0; i < 2; i++ {
			if d := run(n); d < best {
				best = d
			}
		}
		return best
	}
	small, big := minOf(100000), minOf(200000)
	ratio := float64(big) / float64(small)
	t.Logf("100k 列 %v;200k 列 %v;比值 %.2f(線性約 2,平方約 4)", small, big, ratio)
	if ratio > 3.0 {
		t.Fatalf("耗時比 %.2f 超過 3(取 3 次最小值),批次更新不是近線性", ratio)
	}
}

// ---- L1 ----

// 批次被並行更新的列(舊程式仍在寫舊字串)被跳過:訊息寫「有 N 列在轉換期間被並行修改,將於下次啟動重試」,不寫「孤兒」
func TestActorColumnsConcurrentModificationMessage(t *testing.T) {
	fx := newActorFx(t)
	col := ActorColumns[0]
	fx.seedValue(t, col, "o", 0, "a@x", "")
	fx.seedValue(t, col, "o", 1, "a@x", "")
	// 另一個交易先鎖住第二列並改成新的舊字串(還沒 commit):引擎的批次更新在那一列等鎖,commit 之後那一列已是新版本,被跳過
	c := context.Background()
	conn, err := fx.p.root.Acquire(c)
	chk(t, err)
	defer conn.Release()
	tx, err := conn.Begin(c)
	chk(t, err)
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `update ops_issues set created_by = 'z@x' where title = 'created_by-1'`); err != nil {
		t.Fatal(err)
	}
	buf := captureLog(t)
	var wg sync.WaitGroup
	var rep *ActorColumnsReport
	var rerr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		rep, rerr = fx.p.runActorColumns()
	}()
	time.Sleep(1500 * time.Millisecond)
	chk(t, tx.Commit(c))
	wg.Wait()
	chk(t, rerr)
	why := rep.Skipped[col.Key()]
	if !strings.Contains(why, "在轉換期間被並行修改") || !strings.Contains(why, "下次啟動重試") || strings.Contains(why, "孤兒") {
		t.Fatalf("略過原因:%q rep=%+v %s", why, rep, buf.String())
	}
	if !strings.Contains(buf.String(), "在轉換期間被並行修改") {
		t.Fatal("警告要說並行修改:", buf.String())
	}
	wantAllConverted(t, runCols(t, fx.p)) // 下次啟動(重跑)就轉掉了
}

// ---- L2 ----

// 舊值 user:<使用者 id>(例如 user:u_a)對到真正的使用者,不建 handle = u_a 的未註冊使用者;id、handle 都找不到才建
func TestActorColumnsUserIDForm(t *testing.T) {
	fx := newActorFx(t)
	col := ActorColumns[0]
	q0 := fx.seedValue(t, col, "o", 0, "user:u_a", "")
	q1 := fx.seedValue(t, col, "o", 1, "user:u_tim", "")
	q2 := fx.seedValue(t, col, "o", 2, "user:u_nobody", "")
	wantAllConverted(t, runCols(t, fx.p))
	if got := readActor(t, fx.p, q0); got != fx.userA {
		t.Fatalf("user:u_a 要對到使用者 u_a 的 actor %s,得到 %s", fx.userA, got)
	}
	if got := readActor(t, fx.p, q1); got != fx.userTim {
		t.Fatalf("user:u_tim:%s", got)
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_actors where kind = 'user' and handle in ('u_a', 'u_tim')`); n != 0 {
		t.Fatal("不該建 handle 是使用者 id 的未註冊使用者", n)
	}
	g2 := readActor(t, fx.p, q2)
	if n := pgCount(t, fx.p, `select count(*) from omixor_actors where id = $1 and kind = 'user' and handle = 'u_nobody'`, g2); n != 1 {
		t.Fatal("找不到才建未註冊使用者", g2)
	}
}

// ---- L5 ----

// 全空(沒有任何非 NULL 值)的欄位不受大表門檻限制(例如舊庫剛加的 ops_issues.deleted_by);有值的欄位照常被擋
func TestActorColumnsAllNullColumnBypassesOversize(t *testing.T) {
	fx := newActorFx(t)
	pgExec(t, fx.p, `alter table ops_issues alter column deleted_by drop default, alter column deleted_by drop not null`) // 舊庫剛加的欄位:可為 NULL、沒預設值
	fx.seedValue(t, ActorColumns[0], "o", 0, "a@x", "")                                                                 // ops_issues.created_by 有值
	if n := pgCount(t, fx.p, `select count(*) from ops_issues where deleted_by is not null`); n != 0 {
		t.Fatal("deleted_by 要全空", n)
	}
	withMaxRewrite(t, 1)
	rep := runCols(t, fx.p)
	if !containsStr(rep.Converted, "ops_issues.deleted_by") {
		t.Fatalf("全空的 deleted_by 不受門檻限制,要轉完:%+v", rep)
	}
	if !strings.Contains(rep.Skipped["ops_issues.created_by"], "門檻") {
		t.Fatalf("有值的 created_by 照常被大表門檻擋掉:%v", rep.Skipped)
	}
	if ex, _ := fkValidated(t, fx.p, "ops_issues", "fk_ops_issues_deleted_by"); !ex {
		t.Fatal("deleted_by 要加外鍵")
	}
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
