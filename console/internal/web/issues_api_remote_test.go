package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
)

// 遠端模式(假的 omixor server):REST /api/issues 的失敗狀態碼要照遠端回的(409 帶 current、currentVersion;404;403),
// 不能因為是 *client.Error 就變 500、也不能吞錯回 200
func TestIssueAPIRemoteStatusCodes(t *testing.T) {
	issue := map[string]any{"id": "ISS-1", "key": "ISS-1", "number": 1, "title": "遠端", "body": "", "status": "open", "priority": "normal",
		"labels": []string{}, "comments": []any{}, "createdBy": "tim", "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z", "version": 5}
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/orgs/default/issues":
			reply(w, 200, []any{issue})
		case r.Method == "POST" && r.URL.Path == "/api/orgs/default/issues/ISS-1":
			cur := map[string]any{}
			for k, v := range issue {
				cur[k] = v
			}
			cur["version"], cur["title"] = 6, "別人改過"
			reply(w, 409, map[string]any{"error": "這張 Issue 已經被別人改過,請重新載入", "currentVersion": 6, "current": cur})
		case r.Method == "POST" && r.URL.Path == "/api/orgs/default/issues/NOPE/delete":
			reply(w, 404, map[string]any{"error": "找不到 Issue NOPE"})
		case r.Method == "POST" && r.URL.Path == "/api/orgs/default/issues/ISS-1/delete":
			reply(w, 403, map[string]any{"error": "沒有權限刪除"})
		default:
			reply(w, 404, map[string]any{"error": "沒有這個路徑:" + r.URL.Path})
		}
	}))
	t.Cleanup(fake.Close)
	t.Setenv("OMIXOR_SERVER", fake.URL)
	t.Setenv("OMIXOR_SERVER_TOKEN", "fake-token-0123456789")
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	os.Remove(core.DataRoot() + "/auth.json")
	if !core.IsRemote() {
		t.Skip("測試環境沒進遠端模式(設定被快取了)")
	}
	c, _ := newServer(t, auth.New(&auth.MemorySecrets{}))

	code, out := c.do("POST", "/api/issues/default/ISS-1", map[string]any{"title": "x", "version": 3}, nil)
	if code != 409 || out["currentVersion"] == nil || out["current"] == nil {
		t.Fatalf("遠端 409 要原樣帶回(含 current、currentVersion):%d %v", code, out)
	}
	if code, out = c.do("POST", "/api/issues/default/NOPE/delete", nil, nil); code != 404 {
		t.Fatalf("遠端 404:%d %v", code, out)
	}
	if code, out = c.do("POST", "/api/issues/default/ISS-1/delete", nil, nil); code != 403 || !strings.Contains(out["error"].(string), "權限") {
		t.Fatalf("遠端 403:%d %v", code, out)
	}
}
