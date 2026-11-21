package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/config"
	"omixor-web/internal/mcptoken"
)

const goodTok = "omxm_test-token-0123456789abcdef"

func cfgWith(env map[string]string) *config.Config {
	return config.LoadFrom(func(k string) string { return env[k] })
}

func srcWith(env map[string]string, dir string) tokenSource {
	return tokenSource{cfg: cfgWith(env), dir: func() string { return dir }}
}

func TestTokenSourcePriority(t *testing.T) {
	dir := t.TempDir()
	// 都沒有:明確報錯並說明怎麼取得
	_, err := srcWith(nil, dir).get()
	if err == nil || !strings.Contains(err.Error(), "OMIXOR_CONSOLE_MCP_TOKEN") || !strings.Contains(err.Error(), mcptoken.FileName) {
		t.Fatalf("要明確報錯並說明怎麼取得:%v", err)
	}
	// 檔案
	if err := mcptoken.WriteFile(dir, "omxm_from-file-0123456789abcdefghijkl"); err != nil {
		t.Fatal(err)
	}
	if got, err := srcWith(nil, dir).get(); err != nil || got != "omxm_from-file-0123456789abcdefghijkl" {
		t.Fatalf("讀檔案:%q %v", got, err)
	}
	// 環境變數優先於檔案
	if got, err := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": goodTok}, dir).get(); err != nil || got != goodTok {
		t.Fatalf("環境變數要優先:%q %v", got, err)
	}
	// 環境變數太短:報錯,不退回去讀檔案(設錯了要讓人知道)
	if _, err := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": "short"}, dir).get(); err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("太短要報錯且不印值:%v", err)
	}
	// 檔案內容壞掉:報錯,不洩漏內容
	_ = os.WriteFile(mcptoken.Path(dir), []byte("bad content"), 0o600)
	if _, err := srcWith(nil, dir).get(); err == nil || strings.Contains(err.Error(), "bad content") {
		t.Fatalf("壞內容:%v", err)
	}
}

func runHandle(t *testing.T, endpoint string, ts tokenSource, body string) (string, string) {
	t.Helper()
	var out bytes.Buffer
	w := bufio.NewWriter(&out)
	handle(&http.Client{}, endpoint, ts, []byte(body), w)
	_ = w.Flush()
	return strings.TrimSpace(out.String()), ""
}

