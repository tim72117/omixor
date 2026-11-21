package store

// 啟動遷移的加固:大表不自動重寫、維護窗口腳本(舊結構、逐句執行)、
// 鎖等待與釋放、背景補外鍵、整體預算用完只警告;「誰」的欄位清單(給 actor 遷移用)

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// 維護窗口腳本由 projectRefTables 產生;檔案內容必須與產生結果一模一樣(OMIXOR_UPDATE_SQL=1 重新產生)
func TestPreMigrateSQLInSync(t *testing.T) {
	path := filepath.Join("sql", "premigrate.sql")
	want := premigrateSQL()
	if os.Getenv("OMIXOR_UPDATE_SQL") == "1" {
		chk(t, os.MkdirAll("sql", 0o755))
		chk(t, os.WriteFile(path, []byte(want), 0o644))
	}
	raw, err := os.ReadFile(path)
	chk(t, err)
	got := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if got != want {
		t.Fatalf("%s 與 projectRefTables 不一致(用 OMIXOR_UPDATE_SQL=1 go test -run TestPreMigrateSQLInSync ./store 重新產生)", path)
	}
	for _, tb := range projectRefTables {
		if !strings.Contains(got, "alter table "+tb+" add column if not exists project text not null default '';") {
			t.Errorf("腳本缺 %s 補 project", tb)
		}
		if !strings.Contains(got, "alter table "+tb+"\n  add column if not exists "+projectRefColumnDef+";") {
			t.Errorf("腳本缺 %s 的 project_ref", tb)
		}
	}
	if strings.Count(got, "alter table ") != 2*len(projectRefTables) {
		t.Error("每張表一句補 project、一句 project_ref")
	}
	if strings.Contains(got, "omixor_principal") || strings.Contains(got, "omixor_actor_node") {
		t.Error("身分登記層不部署,腳本不該有它")
	}
	if !strings.HasSuffix(got, "reset lock_timeout;\n") {
		t.Error("腳本結尾要有 reset lock_timeout")
	}
	if !strings.Contains(got, "-v ON_ERROR_STOP=1") || !strings.Contains(got, "不可以加 -1") {
		t.Error("檔頭要寫明 ON_ERROR_STOP、不可用 -1")
	}
}

