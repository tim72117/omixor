package errscan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, src string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsRawErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "x.go", `package x

import (
	"fmt"
	"net/http"
)

type ToolError struct{ Message string }
type Sec struct{ Error string }

func a(w http.ResponseWriter, err error) { http.Error(w, err.Error(), 500) }
func b(err error) any                    { return map[string]any{"error": "格式錯誤:" + err.Error()} }
func c(err error) any                    { return map[string]any{"message": err.Error()} }
func badRequest(err error) any           { return map[string]any{"error": err.Error()} }
func d(w http.ResponseWriter)            { http.Error(w, "固定訊息", 500) }
func e(err error) any                    { return map[string]any{"msg": fmt.Sprint(err)} }
func f(err error) error                  { return ToolError{Message: "領取失敗:" + err.Error()} }
func g(w http.ResponseWriter, err error) { fmt.Fprintf(w, "壞了:%v", err) }
func h(w http.ResponseWriter, err error) { http.Error(w, fmt.Sprint(err), 500) }
func i(err error) Sec                    { return Sec{Error: err.Error()} }
func j(err error) error {
	msg := err.Error()
	m2 := "前綴 " + msg
	return ToolError{Message: m2}
}
func o1(err error) error { return fmt.Errorf("失敗:%v", err) }
func o2(err error) error { return fmt.Errorf("失敗:%s", err.Error()) }
func o3(err error) error { return fmt.Errorf("失敗:%w", err) }
func p1(w http.ResponseWriter) { writeJSON(w, 200, map[string]any{"error": "x"}) }
func p2(w http.ResponseWriter, r *http.Request, err error) { writeErr(w, r, 200, err) }
func p3(w http.ResponseWriter) { writeJSON(w, 200, map[string]any{"result": 1}) }
func p4(w http.ResponseWriter, r *http.Request, err error) { writeErr(w, r, 400, err) }
func k(err error) any    { return map[string]any{"error": "固定句子", "note": err.Error()} }
func l(err error) any    { return ToolError{Message: fmt.Sprintf("x %v", err)} }
func m(name string) any  { return ToolError{Message: "找不到 " + name} }
func n(err error) string { s := err.Error(); return "ok" + s[:0] }
`)
	write(t, dir, "sub/y.go", `package sub
func s(err error) any { return map[string]any{"error": err.Error()} }
`)
	write(t, dir, "y_test.go", "package x\nfunc e(err error) any { return map[string]any{\"error\": err.Error()} }\n")
	got, err := Scan(dir, map[string]bool{"x.go:badRequest": true})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	// a b c e f g h i j l o1 o2 p1 p2 + sub/y.go:s;k、m、n、d 不算,badRequest 白名單
	want := []string{"(a)", "(b)", "(c)", "(e)", "(f)", "(g)", "(h)", "(i)", "(j)", "(l)", "(o1)", "(o2)", "(p1)", "(p2)", "sub/y.go:2(s)"}
	if len(got) != len(want) {
		t.Fatalf("要 %d 處,得到 %d:\n%s", len(want), len(got), strings.Join(got, "\n"))
	}
	for _, w := range want {
		ok := false
		for _, g := range got {
			if strings.Contains(g, w) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("沒找到 %s:\n%s", w, strings.Join(got, "\n"))
		}
	}
}

// 白名單是「檔案加函式名」:同名函式在別的檔案不放行
func TestAllowIsPerFile(t *testing.T) {
	dir := t.TempDir()
	src := "package x\nfunc bad(err error) any { return map[string]any{\"error\": err.Error()} }\n"
	write(t, dir, "a.go", src)
	write(t, dir, "b.go", src)
	got, err := Scan(dir, map[string]bool{"a.go:bad": true})
	if err != nil || len(got) != 1 || !strings.HasPrefix(got[0], "b.go:") {
		t.Fatalf("只有 b.go 違規:%v %v", got, err)
	}
}
