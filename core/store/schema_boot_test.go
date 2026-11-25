package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 空資料庫:建出最終結構(含固定的系統 actor)、再啟動一次不動任何東西
func TestBootstrapEmptyDatabase(t *testing.T) {
	url := testPGBlank(t)
	p, err := NewPG(url)
	chk(t, err)
	if n := pgCount(t, p, `select count(*) from omixor_actors where kind = 'system'`); n == 0 {
		t.Fatal("要有固定的系統 actor")
	}
	p.Close()
	p2, err := NewPG(url) // 第二次:已經是最終結構,通過
	chk(t, err)
	p2.Close()
}

// hostTablesSQL 宿主程式(console)在核心開機前就建好的 omixor_ 表;不是核心表
const hostTablesSQL = `
create table omixor_secrets (account text primary key, ciphertext bytea not null);
create table omixor_state (name text primary key, data jsonb not null);
insert into omixor_state (name, data) values ('keep', '{}');
`

// execBlank 建一個空資料庫、先跑 sql,回傳網址
func execBlank(t *testing.T, sql string) string {
	t.Helper()
	url := testPGBlank(t)
	execURL(t, url, sql)
	return url
}

func execURL(t *testing.T, url, sql string) {
	t.Helper()
	c := context.Background()
	pool, err := pgxpool.New(c, url)
	chk(t, err)
	defer pool.Close()
	_, err = pool.Exec(c, sql)
	chk(t, err)
}

// assertFreshResult 開機成功後的結構和版本記錄要與全新安裝一樣,宿主的表和資料原封不動
func assertFreshResult(t *testing.T, p *PG) {
	t.Helper()
	golden, err := loadSchemaManifest()
	chk(t, err)
	actual, err := p.readSchema()
	chk(t, err)
	host := map[string]bool{"omixor_secrets": true, "omixor_state": true}
	var core []manifestTable
	for _, a := range actual {
		if !host[a.Name] {
			core = append(core, a)
		}
	}
	if rep := compareSchema(golden, core); !rep.OK {
		t.Errorf("結構與全新安裝不一致:%+v", rep)
	}
	if n, want := pgCount(t, p, `select count(*) from omixor_schema_version`), 1+len(schemaSteps); n != want {
		t.Errorf("版本記錄筆數 %d,要 %d", n, want)
	}
	if n := pgCount(t, p, `select count(*) from omixor_state where name = 'keep'`); n != 1 {
		t.Errorf("宿主資料要保留,筆數 %d", n)
	}
}

// 只有宿主的 omixor_ 表(沒有任何核心表)= 空資料庫,要建出核心表
func TestSchemaBootWithHostTablesOnly(t *testing.T) {
	p, err := NewPG(execBlank(t, hostTablesSQL))
	chk(t, err)
	t.Cleanup(p.Close)
	assertFreshResult(t, p)
}

// 版本表已蓋章(舊版程式失敗留下的)但沒有任何核心表:要能自己復原
func TestSchemaBootRecoversStampedWithoutCoreTables(t *testing.T) {
	cases := map[string]string{
		"空版本表":  ``,
		"版本1":   `insert into omixor_schema_version (version, name) values (1, 'baseline');`,
		"版本到最新": `insert into omixor_schema_version (version, name) values (1, 'baseline'), (2, 'commits'), (3, 'rename_issues_tables');`,
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := NewPG(execBlank(t, hostTablesSQL+schemaVersionDDL+rows))
			chk(t, err)
			t.Cleanup(p.Close)
			assertFreshResult(t, p)
		})
	}
}

// 沒有核心表、版本比程式新:可能是較新版程式的資料庫,不可以建表
func TestSchemaBootNewerVersionWithoutCoreTablesFails(t *testing.T) {
	url := execBlank(t, schemaVersionDDL+`insert into omixor_schema_version (version, name) values (99, 'future');`)
	assertBootFails(t, url, "99")
}

// 只有部分核心表:明確報錯,不補建、不蓋版本章
func TestSchemaBootPartialCoreTablesFails(t *testing.T) {
	cases := map[string]string{
		"只有一張核心表":      `create table omixor_orgs (id text primary key);`,
		"只有舊名 Issue 表": `create table ops_issues (org text, id text);`,
		"有版本章的部分核心表":   `create table omixor_orgs (id text primary key);` + schemaVersionDDL + `insert into omixor_schema_version (version, name) values (1, 'baseline');`,
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			url := execBlank(t, hostTablesSQL+sql)
			c := context.Background()
			pool, err := pgxpool.New(c, url)
			chk(t, err)
			t.Cleanup(pool.Close)
			count := `select count(*) from pg_tables where schemaname = current_schema()`
			before := scanInt(t, pool, count)
			assertBootFails(t, url, "核心資料表不完整")
			if after := scanInt(t, pool, count); after != before {
				t.Errorf("不可動資料庫:表數 %d → %d", before, after)
			}
		})
	}
	t.Run("完整資料庫少一張表", func(t *testing.T) {
		url := testPGBlank(t)
		p, err := NewPG(url)
		chk(t, err)
		p.Close()
		execURL(t, url, `drop table omixor_audit cascade`)
		assertBootFails(t, url, "omixor_audit")
	})
}

func assertBootFails(t *testing.T, url, want string) {
	t.Helper()
	p, err := NewPG(url)
	if err == nil {
		p.Close()
		t.Fatal("應該要報錯")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("錯誤訊息要含 %q:%v", want, err)
	}
}

// 多個實例同時對只有宿主表的資料庫開機:全部成功,只建一次
func TestSchemaBootConcurrentWithHostTables(t *testing.T) {
	url := execBlank(t, hostTablesSQL)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := NewPG(url)
			if err == nil {
				p.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		chk(t, err)
	}
	p, err := NewPG(url)
	chk(t, err)
	t.Cleanup(p.Close)
	assertFreshResult(t, p)
}

// 從建表語句派生的核心表名單要和 golden 一致(版本表除外)
func TestSchemaCoreTablesMatchManifest(t *testing.T) {
	golden, err := loadSchemaManifest()
	chk(t, err)
	var want []string
	for _, g := range golden {
		if g.Name != schemaVersionTable {
			want = append(want, g.Name)
		}
	}
	got := coreTableNames()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("核心表名單不一致:\n派生:%v\ngolden:%v", got, want)
	}
}
