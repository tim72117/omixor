// Package errscan 守門用的靜態掃描(給各出口套件的測試用):找出把錯誤原文直接寫進回應的寫法,
// 例如 http.Error(w, err.Error(), …)、map[string]any{"error": err.Error()}、mcp.ToolError{Message: err.Error()}。
// 對外的錯誤一律走 httperr(資料庫錯誤等內部細節要遮蔽),不能繞過。
//
// 掃描只看語法(沒有型別資訊),所以:
//   - 「錯誤原文的運算式」指:x.Error()(沒有參數)、fmt.Sprint / Sprintf / Errorf / Sprintln 的參數裡有名字像錯誤的變數
//     (err、e、xxxErr、errXxx);
//   - 變數別名也算:msg := err.Error() 之後,msg 出現在輸出裡就是違規(同一個頂層宣告內,不分區塊)。
//
// 輸出的寫法有:http.Error(…)、鍵名 "error" / "msg" / "message" 的欄位、ToolError{Message: …}、
// fmt.Fprint*(w, …)(w、rw、resp 開頭的寫入器)、struct 欄位 Error: …(例如 ImportSection{Error: err.Error()})。
package errscan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// Scan 掃 dir 底下(含子目錄)非測試的 .go 檔,回傳違規的位置(檔:行(函式) 與簡述)。
// allow 是白名單,鍵是「相對於 dir 的檔案路徑(用 /)加冒號加頂層函式名」,例如 "tools_diagram.go:diagramRender";
// 那個函式(含裡面的匿名函式)的寫法不算違規。白名單要在旁邊註明理由(例如「只回解析器訊息」「診斷類,刻意給人看」)。
func Scan(dir string, allow map[string]bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if path != dir && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "testdata" || n == "dist" || n == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, d := range f.Decls {
			name := "(宣告)"
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Body == nil {
					continue
				}
				name = x.Name.Name
			}
			if allow[rel+":"+name] {
				continue
			}
			out = append(out, scanDecl(fset, rel, name, d)...)
		}
		return nil
	})
	return out, err
}

func scanDecl(fset *token.FileSet, rel, fn string, d ast.Decl) []string {
	var out []string
	alias := map[string]bool{} // 存了錯誤原文的區域變數
	tainted := func(e ast.Expr) bool { return isTainted(e, alias) }
	report := func(n ast.Node, what string) {
		out = append(out, fmt.Sprintf("%s:%d(%s):%s", rel, fset.Position(n.Pos()).Line, fn, what))
	}
	ast.Inspect(d, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == len(x.Rhs) {
				for i, r := range x.Rhs {
					if id, ok := x.Lhs[i].(*ast.Ident); ok && tainted(r) {
						alias[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			if len(x.Names) == len(x.Values) {
				for i, v := range x.Values {
					if tainted(v) {
						alias[x.Names[i].Name] = true
					}
				}
			}
		case *ast.CallExpr:
			switch {
			case isSel(x.Fun, "http", "Error"):
				for _, a := range x.Args {
					if tainted(a) {
						report(x, "http.Error 帶了錯誤原文")
						break
					}
				}
			case isSel(x.Fun, "fmt", "Errorf") && len(x.Args) > 0:
				// 把錯誤原文包進新錯誤:%w 不算(Classify 還找得到型別);.Error() 或 %v / %s 帶 err 會變成沒有型別的字串,
				// 資料庫錯誤就遮蔽不了
				wraps := false
				if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					wraps = strings.Contains(lit.Value, "%w")
				}
				for _, a := range x.Args[1:] {
					if tainted(a) || (!wraps && hasErrIdent(a, alias)) {
						report(x, "fmt.Errorf 把錯誤原文變成字串(要用 %w 包起來)")
						break
					}
				}
			case isOKFailure(x):
				report(x, "失敗卻回 200(要用真的 HTTP 狀態碼;協定層例外請登記白名單並寫理由)")
			case isFprint(x):
				for _, a := range x.Args[1:] {
					if tainted(a) || hasErrIdent(a, alias) {
						report(x, "fmt.Fprint* 把錯誤原文寫進回應")
						break
					}
				}
			}
		case *ast.CompositeLit:
			if isToolError(x.Type) {
				for _, el := range x.Elts {
					v := el
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Message" {
							continue
						}
						v = kv.Value
					}
					if tainted(v) {
						report(x, "ToolError 的 Message 帶了錯誤原文")
					}
				}
			}
		case *ast.KeyValueExpr:
			if !tainted(x.Value) {
				break
			}
			if k := keyName(x.Key); k == "error" || k == "msg" || k == "message" {
				report(x, fmt.Sprintf("%q 欄位帶了錯誤原文", k))
			} else if id, ok := x.Key.(*ast.Ident); ok && id.Name == "Error" {
				report(x, "Error 欄位帶了錯誤原文")
			}
		}
		return true
	})
	return out
}