// 「誰」的欄位清單(給 actor 遷移用)涵蓋所有身分欄位:每個表與欄位都存在、型別是 text;actor 的種類欄位也在。
// 同時確認身分登記層沒有部署:沒有登記表、身分函式、_ref 欄位(project_ref 除外)與 fk_p_ 外鍵
func TestIdentityFieldsExist(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	if len(IdentityFields) != 19 {
		t.Fatal("清單要有 19 個欄位", len(IdentityFields))
	}
	seen := map[string]bool{}
	for _, f := range IdentityFields {
		k := f.Table + "." + f.Column
		if seen[k] {
			t.Fatal("重複", k)
		}
		seen[k] = true
		if f.Form == IdentityActor && f.KindColumn == "" {
			t.Fatal("actor 要有種類欄位", k)
		}
		// 最終結構:IdentityFields 記的是舊欄位,現在存在的是 ActorColumns 的 Target(同欄位名,或改名、合併後的新欄位)
		tgt := ""
		for _, ac := range ActorColumns {
			if ac.Table == f.Table && ac.Column == f.Column {
				tgt = ac.Target
			}
		}
		if tgt == "" {
			t.Errorf("%s 沒有對應的 ActorColumns", k)
			continue
		}
		if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2 and data_type = 'text'`, f.Table, tgt); n != 1 {
			t.Errorf("%s.%s 不存在或不是 text", f.Table, tgt)
		}
	}
	if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and column_name like '%\_ref' and column_name <> 'project_ref'`); n != 0 {
		t.Fatal("不該有身分 _ref 欄位", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_constraint where conname like 'fk\_p\_%'`); n != 0 {
		t.Fatal("不該有 fk_p_ 外鍵", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_class where relname in ('omixor_principals', 'omixor_principal_meta') and relnamespace = current_schema()::regnamespace`); n != 0 {
		t.Fatal("不該有身分登記表", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_proc where (proname like 'omixor\_principal\_%' or proname = 'omixor_actor_node') and pronamespace = current_schema()::regnamespace`); n != 0 {
		t.Fatal("不該有身分函式", n)
	}
}

// ---- 大表保護 ----

func withMaxRewrite(t *testing.T, n int64) {
	t.Helper()
	old := maxAutoRewriteBytes
	maxAutoRewriteBytes = n
	t.Cleanup(func() { maxAutoRewriteBytes = old })
}

// 超過門檻:不自動 ALTER(三張表的 project_ref),只警告並進彙總;沒超過門檻就照做
func TestPGMigrateSkipsOversizedTables(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	for _, tb := range projectRefTables {
		pgExec(t, p, `alter table `+tb+` drop column if exists project_ref cascade`)
	}
	withMaxRewrite(t, 1) // 任何表(索引至少一頁)都超過
	buf := captureLog(t)
	chk(t, p.migrateForeignKeys())
	out := buf.String()
	for _, want := range []string{"ops_issues.project_ref", "omixor_docs.project_ref", "omixor_cabinet_sections.project_ref", "自動重寫可能鎖表很久", "sql/premigrate.sql", "OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("警告缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "project_ref 新增完成") {
		t.Fatal("超過門檻不該下 ALTER:", out)
	}
	if n := pgCount(t, p, `select count(*) from information_schema.columns where table_name in ('ops_issues','omixor_docs','omixor_cabinet_sections') and column_name = 'project_ref'`); n != 0 {
		t.Fatal("欄位不該被建立", n)
	}
	buf.Reset()
	p.logMigrationSummary()
	if !strings.Contains(buf.String(), "需在維護窗口手動處理的大表") || !strings.Contains(buf.String(), "ops_issues") {
		t.Fatal("啟動彙總要列出需手動的大表:", buf.String())
	}
	// 門檻調大:沒超過,照做
	withMaxRewrite(t, 1<<40)
	buf.Reset()
	chk(t, p.migrateForeignKeys())
	p.WaitFKValidate()
	pr, err := p.migrationProgress(bg())
	chk(t, err)
	if len(pr.MissingProjectRef) != 0 || len(pr.ManualTables) != 0 {
		t.Fatalf("沒超過門檻就要做完:%+v\n%s", pr, buf.String())
	}
	// 0 = 不限制
	withMaxRewrite(t, 0)
	if !p.rewriteAllowed(bg(), "omixor_docs", "test") {
		t.Fatal("0 = 不限制")
	}
}

// ---- 維護窗口腳本 ----

// 退回 ISS-34 之前的 ops_issues 結構(沒有 project、key、version、deleted_*、project_ref)與沒有 project_ref 的其他表
func downgradeToOldSchema(t *testing.T, p *PG) {
	t.Helper()
	for _, c := range []string{"project_ref", "project", "key", "version", "deleted_at", "deleted_by"} {
		pgExec(t, p, `alter table ops_issues drop column if exists `+c+` cascade`)
	}
	for _, tb := range []string{"omixor_docs", "omixor_cabinet_sections"} {
		pgExec(t, p, `alter table `+tb+` drop column if exists project_ref cascade`)
	}
}

// fkDump 全部外鍵(名稱、定義、是否已驗證)
func fkDump(t *testing.T, p *PG) string {
	t.Helper()
	rows, err := p.pool.Query(bg(), `select conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid) || ' valid=' || convalidated::text
		from pg_constraint where contype = 'f' and conname like 'fk\_%' order by 1`)
	chk(t, err)
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var x string
		chk(t, rows.Scan(&x))
		b.WriteString(x + "\n")
	}
	chk(t, rows.Err())
	return b.String()
}

func readPreSQL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("sql", "premigrate.sql"))
	chk(t, err)
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// scriptStatements 把腳本切成一句一句(去掉 -- 註解行)
func scriptStatements(script string) []string {
	var keep []string
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "--") {
			continue
		}
		keep = append(keep, l)
	}
	var out []string
	for _, st := range strings.Split(strings.Join(keep, "\n"), ";") {
		if st = strings.TrimSpace(st); st != "" {
			out = append(out, st)
		}
	}
	return out
}

func issuesSchemaDump(t *testing.T, p *PG) string {
	t.Helper()
	rows, err := p.pool.Query(bg(), `select column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '') || ' ' || is_generated || ' ' || coalesce(generation_expression, '')
		from information_schema.columns where table_schema = current_schema() and table_name in ('ops_issues', 'omixor_docs', 'omixor_cabinet_sections') order by table_name, column_name`)
	chk(t, err)
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		chk(t, rows.Scan(&s))
		b.WriteString(s + "\n")
	}
	chk(t, rows.Err())
	return b.String()
}

// 在 ISS-34 之前的舊結構上:先跑腳本、再啟動 NewPG,與直接啟動 NewPG,最後 schema 相同(腳本要先冪等補上 project 欄位,再加 project_ref)
func TestPGPreSQLOnOldIssueSchema(t *testing.T) {
	urlA := testPGBlank(t)
	pa := fkPG(t, urlA)
	downgradeToOldSchema(t, pa)
	pa.Close()
	urlB := testPGBlank(t)
	pb := fkPG(t, urlB)
	downgradeToOldSchema(t, pb)
	pgExec(t, pb, readPreSQL(t))
	pgExec(t, pb, readPreSQL(t)) // 冪等
	if n := pgCount(t, pb, `select count(*) from information_schema.columns where table_name = 'ops_issues' and column_name in ('project', 'project_ref')`); n != 2 {
		t.Fatal("腳本要補上 ops_issues.project 與 project_ref", n)
	}
	pb.Close()
	buf := captureLog(t)
	pb2 := fkPG(t, urlB)
	if strings.Contains(buf.String(), "project_ref 新增完成") {
		t.Fatal("腳本已建好欄位,啟動不該再 ALTER:", buf.String())
	}
	// 路徑 A:舊結構直接啟動
	pa2 := fkPG(t, urlA)
	if a, b := issuesSchemaDump(t, pa2), issuesSchemaDump(t, pb2); a != b {
		t.Fatalf("兩條路的 schema 要相同\nA:\n%s\nB:\n%s", a, b)
	}
	if a, b := fkDump(t, pa2), fkDump(t, pb2); a != b {
		t.Fatalf("外鍵要相同\nA:\n%s\nB:\n%s", a, b)
	}
}

// psql 逐句 autocommit 與一次送出整份(pgExec 用簡單協定一次送出多句,等同一個隱含交易)不同:
// 逐句在同一條連線上執行(set lock_timeout 是 session 層級),結果必須相同
func TestPreSQLStatementByStatement(t *testing.T) {
	urlA := testPGBlank(t)
	pa := fkPG(t, urlA)
	downgradeToOldSchema(t, pa)
	pgExec(t, pa, readPreSQL(t)) // 一次送出
	urlB := testPGBlank(t)
	pb := fkPG(t, urlB)
	downgradeToOldSchema(t, pb)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, urlB)
	chk(t, err)
	defer conn.Close(ctx)
	sts := scriptStatements(readPreSQL(t))
	if len(sts) < 6 {
		t.Fatal("切句不對", len(sts))
	}
	for _, st := range sts { // psql -v ON_ERROR_STOP=1 -f(沒有 -1):每句各自 autocommit
		if _, err := conn.Exec(ctx, st); err != nil {
			t.Fatal(st, err)
		}
	}
	var lt string
	chk(t, conn.QueryRow(ctx, `show lock_timeout`).Scan(&lt))
	if lt != "0" {
		t.Fatal("結尾的 reset lock_timeout 要生效:", lt)
	}
	if a, b := issuesSchemaDump(t, pa), issuesSchemaDump(t, pb); a != b {
		t.Fatalf("逐句與一次送出結果要相同\nA:\n%s\nB:\n%s", a, b)
	}
	if a, b := fkDump(t, pa), fkDump(t, pb); a != b {
		t.Fatal("外鍵要相同")
	}
}

// ---- 鎖等待、重試 ----

func TestRetryLock(t *testing.T) {
	lockErr := &pgconn.PgError{Code: "40P01"}
	calls := 0
	err := retryLock(bg(), 4, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("wrap: %w", lockErr)
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatal("死鎖要重試到成功", err, calls)
	}
	calls = 0
	other := errors.New("boom")
	if err := retryLock(bg(), 4, time.Millisecond, func() error { calls++; return other }); !errors.Is(err, other) || calls != 1 {
		t.Fatal("不是鎖錯誤不重試", err, calls)
	}
	calls = 0
	if err := retryLock(bg(), 3, time.Millisecond, func() error { calls++; return lockErr }); err == nil || calls != 3 {
		t.Fatal("一直鎖錯誤:試滿次數後回錯", err, calls)
	}
}

// 等鎖有獨立上限(不把整體預算花在等鎖):別人持有鎖時只警告、略過;放掉後拿得到;unlock 失敗(連線被砍)不 panic、鎖不殘留
func TestSessionLockWaitCapAndRelease(t *testing.T) {
	url := testPGBlank(t)
	p := fkPG(t, url)
	old := migrateLockWait
	migrateLockWait = 400 * time.Millisecond
	defer func() { migrateLockWait = old }()
	ctx := context.Background()
	other, err := pgx.Connect(ctx, url)
	chk(t, err)
	defer other.Close(ctx)
	_, err = other.Exec(ctx, `select pg_advisory_lock(hashtext('omixor-t-lock'))`)
	chk(t, err)
	buf := captureLog(t)
	var ok bool
	within(t, 10*time.Second, "sessionLock 等鎖上限", func() {
		_, ok, err = p.sessionLock(bg(), "omixor-t-lock", "測試")
	})
	chk(t, err)
	if ok || !strings.Contains(buf.String(), "等不到遷移鎖") {
		t.Fatal("等不到要警告並略過:", ok, buf.String())
	}
	_, err = other.Exec(ctx, `select pg_advisory_unlock(hashtext('omixor-t-lock'))`)
	chk(t, err)
	unlock, ok, err := p.sessionLock(bg(), "omixor-t-lock", "測試")
	chk(t, err)
	if !ok {
		t.Fatal("鎖放掉後要拿得到")
	}
	unlock()
	var got bool
	chk(t, other.QueryRow(ctx, `select pg_try_advisory_lock(hashtext('omixor-t-lock'))`).Scan(&got))
	if !got {
		t.Fatal("unlock 後別的連線要拿得到鎖")
	}
	_, _ = other.Exec(ctx, `select pg_advisory_unlock(hashtext('omixor-t-lock'))`)
	// unlock 失敗:持鎖的連線被砍掉
	unlock, ok, err = p.sessionLock(bg(), "omixor-t-lock", "測試")
	chk(t, err)
	if !ok {
		t.Fatal("要拿得到")
	}
	pgExec(t, p, `select pg_terminate_backend(pid) from pg_locks where locktype = 'advisory' and granted and database = (select oid from pg_database where datname = current_database())`)
	time.Sleep(200 * time.Millisecond)
	buf.Reset()
	unlock()
	if !strings.Contains(buf.String(), "釋放遷移鎖失敗") {
		t.Fatal("unlock 失敗要警告並關連線:", buf.String())
	}
	unlock, ok, err = p.sessionLock(bg(), "omixor-t-lock", "測試")
	chk(t, err)
	if !ok {
		t.Fatal("鎖不該殘留")
	}
	unlock()
}

// 背景補外鍵:被長交易擋住補不上的外鍵,每隔一段時間在背景重試,鎖放掉後補上並 validate;Close 能取消
func TestPGBackgroundFKRetry(t *testing.T) {
	url := testPGBlank(t)
	p := fkPG(t, url)
	oldT, oldR, oldW, oldE := fkLockTimeout, fkRetries, fkRetryWait, fkBackgroundRetryEvery
	fkLockTimeout, fkRetries, fkRetryWait, fkBackgroundRetryEvery = "100ms", 2, 10*time.Millisecond, 100*time.Millisecond
	defer func() { fkLockTimeout, fkRetries, fkRetryWait, fkBackgroundRetryEvery = oldT, oldR, oldW, oldE }()
	pgExec(t, p, `alter table omixor_docs drop constraint fk_docs_project`)
	release := holdTx(t, url, `lock table omixor_docs in row exclusive mode`) // 擋住 add constraint
	defer release()
	p.startFKValidate(fkSpecs())
	within(t, 10*time.Second, "第一輪", p.WaitFKValidate)
	if ex, _ := fkValidated(t, p, "omixor_docs", "fk_docs_project"); ex {
		t.Fatal("被擋住,還不該補上")
	}
	time.Sleep(400 * time.Millisecond) // 至少一輪重試也補不上
	release()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if ex, ok := fkValidated(t, p, "omixor_docs", "fk_docs_project"); ex && ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("鎖放掉後背景要把外鍵補上並驗證")
		}
		time.Sleep(100 * time.Millisecond)
	}
	within(t, 12*time.Second, "Close", p.Close)
}

// 整體預算用完:只警告、不讓啟動失敗;預算恢復後下次啟動補齊
func TestPGStartupBudgetExhaustedOnlyWarns(t *testing.T) {
	url := testPGBlank(t)
	old := migrateStartupBudget
	migrateStartupBudget = time.Millisecond
	buf := captureLog(t)
	p, err := NewPG(url)
	migrateStartupBudget = old
	if err != nil {
		// 唯一的例外:「誰」欄位的轉換沒做完就不能服務(程式只認轉換後的結構,見 pg_actor_columns.go);其他階段預算用完只警告
		if !strings.Contains(err.Error(), "actor 欄位遷移失敗") {
			t.Fatal("預算用完不該讓啟動失敗(actor 欄位轉換除外):", err)
		}
	} else {
		p.WaitFKValidate()
		p.Close()
	}
	if !strings.Contains(buf.String(), "[遷移警告]") {
		t.Fatal("要有警告:", buf.String())
	}
	if n := strings.Count(buf.String(), "查外鍵"); n > 3 {
		t.Fatalf("預算用完不該逐條外鍵各印一行(%d 行):%s", n, buf.String())
	}
	p2 := fkPG(t, url)
	if pr, err := p2.migrationProgress(bg()); err != nil || len(pr.MissingProjectRef) != 0 || len(pr.MissingFKs) != 0 {
		t.Fatalf("下次啟動要補齊:%+v %v", pr, err)
	}
}

func bg() context.Context { return context.Background() }

// ---- actor 欄位遷移的守門(ISS-36 C-1;DOC-13 §1.3、§7 第 10 點)----

// ActorColumns 的每個欄位都在資料庫裡(型別 text);Copy 模式的新欄位在轉換前還不存在。
// 反過來:資料庫裡名稱像「誰」的欄位(created_by、author、assignee、member、updated_by、deleted_by、commander、claimed_by、from_node、to_node、actor_*、user_id)
// 都必須登記在 ActorColumns(含改名後、複製出來的新欄位與事件的 actor_kind),或明確列在排除名單。以後新增這類欄位忘了登記,這個測試會失敗。
func TestActorColumnsGuard(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	registered := map[string]bool{}
	for _, c := range ActorColumns {
		registered[c.Table+"."+c.Column] = true
		registered[c.Table+"."+c.Target] = true
		if c.KindColumn != "" {
			registered[c.Table+"."+c.KindColumn] = true
		}
		if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2 and data_type = 'text'`, c.Table, c.Target); n != 1 {
			t.Errorf("%s.%s(最終結構)不存在或不是 text", c.Table, c.Target)
		}
		if c.Mode != ActorColInPlace { // 改名、複製的欄位:最終結構沒有舊欄位
			cols := []string{c.Column}
			if c.KindColumn != "" {
				cols = append(cols, c.KindColumn)
			}
			for _, oc := range cols {
				if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2`, c.Table, oc); n != 0 {
					t.Errorf("最終結構不該有舊欄位 %s.%s", c.Table, oc)
				}
			}
		}
	}
	// 排除名單:不是「誰」(或不由這個引擎轉)的同名欄位
	excluded := map[string]string{
		"omixor_tokens.user_id":         "使用者 id(u_…,D11 不改寫),不是 actor id",
		"omixor_org_members.user_id":    "使用者 id(u_…,D11 不改寫),不是 actor id",
		"omixor_users.actor_id":         "使用者指向自己的 actor(B-2b)",
		"omixor_agents.actor_id":        "角色指向自己的 actor(B-2c)",
		"omixor_actor_aliases.actor_id": "別名表指向 actor(C-1)",
	}
	rows, err := p.pool.Query(bg(), `select table_name || '.' || column_name from information_schema.columns
where table_schema = current_schema() and column_name ~ '^(created_by|author|assignee|member|updated_by|deleted_by|commander|claimed_by|from_node|to_node|actor_.*|user_id)$' order by 1`)
	chk(t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		chk(t, rows.Scan(&k))
		if !registered[k] && excluded[k] == "" {
			t.Errorf("%s 看起來是「誰」的欄位,卻沒登記在 ActorColumns(identity_fields.go),也不在排除名單", k)
		}
	}
	chk(t, rows.Err())
	// 排除名單裡的都還存在(欄位被移除就要一併拿掉排除項)
	for k := range excluded {
		parts := strings.SplitN(k, ".", 2)
		if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2`, parts[0], parts[1]); n != 1 {
			t.Errorf("排除名單的 %s 不存在了,請拿掉", k)
		}
	}
}
