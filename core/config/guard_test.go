package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 守門:core、console、server 的非測試 Go 檔,除了白名單,不能直接碰環境變數,也不能直接 import omixor-core/env(一律經 core/config)。
// 檢查的是「任何對下列函式的引用」(呼叫、`f := os.Getenv` 這種函式值、當參數傳 os.Getenv 都算),另外 dot import os / syscall 也不行:
//
//	os:      Getenv LookupEnv Environ ExpandEnv Expand Setenv Unsetenv Clearenv
//	syscall: Getenv Environ Setenv Unsetenv Clearenv
//
// 白名單(相對 repo 根目錄):
//   - core/config/、core/env/  設定載入本身與新舊名稱相容的底層(整個目錄)
//   - core/storetest/testdb/testdb.go、console/internal/core/coretest/coretest.go  測試輔助,讀 OMIXOR_TEST_* 測試用變數(整個檔)
//   - guardAllowUses:其他檔案只放行「這個檔案做這件事」,同檔其他種類的用法仍然違規
//
// 讀系統變數 PATH、SHELL(字串常數)的 Getenv / LookupEnv 放行,它們不屬於 omixor 設定。
var guardAllowFiles = map[string]bool{
	"core/storetest/testdb/testdb.go":            true,
	"console/internal/core/coretest/coretest.go": true,
}

var guardAllowDirs = []string{"core/config/", "core/env/"}

// guardAllowUses 檔案 → 放行的行為(例如 "os.Environ")。要加新的先問自己:能不能改走 core/config
var guardAllowUses = map[string][]string{
	"core/claude/claude.go":                {"os.Environ"},  // 啟動 claude 子程序時繼承目前環境再附加變數
	"console/internal/core/voice_shell.go": {"os.Environ"},  // 語音 shell 子程序的環境
	"server/local_run.go":                  {"os.Unsetenv"}, // OMIXOR_LOCAL_TOKEN 讀完立刻從環境移除(子程序不繼承)
	"server/embeddedpg.go":                 {"os.Setenv"},   // PGCTLTIMEOUT:pg_ctl 認的環境變數,函式庫只吃行程環境
}

var guardSystemVars = map[string]bool{"PATH": true, "SHELL": true}

// 受管制的函式
var guardFuncs = map[string]map[string]bool{
	"os":      {"Getenv": true, "LookupEnv": true, "Environ": true, "ExpandEnv": true, "Expand": true, "Setenv": true, "Unsetenv": true, "Clearenv": true},
	"syscall": {"Getenv": true, "Environ": true, "Setenv": true, "Unsetenv": true, "Clearenv": true},
}

// scanEnvUse 掃一個檔案的原始碼,回傳違規(位置:說明)與這個檔案用到的受管制行為(給白名單檢查);rel 是相對 repo 根目錄的路徑
func scanEnvUse(fset *token.FileSet, rel string, src any) (problems []string, uses map[string]bool, err error) {
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, nil, err
	}
	uses = map[string]bool{}
	names := map[string]string{} // 區域名稱 → import 路徑
	for _, im := range f.Imports {
		path, _ := strconv.Unquote(im.Path.Value)
		local := filepath.Base(path)
		if im.Name != nil {
			local = im.Name.Name
		}
		switch {
		case path == "omixor-core/env":
			problems = append(problems, rel+":不要直接 import omixor-core/env,用 omixor-core/config")
		case guardFuncs[path] != nil && local == ".":
			problems = append(problems, rel+":"+strconv.Itoa(fset.Position(im.Pos()).Line)+":dot import "+path+"(會繞過檢查)")
		}
		names[local] = path
	}
	okSel := map[token.Pos]bool{} // 讀 PATH / SHELL 字串常數的 Getenv:放行
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) == 1 {
				if id, ok := sel.X.(*ast.Ident); ok && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv") && guardFuncs[names[id.Name]] != nil {
					if lit, ok := x.Args[0].(*ast.BasicLit); ok {
						if s, err := strconv.Unquote(lit.Value); err == nil && guardSystemVars[s] {
							okSel[sel.Pos()] = true
						}
					}
				}
			}
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok || id.Obj != nil { // id.Obj != nil:同名的區域變數,不是 import 的套件
				return true
			}
			pkg := names[id.Name]
			if guardFuncs[pkg] == nil || !guardFuncs[pkg][x.Sel.Name] || okSel[x.Pos()] {
				return true
			}
			use := pkg + "." + x.Sel.Name
			uses[use] = true
			problems = append(problems, rel+":"+strconv.Itoa(fset.Position(x.Pos()).Line)+":"+use)
		}
		return true
	})
	return problems, uses, nil
}

// filterAllowed 去掉白名單放行的行為(只放行該檔案列出的行為)
func filterAllowed(rel string, problems []string) []string {
	allowed := guardAllowUses[rel]
	var out []string
	for _, p := range problems {
		ok := false
		for _, a := range allowed {
			if strings.HasSuffix(p, ":"+a) {
				ok = true
			}
		}
		if !ok {
			out = append(out, p)
		}
	}
	return out
}