func isSel(e ast.Expr, pkg, name string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != name {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func isFprint(c *ast.CallExpr) bool {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || len(c.Args) < 2 {
		return false
	}
	if id, ok := s.X.(*ast.Ident); !ok || id.Name != "fmt" || !strings.HasPrefix(s.Sel.Name, "Fprint") {
		return false
	}
	w, ok := c.Args[0].(*ast.Ident)
	if !ok {
		return false
	}
	n := strings.ToLower(w.Name)
	return n == "w" || n == "rw" || strings.HasPrefix(n, "resp")
}

func isToolError(t ast.Expr) bool {
	switch x := t.(type) {
	case *ast.Ident:
		return x.Name == "ToolError" || x.Name == "UserError" || x.Name == "StatusError"
	case *ast.SelectorExpr:
		return x.Sel.Name == "ToolError" || x.Sel.Name == "UserError" || x.Sel.Name == "StatusError"
	}
	return false
}

func keyName(e ast.Expr) string {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return ""
	}
	s, _ := strconv.Unquote(b.Value)
	return s
}

// isErrName 變數名字像不像 error
func isErrName(n string) bool {
	l := strings.ToLower(n)
	return l == "e" || strings.HasPrefix(l, "err") || strings.HasSuffix(l, "err")
}

// isTainted e 裡面有沒有錯誤原文:x.Error()、fmt.Sprint*(…err…)、存過錯誤原文的別名
func isTainted(e ast.Expr, alias map[string]bool) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.Ident:
			if alias[x.Name] {
				found = true
			}
		case *ast.CallExpr:
			if s, ok := x.Fun.(*ast.SelectorExpr); ok {
				if s.Sel.Name == "Error" && len(x.Args) == 0 {
					found = true
				} else if id, ok := s.X.(*ast.Ident); ok && id.Name == "fmt" && (strings.HasPrefix(s.Sel.Name, "Sprint") || s.Sel.Name == "Errorf") {
					for _, a := range x.Args {
						if hasErrIdent(a, alias) {
							found = true
						}
					}
				}
			}
		}
		return !found
	})
	return found
}

// hasErrIdent 運算式本身(不含呼叫的結果)是不是錯誤變數:err、xxxErr、別名
func hasErrIdent(e ast.Expr, alias map[string]bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return isErrName(x.Name) || alias[x.Name]
	case *ast.SelectorExpr:
		return isErrName(x.Sel.Name)
	}
	return false
}

// isOKFailure writeJSON(w, 200, {…"error"…}) 或 writeErr*(w, r, 200, …):失敗卻回 200
func isOKFailure(c *ast.CallExpr) bool {
	id, ok := c.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	isInt := func(i int, v string) bool {
		if len(c.Args) <= i {
			return false
		}
		b, ok := c.Args[i].(*ast.BasicLit)
		return ok && b.Kind == token.INT && b.Value == v
	}
	switch id.Name {
	case "writeErr", "writeErrDetail", "writeErrPrefix", "localFail":
		return isInt(2, "200")
	case "writeJSON", "writeLocal":
		if !isInt(1, "200") || len(c.Args) < 3 {
			return false
		}
		found := false
		ast.Inspect(c.Args[2], func(n ast.Node) bool {
			if kv, ok := n.(*ast.KeyValueExpr); ok && keyName(kv.Key) == "error" {
				found = true
			}
			return !found
		})
		return found
	}
	return false
}
