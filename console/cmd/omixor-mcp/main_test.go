package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/config"
	"omixor-web/internal/mcpauth"
)

func testCLI(t *testing.T, env map[string]string) *cli {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	env["OMIXOR_CONFIG_DIR"] = t.TempDir()
	return newCLI(config.LoadFrom(func(k string) string { return env[k] }, config.MCP))
}

// 網址:預設本機 console 的畫面連接埠;OMIXOR_MCP_URL 可改;執行者端點是它後面接 /worker/<組織>
func TestResource(t *testing.T) {
	if c := testCLI(t, nil); c.base != "http://127.0.0.1:5191/mcp" {
		t.Fatal(c.base)
	}
	if c := testCLI(t, map[string]string{"OMIXOR_MCP_URL": "https://x.test/mcp"}); c.base != "https://x.test/mcp" {
		t.Fatal(c.base)
	}
	got, err := resource("https://x.test/mcp/", "lab", true)
	if err != nil || got != "https://x.test/mcp/worker/lab" {
		t.Fatal(got, err)
	}
	for _, bad := range [][2]string{{"https://x.test/mcp", ""}, {"https://x.test/mcp", "../x"}, {"https://x.test/", "lab"}} {
		if _, err := resource(bad[0], bad[1], true); err == nil {
			t.Errorf("要拒絕 %v", bad)
		}
	}
	if _, err := resource("https://x.test/mcp", "lab", false); err == nil {
		t.Error("統一入口不能指定組織")
	}
	if got := loginCommand("https://x.test/mcp/worker/lab"); got != "omixor-mcp login --worker --org lab --url https://x.test/mcp" {
		t.Error(got)
	}
}

func testBridge(url string, token func(string) (string, error)) *bridge {
	return &bridge{http: &http.Client{}, endpoint: url, token: token}
}

func run(b *bridge, body string) string {
	var out bytes.Buffer
	w := bufio.NewWriter(&out)
	b.handle([]byte(body), w)
	_ = w.Flush()
	return strings.TrimSpace(out.String())
}

// 轉接帶 token;401 換一次 token 再送;沒有登入就不送請求,回 JSON-RPC 錯誤
func TestBridge(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()
	b := testBridge(srv.URL, func(rejected string) (string, error) {
		if rejected == "old" {
			return "new", nil
		}
		return "old", nil
	})
	if out := run(b, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); !strings.Contains(out, "result") {
		t.Fatal(out)
	}
	if strings.Join(got, ",") != "Bearer old,Bearer new" {
		t.Fatalf("%q", got)
	}
	got = nil
	b = testBridge(srv.URL, func(string) (string, error) { return "", errors.New("請執行:omixor-mcp login") })
	out := run(b, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if len(got) != 0 || !strings.Contains(out, "omixor-mcp login") {
		t.Fatal(got, out)
	}
	if out := run(b, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); out != "" {
		t.Fatal("通知不回覆:", out)
	}
}

// 沒登入:頻道開不起來,錯誤帶登入指令
func TestChannelNeedsLogin(t *testing.T) {
	c := testCLI(t, nil)
	_, err := c.channelSource("http://127.0.0.1:5191/mcp/worker/lab")
	if err == nil || !strings.Contains(err.Error(), "omixor-mcp login --worker --org lab") || !strings.Contains(err.Error(), mcpauth.ErrLoginRequired.Error()) {
		t.Fatal(err)
	}
}

// 執行者資料夾:指向執行者端點、頻道帶 --org,沒有任何 token;一定要給組織
func TestInitWorker(t *testing.T) {
	c := testCLI(t, nil)
	if err := c.initWorker([]string{filepath.Join(t.TempDir(), "w")}); err == nil {
		t.Fatal("沒給 --org 要失敗")
	}
	dir := filepath.Join(t.TempDir(), "w")
	if err := c.initWorker([]string{dir, "--org", "lab"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Args    []string          `json:"args"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	ox, ch := cfg.MCPServers["omixor"], cfg.MCPServers["omixor-tasks"]
	if ox.URL != "http://127.0.0.1:5191/mcp/worker/lab" || len(ox.Headers) != 0 || strings.Contains(string(b), "Bearer") {
		t.Fatalf("%s", b)
	}
	if strings.Join(ch.Args, " ") != "--channel --auto --org lab --url http://127.0.0.1:5191/mcp" {
		t.Fatalf("%q", ch.Args)
	}
	if err := c.initWorker([]string{dir, "--org", "lab"}); err == nil {
		t.Fatal("已有檔案要 --force")
	}
}
