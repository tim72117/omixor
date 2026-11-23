// schema_check.go 資料庫結構檢核:比對 golden(schema_manifest.json,全新資料庫的結構)與目前資料庫的實際結構

package store

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"omixor-core/model"
)

// SchemaChecker 是可選介面:有資料庫的 store(PG)才有;檔案模式沒有。service 用型別斷言。
type SchemaChecker interface {
	CheckSchema() (*model.SchemaReport, error)
}

// schemaManifestJSON 是「全新資料庫跑完 NewPG 之後」的結構。
// 改了建表語句後重新產生:OMIXOR_UPDATE_SQL=1 go test -run TestSchemaManifestInSync ./store
//
//go:embed schema_manifest.json
var schemaManifestJSON []byte

// manifestTable 是一張表的結構(golden 與實際查回來的都用這個形狀)。
type manifestTable struct {
	Name       string           `json:"name"`
	Columns    []manifestColumn `json:"columns"`
	PrimaryKey []string         `json:"primaryKey"`
}

type manifestColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

func (c manifestColumn) describe() string {
	if c.Nullable {
		return c.Type + " null"
	}
	return c.Type + " not null"
}

func loadSchemaManifest() ([]manifestTable, error) {
	var out []manifestTable
	if err := json.Unmarshal(schemaManifestJSON, &out); err != nil {
		return nil, fmt.Errorf("schema_manifest.json 格式不對:%w", err)
	}
	return out, nil
}

// compareSchema 是純函式:輸入 golden 與實際結構,輸出報告(依表名、欄位名排序;不含版本,版本由 CheckSchema 補)。
func compareSchema(golden, actual []manifestTable) *model.SchemaReport {
	rep := &model.SchemaReport{
		Tables:        []model.SchemaTableCheck{},
		MissingTables: []string{},
		OrphanTables:  []string{},
	}
	act := map[string]manifestTable{}
	for _, t := range actual {
		act[t.Name] = t
	}
	gold := map[string]manifestTable{}
	for _, t := range golden {
		gold[t.Name] = t
	}
	for _, g := range sortedTables(golden) {
		a, ok := act[g.Name]
		if !ok {
			rep.MissingTables = append(rep.MissingTables, g.Name)
			continue
		}
		rep.Tables = append(rep.Tables, compareTable(g, a))
	}
	for _, a := range sortedTables(actual) {
		if _, ok := gold[a.Name]; !ok {
			rep.OrphanTables = append(rep.OrphanTables, a.Name)
		}
	}
	rep.OK = len(rep.MissingTables) == 0 && len(rep.OrphanTables) == 0
	for _, t := range rep.Tables {
		if !t.OK {
			rep.OK = false
		}
	}
	return rep
}

