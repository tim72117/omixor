package core

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"omixor-core/mcp"
	"omixor-web/internal/mcptoken"
)

// 權杖測試的共同設定:乾淨的資料夾、沒有環境變數覆蓋、log 收進 buffer(檢查不外洩)
func mcpTokenEnv(t *testing.T) (dir string, logs *bytes.Buffer) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", dir)
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "")
	logs = &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return dir, logs
}

func TestMCPTokenGenerateAndReuse(t *testing.T) {
	dir, logs := mcpTokenEnv(t)
	a := New()
	tok, err := MCPTokenValue(a)
	if err != nil || !strings.HasPrefix(tok, mcptoken.Prefix) {
		t.Fatalf("第一次啟動要產生 omxm_ 權杖:%q %v", tok, err)
	}
	if got, err := mcptoken.ReadFile(dir); err != nil || got != tok {
		t.Fatalf("要存在 console 資料夾的 mcp_token:%v", err)
	}
	// 重啟(新的 App)沿用同一把
	b := New()
	if again, _ := MCPTokenValue(b); again != tok {
		t.Fatal("重啟要沿用")
	}
	if strings.Contains(logs.String(), tok) {
		t.Fatalf("權杖不能出現在 log:%s", logs.String())
	}
}

func TestMCPTokenVerifyRotate(t *testing.T) {
	dir, logs := mcpTokenEnv(t)
	a := New()
	old, _ := MCPTokenValue(a)
	if !MCPTokenVerify(a, old, mcp.TokenEqual) {
		t.Fatal("正確的要通過")
	}
	for _, bad := range []string{"", "omxm_wrong", old + "x", old[:len(old)-1], "omx_" + old[len(mcptoken.Prefix):]} {
		if MCPTokenVerify(a, bad, mcp.TokenEqual) {
			t.Fatalf("%q 不該通過", bad)
		}
	}
	nu, err := MCPTokenRotate(a)
	if err != nil || nu == old {
		t.Fatalf("換新:%v", err)
	}
	if MCPTokenVerify(a, old, mcp.TokenEqual) {
		t.Fatal("換新後舊權杖要立刻失效")
	}
	if !MCPTokenVerify(a, nu, mcp.TokenEqual) {
		t.Fatal("新權杖要通過")
	}
	if got, _ := mcptoken.ReadFile(dir); got != nu {
		t.Fatal("檔案也要換成新的")
	}
	if again, _ := MCPTokenValue(New()); again != nu {
		t.Fatal("重啟後沿用換新後的")
	}
	if s := logs.String(); strings.Contains(s, old) || strings.Contains(s, nu) {
		t.Fatalf("權杖不能出現在 log:%s", s)
	}
}

func TestMCPTokenEnvOverride(t *testing.T) {
	dir, _ := mcpTokenEnv(t)
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "omxm_from-environment-0123456789abcdef")
	a := New()
	tok, err := MCPTokenValue(a)
	if err != nil || tok != "omxm_from-environment-0123456789abcdef" {
		t.Fatalf("環境變數要覆蓋:%q %v", tok, err)
	}
	if _, err := os.Stat(mcptoken.Path(dir)); err == nil {
		t.Fatal("環境變數指定的不保存成檔案")
	}
	if _, err := MCPTokenRotate(a); err == nil || !strings.Contains(err.Error(), "OMIXOR_CONSOLE_MCP_TOKEN") {
		t.Fatalf("環境變數指定的不能換:%v", err)
	}
	if !MCPTokenVerify(a, tok, mcp.TokenEqual) || MCPTokenVerify(a, "omxm_other", mcp.TokenEqual) {
		t.Fatal("驗證要用環境變數的那把")
	}
	// 結尾換行會先去掉
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "omxm_from-environment-0123456789abcdef\n")
	if got, err := MCPTokenValue(New()); err != nil || got != "omxm_from-environment-0123456789abcdef" {
		t.Fatalf("結尾換行要去掉:%q %v", got, err)
	}
}

// 環境變數有設但無效(太短、含空白或換行):不悄悄改用自己保存的,而是失敗並說明(錯誤不含權杖內容)
func TestMCPTokenEnvInvalidFails(t *testing.T) {
	dir, _ := mcpTokenEnv(t)
	good, _ := mcptoken.New()
	if err := mcptoken.WriteFile(dir, good); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"太短(31 字元)": "omxm_" + strings.Repeat("a", 26),
		"含空白":       "omxm_" + strings.Repeat("a", 20) + " " + strings.Repeat("b", 20),
		"中間有換行":     "omxm_" + strings.Repeat("a", 20) + "\n" + strings.Repeat("b", 20),
		"太長":        strings.Repeat("x", 513),
	} {
		t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", bad)
		if err := CheckMCPTokenEnv(); err == nil || !strings.Contains(err.Error(), "OMIXOR_CONSOLE_MCP_TOKEN") || !strings.Contains(err.Error(), "32") {
			t.Errorf("%s:啟動檢查要失敗並說明:%v", name, err)
		} else if strings.Contains(err.Error(), bad) {
			t.Errorf("%s:錯誤不能含權杖內容", name)
		}
		a := New()
		if err := MCPTokenInit(a); err == nil {
			t.Errorf("%s:MCPTokenInit 要失敗(不改用保存的權杖)", name)
		}
		if MCPTokenVerify(a, good, mcp.TokenEqual) {
			t.Errorf("%s:無效時拒絕所有連線", name)
		}
	}
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "")
	if err := CheckMCPTokenEnv(); err != nil {
		t.Fatalf("沒設不能失敗:%v", err)
	}
}