func TestNoDirectEnvReads(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	fset := token.NewFileSet()
	for _, top := range []string{"core", "console", "server"} {
		_ = filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".git", "vendor", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if guardAllowFiles[rel] {
				return nil
			}
			for _, dir := range guardAllowDirs {
				if strings.HasPrefix(rel, dir) {
					return nil
				}
			}
			problems, _, err := scanEnvUse(fset, p, nil)
			if err != nil {
				bad = append(bad, rel+":無法解析:"+err.Error())
				return nil
			}
			for _, pr := range filterAllowed(rel, problems) {
				bad = append(bad, rel+strings.TrimPrefix(pr, p)) // scanEnvUse 用完整路徑當位置,這裡換成相對路徑
			}
			return nil
		})
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("以下位置直接碰環境變數,請改用 omixor-core/config(新增設定先到 core/config/vars.go 定義):\n  %s", strings.Join(bad, "\n  "))
	}
	// 白名單不能過期:列在 guardAllowUses 的行為,檔案裡真的要有(沒有了就把白名單拿掉)
	for rel, allowed := range guardAllowUses {
		_, uses, err := scanEnvUse(fset, filepath.Join(root, filepath.FromSlash(rel)), nil)
		if err != nil {
			t.Errorf("白名單的檔案 %s 讀不到:%v", rel, err)
			continue
		}
		for _, a := range allowed {
			if !uses[a] {
				t.Errorf("白名單 %s 的 %s 已經沒有用到,請從 guardAllowUses 移除", rel, a)
			}
		}
	}
}

// 守門本身的測試:各種繞法都要被抓到
func TestGuardCatchesBypasses(t *testing.T) {
	cases := []struct {
		name, src string
		want      int // 預期違規數
	}{
		{"Getenv", `package x; import "os"; var _ = os.Getenv("A")`, 1},
		{"LookupEnv", `package x; import "os"; func f() { _, _ = os.LookupEnv("A") }`, 1},
		{"Environ", `package x; import "os"; var _ = os.Environ()`, 1},
		{"ExpandEnv", `package x; import "os"; var _ = os.ExpandEnv("$A")`, 1},
		{"Expand", `package x; import "os"; var _ = os.Expand("$A", func(string) string { return "" })`, 1},
		{"Setenv", `package x; import "os"; func f() { _ = os.Setenv("A", "b") }`, 1},
		{"Unsetenv", `package x; import "os"; func f() { _ = os.Unsetenv("A") }`, 1},
		{"Clearenv", `package x; import "os"; func f() { os.Clearenv() }`, 1},
		{"函式值", `package x; import "os"; var f = os.Getenv`, 1},
		{"當參數傳", `package x; import "os"; func g(func(string) string) {}; func f() { g(os.Getenv) }`, 1},
		{"別名", `package x; import o "os"; var _ = o.Getenv("A")`, 1},
		{"dot import", `package x; import . "os"; var _ = Getenv("A")`, 1},
		{"syscall Getenv", `package x; import "syscall"; var _, _ = syscall.Getenv("A")`, 1},
		{"syscall Environ", `package x; import "syscall"; var _ = syscall.Environ()`, 1},
		{"syscall dot import", `package x; import . "syscall"; var _ = Environ()`, 1},
		{"直接 import env", `package x; import _ "omixor-core/env"`, 1},
		{"PATH 放行", `package x; import "os"; var _ = os.Getenv("PATH"); var _, _ = os.LookupEnv("SHELL")`, 0},
		{"PATH 以外不放行", `package x; import "os"; var _ = os.Getenv("HOME")`, 1},
		{"區域變數叫 os 不算", `package x; type T struct{ Getenv int }; func f() { os := T{}; _ = os.Getenv }`, 0},
		{"無關的 os 函式", `package x; import "os"; var _ = os.ReadFile; var _ = os.Args`, 0},
	}
	for _, c := range cases {
		problems, _, err := scanEnvUse(token.NewFileSet(), "x.go", c.src)
		if err != nil {
			t.Errorf("%s:解析失敗 %v", c.name, err)
			continue
		}
		if len(problems) != c.want {
			t.Errorf("%s:抓到 %d 個,預期 %d:%v", c.name, len(problems), c.want, problems)
		}
	}
	// 白名單只放行列出的行為
	p, _, _ := scanEnvUse(token.NewFileSet(), "core/claude/claude.go", `package x; import "os"; var _ = os.Environ(); var _ = os.Getenv("A")`)
	if got := filterAllowed("core/claude/claude.go", p); len(got) != 1 || !strings.HasSuffix(got[0], "os.Getenv") {
		t.Errorf("白名單只該放行 os.Environ,實際剩下 %v", got)
	}
}
