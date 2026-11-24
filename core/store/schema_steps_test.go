package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stepDB 開一個全新資料庫(NewPG 建好最終結構與版本表),回傳它的連線池與網址。測試自己用 runSteps 套用注入的步驟
func stepDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := testPGBlank(t)
	p, err := NewPG(url)
	chk(t, err)
	t.Cleanup(p.Close)
	return p.root, url
}

func scanInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	chk(t, pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	chk(t, pool.QueryRow(context.Background(), `select to_regclass($1) is not null`, name).Scan(&ok))
	return ok
}

func maxVersion(t *testing.T, pool *pgxpool.Pool) int {
	return scanInt(t, pool, `select max(version) from omixor_schema_version`)
}

// (a) 步驟套用一次、版本被記錄;重跑是 no-op(步驟的 SQL 沒有 if not exists,真的再跑一次會失敗)
func TestRunStepsAppliesOnce(t *testing.T) {
	pool, _ := stepDB(t)
	b := knownSchemaVersion(schemaSteps) // 全新資料庫已記錄所有真正的步驟;測試用的步驟接在後面
	steps := []schemaStep{{b + 1, "add_zz_once", `create table zz_once (id int); alter table omixor_org_system add column if not exists zz_col text`}}
	c := context.Background()
	chk(t, runSteps(c, pool, steps))
	if !tableExists(t, pool, "zz_once") || maxVersion(t, pool) != b+1 {
		t.Fatalf("步驟沒套用或沒記版本:max=%d", maxVersion(t, pool))
	}
	if n := scanInt(t, pool, `select count(*) from information_schema.columns where table_name = 'omixor_org_system' and column_name = 'zz_col'`); n != 1 {
		t.Fatalf("欄位沒加:%d", n)
	}
	if n := scanInt(t, pool, `select count(*) from omixor_schema_version where version = $1 and name = 'add_zz_once'`, b+1); n != 1 {
		t.Fatalf("版本記錄不對:%d", n)
	}
	chk(t, runSteps(c, pool, steps)) // 已經做過:略過,不會因為 create table 重複而失敗
	if n := scanInt(t, pool, `select count(*) from omixor_schema_version`); n != b+1 {
		t.Fatalf("重跑後版本記錄數應仍是 %d(baseline + 所有步驟):%d", b+1, n)
	}
}

// (b) 失敗的步驟:該步驟的交易回滾、不寫記錄、錯誤含版本與名稱;之前成功的步驟保留
func TestRunStepsFailureRollsBack(t *testing.T) {
	pool, _ := stepDB(t)
	b := knownSchemaVersion(schemaSteps)
	steps := []schemaStep{
		{b + 1, "ok_step", `create table zz_ok (id int)`},
		{b + 2, "bad_step", `create table zz_bad (id int); select 1/0`},
		{b + 3, "after_bad", `create table zz_after (id int)`},
	}
	err := runSteps(context.Background(), pool, steps)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(b+2)) || !strings.Contains(err.Error(), "bad_step") {
		t.Fatalf("錯誤要含步驟版本與名稱:%v", err)
	}
	if !tableExists(t, pool, "zz_ok") || maxVersion(t, pool) != b+1 {
		t.Fatalf("之前成功的步驟要保留:max=%d", maxVersion(t, pool))
	}
	if tableExists(t, pool, "zz_bad") {
		t.Error("失敗步驟的交易要回滾(zz_bad 不該存在)")
	}
	if tableExists(t, pool, "zz_after") {
		t.Error("失敗之後的步驟不該執行")
	}
	if n := scanInt(t, pool, `select count(*) from omixor_schema_version where version >= $1`, b+2); n != 0 {
		t.Errorf("失敗的步驟不該寫版本記錄:%d", n)
	}
}

// (c) 兩個連線池同時執行:步驟只套用一次(不重複、不死鎖)
func TestRunStepsConcurrent(t *testing.T) {
	pool1, url := stepDB(t)
	pool2, err := pgxpool.New(context.Background(), url)
	chk(t, err)
	t.Cleanup(pool2.Close)
	b := knownSchemaVersion(schemaSteps)
	steps := []schemaStep{
		{b + 1, "count_a", `create table if not exists zz_cnt (n int); insert into zz_cnt values (1); select pg_sleep(0.5)`},
		{b + 2, "count_b", `insert into zz_cnt values (2)`},
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, pl := range []*pgxpool.Pool{pool1, pool2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = runSteps(context.Background(), pl, steps)
		}()
	}
	wg.Wait()
	for _, e := range errs {
		chk(t, e)
	}
	if n := scanInt(t, pool1, `select count(*) from zz_cnt`); n != 2 {
		t.Fatalf("步驟應各套用一次(共 2 列),實際 %d 列", n)
	}
	if n := scanInt(t, pool1, `select count(*) from omixor_schema_version`); n != b+2 {
		t.Fatalf("版本記錄應有 baseline + 所有步驟 + 2 個測試步驟(共 %d 筆):%d", b+2, n)
	}
}

// (d) 資料庫版本比程式已知的新(部署回滾):不報錯、不動資料庫
func TestRunStepsDatabaseNewerThanProgram(t *testing.T) {
	pool, _ := stepDB(t)
	b := knownSchemaVersion(schemaSteps)
	before := scanInt(t, pool, `select count(*) from omixor_schema_version`)
	_, err := pool.Exec(context.Background(), `insert into omixor_schema_version (version, name) values (99, 'future')`)
	chk(t, err)
	chk(t, runSteps(context.Background(), pool, []schemaStep{{b + 1, "old_known", `create table zz_future (id int)`}}))
	if tableExists(t, pool, "zz_future") {
		t.Error("資料庫版本較新時不該執行步驟")
	}
	if maxVersion(t, pool) != 99 || scanInt(t, pool, `select count(*) from omixor_schema_version`) != before+1 {
		t.Error("不該改動版本記錄")
	}
}

