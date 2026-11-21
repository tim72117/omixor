package testdb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMain(m *testing.M) { Main(m) }

func TestCheckTarget(t *testing.T) {
	ok := []string{
		"postgres://omixor:pw@127.0.0.1:5433/omixor_fx3?sslmode=disable",
		"postgres://u:p@localhost/mytest",
		"postgres://u:p@[::1]:5432/omixor_x",
		"postgres:///omixor_t?host=/var/run/postgresql",
		"host=127.0.0.1 dbname=omixor_dev user=u",
	}
	for _, u := range ok {
		if err := checkTarget(u, false); err != nil {
			t.Errorf("%s 應該放行:%v", u, err)
		}
	}
	bad := []string{
		"postgres://u:secretpw@db.prod.example.com:5432/omixor_x",
		"postgres://u:p@10.0.0.5/omixor_x",
		"postgres://u:p@127.0.0.1/omixor",   // 不是 omixor_ 前綴也不含 test
		"postgres://u:p@127.0.0.1/postgres", // 預設庫
		"postgres://u:p@127.0.0.1,db.example.com/omixor_x",
		"host=db.example.com dbname=omixor_test",
	}
	for _, u := range bad {
		err := checkTarget(u, false)
		if err == nil {
			t.Errorf("%s 應該拒絕", u)
			continue
		}
		if strings.Contains(err.Error(), "secretpw") {
			t.Errorf("錯誤訊息不能含密碼:%v", err)
		}
		if err := checkTarget(u, true); err != nil {
			t.Errorf("%s 設了 %s 應該放行:%v", u, AllowRemoteEnv, err)
		}
	}
}

// ---- 結構指紋與回收 ----

// 測試用的中繼資料表 fp_meta 放進白名單(正式的白名單目前是空的,這裡驗證機制)
func init() {
	structureMetaTables = append(structureMetaTables, "fp_meta", "fp_keep")
	metaTableKeep["fp_keep"] = "kind = 'fixed'"
}

func buildFP(url string) error {
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	_, err = c.Exec(ctx, `
create table fp_a (id serial primary key, v text not null default '');
create table fp_b (id serial primary key, a_id int not null, constraint fk_fp_b_a foreign key (a_id) references fp_a (id));
create table fp_meta (key text primary key, value text not null);
insert into fp_meta values ('rule_version', '1');
create table fp_keep (k text primary key, kind text not null);
insert into fp_keep values ('f1', 'fixed');`)
	return err
}

