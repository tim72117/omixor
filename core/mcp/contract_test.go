package mcp_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"omixor-core/model"
)

// 工具結果的形狀合約(登記表驅動):對管理端點的每一個核心工具各呼叫幾種參數,
// 不管成功或失敗都必須有 structuredContent(JSON 物件)、content[0].text 是同一個物件的 JSON;
// 失敗是 {"error": {code, message, status}}、isError = true。
func TestToolResultContract(t *testing.T) {
	u := newUni(t)
	// 種一個 Issue,讓需要 id 的讀取工具有東西可讀
	issueID, _ := callRaw(t, u.old, "issue_create", map[string]any{"title": "合約測試"})["structuredContent"].(map[string]any)["id"].(string)

	// 每個工具的呼叫:空參數、亂參數(多半是失敗)、帶種子 id
	variants := []map[string]any{{}, {"__bogus__": 1}, {"id": issueID, "issue": issueID}}
	// 一定要成功的代表呼叫(其餘工具在這個測試環境只驗證形狀,不要求成功)
	mustOK := map[string]map[string]any{
		"orgs": {}, "issues": {}, "agents": {}, "projects": {}, "channels": {}, "cabinet": {}, "docs": {},
		"chain": {}, "incidents": {}, "agent_tasks": {}, "issue_create": {"title": "另一個"},
		"issues:id": {"id": issueID},
	}
	listed := map[string]bool{}
	for _, d := range listTools(t, u.old, "") {
		listed[d] = true
	}
	var okSeen, errSeen int
	wrapped := map[string]bool{}
	for _, ct := range model.CoreTools {
		if !ct.Admin {
			continue
		}
		if !listed[ct.Name] {
			t.Errorf("登記表的管理端點工具 %s 不在 tools/list", ct.Name)
			continue
		}
		for _, args := range variants {
			res := callRaw(t, u.old, ct.Name, args)
			isErr := checkShape(t, ct.Name, res)
			if isErr {
				errSeen++
			} else {
				okSeen++
				if sc := res["structuredContent"].(map[string]any); len(sc) == 1 && sc["result"] != nil {
					wrapped[ct.Name] = true
				}
			}
		}
	}
	for key, args := range mustOK {
		name := key
		if key == "issues:id" {
			name = "issues"
		}
		res := callRaw(t, u.old, name, args)
		if checkShape(t, name, res) {
			t.Errorf("%s 預期成功:%v", key, res["structuredContent"])
		}
	}
	if okSeen == 0 || errSeen == 0 {
		t.Errorf("成功與失敗都要有:ok=%d err=%d", okSeen, errSeen)
	}
	t.Logf("成功 %d、失敗 %d;包成 {result} 的工具:%v", okSeen, errSeen, keys(wrapped))
}

// checkShape 驗證一個 tools/call 結果的合約,回傳它是不是失敗
func checkShape(t *testing.T, tool string, res map[string]any) bool {
	t.Helper()
	sc, ok := res["structuredContent"].(map[string]any)
	if !ok || sc == nil {
		t.Errorf("%s:structuredContent 必須是 JSON 物件:%v", tool, res["structuredContent"])
		return false
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Errorf("%s:沒有 content", tool)
		return false
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil || !reflect.DeepEqual(parsed, any(sc)) && !(isErr && sameExceptLargeErrData(parsed, sc)) {
		t.Errorf("%s:content[0].text 要是 structuredContent 的 JSON:%.200s", tool, text)
	}
	if !isErr {
		return false
	}
	e, _ := sc["error"].(map[string]any)
	code, _ := e["code"].(string)
	msg, _ := e["message"].(string)
	status, _ := e["status"].(float64)
	if len(sc) != 1 || code == "" || msg == "" || status < 400 || status > 599 {
		t.Errorf("%s:失敗要是 {error:{code,message,status}}:%v", tool, sc)
	}
	return true
}

// callRaw 呼叫工具,回傳原始的 tools/call result(JSON 解碼後)
func callRaw(t *testing.T, h http.Handler, tool string, args map[string]any) map[string]any {
	t.Helper()
	w := rpcPost(h, rpcReq(1, tool, args), "")
	var m struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.Result == nil {
		t.Fatalf("%s:HTTP %d %s", tool, w.Code, w.Body.String())
	}
	return m.Result
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sameExceptLargeErrData 文件規定的唯一例外:失敗結果的 text 在 error.data 少了大欄位、多一個 truncated: true
// (只有 text 帶 truncated,且 text 的 data 每個欄位都與 structuredContent 相同),其餘完全相同
func sameExceptLargeErrData(text any, sc map[string]any) bool {
	te, _ := text.(map[string]any)["error"].(map[string]any)
	se, _ := sc["error"].(map[string]any)
	td, _ := te["data"].(map[string]any)
	sd, _ := se["data"].(map[string]any)
	if td == nil || sd == nil || td["truncated"] != true || sd["truncated"] != nil {
		return false
	}
	for k, v := range td {
		if k != "truncated" && !reflect.DeepEqual(v, sd[k]) {
			return false
		}
	}
	rest := func(e map[string]any) map[string]any {
		c := map[string]any{}
		for k, v := range e {
			if k != "data" {
				c[k] = v
			}
		}
		return c
	}
	return reflect.DeepEqual(rest(te), rest(se))
}
