package web

import (
	"strings"
	"testing"
)

// 受限專案不能從舊編號、衍生資料(子項、待決定、文件的分段讀取)繞出來
func TestRestrictedNoLeakByAliasOrDerivedReads(t *testing.T) {
	bob, alice, app := adminTestClients(t)
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()

	ok := func(c *client, path string, body map[string]any) map[string]any {
		t.Helper()
		code, out := c.do("POST", path, body, nil)
		if code != 200 || out["error"] != nil {
			t.Fatalf("%s:%d %v", path, code, out)
		}
		r, _ := out["result"].(map[string]any)
		return r
	}
	sec := ok(alice, "/api/action/project_create", map[string]any{"name": "機密2", "key": "SECB", "restricted": true})["id"].(string)
	moved := ok(alice, "/api/issues/"+org, map[string]any{"title": "搬走的標題", "body": "搬走的內文"})
	oldKey := moved["key"].(string)
	after := ok(alice, "/api/issues/"+org+"/"+oldKey, map[string]any{"project": sec})
	if after["key"] == oldKey {
		t.Fatal("搬專案要換 key")
	}
	for _, ref := range []string{oldKey, strings.ToLower(oldKey), moved["id"].(string), after["key"].(string)} {
		for _, body := range []map[string]any{{}, {"title": "搬走的標題"}, {"title": "被改"}} {
			code, out := bob.do("POST", "/api/issues/"+org+"/"+ref, body, nil)
			if code != 404 || strings.Contains(core2s(out), "搬走的內文") {
				t.Fatalf("Issue API %s %v:要 404,收到 %d %v", ref, body, code, out)
			}
		}
		if code, out := bob.do("POST", "/api/issues/"+org+"/"+ref+"/delete", map[string]any{}, nil); code != 404 {
			t.Fatalf("刪除 %s:%d %v", ref, code, out)
		}
		toolLikeMissing(t, bob, "issues", ref, nil)
	}
	code, out := bob.do("POST", "/api/action/issue_resolve", map[string]any{"ref": oldKey}, nil)
	if code != 404 {
		t.Fatalf("issue_resolve 不能把舊編號解析成受限 Issue 的新編號:%d %v", code, out)
	}

	key := after["key"].(string)
	ok(alice, "/api/tool/issue_item_add", map[string]any{"issue": key, "title": "機密子項內容"})
	ok(alice, "/api/tool/issue_decision_add", map[string]any{"issue": key, "kind": "decision", "source_issue": key, "title": "機密待決定內容", "options": []any{"甲", "乙"}})
	doc := ok(alice, "/api/tool/doc_save", map[string]any{"project": sec, "title": "機密文件B", "section": "docs", "body": "# 段落\n機密文件內文"})
	docID, _ := doc["id"].(string)
	if docID == "" {
		t.Fatal("文件 id", doc)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"issue_items", map[string]any{"issue": key}},
		{"issue_items", map[string]any{"issue": oldKey}},
		{"issue_items", map[string]any{"id": key + ".1"}},
		{"issue_decisions", map[string]any{}},
		{"issue_decisions", map[string]any{"issue": key, "status": "all"}},
		{"commits", map[string]any{"issue": key}},
		{"docs", map[string]any{"id": docID}},
		{"docs", map[string]any{"id": docID, "outline": true}},
		{"docs", map[string]any{"id": docID, "search": "機密"}},
	} {
		_, out := bob.do("POST", "/api/tool/"+c.tool, c.args, nil)
		s := core2s(out)
		for _, leak := range []string{"機密子項內容", "機密待決定內容", "機密文件內文", "機密文件B", "搬走的標題"} {
			if strings.Contains(s, leak) {
				t.Errorf("%s %v 洩漏 %s:%v", c.tool, c.args, leak, out)
			}
		}
	}
	// 管理員照舊讀得到
	for tool, args := range map[string]map[string]any{"issue_items": {"issue": key}, "issue_decisions": {}} {
		_, out := alice.do("POST", "/api/tool/"+tool, args, nil)
		if !strings.Contains(core2s(out), "機密") {
			t.Fatalf("管理員 %s:%v", tool, out)
		}
	}
}