// 沒有版本記錄的舊資料庫(有表、沒有版本表):啟動時補版本表並蓋 baseline,再套用步驟
func TestStampBaselineOnLegacyDatabase(t *testing.T) {
	pool, _ := stepDB(t)
	c := context.Background()
	_, err := pool.Exec(c, `drop table omixor_schema_version`)
	chk(t, err)
	chk(t, bootstrapSchema(c, pool))
	if v := maxVersion(t, pool); v != knownSchemaVersion(schemaSteps) {
		t.Fatalf("補版本後最大版本應是程式已知的 %d:%d", knownSchemaVersion(schemaSteps), v)
	}
	if n := scanInt(t, pool, `select count(*) from omixor_schema_version where version = $1 and name = 'baseline'`, baselineVersion); n != 1 {
		t.Fatalf("要蓋上 baseline:%d", n)
	}
}

func TestRetryLock(t *testing.T) {
	lockErr := &pgconn.PgError{Code: "55P03"}
	calls := 0
	err := retryLock(context.Background(), 5, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return lockErr
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("鎖逾時應重試到成功:calls=%d err=%v", calls, err)
	}
	calls = 0
	err = retryLock(context.Background(), 3, time.Millisecond, func() error { calls++; return &pgconn.PgError{Code: "40P01"} })
	if calls != 3 || !isLockErr(err) {
		t.Fatalf("死鎖最多重試 3 次後回錯誤:calls=%d err=%v", calls, err)
	}
	calls = 0
	other := errors.New("syntax")
	if err := retryLock(context.Background(), 5, time.Millisecond, func() error { calls++; return other }); calls != 1 || err != other {
		t.Fatalf("非鎖錯誤不重試:calls=%d err=%v", calls, err)
	}
}

// stepRuleWhitelist 破例名單:步驟版本 → 原因。要允許 drop、rename、改型別的步驟,必須在這裡加一筆並寫原因(且確認舊版程式仍相容)
var stepRuleWhitelist = map[int]string{}

// (e) 步驟清單的守門:版本嚴格遞增且連續、從 2 開始、名稱與 SQL 非空、禁止 drop / rename / alter column ... type
func TestSchemaStepsRules(t *testing.T) {
	for _, p := range stepProblems(schemaSteps) {
		t.Error(p)
	}
}

// stepProblems 檢查步驟清單,回傳問題描述(空 = 沒問題)
func stepProblems(steps []schemaStep) []string {
	var out []string
	prev := baselineVersion
	for i, s := range steps {
		if i == 0 && s.Version != baselineVersion+1 {
			out = append(out, fmt.Sprintf("第一個步驟的版本要是 %d(1 保留給 baseline):%d", baselineVersion+1, s.Version))
		}
		if s.Version <= prev {
			out = append(out, fmt.Sprintf("步驟版本要嚴格遞增、不重複:%d(%s)接在 %d 後面", s.Version, s.Name, prev))
		} else if i > 0 && s.Version != prev+1 {
			out = append(out, fmt.Sprintf("步驟版本要連續(目前最大 + 1):%d(%s)接在 %d 後面", s.Version, s.Name, prev))
		}
		prev = s.Version
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.SQL) == "" {
			out = append(out, fmt.Sprintf("步驟 %d 的名稱與 SQL 都不能空", s.Version))
		}
		if _, ok := stepRuleWhitelist[s.Version]; ok {
			continue
		}
		for _, stmt := range strings.Split(s.SQL, ";") {
			l := " " + strings.ToLower(strings.Join(strings.Fields(stmt), " ")) + " "
			if strings.HasPrefix(l, " drop ") || strings.Contains(l, " rename ") || strings.Contains(l, " drop column ") ||
				strings.Contains(l, " drop constraint ") && !strings.Contains(l, " if exists ") ||
				strings.HasPrefix(l, " alter table ") && strings.Contains(l, " alter column ") && strings.Contains(l, " type ") {
				out = append(out, fmt.Sprintf("步驟 %d(%s)有不向後相容的語句(drop、rename、改型別;要破例請加進 stepRuleWhitelist 並寫原因):%s", s.Version, s.Name, strings.TrimSpace(stmt)))
			}
		}
	}
	return out
}

// 守門檢查本身要擋得住壞的清單
func TestStepProblemsCatchesBadLists(t *testing.T) {
	bad := [][]schemaStep{
		{{3, "skip", "select 1"}},                            // 不是從 2 開始
		{{2, "a", "select 1"}, {2, "dup", "select 1"}},       // 重複
		{{2, "a", "select 1"}, {4, "gap", "select 1"}},       // 不連續
		{{2, "", "select 1"}},                                // 名稱空
		{{2, "a", " "}},                                      // SQL 空
		{{2, "d", "drop table x"}},                           // drop
		{{2, "r", "alter table x rename column a to b"}},     // rename
		{{2, "t", "alter table x alter column a type text"}}, // 改型別
		{{2, "dc", "alter table x drop column a"}},           // 刪欄位
	}
	for i, l := range bad {
		if len(stepProblems(l)) == 0 {
			t.Errorf("壞清單 #%d 沒被擋下:%+v", i, l)
		}
	}
	good := []schemaStep{{2, "ok", "alter table x add column if not exists a text"}, {3, "ix", "create index if not exists i on x (a)"}}
	if p := stepProblems(good); len(p) != 0 {
		t.Errorf("正常的清單不該被擋:%v", p)
	}
}