func TestMCPAccessViewHidesToken(t *testing.T) {
	mcpTokenEnv(t)
	a := New()
	a.MCP.Port = 3799
	tok, _ := MCPTokenValue(a)
	v, ok := View(a, "mcp_access")
	if !ok {
		t.Fatal("要有 mcp_access view")
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), tok) || strings.Contains(string(b), tok[len(mcptoken.Prefix):len(tok)-4]) {
		t.Fatalf("view 不能含完整權杖:%s", b)
	}
	m := v.(map[string]any)
	if m["tokenSet"] != true || m["tokenHint"] != mcptoken.Hint(tok) || m["source"] != "stored" || m["canRotate"] != true {
		t.Fatalf("view:%v", m)
	}
	cmd, _ := m["claudeCodeCommand"].(string)
	if !strings.Contains(cmd, "127.0.0.1:3799/mcp") || !strings.Contains(cmd, "Bearer <TOKEN>") {
		t.Fatalf("範例設定:%s", cmd)
	}
	// 舊的 mcp view 也不能帶權杖
	old, _ := View(a, "mcp")
	if ob, _ := json.Marshal(old); strings.Contains(string(ob), tok) {
		t.Fatal("mcp view 不能含權杖")
	}
}

func TestMCPTokenActionsLogged(t *testing.T) {
	mcpTokenEnv(t)
	a := New()
	out, err := Action(a, "mcp_token_reveal", map[string]any{UserKey: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	tok := out.(map[string]any)["token"].(string)
	if !MCPTokenVerify(a, tok, mcp.TokenEqual) {
		t.Fatal("reveal 要回目前的權杖")
	}
	lastLog := func() string {
		for i := 0; i < 50; i++ { // Log 是同步寫進 a.C,但 Changed 在 goroutine
			a.Mu.Lock()
			l := a.C["activity"]
			a.Mu.Unlock()
			if len(l) > 0 {
				s, _ := l[0]["text"].(string)
				return s
			}
			time.Sleep(10 * time.Millisecond)
		}
		return ""
	}
	if got := lastLog(); got != "查看了 MCP 權杖" {
		t.Fatalf("動態牆:%q", got)
	}
	out, err = Action(a, "mcp_token_rotate", map[string]any{UserKey: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	if MCPTokenVerify(a, tok, mcp.TokenEqual) || !MCPTokenVerify(a, out.(map[string]any)["token"].(string), mcp.TokenEqual) {
		t.Fatal("rotate 後舊的失效、回傳的是新的")
	}
	if !strings.Contains(lastLog(), "換新了 MCP 權杖") {
		t.Fatalf("動態牆:%q", lastLog())
	}
}

// 有資料庫時存在加密的機密表(Vault),並鏡射一份檔案給同一台機器的 omixor-mcp
func TestMCPTokenInDatabaseVault(t *testing.T) {
	withDB(t)
	dir, logs := mcpTokenEnv(t)
	a := New()
	tok, err := MCPTokenValue(a)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := Vault.Get(mcpTokenAccount); !ok || string(b) != tok {
		t.Fatal("要存在 Vault(ops_secrets,加密)")
	}
	if got, _ := mcptoken.ReadFile(dir); got != tok {
		t.Fatal("要鏡射一份檔案")
	}
	if again, _ := MCPTokenValue(New()); again != tok {
		t.Fatal("重啟沿用 Vault 的")
	}
	nu, _ := MCPTokenRotate(a)
	if b, _ := Vault.Get(mcpTokenAccount); string(b) != nu {
		t.Fatal("換新要寫進 Vault")
	}
	if got, _ := mcptoken.ReadFile(dir); got != nu {
		t.Fatal("鏡射檔也要更新")
	}
	if s := logs.String(); strings.Contains(s, tok) || strings.Contains(s, nu) {
		t.Fatal("權杖不能出現在 log")
	}
}

// A8:權杖讀取失敗時,mcp_access view(所有登入的人看得到)只給固定句子,不含路徑、環境變數內容或底層錯誤;原文進 log
func TestMCPAccessViewErrorIsFixedSentence(t *testing.T) {
	_, logs := mcpTokenEnv(t)
	t.Setenv("OMIXOR_CONSOLE_MCP_TOKEN", "bad token with spaces and secret-xyz")
	a := New()
	v, _ := View(a, "mcp_access")
	m := v.(map[string]any)
	msg, _ := m["error"].(string)
	if msg == "" || m["tokenSet"] != false {
		t.Fatalf("要有 error 且 tokenSet=false:%v", m)
	}
	for _, leak := range []string{"secret-xyz", "OMIXOR_CONSOLE_MCP_TOKEN", `\`, "/", "32"} {
		if strings.Contains(msg, leak) {
			t.Errorf("view 的錯誤不能含 %q:%s", leak, msg)
		}
	}
	if !strings.Contains(logs.String(), "OMIXOR_CONSOLE_MCP_TOKEN") {
		t.Errorf("原文要進 log:%s", logs.String())
	}
}