func sortedTables(in []manifestTable) []manifestTable {
	out := append([]manifestTable(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func compareTable(g, a manifestTable) model.SchemaTableCheck {
	c := model.SchemaTableCheck{
		Table:          g.Name,
		MissingColumns: []string{},
		ExtraColumns:   []string{},
		TypeMismatches: []model.SchemaTypeMismatch{},
	}
	gc := map[string]manifestColumn{}
	for _, col := range g.Columns {
		gc[col.Name] = col
	}
	ac := map[string]manifestColumn{}
	for _, col := range a.Columns {
		ac[col.Name] = col
	}
	for name, want := range gc {
		got, ok := ac[name]
		switch {
		case !ok:
			c.MissingColumns = append(c.MissingColumns, name)
		case got.Type != want.Type || got.Nullable != want.Nullable:
			c.TypeMismatches = append(c.TypeMismatches, model.SchemaTypeMismatch{Column: name, Expected: want.describe(), Actual: got.describe()})
		}
	}
	for name := range ac {
		if _, ok := gc[name]; !ok {
			c.ExtraColumns = append(c.ExtraColumns, name)
		}
	}
	sort.Strings(c.MissingColumns)
	sort.Strings(c.ExtraColumns)
	sort.Slice(c.TypeMismatches, func(i, j int) bool { return c.TypeMismatches[i].Column < c.TypeMismatches[j].Column })
	// 主鍵:欄位順序也算(複合主鍵順序不同,索引就不同)
	if strings.Join(g.PrimaryKey, ",") != strings.Join(a.PrimaryKey, ",") {
		c.PrimaryKeyMismatch = &model.SchemaPrimaryKeyDiff{Expected: emptyIfNil(g.PrimaryKey), Actual: emptyIfNil(a.PrimaryKey)}
	}
	c.OK = len(c.MissingColumns) == 0 && len(c.ExtraColumns) == 0 && len(c.TypeMismatches) == 0 && c.PrimaryKeyMismatch == nil
	return c
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// CheckSchema 比對 golden 與目前資料庫(current_schema())的實際結構。只查 catalog,不掃資料表、不拿鎖。
func (p *PG) CheckSchema() (*model.SchemaReport, error) {
	golden, err := loadSchemaManifest()
	if err != nil {
		return nil, err
	}
	actual, err := p.readSchema()
	if err != nil {
		return nil, err
	}
	rep := compareSchema(golden, actual)
	c, cancel := ctx()
	defer cancel()
	v, _, err := currentSchemaVersion(c, p.pool)
	if err != nil {
		return nil, err
	}
	applyVersionCheck(rep, v, knownSchemaVersion(schemaSteps))
	return rep, nil
}

// applyVersionCheck 把結構版本寫進報告;版本落後(還有步驟沒套用)時整體 ok 為 false。
// 資料庫版本比程式新(部署回滾)不算問題:步驟向後相容
func applyVersionCheck(rep *model.SchemaReport, version, expected int) {
	rep.SchemaVersion = version
	rep.ExpectedVersion = expected
	if version < expected {
		rep.OK = false
	}
}

// readSchema 讀目前資料庫的結構:資料表(只含一般表)、欄位(名稱、型別、可否 NULL)與主鍵欄位(依主鍵內順序)
func (p *PG) readSchema() ([]manifestTable, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `
select c.table_name, c.column_name,
       case when c.data_type in ('ARRAY', 'USER-DEFINED') then c.udt_name else c.data_type end,
       c.is_nullable = 'YES'
  from information_schema.columns c
  join information_schema.tables t
    on t.table_schema = c.table_schema and t.table_name = c.table_name and t.table_type = 'BASE TABLE'
 where c.table_schema = current_schema()
 order by c.table_name, c.column_name`)
	if err != nil {
		return nil, fmt.Errorf("讀資料表欄位失敗:%w", err)
	}
	byName := map[string]*manifestTable{}
	var order []string
	for rows.Next() {
		var tb string
		var col manifestColumn
		if err := rows.Scan(&tb, &col.Name, &col.Type, &col.Nullable); err != nil {
			rows.Close()
			return nil, err
		}
		t, ok := byName[tb]
		if !ok {
			t = &manifestTable{Name: tb, Columns: []manifestColumn{}, PrimaryKey: []string{}}
			byName[tb] = t
			order = append(order, tb)
		}
		t.Columns = append(t.Columns, col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 { // current_schema() 是 NULL、或這個帳號什麼表都看不到:當成讀取失敗,不要回「全部的表都缺」讓人誤會資料庫壞了
		return nil, fmt.Errorf("讀不到任何資料表的結構(目前的 schema 不存在,或這個資料庫帳號沒有權限)")
	}
	pk, err := p.pool.Query(c, `
select cl.relname, a.attname
  from pg_constraint con
  join pg_class cl on cl.oid = con.conrelid
  join pg_namespace n on n.oid = cl.relnamespace and n.nspname = current_schema()
  cross join lateral unnest(con.conkey) with ordinality as k(attnum, ord)
  join pg_attribute a on a.attrelid = con.conrelid and a.attnum = k.attnum
 where con.contype = 'p'
 order by cl.relname, k.ord`)
	if err != nil {
		return nil, fmt.Errorf("讀主鍵失敗:%w", err)
	}
	defer pk.Close()
	for pk.Next() {
		var tb, col string
		if err := pk.Scan(&tb, &col); err != nil {
			return nil, err
		}
		if t, ok := byName[tb]; ok {
			t.PrimaryKey = append(t.PrimaryKey, col)
		}
	}
	if err := pk.Err(); err != nil {
		return nil, err
	}
	out := make([]manifestTable, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}
