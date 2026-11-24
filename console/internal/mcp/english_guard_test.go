package mcp

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"omixor-web/spec"
)

// 給 LLM 看的文字一律英文(使用者決定:回應語言全改為英文),console 的 MCP 也一樣。
// 做法同核心的 core/mcp/english_guard_test.go;那邊的輔助函式在 _test 套件裡不能共用,這裡只留最小的一份。
// 不在此限:人看的畫面、a.Log 寫進動態牆的文字、service.Error 的 Msg(服務層訊息,之後在 MCP 邊界統一翻譯)。

func hasCJK(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
	}) >= 0
}

// specTextAllow 規格文字裡「本身就是資料」的片語(檢查前先拿掉):待決定的標籤值是 label 的字面值,不能翻譯(同核心的 guard)
var specTextAllow = []string{"待決定", "需要協助"}

// walkCJK 遞迴走過 JSON 值,回傳含 CJK 的字串(連同路徑)
func walkCJK(path string, v any, out *[]string) {
	switch x := v.(type) {
	case string:
		for _, a := range specTextAllow {
			x = strings.ReplaceAll(x, a, "")
		}
		if hasCJK(x) {
			*out = append(*out, path+": "+x)
		}
	case []any:
		for i, e := range x {
			walkCJK(path+"["+strconv.Itoa(i)+"]", e, out)
		}
	case map[string]any:
		for k, e := range x {
			walkCJK(path+"."+k, e, out)
		}
	}
}

// tools/list 的工具規格(含 Swift 匯出的)與 initialize 的 instructions 不能有中文
func TestConsoleLLMFacingSpecIsEnglish(t *testing.T) {
	checked := 0
	for _, d := range ToolDefs() {
		checked++
		var bad []string
		walkCJK(d.(map[string]any)["name"].(string), d, &bad)
		for _, s := range bad {
			t.Errorf("給 LLM 的工具規格含中文(要改英文):%.200s", s)
		}
	}
	if checked < 40 { // 核心工具 + Swift 匯出 + tools_console.json
		t.Fatalf("只檢查到 %d 個工具", checked)
	}

	var init map[string]any
	if err := json.Unmarshal(initializeWithVersion(spec.InitializeJSON), &init); err != nil {
		t.Fatal(err)
	}
	var bad []string
	walkCJK("initialize", init, &bad)
	for _, s := range bad {
		t.Errorf("initialize 含中文(要改英文):%.200s", s)
	}
}

// 每一組改寫的原文都要真的出現在 spec/initialize.json 的 instructions 裡;Swift 改了句子(例如轉英文)就會在這裡擋下,不會悄悄失效
func TestInitInstructionRewritesApply(t *testing.T) {
	var init map[string]any
	if err := json.Unmarshal(spec.InitializeJSON, &init); err != nil {
		t.Fatal(err)
	}
	ins, _ := init["instructions"].(string)
	for _, p := range initInstructionRewrites {
		if !strings.Contains(ins, p[0]) {
			t.Errorf("spec/initialize.json 的 instructions 沒有這句,改寫不會生效:%.80s", p[0])
		}
	}
}

// llmTextKey map 的鍵是不是「給 LLM 看的提示文字」
func llmTextKey(k *ast.BasicLit) bool {
	if k.Kind != token.STRING {
		return false
	}
	name, _ := strconv.Unquote(k.Value)
	return slices.Contains([]string{"message", "note", "hint", "hints", "nextSteps", "warnings", "description", "title", "error"}, name)
}

// 靜態檢查:console/internal/mcp 非測試檔裡,「給 LLM 的字串」的呼叫點不能有含中文的字串字面值
//   - ToolError{Message: …}、Errorf(…)、PrefixErr(…)(工具錯誤)
//   - map 裡 "message"、"note"、"hint"、"hints"、"nextSteps"、"warnings"、"description"、"title"、"error" 這些鍵的值
//
// 不看:a.Log(寫進動態牆給人看)、service.Error 的 Msg、log、註解、httperr.Report 的情境名稱。
func TestNoCJKInLLMFacingStringLiterals(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	scanned := 0
	fs := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fs, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		report := func(n ast.Node, where string) {
			ast.Inspect(n, func(x ast.Node) bool {
				if c, ok := x.(*ast.CallExpr); ok { // httperr.Report("情境名稱", err):情境名稱只進 log
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Report" {
						return false
					}
				}
				if b, ok := x.(*ast.BasicLit); ok && b.Kind == token.STRING {
					if v, _ := strconv.Unquote(b.Value); hasCJK(v) {
						t.Errorf("%s:%d %s 含中文字串(給 LLM 的文字要英文):%.80q", f, fs.Position(b.Pos()).Line, where, v)
					}
				}
				return true
			})
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				if id, ok := x.Type.(*ast.Ident); ok && id.Name == "ToolError" {
					for _, e := range x.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Message" {
								report(kv.Value, "ToolError.Message")
							}
						}
					}
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "Errorf" || id.Name == "PrefixErr") {
					for _, a := range x.Args {
						report(a, id.Name)
					}
				}
			case *ast.KeyValueExpr:
				if k, ok := x.Key.(*ast.BasicLit); ok && llmTextKey(k) {
					report(x.Value, "key "+k.Value)
				}
			case *ast.AssignStmt: // o["note"] = "…"
				for i, l := range x.Lhs {
					if ix, ok := l.(*ast.IndexExpr); ok && i < len(x.Rhs) {
						if k, ok := ix.Index.(*ast.BasicLit); ok && llmTextKey(k) {
							report(x.Rhs[i], "key "+k.Value)
						}
					}
				}
			}
			return true
		})
	}
	if scanned < 15 {
		t.Fatalf("只掃到 %d 個檔,路徑不對?", scanned)
	}
}
