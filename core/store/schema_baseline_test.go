package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 凍結的 baseline(schema_baseline_a_test.go、_b_、_fk_)是版本 1 的資料庫結構,不隨 pgSchema 演進。
// 守住「全新安裝 = baseline 舊結構 + 版本表蓋章 + 所有 schemaSteps」:每新增一個步驟,這個測試就會比對步驟套用後的結構是否和全新安裝完全一致。

// structureSnapshot 資料庫的結構快照:表與欄位、主鍵(readSchema)、約束(含外鍵定義)、索引、版本記錄(版本與名稱)
type structureSnapshot struct {
	Tables      []manifestTable
	Constraints []string
	Indexes     []string
	Versions    []string
}

func snapshotOf(t *testing.T, p *PG) structureSnapshot {
	t.Helper()
	tables, err := p.readSchema()
	chk(t, err)
	var s structureSnapshot
	s.Tables = tables
	strs := func(sql string) []string {
		rows, err := p.root.Query(context.Background(), sql)
		chk(t, err)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			chk(t, rows.Scan(&v))
			out = append(out, v)
		}
		chk(t, rows.Err())
		return out
	}
	s.Constraints = strs(`select conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid) || ' validated=' || convalidated
		from pg_constraint where connamespace = current_schema()::regnamespace order by 1`)
	s.Indexes = strs(`select indexdef from pg_indexes where schemaname = current_schema() order by indexname`)
	s.Versions = strs(`select version || ':' || name from omixor_schema_version order by version`)
	return s
}

// 用凍結的 baseline 建出沒有版本表的舊資料庫 → NewPG(蓋 baseline、套用所有步驟)→ 結構必須和全新安裝完全一致
func TestSchemaStepsEquivalentToFreshInstall(t *testing.T) {
	// 舊資料庫:baseline 結構、沒有 omixor_schema_version
	oldURL := testPGBlank(t)
	c := context.Background()
	pool, err := pgxpool.New(c, oldURL)
	chk(t, err)
	_, err = pool.Exec(c, baselineSchemaA+baselineSchemaB+baselineFKSQL)
	chk(t, err)
	if tableExists(t, pool, schemaVersionTable) {
		t.Fatal("baseline 不該含版本表")
	}
	pool.Close()

	old, err := NewPG(oldURL)
	chk(t, err)
	t.Cleanup(old.Close)
	fresh, err := NewPG(testPGBlank(t))
	chk(t, err)
	t.Cleanup(fresh.Close)

	got, want := snapshotOf(t, old), snapshotOf(t, fresh)
	if !reflect.DeepEqual(got.Tables, want.Tables) {
		t.Errorf("表與欄位不一致(舊結構 + 步驟 ≠ 全新安裝);用 compareSchema 看差異:%+v", compareSchema(want.Tables, got.Tables))
	}
	if !reflect.DeepEqual(got.Constraints, want.Constraints) {
		t.Errorf("約束(含外鍵)不一致:\n舊結構+步驟:%v\n全新安裝:%v", got.Constraints, want.Constraints)
	}
	if !reflect.DeepEqual(got.Indexes, want.Indexes) {
		t.Errorf("索引不一致:\n舊結構+步驟:%v\n全新安裝:%v", got.Indexes, want.Indexes)
	}
	if !reflect.DeepEqual(got.Versions, want.Versions) {
		t.Errorf("版本記錄不一致:%v vs %v", got.Versions, want.Versions)
	}
	// 也和 golden(schema_manifest.json)一致
	golden, err := loadSchemaManifest()
	chk(t, err)
	if rep := compareSchema(golden, got.Tables); !rep.OK {
		t.Errorf("與 schema_manifest.json 不一致:%+v", rep)
	}
	rep, err := old.CheckSchema()
	chk(t, err)
	if !rep.OK || rep.SchemaVersion != rep.ExpectedVersion || rep.ExpectedVersion != knownSchemaVersion(schemaSteps) {
		t.Errorf("CheckSchema 應 ok 且版本到位:%+v", rep)
	}
}

// 版本落後時 CheckSchema 的 ok 是 false;版本比程式新不算問題
func TestCheckSchemaVersionBehind(t *testing.T) {
	p, err := NewPG(testPGBlank(t))
	chk(t, err)
	t.Cleanup(p.Close)
	c := context.Background()
	_, err = p.root.Exec(c, `delete from omixor_schema_version`)
	chk(t, err)
	rep, err := p.CheckSchema()
	chk(t, err)
	if rep.OK || rep.SchemaVersion != 0 || rep.ExpectedVersion != knownSchemaVersion(schemaSteps) {
		t.Fatalf("版本落後要 ok=false:%+v", rep)
	}
	_, err = p.root.Exec(c, `insert into omixor_schema_version (version, name) values (99, 'future')`)
	chk(t, err)
	if rep, err = p.CheckSchema(); err != nil || !rep.OK || rep.SchemaVersion != 99 {
		t.Fatalf("版本比程式新不算問題:%+v %v", rep, err)
	}
	// 純函式
	r := compareSchema(nil, nil)
	applyVersionCheck(r, 2, 3)
	if r.OK || r.SchemaVersion != 2 || r.ExpectedVersion != 3 {
		t.Errorf("applyVersionCheck:%+v", r)
	}
}