func sql(t *testing.T, url, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, url, q string) int {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var n int
	if err := c.QueryRow(ctx, q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 資料改動會回收(同一個資料庫給下一個測試,資料已清空;中繼資料表的列保留)
func TestRecycleKeepsDataOnlyTests(t *testing.T) {
	var first string
	t.Run("a", func(t *testing.T) {
		first = Template(t, "fp", buildFP)
		sql(t, first, `insert into fp_a (v) values ('x')`)
		sql(t, first, `insert into fp_b (a_id) values (1)`)
	})
	t.Run("b", func(t *testing.T) {
		u := Template(t, "fp", buildFP)
		if u != first {
			t.Fatalf("只改資料,應該回收同一個資料庫:%s vs %s", u, first)
		}
		if n := count(t, u, `select count(*) from fp_a`) + count(t, u, `select count(*) from fp_b`); n != 0 {
			t.Fatalf("回收後資料要清空:%d", n)
		}
		if n := count(t, u, `select count(*) from fp_meta`); n != 1 {
			t.Fatalf("中繼資料表的列不能被 truncate:%d", n)
		}
	})
}

// 審查者實測的兩個污染情境:建了多的表、拆掉外鍵。都要丟棄不回收,後面的測試拿到乾淨的
func TestRecycleDiscardsStructureChanges(t *testing.T) {
	cases := []struct {
		name  string
		dirty func(t *testing.T, url string)
		clean func(t *testing.T, url string) bool
	}{
		{"殘留表", func(t *testing.T, u string) { sql(t, u, `create table zz_leak (id int)`) },
			func(t *testing.T, u string) bool {
				return count(t, u, `select count(*) from pg_tables where tablename = 'zz_leak'`) == 0
			}},
		{"缺外鍵", func(t *testing.T, u string) { sql(t, u, `alter table fp_b drop constraint fk_fp_b_a`) },
			func(t *testing.T, u string) bool {
				return count(t, u, `select count(*) from pg_constraint where conname = 'fk_fp_b_a'`) == 1
			}},
		{"多一個索引", func(t *testing.T, u string) { sql(t, u, `create index zz_idx on fp_a (v)`) },
			func(t *testing.T, u string) bool {
				return count(t, u, `select count(*) from pg_indexes where indexname = 'zz_idx'`) == 0
			}},
		{"改了中繼資料列", func(t *testing.T, u string) { sql(t, u, `update fp_meta set value = '2'`) },
			func(t *testing.T, u string) bool {
				return count(t, u, `select count(*) from fp_meta where value = '1'`) == 1
			}},
	}
	for _, c := range cases {
		var dirtied string
		t.Run(c.name+"/髒", func(t *testing.T) {
			dirtied = Template(t, "fp", buildFP)
			c.dirty(t, dirtied)
		})
		t.Run(c.name+"/後續", func(t *testing.T) {
			u := Template(t, "fp", buildFP)
			if u == dirtied {
				t.Fatalf("改過結構的資料庫不該被回收")
			}
			if !c.clean(t, u) {
				t.Fatal("後續測試拿到被污染的資料庫")
			}
		})
	}
}

// ---- 孤兒清掃 ----

func mkOrphan(t *testing.T, tmp, name string, age time.Duration, pidFile string) string {
	t.Helper()
	d := filepath.Join(tmp, name)
	if err := os.MkdirAll(filepath.Join(d, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if pidFile != "" {
		if err := os.WriteFile(filepath.Join(d, "data", "postmaster.pid"), []byte(pidFile), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(d, old, old); err != nil {
		t.Fatal(err)
	}
	return d
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSweepOrphans(t *testing.T) {
	tmp := t.TempDir()
	var stopped []string
	oldIs, oldStop := pidIsPostgres, stopPostgres
	t.Cleanup(func() { pidIsPostgres, stopPostgres = oldIs, oldStop })
	pidIsPostgres = func(pid int) (bool, bool) { return pid == 4242, true }
	stopPostgres = func(rt, data string) { stopped = append(stopped, data) }

	fresh := mkOrphan(t, tmp, "omixor-testpg-fresh", 10*time.Minute, "")
	oldNoPid := mkOrphan(t, tmp, "omixor-testpg-oldnopid", 3*time.Hour, "")
	other := mkOrphan(t, tmp, "other-prefix-old", 3*time.Hour, "")
	dataOf := func(d string) string { return filepath.Join(d, "data") }
	notPG := mkOrphan(t, tmp, "omixor-testpg-notpg", 3*time.Hour, "99\n")
	_ = os.WriteFile(filepath.Join(dataOf(notPG), "postmaster.pid"), []byte("99\n"+dataOf(notPG)+"\n"), 0o644)
	livePG := mkOrphan(t, tmp, "omixor-testpg-live", 3*time.Hour, "")
	_ = os.WriteFile(filepath.Join(dataOf(livePG), "postmaster.pid"), []byte("4242\n"+dataOf(livePG)+"\n"), 0o644)
	otherDirPG := mkOrphan(t, tmp, "omixor-testpg-otherdir", 3*time.Hour, "")
	_ = os.WriteFile(filepath.Join(dataOf(otherDirPG), "postmaster.pid"), []byte("4242\n"+filepath.Join(tmp, "somewhere-else")+"\n"), 0o644)

	n := sweepOrphans(tmp, embDirPrefix, time.Hour, time.Now())
	if n != 4 {
		t.Fatalf("該清 4 個(無 pid、pid 不是 postgres、postgres、資料目錄不符),實際 %d", n)
	}
	if !exists(fresh) || !exists(other) {
		t.Fatal("新資料夾與不同前綴的資料夾不可被清掉")
	}
	for _, d := range []string{oldNoPid, notPG, livePG, otherDirPG} {
		if exists(d) {
			t.Fatalf("%s 該被清掉", d)
		}
	}
	if len(stopped) != 1 || !sameDir(stopped[0], dataOf(livePG)) {
		t.Fatalf("只有 pid 是 postgres 且資料目錄相符的才送停止:%v", stopped)
	}
}

// 登記了固定列條件的中繼資料表(omixor_actors 的系統 actor 就是這種):固定列保留、其他列在回收時刪掉且不算結構污染;改了固定列才丟棄
func TestRecycleMetaKeepPredicate(t *testing.T) {
	var first string
	t.Run("加了測試自己的列", func(t *testing.T) {
		first = Template(t, "fp", buildFP)
		sql(t, first, `insert into fp_keep values ('u1', 'user'), ('u2', 'user')`)
	})
	t.Run("同一個資料庫回收、只剩固定列", func(t *testing.T) {
		u := Template(t, "fp", buildFP)
		if u != first {
			t.Fatalf("只多了固定列以外的列,應該回收同一個資料庫:%s vs %s", u, first)
		}
		if n := count(t, u, `select count(*) from fp_keep`); n != 1 {
			t.Fatalf("回收後只剩固定列:%d", n)
		}
		if n := count(t, u, `select count(*) from fp_keep where kind = 'fixed'`); n != 1 {
			t.Fatal("固定列要保留")
		}
	})
	var dirtied string
	t.Run("改了固定列/髒", func(t *testing.T) {
		dirtied = Template(t, "fp", buildFP)
		sql(t, dirtied, `update fp_keep set k = 'changed' where kind = 'fixed'`)
	})
	t.Run("改了固定列/後續", func(t *testing.T) {
		u := Template(t, "fp", buildFP)
		if u == dirtied {
			t.Fatal("改了固定列的資料庫不該被回收")
		}
		if n := count(t, u, `select count(*) from fp_keep where k = 'f1'`); n != 1 {
			t.Fatal("後續測試拿到被污染的資料庫")
		}
	})
	t.Run("範本不一定有這張表", func(t *testing.T) {
		// fp 以外的範本沒有 fp_keep、omixor_actors:回收的 SQL 要能跑
		u := Template(t, "fp2", func(url string) error {
			ctx := context.Background()
			c, err := pgx.Connect(ctx, url)
			if err != nil {
				return err
			}
			defer c.Close(ctx)
			_, err = c.Exec(ctx, `create table fp2_a (id int)`)
			return err
		})
		sql(t, u, `insert into fp2_a values (1)`)
	})
}
