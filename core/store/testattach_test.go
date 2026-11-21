package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/storetest/testdb"
)

func TestCheckAttachDB(t *testing.T) {
	all := map[string]bool{}
	for _, n := range attachRequiredTables {
		all[n] = true
	}
	if err := checkAttachDB("omixor_t_1_2_3", all); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"omixor", "postgres", "omixor_prod", "omixor_tpl_store_1_2_3", "x_omixor_t_1"} {
		if err := checkAttachDB(name, all); err == nil {
			t.Fatalf("%s 不是測試資料庫,應該被拒絕", name)
		}
	}
	for _, miss := range attachRequiredTables {
		part := map[string]bool{}
		for _, n := range attachRequiredTables {
			part[n] = n != miss
		}
		if err := checkAttachDB("omixor_t_1_2_3", part); err == nil || !strings.Contains(err.Error(), miss) {
			t.Fatalf("缺 %s 應該回錯誤並點名:%v", miss, err)
		}
	}
}

// 空的(還沒遷移)測試資料庫:AttachMigratedPG 要拒絕,不能默默用一個沒表的 store
func TestAttachMigratedPGRejectsUnmigrated(t *testing.T) {
	p, err := AttachMigratedPG(testdb.Blank(t))
	if err == nil {
		p.Close()
		t.Fatal("沒遷移的資料庫應該回錯誤")
	}
	if !strings.Contains(err.Error(), "沒遷移完成") && !strings.Contains(err.Error(), "還沒遷移完成") {
		t.Fatalf("錯誤訊息不對:%v", err)
	}
}

// 正式程式(core、console、server 的非測試、非 core/storetest 檔)不得呼叫 AttachMigratedPG:它繞過 NewPG 的建表、遷移與背景驗證
func TestNoProductionCallsAttachMigratedPG(t *testing.T) {
	roots := []string{"../../core", "../../console", "../../server"}
	fset := token.NewFileSet()
	scanned := 0
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			slash := filepath.ToSlash(p)
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".git", "ui", "dist":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			if strings.Contains(slash, "/core/storetest/") || strings.HasSuffix(slash, "/core/store/testattach.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				t.Errorf("解析 %s 失敗:%v", p, err)
				return nil
			}
			scanned++
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if x.Sel.Name == "AttachMigratedPG" {
						t.Errorf("%s:正式程式不得使用 AttachMigratedPG(只給 core/storetest)", fset.Position(x.Pos()))
					}
				case *ast.Ident:
					if x.Name == "AttachMigratedPG" {
						t.Errorf("%s:正式程式不得使用 AttachMigratedPG(只給 core/storetest)", fset.Position(x.Pos()))
					}
				}
				return true
			})
			return nil
		})
	}
	if scanned < 50 {
		t.Fatalf("只掃到 %d 個檔,路徑可能不對", scanned)
	}
}
