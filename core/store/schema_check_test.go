package store

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"omixor-core/model"
)

func tbl(name string, pk []string, cols ...manifestColumn) manifestTable {
	return manifestTable{Name: name, Columns: cols, PrimaryKey: pk}
}

func col(name, typ string, nullable bool) manifestColumn {
	return manifestColumn{Name: name, Type: typ, Nullable: nullable}
}

func findTable(t *testing.T, r *model.SchemaReport, name string) model.SchemaTableCheck {
	t.Helper()
	for _, x := range r.Tables {
		if x.Table == name {
			return x
		}
	}
	t.Fatalf("報告裡沒有表 %s", name)
	return model.SchemaTableCheck{}
}

func TestCompareSchemaSame(t *testing.T) {
	g := []manifestTable{tbl("a", []string{"id"}, col("id", "text", false), col("n", "integer", true))}
	r := compareSchema(g, g)
	if !r.OK || len(r.Tables) != 1 || !r.Tables[0].OK {
		t.Fatalf("相同結構應 ok:%+v", r)
	}
	if r.MissingTables == nil || r.OrphanTables == nil || r.Tables[0].MissingColumns == nil {
		t.Error("陣列不該是 nil")
	}
}

func TestCompareSchemaDiffs(t *testing.T) {
	g := []manifestTable{
		tbl("b", []string{"org", "id"}, col("org", "text", false), col("id", "text", false), col("gone", "text", false), col("t", "integer", false)),
		tbl("a", []string{"id"}, col("id", "text", false)),
		tbl("missing", []string{"id"}, col("id", "text", false)),
	}
	a := []manifestTable{
		tbl("z_orphan", nil, col("x", "text", true)),
		tbl("b", []string{"id", "org"}, col("org", "text", false), col("id", "text", false), col("zz", "text", true), col("extra", "text", true), col("t", "bigint", true)),
		tbl("a", []string{"id"}, col("id", "text", false)),
		tbl("m_orphan", nil),
	}
	r := compareSchema(g, a)
	if r.OK {
		t.Fatal("有差異不該 ok")
	}
	if !reflect.DeepEqual(r.MissingTables, []string{"missing"}) {
		t.Errorf("missingTables=%v", r.MissingTables)
	}
	if !reflect.DeepEqual(r.OrphanTables, []string{"m_orphan", "z_orphan"}) {
		t.Errorf("orphanTables=%v", r.OrphanTables)
	}
	if len(r.Tables) != 2 || r.Tables[0].Table != "a" || r.Tables[1].Table != "b" || !r.Tables[0].OK {
		t.Fatalf("表順序或 a 的結果不對:%+v", r.Tables)
	}
	b := findTable(t, r, "b")
	if b.OK || !reflect.DeepEqual(b.MissingColumns, []string{"gone"}) || !reflect.DeepEqual(b.ExtraColumns, []string{"extra", "zz"}) {
		t.Errorf("b 的缺/多欄位不對:%+v", b)
	}
	if len(b.TypeMismatches) != 1 || b.TypeMismatches[0] != (model.SchemaTypeMismatch{Column: "t", Expected: "integer not null", Actual: "bigint null"}) {
		t.Errorf("型別不符不對:%+v", b.TypeMismatches)
	}
	if b.PrimaryKeyMismatch == nil || !reflect.DeepEqual(b.PrimaryKeyMismatch.Expected, []string{"org", "id"}) || !reflect.DeepEqual(b.PrimaryKeyMismatch.Actual, []string{"id", "org"}) {
		t.Errorf("主鍵不符不對:%+v", b.PrimaryKeyMismatch)
	}
}

func TestCompareSchemaOnlyOrphanOrMissing(t *testing.T) {
	g := []manifestTable{tbl("a", nil, col("x", "text", true))}
	if r := compareSchema(g, append(append([]manifestTable{}, g...), tbl("o", nil))); r.OK || len(r.OrphanTables) != 1 {
		t.Errorf("只多一張遺留表也要不 ok:%+v", r)
	}
	if r := compareSchema(g, nil); r.OK || len(r.MissingTables) != 1 {
		t.Errorf("缺表要不 ok:%+v", r)
	}
}

