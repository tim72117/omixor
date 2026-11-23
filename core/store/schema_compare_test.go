package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// 輸出順序固定:不管輸入順序,表名、欄位名、型別不符都依名稱排序
func TestCompareSchemaOrderStable(t *testing.T) {
	g := []manifestTable{
		tbl("c", nil, col("z", "text", true), col("m", "text", true), col("a", "text", true), col("q", "integer", false), col("b", "integer", false)),
		tbl("a", nil, col("x", "text", true)),
		tbl("b", nil, col("x", "text", true)),
		tbl("miss_z", nil), tbl("miss_a", nil),
	}
	a := []manifestTable{
		tbl("o_z", nil), tbl("c", nil, col("q", "bigint", true), col("b", "bigint", true), col("yy", "text", true), col("ee", "text", true)),
		tbl("b", nil, col("x", "text", true)), tbl("o_a", nil), tbl("a", nil, col("x", "text", true)),
	}
	r := compareSchema(g, a)
	if r.Tables[0].Table != "a" || r.Tables[1].Table != "b" || r.Tables[2].Table != "c" || len(r.Tables) != 3 {
		t.Fatalf("表順序:%+v", r.Tables)
	}
	if !reflect.DeepEqual(r.MissingTables, []string{"miss_a", "miss_z"}) || !reflect.DeepEqual(r.OrphanTables, []string{"o_a", "o_z"}) {
		t.Errorf("missing=%v orphan=%v", r.MissingTables, r.OrphanTables)
	}
	c := r.Tables[2]
	if !reflect.DeepEqual(c.MissingColumns, []string{"a", "m", "z"}) || !reflect.DeepEqual(c.ExtraColumns, []string{"ee", "yy"}) {
		t.Errorf("欄位順序:missing=%v extra=%v", c.MissingColumns, c.ExtraColumns)
	}
	if len(c.TypeMismatches) != 2 || c.TypeMismatches[0].Column != "b" || c.TypeMismatches[1].Column != "q" {
		t.Errorf("型別不符順序:%+v", c.TypeMismatches)
	}
	// 輸入反過來排,結果相同
	rev := func(in []manifestTable) []manifestTable {
		out := make([]manifestTable, len(in))
		for i, x := range in {
			out[len(in)-1-i] = x
		}
		return out
	}
	if r2 := compareSchema(rev(g), rev(a)); !reflect.DeepEqual(r, r2) {
		t.Errorf("輸入順序不同,輸出應相同:\n%+v\n%+v", r, r2)
	}
}

// ok 只在全部乾淨時為 true:任何一種差異(欄位、型別、可否 NULL、主鍵、缺表、遺留表)都讓 ok=false;單表 ok 同理
func TestCompareSchemaOKOnlyWhenClean(t *testing.T) {
	base := func() []manifestTable {
		return []manifestTable{tbl("a", []string{"id"}, col("id", "text", false), col("n", "integer", true))}
	}
	if r := compareSchema(base(), base()); !r.OK || !r.Tables[0].OK {
		t.Fatalf("相同應 ok:%+v", r)
	}
	if r := compareSchema(nil, nil); !r.OK || len(r.Tables) != 0 {
		t.Errorf("兩邊都空應 ok:%+v", r)
	}
	cases := map[string]func(a []manifestTable) []manifestTable{
		"少欄位": func(a []manifestTable) []manifestTable { a[0].Columns = a[0].Columns[:1]; return a },
		"多欄位": func(a []manifestTable) []manifestTable {
			a[0].Columns = append(a[0].Columns, col("x", "text", true))
			return a
		},
		"型別不同":    func(a []manifestTable) []manifestTable { a[0].Columns[1].Type = "bigint"; return a },
		"可否 NULL": func(a []manifestTable) []manifestTable { a[0].Columns[1].Nullable = false; return a },
		"主鍵不同":    func(a []manifestTable) []manifestTable { a[0].PrimaryKey = nil; return a },
		"遺留表":     func(a []manifestTable) []manifestTable { return append(a, tbl("o", nil)) },
		"缺表":      func(a []manifestTable) []manifestTable { return nil },
	}
	for name, mut := range cases {
		r := compareSchema(base(), mut(base()))
		if r.OK {
			t.Errorf("%s:不該 ok:%+v", name, r)
		}
		for _, x := range r.Tables {
			if x.OK && name != "遺留表" {
				t.Errorf("%s:單表不該 ok:%+v", name, x)
			}
		}
	}
	// 遺留表只影響整體 ok,不影響原有表的 ok
	if r := compareSchema(base(), append(base(), tbl("o", nil))); r.OK || !r.Tables[0].OK {
		t.Errorf("遺留表:整體不 ok、單表仍 ok:%+v", r)
	}
	// 欄位順序不同不算差異
	a := base()
	a[0].Columns[0], a[0].Columns[1] = a[0].Columns[1], a[0].Columns[0]
	if r := compareSchema(base(), a); !r.OK {
		t.Errorf("欄位順序不同不該算差異:%+v", r)
	}
}

// 空的 slice 序列化成 [] 而不是 null;沒有主鍵差異時省略 primaryKeyMismatch,有差異時主鍵 nil 也輸出 []
func TestCompareSchemaJSONEmptySlices(t *testing.T) {
	g := []manifestTable{tbl("a", nil, col("x", "text", true))}
	raw, err := json.Marshal(compareSchema(g, g))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "null") {
		t.Fatalf("JSON 不該有 null:%s", s)
	}
	for _, want := range []string{`"tables":[{`, `"missingTables":[]`, `"orphanTables":[]`, `"missingColumns":[]`, `"extraColumns":[]`, `"typeMismatches":[]`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 缺 %s:%s", want, s)
		}
	}
	if strings.Contains(s, "primaryKeyMismatch") {
		t.Errorf("沒差異不該輸出 primaryKeyMismatch:%s", s)
	}
	// 兩邊都空:tables 也是 []
	raw, _ = json.Marshal(compareSchema(nil, nil))
	if strings.Contains(string(raw), "null") || !strings.Contains(string(raw), `"tables":[]`) {
		t.Errorf("空報告:%s", raw)
	}
	// 主鍵 nil 對有主鍵:Actual 輸出 [] 不是 null
	raw, _ = json.Marshal(compareSchema([]manifestTable{tbl("a", []string{"id"}, col("id", "text", false))}, []manifestTable{tbl("a", nil, col("id", "text", false))}))
	if strings.Contains(string(raw), "null") || !strings.Contains(string(raw), `"actual":[]`) {
		t.Errorf("主鍵差異:%s", raw)
	}
}
