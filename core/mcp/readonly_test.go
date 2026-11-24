package mcp_test

import (
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
)

// 唯讀授權(OAuth mcp:read)的白名單固定在這裡:新增工具預設不是唯讀;要加進白名單必須同時改這份清單(逼你想過是不是「純讀取、沒有任何副作用」)。
// 不在名單裡的理由:channel_messages 讀了會標已讀;agent_context 的 mentions、角色憑證相關資訊較多且不必要;其餘都會寫入。
var wantReadOnly = []string{"agent_tasks", "agents", "cabinet", "chain", "channels", "commits", "docs", "flowchart_render", "incidents", "issue_decisions", "issue_items", "issues", "orgs", "projects", "schema_check"}

func TestReadOnlyAllowlistPinned(t *testing.T) {
	var got []string
	for _, ct := range model.CoreTools {
		if ct.ReadOnly {
			got = append(got, ct.Name)
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, wantReadOnly) {
		t.Fatalf("唯讀工具名單變了:\n得到 %v\n要   %v", got, wantReadOnly)
	}
	// 唯讀工具一定是管理端點有的工具(OAuth 只連管理端點);規格裡標了 readOnlyHint=false 的工具(寫入工具)不能在名單裡,
	// 例外 flowchart_render(只畫圖;存檔的 save 參數在端點被擋)
	defs := map[string]map[string]any{}
	for _, d := range mcp.CoreToolDefs() {
		m := d.(map[string]any)
		defs[m["name"].(string)] = m
	}
	for _, n := range got {
		ct, _ := model.CoreToolByName(n)
		if !ct.Admin || defs[n] == nil {
			t.Errorf("%s 不是管理端點的工具", n)
		}
		if n == "flowchart_render" {
			continue
		}
		if ann, _ := defs[n]["annotations"].(map[string]any); ann["readOnlyHint"] != true {
			t.Errorf("%s 在唯讀名單,但規格的 readOnlyHint 不是 true", n)
		}
	}
	for n := range mcp.ReadOnlyTools {
		if !slices.Contains(got, n) {
			t.Error(n)
		}
	}
}

// Server.Handle 的 Deny:被 Allow 擋掉的工具回 isError + 指定訊息;不存在的工具仍是 JSON-RPC 錯誤
func TestDenyMessageAndResources(t *testing.T) {
	s := mcp.NewCoreServer("x")
	h := mcp.Hooks{Allow: func(n string) bool { return n == "orgs" }, Deny: func(string) string { return mcp.ReadOnlyMessage }, Resources: mcp.UIResources()}
	call := func(method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		var m map[string]any
		_ = json.Unmarshal(s.Handle(b, h), &m)
		return m
	}
	r := call("tools/call", map[string]any{"name": "issue_create", "arguments": map[string]any{"title": "x"}})["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), mcp.ReadOnlyMessage) {
		t.Fatal(r)
	}
	if call("tools/call", map[string]any{"name": "nope"})["error"] == nil {
		t.Fatal("不存在的工具要 JSON-RPC 錯誤")
	}
	res := call("resources/read", map[string]any{"uri": mcp.UIFlowchartURI})["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
	if res["mimeType"] != "text/html;profile=mcp-app" {
		t.Fatal(res)
	}
	// 沒有 Resources 的端點:resources/list 仍是空的、initialize 不宣告 resources(角色端點等維持原樣)
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	var m map[string]any
	_ = json.Unmarshal(s.Handle(b, mcp.Hooks{}), &m)
	if m["result"].(map[string]any)["capabilities"].(map[string]any)["resources"] != nil {
		t.Fatal("沒有資源不宣告 resources 能力")
	}
}

// 兩個畫面的共用區塊要一致(手寫成品,沒有建置步驟),而且不引用任何外部來源
func TestUIViewsCommonBlock(t *testing.T) {
	re := regexp.MustCompile(`(?s)/\* COMMON-(CSS|JS)-BEGIN \*/.*?/\* COMMON-(CSS|JS)-END \*/`)
	var blocks [][]string
	for _, r := range mcp.UIResources() {
		if r.MimeType != mcp.UIMimeType || r.Meta["ui"].(map[string]any)["prefersBorder"] != true {
			t.Fatal(r.URI)
		}
		bs := re.FindAllString(r.Text, -1)
		if len(bs) != 2 {
			t.Fatal(r.URI, "要有兩個共用區塊")
		}
		blocks = append(blocks, bs)
		for _, bad := range []string{"http://", "https://", "import(", "fetch(", "XMLHttpRequest", "WebSocket", "eval("} {
			if strings.Contains(r.Text, bad) {
				t.Errorf("%s 含有 %s", r.URI, bad)
			}
		}
	}
	if blocks[0][0] != blocks[1][0] || blocks[0][1] != blocks[1][1] {
		t.Fatal("兩個畫面的共用區塊不一致")
	}
}
