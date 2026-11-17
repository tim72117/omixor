package mcp

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-web/spec"
)

// tools/list 的規格:核心工具的定義一律取自核心(coremcp.CoreToolDefs,與 /mcp/org 同一份),
// web 自己的工具取自 spec/tools.json(只放 web 的工具)。

// moduleTools 選用模組的工具:這個測試套件沒 import 模組,所以沒有實作(有模組的情況在 internal/modules/integrations 測)
var moduleTools = []string{"github", "github_pull", "github_run", "github_runs"}

// specDefs 規格裡全部的工具定義(含沒編進模組、tools/list 不列的),依名稱
func specDefs(t *testing.T) map[string]any {
	t.Helper()
	b, err := webSpec()
	if err != nil {
		t.Fatal(err)
	}
	var defs []any
	if err := json.Unmarshal(b, &defs); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, d := range defs {
		n := d.(map[string]any)["name"].(string)
		if _, dup := out[n]; dup {
			t.Fatal("工具名稱重複:", n)
		}
		out[n] = d
	}
	return out
}

func canon(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var x any
	_ = json.Unmarshal(b, &x)
	b, _ = json.Marshal(x)
	return string(b)
}

// 核心工具的定義和 coremcp.CoreToolDefs 完全一樣;開放的是 model.CoreTools 的 Admin(加上 webExtraCoreTools)
func TestCoreToolDefsFromCore(t *testing.T) {
	got := specDefs(t)
	want := 0
	for _, d := range coremcp.CoreToolDefs() {
		n := d.(map[string]any)["name"].(string)
		ct, ok := model.CoreToolByName(n)
		if !ok {
			t.Fatal("核心規格的工具沒有登記:", n)
		}
		g, listed := got[n]
		if open := ct.Admin || webExtraCoreTools[n]; open != listed {
			t.Errorf("%s:Admin=%v,web 的規格裡有=%v", n, ct.Admin, listed)
			continue
		}
		if !listed {
			continue
		}
		want++
		if canon(t, g) != canon(t, d) {
			t.Errorf("%s 的定義和核心不同\nweb: %s\ncore:%s", n, canon(t, g), canon(t, d))
		}
	}
	// Admin 的核心工具一定都在
	for _, ct := range model.CoreTools {
		if ct.Admin {
			if _, ok := got[ct.Name]; !ok {
				t.Error("少了管理端點的核心工具", ct.Name)
			}
		}
	}
	for n := range webExtraCoreTools {
		if _, ok := model.CoreToolByName(n); !ok {
			t.Error("webExtraCoreTools 不是核心工具:", n)
		}
	}
	own, _ := webToolDefs()
	if len(got) != want+len(own) {
		t.Fatalf("規格 %d 個 ≠ 核心 %d + web %d", len(got), want, len(own))
	}

	// 以前缺的:Issue 的 assignee(而且 additionalProperties:false 時要列在 properties)、incidents
	for _, n := range []string{"issue_create", "issue_update"} {
		props := got[n].(map[string]any)["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if _, ok := props["assignee"]; !ok {
			t.Error(n, "沒有 assignee")
		}
	}
	for _, n := range []string{"incidents", "incident_declare", "incident_update", "wireframe_render", "flowchart_render"} {
		if !slices.Contains(ToolNames(), n) {
			t.Error("tools/list 沒有", n)
		}
	}
}

// web 自己的工具不能和核心工具撞名(撞名時以核心為準,web 的那筆不收,這裡擋下)
func TestWebToolsDoNotShadowCore(t *testing.T) {
	var all []map[string]any
	if err := json.Unmarshal(spec.ToolsJSON, &all); err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("spec/tools.json 沒有工具")
	}
	core := map[string]bool{}
	for _, ct := range model.CoreTools {
		core[ct.Name] = true
	}
	for _, d := range append(coremcp.CoreToolDefs(), coremcp.WorkerToolDefs()...) {
		core[d.(map[string]any)["name"].(string)] = true
	}
	seen := map[string]bool{}
	for _, d := range all {
		n, _ := d["name"].(string)
		if core[n] {
			t.Errorf("spec/tools.json 的 %s 和核心工具同名(核心工具的定義取自核心,不要放在 spec/tools.json)", n)
		}
		if strings.HasPrefix(n, "ops_") {
			t.Errorf("%s:工具名稱不用 ops_ 前綴(舊名稱由核心的 LegacyToolName 相容)", n)
		}
		if seen[n] {
			t.Error("spec/tools.json 工具重複:", n)
		}
		seen[n] = true
	}
	own, _ := webToolDefs()
	if len(own) != len(all) {
		t.Fatalf("web 工具 %d 個,有 %d 個被核心蓋掉", len(all), len(all)-len(own))
	}
}

// 每個工具都有實作:沒實作的只能是選用模組的工具(這個套件沒編進模組),而且它們不列在 tools/list
func TestEveryToolImplemented(t *testing.T) {
	if m := Missing(); !reflect.DeepEqual(m, moduleTools) {
		t.Fatalf("沒實作的工具應該只有選用模組的 %v:%v", moduleTools, m)
	}
	listed := ToolNames()
	for n := range specDefs(t) {
		if slices.Contains(moduleTools, n) {
			if slices.Contains(listed, n) {
				t.Error("沒編進模組的工具不該列出:", n)
			}
			continue
		}
		if !slices.Contains(listed, n) {
			t.Error("tools/list 少了", n)
		}
	}
}

// tools/list 實際回傳的(經過功能開關過濾)也是核心的定義;舊名稱(ops_ 前綴)照樣能呼叫核心工具
func TestToolsListOverRPC(t *testing.T) {
	a := extTestApp(t)
	h := &Handler{App: a}
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)), &resp); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, d := range resp.Result.Tools {
		byName[d["name"].(string)] = d
	}
	for _, d := range coreToolDefs() {
		n := d.(map[string]any)["name"].(string)
		g, ok := byName[n]
		if !ok {
			t.Error("tools/list 沒有核心工具", n)
			continue
		}
		if canon(t, g) != canon(t, d) {
			t.Error(n, "的定義和核心不同")
		}
	}
	for _, name := range []string{"ops_incidents", "incidents"} {
		var r struct {
			Result map[string]any `json:"result"`
			Error  map[string]any `json:"error"`
		}
		body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`
		if err := json.Unmarshal(h.Handle([]byte(body)), &r); err != nil {
			t.Fatal(err)
		}
		if r.Error != nil {
			t.Fatal(name, r.Error)
		}
	}
}
