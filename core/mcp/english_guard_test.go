package mcp_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

// 給 LLM 看的文字一律英文(使用者決定:回應語言全改為英文)。
// 人看的畫面(core/mcp/ui/*.html、model.CoreTool.Title)與使用者/組織資料(文件櫃區塊名稱、Issue 內容)不在此限。

// guardIsCJK 漢字,或中日韓標點與全形符號(「」、。,:等)
func guardIsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

func guardHasCJK(s string) bool { return strings.IndexFunc(s, guardIsCJK) >= 0 }

// specTextAllow 規格文字裡「本身就是資料」的片語(會在檢查前先拿掉):
// 待決定的標籤值是 label 的字面值(見 issue_decision_add 的 labels 說明),不能翻譯
var specTextAllow = []string{"待決定", "需要協助"}

// guardWalkStrings 遞迴走過 JSON 值,回傳所有含 CJK 的字串(連同路徑)
func guardWalkStrings(path string, v any, out *[]string) {
	switch x := v.(type) {
	case string:
		for _, a := range specTextAllow {
			x = strings.ReplaceAll(x, a, "")
		}
		if guardHasCJK(x) {
			*out = append(*out, path+": "+x)
		}
	case []any:
		for i, e := range x {
			guardWalkStrings(path+"["+strconv.Itoa(i)+"]", e, out)
		}
	case map[string]any:
		for k, e := range x {
			guardWalkStrings(path+"."+k, e, out)
		}
	}
}

func guardNoCJK(t *testing.T, what string, v any) {
	t.Helper()
	// 先轉成 JSON 再解回來:[]any 裡的 map[string]any、[]string 才會統一成可走的形狀
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(what, err)
	}
	var x any
	_ = json.Unmarshal(b, &x)
	var bad []string
	guardWalkStrings(what, x, &bad)
	sort.Strings(bad)
	for _, s := range bad {
		t.Errorf("給 LLM 的文字含中文(要改英文):%.200s", s)
	}
}

// 每個端點的 tools/list(title、description、參數說明、annotations 全部)與 initialize(instructions、serverInfo)都不能有中文
func TestLLMFacingSpecIsEnglish(t *testing.T) {
	u := newUni(t)

	// 規格表本身(含只有角色端點、執行者端點才列的工具)
	for name, defs := range map[string][]any{
		"core": mcp.CoreToolDefs(), "worker": mcp.WorkerToolDefs(), "chain": mcp.ChainToolDefs(), "commits": mcp.CommitToolDefs(),
		"decision": mcp.DecisionToolDefs(), "items": mcp.ItemToolDefs(), "channel": mcp.ChannelToolDefs(), "incident": mcp.IncidentToolDefs(),
		"flowchart": mcp.FlowchartToolDefs(), "wireframe": mcp.WireframeToolDefs(),
	} {
		if len(defs) == 0 {
			t.Errorf("%s 規格是空的", name)
		}
		guardNoCJK(t, name+" defs", defs)
	}
	guardNoCJK(t, "CoreInitialize", mcp.CoreInitialize(""))
	guardNoCJK(t, "WorkerInitialize", mcp.WorkerInitialize(""))
	guardNoCJK(t, "UIResources(title/description)", func() any {
		var out []map[string]string
		for _, r := range mcp.UIResources() {
			out = append(out, map[string]string{"name": r.Name, "title": r.Title, "description": r.Description, "uri": r.URI})
		}
		return out
	}())
	// 登記表的英文標題:每個工具都要有,而且是英文
	for _, ct := range model.CoreTools {
		if strings.TrimSpace(ct.TitleEN) == "" {
			t.Errorf("model.CoreTools %s 沒有 TitleEN", ct.Name)
		}
		if guardHasCJK(ct.TitleEN) {
			t.Errorf("model.CoreTools %s 的 TitleEN 含中文:%s", ct.Name, ct.TitleEN)
		}
	}

	// 各端點實際回的 initialize 與 tools/list
	svc := u.svc
	boss := u.boss
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "solo", Name: "獨行", Active: true}); err != nil {
		t.Fatal(err)
	}
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "solo", Title: "做事"})
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "solo", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	agentH := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	workerH := mcp.WorkerEndpoint(mcp.WorkerEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: boss}, "lab", nil
	}})
	// 唯讀授權的統一入口也走一次(tools/list 會篩工具,不改文字,但確認路徑都沒問題)
	for name, ep := range map[string]struct {
		h    http.Handler
		auth string
	}{
		"unified": {u.unified, ""}, "admin": {u.old, ""}, "worker": {workerH, ""},
		"agent": {agentH, "Bearer solo:" + res.Credential.Secret},
	} {
		for _, method := range []string{"initialize", "tools/list"} {
			w := rpcPost(ep.h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{}}, ep.auth)
			var m map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m["result"] == nil {
				t.Fatalf("%s %s:%s", name, method, w.Body.String())
			}
			if method == "tools/list" {
				if tools, _ := m["result"].(map[string]any)["tools"].([]any); len(tools) == 0 {
					t.Fatalf("%s tools/list 是空的", name)
				}
			} else if ins, _ := m["result"].(map[string]any)["instructions"].(string); strings.TrimSpace(ins) == "" {
				t.Errorf("%s initialize 沒有 instructions", name)
			}
			guardNoCJK(t, name+" "+method, m["result"])
		}
	}
}

// 靜態檢查:core/mcp 非測試檔裡,「給 LLM 的字串」的呼叫點不能有含中文的字串字面值。
// 刻意保守,只看這幾種呼叫點:
//   - ToolError{Message: …}、Errorf(…)、prefixErr(…)(工具錯誤)
//   - map 裡 "message"、"note"、"hint"、"hints"、"nextSteps"、"warnings" 這些鍵的值(工具結果的提示文字)
//   - channelNote(…)、pStr/pBool/pInt/pEnum/pStrs/pObjs/coreDef/ToolDef(…)(推給 Claude Code 的通知本文、規格說明)
//
// 不看:log(logf、fmt.Errorf 的回傳給程式日誌)、httperr.Report 的情境名稱、panic 訊息、註解。
func TestNoCJKInLLMFacingStringLiterals(t *testing.T) {
	// 允許清單:字面值本身就是資料(不能翻譯)。key 是檔名,值是字面值的開頭。
	// 目前只有一筆:待決定的標籤字面值 [待決定]、[需要協助](資料庫裡存的就是這兩個字串)
	allow := map[string]string{"tools_decision.go": "Labels, default [待決定]"}

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
				if b, ok := x.(*ast.BasicLit); ok && b.Kind == token.STRING {
					v, _ := strconv.Unquote(b.Value)
					if guardHasCJK(v) && !(allow[f] != "" && strings.HasPrefix(v, allow[f])) {
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
				if id, ok := x.Fun.(*ast.Ident); ok {
					switch id.Name {
					case "Errorf", "prefixErr", "channelNote", "pStr", "pBool", "pInt", "pEnum", "pStrs", "pObjs", "coreDef", "ToolDef":
						for _, a := range x.Args {
							report(a, id.Name)
						}
					}
				}
			case *ast.KeyValueExpr:
				if k, ok := x.Key.(*ast.BasicLit); ok && k.Kind == token.STRING {
					switch name, _ := strconv.Unquote(k.Value); name {
					case "message", "note", "hint", "hints", "nextSteps", "warnings", "description", "title":
						report(x.Value, "key "+name)
					}
				}
			}
			return true
		})
	}
	if scanned < 20 {
		t.Fatalf("只掃到 %d 個檔,路徑不對?", scanned)
	}
}
