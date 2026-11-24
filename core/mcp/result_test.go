package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// decodeResult 取出結果的 structuredContent(必須是 JSON 物件)與 text 解出的值
func decodeResult(t *testing.T, res map[string]any) (sc map[string]any, text any) {
	t.Helper()
	raw, ok := res["structuredContent"].(json.RawMessage)
	if !ok {
		t.Fatalf("沒有 structuredContent:%v", res)
	}
	if err := json.Unmarshal(raw, &sc); err != nil || sc == nil {
		t.Fatalf("structuredContent 不是 JSON 物件:%s", raw)
	}
	if err := json.Unmarshal([]byte(resultText(res)), &text); err != nil {
		t.Fatalf("text 不是 JSON:%q", resultText(res))
	}
	return sc, text
}

// 組裝函式:各種值的型別都有 structuredContent(物件),text 解出來與它相同
func TestOKResultShapes(t *testing.T) {
	type row struct {
		Name string `json:"name"`
	}
	cases := []struct {
		name string
		in   any
		want map[string]any
	}{
		{"物件", map[string]any{"a": 1.0, "b": "<x>"}, map[string]any{"a": 1.0, "b": "<x>"}},
		{"struct", row{"n"}, map[string]any{"name": "n"}},
		{"nil(只回執)", nil, map[string]any{}},
		{"陣列", []any{1.0, "a"}, map[string]any{"result": []any{1.0, "a"}}},
		{"空陣列", []string{}, map[string]any{"result": []any{}}},
		{"字串", "<svg/>", map[string]any{"result": "<svg/>"}},
		{"數字", 3, map[string]any{"result": 3.0}},
		{"布林", false, map[string]any{"result": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := OKResult(c.in, marshal, nil)
			sc, text := decodeResult(t, res)
			if res["isError"] != false || !reflect.DeepEqual(sc, c.want) || !reflect.DeepEqual(text, any(sc)) {
				t.Errorf("sc=%v text=%v isError=%v", sc, text, res["isError"])
			}
			if _, ok := res["_meta"]; ok {
				t.Error("沒給 meta 不該有 _meta")
			}
		})
	}
	if res := OKResult(map[string]any{}, marshal, map[string]any{"omixor/org": "lab"}); res["_meta"].(map[string]any)["omixor/org"] != "lab" {
		t.Errorf("_meta 要保留:%v", res)
	}
}

func TestErrResultShapes(t *testing.T) {
	cases := []struct {
		name         string
		code         string
		status       int
		data         map[string]any
		wantCode     string
		wantHasData  bool
		wantStatusIs float64
	}{
		{"預設 code:404", "", 404, nil, "not_found", false, 404},
		{"預設 code:403", "", 403, nil, "forbidden", false, 403},
		{"預設 code:400", "", 400, nil, "invalid_argument", false, 400},
		{"預設 code:409 帶 data", "", 409, map[string]any{"currentVersion": 3}, "conflict", true, 409},
		{"預設 code:500", "", 500, nil, "internal", false, 500},
		{"其他 4xx", "", 418, nil, "request_failed", false, 418},
		{"指定 code", "agentHasTasks", 409, map[string]any{}, "agentHasTasks", false, 409},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := ErrResult(c.code, "msg", c.status, c.data)
			sc, text := decodeResult(t, res)
			e, _ := sc["error"].(map[string]any)
			if res["isError"] != true || len(sc) != 1 || e["code"] != c.wantCode || e["message"] != "msg" || e["status"] != c.wantStatusIs || (e["data"] != nil) != c.wantHasData {
				t.Errorf("%v", sc)
			}
			if !reflect.DeepEqual(text, any(sc)) {
				t.Errorf("text 要與 structuredContent 相同:%v", text)
			}
		})
	}
}

// 靜態守門:tools/call 的結果只由 result.go 組裝(其他檔不准自己寫 structuredContent / isError 的結果欄位),
// 新增的工具、新的回傳路徑都不可能繞過組裝函式
func TestResultsAssembledOnlyInResultGo(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if f == "result.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "//") || strings.Contains(l, "`json:\"") {
				continue
			}
			if strings.Contains(l, `"structuredContent"`) || strings.Contains(l, `"isError"`) {
				t.Errorf("%s:%d 結果欄位要由 result.go 的 OKResult / ErrResult 組裝:%s", f, i+1, l)
			}
		}
	}
}

// 大欄位(current 或超過 errDataValueMax)只放 structuredContent;text 只留小欄位與 truncated 標記
func TestErrResultLargeDataOnlyInStructured(t *testing.T) {
	big := strings.Repeat("長", errDataValueMax)
	res := ErrResult("", "msg", 409, map[string]any{"currentVersion": 3, "current": map[string]any{"id": "x"}, "taskCounts": big})
	sc, text := decodeResult(t, res)
	d := sc["error"].(map[string]any)["data"].(map[string]any)
	if d["current"] == nil || d["taskCounts"] != big || d["currentVersion"] != 3.0 || d["truncated"] != nil {
		t.Errorf("structuredContent 的 data 要完整、沒有 truncated:%v", d)
	}
	td := text.(map[string]any)["error"].(map[string]any)["data"].(map[string]any)
	if !reflect.DeepEqual(td, map[string]any{"currentVersion": 3.0, "truncated": true}) {
		t.Errorf("text 的 data 只留小欄位與 truncated:%v", td)
	}
	if strings.Contains(resultText(res), big) {
		t.Error("大欄位不該出現在 text")
	}
}

// 小欄位:text 與 structuredContent 相同、沒有 truncated
func TestErrResultSmallDataIdentical(t *testing.T) {
	sc, text := decodeResult(t, ErrResult("", "msg", 409, map[string]any{"currentVersion": 3, "taskCounts": map[string]any{"open": 2}}))
	if !reflect.DeepEqual(text, any(sc)) || strings.Contains(resultText(ErrResult("", "m", 409, map[string]any{"a": 1})), "truncated") {
		t.Errorf("sc=%v text=%v", sc, text)
	}
}

// data 裡有序列化不了的值:退回只有 code、message、status 的本體,結果仍是合法 JSON
func TestErrResultUnmarshalableData(t *testing.T) {
	res := ErrResult("", "msg", 409, map[string]any{"currentVersion": 3, "bad": make(chan int)})
	sc, text := decodeResult(t, res)
	want := map[string]any{"error": map[string]any{"code": "conflict", "message": "msg", "status": 409.0}}
	if res["isError"] != true || !reflect.DeepEqual(sc, want) || !reflect.DeepEqual(text, any(want)) {
		t.Errorf("sc=%v text=%v", sc, text)
	}
}
