package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"omixor-core/model"
)

// 組織設定文件(通用,選用模組用):讀寫、整份取代、刪組織一併刪除;File 寫入會存檔,重開讀得回來;保留的 kind 不能用;
// 要是 JSON 物件;設定本身不含 updatedAt、updatedBy(一致性測試的一項,見 conformance_test.go)
func suiteOrgDoc(t *testing.T, impl storeImpl) {
	f := impl.open(t)
	at := mt("2026-11-13T01:02:03Z")
	if err := f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetOrgDoc("o", "github"); !errors.Is(err, ErrNotFound) {
		t.Fatal("沒存過回 ErrNotFound", err)
	}
	for _, k := range []string{"mcp", ""} {
		if err := f.SaveOrgDoc("o", k, OrgDoc{Data: json.RawMessage(`{}`)}); err == nil {
			t.Fatalf("%q 是保留的 kind", k)
		}
		if _, err := f.GetOrgDoc("o", k); err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("%q 是保留的 kind:%v", k, err)
		}
	}
	for _, bad := range []string{`[1]`, `"x"`, `{`} {
		if err := f.SaveOrgDoc("o", "github", OrgDoc{Data: json.RawMessage(bad)}); err == nil {
			t.Fatalf("要是 JSON 物件:%s", bad)
		}
	}
	if _, err := f.GetOrgDoc("o", "github"); !errors.Is(err, ErrNotFound) {
		t.Fatal("存失敗的不能留下來", err)
	}
	// Data 裡的 updatedAt、updatedBy 不算設定本身(那兩個另外存)
	if err := f.SaveOrgDoc("o", "github", OrgDoc{Data: json.RawMessage(`{"repos":["a/b"],"saved":false,"updatedAt":"x","updatedBy":"y"}`), UpdatedAt: &at, UpdatedBy: "boss@x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveOrgDoc("o", "gitlab", OrgDoc{Data: json.RawMessage(`{"baseUrl":"https://gl.example.com","projects":["g/p"]}`)}); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveOrgDoc("o", "empty", OrgDoc{}); err != nil {
		t.Fatal(err)
	}
	// 外部 MCP 登記和通用文件互不影響(PG 是同一張表)
	if err := f.SaveMCPConfig("o", model.OrgMCPConfig{UpdatedAt: &at}); err != nil {
		t.Fatal(err)
	}
	f2 := impl.reopen(t, f)
	g, err := f2.GetOrgDoc("o", "github")
	if err != nil || g.UpdatedBy != "boss@x" || g.UpdatedAt == nil || !g.UpdatedAt.Equal(at.Time) {
		t.Fatalf("重開讀回:%+v %v", g, err)
	}
	var gh map[string]any
	if err := json.Unmarshal(g.Data, &gh); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "Data(不含 updatedAt、updatedBy)", gh, map[string]any{"repos": []string{"a/b"}, "saved": false})
	l, err := f2.GetOrgDoc("o", "gitlab")
	if err != nil {
		t.Fatal(err)
	}
	// 沒給時間:Mem / File 讀回沒有時間;PG 存的時候用現在時間(見 OrgDoc.UpdatedAt 的說明)
	if impl.name == "pg" {
		if l.UpdatedAt == nil {
			t.Fatal("PG 讀回一定有 UpdatedAt")
		}
	} else if l.UpdatedAt != nil {
		t.Fatalf("沒給時間就沒有:%+v", l)
	}
	var gl map[string]any
	if err := json.Unmarshal(l.Data, &gl); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "gitlab 的 Data", gl, map[string]any{"baseUrl": "https://gl.example.com", "projects": []string{"g/p"}})
	if e, err := f2.GetOrgDoc("o", "empty"); err != nil || string(e.Data) != "{}" {
		t.Fatalf("空的 Data 讀回是 {}:%+v %v", e, err)
	}
	if _, err := f2.GetMCPConfig("o"); err != nil {
		t.Fatal("MCP 登記", err)
	}
	// 整份取代
	if err := f2.SaveOrgDoc("o", "github", OrgDoc{Data: json.RawMessage(`{"repos":[]}`), UpdatedAt: &at}); err != nil {
		t.Fatal(err)
	}
	if g, _ := f2.GetOrgDoc("o", "github"); g.UpdatedBy != "" {
		t.Fatalf("整份取代:%+v", g)
	} else if gh = nil; json.Unmarshal(g.Data, &gh) != nil || len(gh) != 1 {
		t.Fatalf("整份取代:%s %v", g.Data, err)
	}
	if _, err := f2.GetOrgDoc("p", "github"); !errors.Is(err, ErrNotFound) {
		t.Fatal("別的組織", err)
	}
	if err := f2.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	f2 = impl.reopen(t, f2)
	if _, err := f2.GetOrgDoc("o", "github"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪組織一併刪除", err)
	}
}

// 以前的檔案(integrations 是 model.OrgIntegrations 的 github、gitlab 欄位)照讀
func TestOrgDocReadsOldFileFormat(t *testing.T) {
	dir := t.TempDir()
	old := `{"version":1,"integrations":{"o":{"github":{"repos":["a/b"],"saved":true,"updatedAt":"2026-11-13T01:02:03Z","updatedBy":"boss@x"}}}}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.GetOrgDoc("o", "github")
	if err != nil || d.UpdatedBy != "boss@x" || d.UpdatedAt == nil {
		t.Fatalf("%+v %v", d, err)
	}
	var x struct {
		Repos []string `json:"repos"`
	}
	if json.Unmarshal(d.Data, &x) != nil || len(x.Repos) != 1 || x.Repos[0] != "a/b" {
		t.Fatalf("%s", d.Data)
	}
}