// stdio 橋接:轉送的每個請求都帶 Authorization: Bearer
func TestBridgeSendsToken(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()
	ts := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": goodTok}, t.TempDir())
	out, _ := runHandle(t, srv.URL, ts, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(got) != 1 || got[0] != "Bearer "+goodTok || !strings.Contains(out, "result") {
		t.Fatalf("要帶權杖:%q 回應 %s", got, out)
	}
}

// 沒有權杖:不送請求(不做沒有驗證的連線),有 id 的請求回 JSON-RPC 錯誤說明怎麼取得
func TestBridgeMissingToken(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	out, _ := runHandle(t, srv.URL, srcWith(nil, t.TempDir()), `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if called {
		t.Fatal("沒有權杖不能送出請求")
	}
	var r struct {
		Error struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || !strings.Contains(r.Error.Message, "OMIXOR_CONSOLE_MCP_TOKEN") {
		t.Fatalf("要回說明怎麼取得的錯誤:%s", out)
	}
	// 通知(沒有 id)不回覆
	if out, _ := runHandle(t, srv.URL, srcWith(nil, t.TempDir()), `{"jsonrpc":"2.0","method":"notifications/initialized"}`); out != "" {
		t.Fatalf("通知不回覆:%s", out)
	}
}

// console 回 401(權杖換新或不對):錯誤訊息說明原因,不含權杖
func TestBridgeUnauthorizedMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("Unauthorized"))
	}))
	defer srv.Close()
	ts := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": goodTok}, t.TempDir())
	out, _ := runHandle(t, srv.URL, ts, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if !strings.Contains(out, "拒絕了 MCP 權杖") || strings.Contains(out, goodTok) {
		t.Fatalf("401 的說明:%s", out)
	}
}

// 頻道:讀任務的請求帶權杖,換新後(檔案改了)下一次請求就用新的;沒有權杖時開不起來
func TestChannelSourceSendsToken(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"current":"lab"}`}}}})
	}))
	defer srv.Close()
	dir := t.TempDir()
	if _, err := channelSource(srv.URL, srcWith(nil, dir)); err == nil {
		t.Fatal("沒有權杖不能開頻道")
	}
	_ = mcptoken.WriteFile(dir, "omxm_first-0123456789abcdefghijklmno")
	src, err := channelSource(srv.URL, srcWith(nil, dir))
	if err != nil {
		t.Fatal(err)
	}
	if org, err := src.CurrentOrg(); err != nil || org != "lab" {
		t.Fatalf("%q %v", org, err)
	}
	_ = mcptoken.WriteFile(dir, "omxm_second-0123456789abcdefghijklmn")
	_, _ = src.CurrentOrg()
	if len(got) != 2 || got[0] != "Bearer omxm_first-0123456789abcdefghijklmno" || got[1] != "Bearer omxm_second-0123456789abcdefghijklmn" {
		t.Fatalf("標頭:%q", got)
	}
	// A7:執行中權杖檔不見了(console 資料夾被清掉):這一輪不送請求(尤其不送沒帶 Authorization 的),回錯誤;檔案回來後下一輪恢復
	if err := os.Remove(mcptoken.Path(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := src.CurrentOrg(); err == nil {
		t.Fatal("讀不到權杖要回錯誤")
	}
	if len(got) != 2 {
		t.Fatalf("讀不到權杖時不能送請求:%q", got)
	}
	_ = mcptoken.WriteFile(dir, "omxm_third-0123456789abcdefghijklmno")
	if _, err := src.CurrentOrg(); err != nil || len(got) != 3 || got[2] != "Bearer omxm_third-0123456789abcdefghijklmno" {
		t.Fatalf("下一輪要恢復:%v %q", err, got)
	}
}

func TestInitWorkerWritesTokenAndGitignore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "worker")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules"), 0o644) // 結尾沒有換行
	ts := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": goodTok}, t.TempDir())
	if err := initWorker("http://127.0.0.1:3765/mcp", ts, []string{dir}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range cfg.MCPServers {
		if s.Headers["Authorization"] == "Bearer "+goodTok && strings.HasSuffix(s.URL, "/mcp") {
			found = true
		}
	}
	if !found {
		t.Fatalf(".mcp.json 要有帶權杖的 header:%s", strings.ReplaceAll(string(b), goodTok, "<TOKEN>"))
	}
	for name, content := range map[string][]byte{"CLAUDE.md": mustRead(t, filepath.Join(dir, "CLAUDE.md"))} {
		if bytes.Contains(content, []byte(goodTok)) {
			t.Fatalf("%s 不能含權杖", name)
		}
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(gi) != "node_modules\n.mcp.json\n" {
		t.Fatalf(".gitignore:%q", gi)
	}
	// 再跑一次(--force)不重複加
	if err := initWorker("http://127.0.0.1:3765/mcp", ts, []string{dir, "--force"}); err != nil {
		t.Fatal(err)
	}
	if gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); strings.Count(string(gi), ".mcp.json") != 1 {
		t.Fatalf("不要重複:%q", gi)
	}
}

func TestInitWorkerNoGitNoIgnoreAndNoTokenFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "w")
	ts := srcWith(map[string]string{"OMIXOR_CONSOLE_MCP_TOKEN": goodTok}, t.TempDir())
	if err := initWorker("http://127.0.0.1:3765/mcp", ts, []string{dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Fatal("不是 git repo 不寫 .gitignore")
	}
	// 沒有權杖:報錯、不產生任何檔案
	dir2 := filepath.Join(t.TempDir(), "w2")
	err := initWorker("http://127.0.0.1:3765/mcp", srcWith(nil, t.TempDir()), []string{dir2})
	if err == nil || !strings.Contains(err.Error(), "OMIXOR_CONSOLE_MCP_TOKEN") {
		t.Fatalf("要明確報錯:%v", err)
	}
	if _, err := os.Stat(dir2); err == nil {
		t.Fatal("沒權杖不該建資料夾")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
