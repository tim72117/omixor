package mcp_test

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 回固定報告(或固定錯誤)的假 Backend,其餘操作沿用 Unsupported
type schemaBackend struct {
	backend.Unsupported
	rep *model.SchemaReport
	err error
}

func (b schemaBackend) SchemaCheck() (*model.SchemaReport, error) { return b.rep, b.err }

// 沒有實作 SchemaChecker 的 store(只內嵌介面,不碰資料庫)
type noDBStore struct{ store.Store }

func schemaEndpoint(b backend.Backend) http.Handler {
	return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return b, "lab", nil
	}})
}

func TestSchemaCheckToolFields(t *testing.T) {
	rep := &model.SchemaReport{
		OK:            false,
		MissingTables: []string{"m1"},
		OrphanTables:  []string{"o1", "o2"},
		Tables: []model.SchemaTableCheck{
			{Table: "a", MissingColumns: []string{}, ExtraColumns: []string{}, TypeMismatches: []model.SchemaTypeMismatch{}, OK: true},
			{
				Table:              "b",
				MissingColumns:     []string{"gone"},
				ExtraColumns:       []string{"extra"},
				TypeMismatches:     []model.SchemaTypeMismatch{{Column: "t", Expected: "integer not null", Actual: "bigint null"}},
				PrimaryKeyMismatch: &model.SchemaPrimaryKeyDiff{Expected: []string{"org", "id"}, Actual: []string{"id", "org"}},
			},
		},
	}
	h := schemaEndpoint(schemaBackend{rep: rep})
	out := ok(t, h, "", "schema_check", map[string]any{})
	for _, k := range []string{"ok", "tables", "missingTables", "orphanTables"} {
		if _, has := out[k]; !has {
			t.Errorf("回傳缺欄位 %s:%v", k, out)
		}
	}
	if out["ok"] != false {
		t.Errorf("ok=%v", out["ok"])
	}
	if !reflect.DeepEqual(out["missingTables"], []any{"m1"}) || !reflect.DeepEqual(out["orphanTables"], []any{"o1", "o2"}) {
		t.Errorf("缺表/遺留表:%v %v", out["missingTables"], out["orphanTables"])
	}
	tables := out["tables"].([]any)
	if len(tables) != 2 {
		t.Fatalf("tables=%v", tables)
	}
	a, b := tables[0].(map[string]any), tables[1].(map[string]any)
	if a["table"] != "a" || a["ok"] != true {
		t.Errorf("a:%v", a)
	}
	// 沒有主鍵差異時省略 primaryKeyMismatch;空陣列是 [] 而不是 null
	if _, has := a["primaryKeyMismatch"]; has {
		t.Errorf("a 不該有 primaryKeyMismatch:%v", a)
	}
	for _, k := range []string{"missingColumns", "extraColumns", "typeMismatches"} {
		if v, has := a[k]; !has || !reflect.DeepEqual(v, []any{}) {
			t.Errorf("a.%s 應為 []:%#v", k, v)
		}
	}
	if b["table"] != "b" || b["ok"] != false {
		t.Errorf("b:%v", b)
	}
	if !reflect.DeepEqual(b["missingColumns"], []any{"gone"}) || !reflect.DeepEqual(b["extraColumns"], []any{"extra"}) {
		t.Errorf("b 缺/多欄位:%v", b)
	}
	tm := b["typeMismatches"].([]any)
	if len(tm) != 1 || !reflect.DeepEqual(tm[0], map[string]any{"column": "t", "expected": "integer not null", "actual": "bigint null"}) {
		t.Errorf("typeMismatches:%v", tm)
	}
	want := map[string]any{"expected": []any{"org", "id"}, "actual": []any{"id", "org"}}
	if !reflect.DeepEqual(b["primaryKeyMismatch"], want) {
		t.Errorf("primaryKeyMismatch:%v", b["primaryKeyMismatch"])
	}
}

func TestSchemaCheckToolClean(t *testing.T) {
	rep := &model.SchemaReport{OK: true, Tables: []model.SchemaTableCheck{}, MissingTables: []string{}, OrphanTables: []string{}}
	out := ok(t, schemaEndpoint(schemaBackend{rep: rep}), "", "schema_check", map[string]any{})
	if out["ok"] != true || !reflect.DeepEqual(out["tables"], []any{}) || !reflect.DeepEqual(out["missingTables"], []any{}) || !reflect.DeepEqual(out["orphanTables"], []any{}) {
		t.Fatalf("乾淨的報告:%v", out)
	}
}

// 錯誤原樣帶出:假 Backend 的錯誤、沒有資料庫(400)、非伺服器管理員(403)
func TestSchemaCheckToolErrors(t *testing.T) {
	failsWith(t, schemaEndpoint(schemaBackend{err: errors.New("連不上核心")}), "", "schema_check", map[string]any{}, "連不上核心")

	svc := &service.Service{Store: noDBStore{}}
	admin := schemaEndpoint(&backend.Local{Svc: svc, P: &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "admin"}}})
	fails(t, admin, "", "schema_check", map[string]any{}, "這台伺服器沒有使用資料庫,不適用")

	member := schemaEndpoint(&backend.Local{Svc: svc, P: &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}})
	fails(t, member, "", "schema_check", map[string]any{}, "要 omixor 伺服器管理員才能做")
}