// golden 與全新資料庫的實際結構必須一致;改了建表或遷移後重新產生(OMIXOR_UPDATE_SQL=1)
func TestSchemaManifestInSync(t *testing.T) {
	p := testPG(t).(*PG)
	actual, err := p.readSchema()
	chk(t, err)
	raw, err := json.MarshalIndent(actual, "", "  ")
	chk(t, err)
	want := string(raw) + "\n"
	const path = "schema_manifest.json"
	if os.Getenv("OMIXOR_UPDATE_SQL") == "1" {
		chk(t, os.WriteFile(path, []byte(want), 0o644))
	}
	got, err := os.ReadFile(path)
	chk(t, err)
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Fatalf("%s 與全新資料庫的結構不一致(用 OMIXOR_UPDATE_SQL=1 go test -run TestSchemaManifestInSync ./store 重新產生)", path)
	}
	golden, err := loadSchemaManifest()
	chk(t, err)
	if len(golden) < 20 {
		t.Fatalf("golden 只有 %d 張表,不合理", len(golden))
	}
}

func TestCheckSchemaPG(t *testing.T) {
	// 這個測試會改結構,不能用共用範本的 testPG,改開全新資料庫(testPGBlank)自己跑 NewPG
	p, err := NewPG(testPGBlank(t))
	chk(t, err)
	t.Cleanup(p.Close)
	var sc SchemaChecker = p
	rep, err := sc.CheckSchema()
	chk(t, err)
	if !rep.OK {
		t.Fatalf("全新資料庫應 ok:%+v", rep)
	}
	exec := func(sql string) {
		t.Helper()
		c, cancel := ctx()
		defer cancel()
		_, err := p.pool.Exec(c, sql)
		chk(t, err)
	}
	// 多一欄、少一欄、改型別、改主鍵、遺留表、缺表
	exec(`alter table omixor_org_system add column zz_extra text`)
	exec(`alter table omixor_project_seq drop column last`)
	exec(`alter table omixor_server_features alter column features type text using features::text`)
	exec(`alter table omixor_org_chain_edges drop constraint omixor_org_chain_edges_pkey`)
	exec(`create table zz_orphan (id text primary key)`)
	exec(`drop table omixor_channel_reads`)
	rep, err = sc.CheckSchema()
	chk(t, err)
	if rep.OK {
		t.Fatal("改壞後不該 ok")
	}
	if !reflect.DeepEqual(rep.OrphanTables, []string{"zz_orphan"}) || !reflect.DeepEqual(rep.MissingTables, []string{"omixor_channel_reads"}) {
		t.Errorf("遺留表/缺表不對:orphan=%v missing=%v", rep.OrphanTables, rep.MissingTables)
	}
	if x := findTable(t, rep, "omixor_org_system"); !reflect.DeepEqual(x.ExtraColumns, []string{"zz_extra"}) || x.OK {
		t.Errorf("多欄位沒被檢出:%+v", x)
	}
	if x := findTable(t, rep, "omixor_project_seq"); !reflect.DeepEqual(x.MissingColumns, []string{"last"}) {
		t.Errorf("少欄位沒被檢出:%+v", x)
	}
	if x := findTable(t, rep, "omixor_server_features"); len(x.TypeMismatches) != 1 || x.TypeMismatches[0].Column != "features" {
		t.Errorf("型別不符沒被檢出:%+v", x)
	}
	if x := findTable(t, rep, "omixor_org_chain_edges"); x.PrimaryKeyMismatch == nil || len(x.PrimaryKeyMismatch.Actual) != 0 {
		t.Errorf("主鍵不符沒被檢出:%+v", x)
	}
}

// 宿主程式登記的額外表不算遺留表;沒登記的陌生表照樣報告;ok 只受遺留表影響
func TestCheckSchemaPGRegisteredExtraTables(t *testing.T) {
	p, err := NewPG(testPGBlank(t))
	chk(t, err)
	t.Cleanup(p.Close)
	exec := func(sql string) {
		t.Helper()
		c, cancel := ctx()
		defer cancel()
		_, err := p.pool.Exec(c, sql)
		chk(t, err)
	}
	exec(`create table omixor_host_extra (id text primary key)`)
	exec(`create table omixor_host_stray (id text primary key)`)
	RegisterExtraTables("omixor_host_extra")
	RegisterExtraTables("omixor_host_extra") // 冪等
	rep, err := p.CheckSchema()
	chk(t, err)
	if rep.OK || !reflect.DeepEqual(rep.OrphanTables, []string{"omixor_host_stray"}) {
		t.Fatalf("只該剩沒登記的遺留表:ok=%v orphan=%v", rep.OK, rep.OrphanTables)
	}
	exec(`drop table omixor_host_stray`)
	rep, err = p.CheckSchema()
	chk(t, err)
	if !rep.OK || len(rep.OrphanTables) != 0 {
		t.Fatalf("只剩登記的表應 ok:%+v", rep)
	}
}
