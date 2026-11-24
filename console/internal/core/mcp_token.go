package core

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"sync"

	"omixor-core/config"
	"omixor-web/internal/mcptoken"
)

// console 的 MCP 連接埠權杖(所有接進 /mcp、/mcp/worker 的 MCP 都要帶 Authorization: Bearer <權杖>)。
//
// 來源與保存(優先序):
//  1. 環境變數 OMIXOR_CONSOLE_MCP_TOKEN(至少 32 字元,有設但無效就拒絕啟動;給 Docker、自動化用):有設就用它,不保存、不能在畫面換(要換請改環境變數後重啟)
//  2. 有資料庫:Vault 的帳號 mcp:token(用 OMIXOR_SECRET_KEY 加密的 omixor_secrets);同時鏡射一份到 console 資料夾的 mcp_token(權限 0600),
//     讓同一台機器上的 omixor-mcp 讀得到
//  3. 沒有資料庫:只存 console 資料夾的 mcp_token(權限 0600)
//
// 第一次啟動(都沒有)時產生一把 32 位元組隨機的 omxm_ 權杖。權杖不寫進 log、不放進 view(只給 tokenHint),
// 完整權杖只有管理員專用的 action mcp_token_reveal 與 mcp_token_rotate 會回傳。

const mcpTokenAccount = "mcp:token"

type mcpTokenStore struct {
	mu      sync.Mutex
	token   string
	fromEnv bool
	loaded  bool
}

var mcpTokens sync.Map // *App → *mcpTokenStore(不放在 App.Mu 底下:view 在拿著 Mu 時呼叫)

func mcpTokenOf(a *App) *mcpTokenStore {
	v, _ := mcpTokens.LoadOrStore(a, &mcpTokenStore{})
	return v.(*mcpTokenStore)
}

// load 取得目前的權杖(第一次呼叫時讀或產生);呼叫時要拿著 s.mu
func (s *mcpTokenStore) load() error {
	if s.loaded {
		return nil
	}
	env, set, err := mcptoken.FromEnv(config.Current().Str(config.MCPToken))
	if err != nil { // 有設但無效:不悄悄改用別把(使用者以為在用自己給的那把),直接失敗
		return err
	}
	if set {
		s.token, s.fromEnv, s.loaded = env, true, true
		return nil
	}
	tok, err := readStoredMCPToken()
	if err != nil {
		return err
	}
	if tok == "" {
		if tok, err = mcptoken.New(); err != nil {
			return err
		}
		if err := writeStoredMCPToken(tok); err != nil {
			return err
		}
		log.Printf("MCP:已產生新的連接埠權杖(%s);要取得完整權杖用畫面的 MCP 設定,或讀 %s", mcptoken.Hint(tok), mcptoken.Path(DataRoot()))
	}
	s.token, s.loaded = tok, true
	return nil
}

func readStoredMCPToken() (string, error) {
	if DB() != nil {
		if b, ok := Vault.Get(mcpTokenAccount); ok && mcptoken.Valid(string(b)) {
			mirrorMCPTokenFile(string(b))
			return string(b), nil
		}
		return "", nil
	}
	tok, err := mcptoken.ReadFile(DataRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return tok, err
}

func writeStoredMCPToken(tok string) error {
	if DB() != nil {
		if err := Vault.Set(mcpTokenAccount, []byte(tok)); err != nil {
			return fmt.Errorf("保存 MCP 權杖失敗:%w", err)
		}
		mirrorMCPTokenFile(tok)
		return nil
	}
	if err := mcptoken.WriteFile(DataRoot(), tok); err != nil {
		return fmt.Errorf("保存 MCP 權杖失敗:%w", err)
	}
	return nil
}

// mirrorMCPTokenFile 資料庫模式也寫一份檔案給同一台機器上的 omixor-mcp 讀;失敗只記 log(不含權杖)
func mirrorMCPTokenFile(tok string) {
	if cur, err := mcptoken.ReadFile(DataRoot()); err == nil && cur == tok {
		return
	}
	if err := mcptoken.WriteFile(DataRoot(), tok); err != nil {
		log.Printf("MCP 權杖鏡射到 %s 失敗:%v", mcptoken.Path(DataRoot()), err)
	}
}

// MCPTokenVerify MCP 連接埠驗證用:token 是不是目前的權杖(常數時間比對,由呼叫端的 coremcp.TokenEqual 做)
func MCPTokenVerify(a *App, token string, equal func(a, b string) bool) bool {
	s := mcpTokenOf(a)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		log.Printf("MCP 權杖讀取失敗,拒絕所有連線:%v", err)
		return false
	}
	return equal(s.token, token)
}

// CheckMCPTokenEnv 啟動最前面檢查環境變數 OMIXOR_CONSOLE_MCP_TOKEN:有設但無效回傳說明(呼叫端要結束程式);沒設或有效回傳 nil
func CheckMCPTokenEnv() error {
	_, _, err := mcptoken.FromEnv(config.Current().Str(config.MCPToken))
	return err
}

// MCPTokenInit 啟動時先把權杖準備好(產生或讀取);失敗回傳錯誤,呼叫端不要開 MCP 連接埠
func MCPTokenInit(a *App) error {
	s := mcpTokenOf(a)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// MCPTokenValue 目前的完整權杖(只給管理員專用的 action 與測試)
func MCPTokenValue(a *App) (string, error) {
	s := mcpTokenOf(a)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return "", err
	}
	return s.token, nil
}

// MCPTokenRotate 換新權杖(撤銷舊的,舊權杖立刻失效);環境變數指定的不能換
func MCPTokenRotate(a *App) (string, error) {
	s := mcpTokenOf(a)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return "", err
	}
	if s.fromEnv {
		return "", UserError{"MCP 權杖由環境變數 OMIXOR_CONSOLE_MCP_TOKEN 指定,要換請改環境變數後重新啟動"}
	}
	tok, err := mcptoken.New()
	if err != nil {
		return "", err
	}
	if err := writeStoredMCPToken(tok); err != nil {
		return "", err
	}
	s.token = tok
	return tok, nil
}

// mcpClaudeCodeCommand 給 Claude Code 的設定指令範例(權杖用佔位字,不放真的)
func mcpClaudeCodeCommand(port int) string {
	return fmt.Sprintf(`claude mcp add --transport http omixor http://127.0.0.1:%d/mcp --header "Authorization: Bearer <TOKEN>"`, port)
}

func init() {
	// 另開一個 view(不改 registry.go 裡的 mcp view):只有遮罩的 tokenHint,永遠不回整把
	RegisterView("mcp_access", func(a *App) any {
		port := a.MCP.Port
		s := mcpTokenOf(a)
		s.mu.Lock()
		defer s.mu.Unlock()
		out := map[string]any{
			"tokenSet":          false,
			"url":               fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
			"claudeCodeCommand": mcpClaudeCodeCommand(port),
			"workerUrl":         fmt.Sprintf("http://127.0.0.1:%d/mcp/worker", port),
			"canRotate":         true,
		}
		if err := s.load(); err != nil {
			log.Printf("mcp_access:MCP 權杖讀取失敗:%v", err)
			out["error"] = "MCP 權杖讀取失敗,詳情看 console 的啟動紀錄"
			return out
		}
		out["tokenSet"] = true
		out["tokenHint"] = mcptoken.Hint(s.token)
		out["source"] = map[bool]string{true: "env", false: "stored"}[s.fromEnv]
		out["canRotate"] = !s.fromEnv
		return out
	})
	// 完整權杖只透過這兩個管理員專用的 action(web 的 adminActions 擋非管理員),每次呼叫都寫進動態牆
	RegisterAction("mcp_token_reveal", func(a *App, args map[string]any) (any, error) {
		tok, err := MCPTokenValue(a)
		if err != nil {
			return nil, err
		}
		a.Mu.Lock()
		a.Log(UserActor(a, args), "person.badge.key", "查看了 MCP 權杖", "settings")
		a.Mu.Unlock()
		return map[string]any{"token": tok, "tokenHint": mcptoken.Hint(tok)}, nil
	})
	RegisterAction("mcp_token_rotate", func(a *App, args map[string]any) (any, error) {
		tok, err := MCPTokenRotate(a)
		if err != nil {
			return nil, err
		}
		a.Mu.Lock()
		a.Log(UserActor(a, args), "person.badge.key", "換新了 MCP 權杖(舊的立刻失效)", "settings")
		a.Mu.Unlock()
		a.Changed("mcp_access")
		return map[string]any{"token": tok, "tokenHint": mcptoken.Hint(tok)}, nil
	})
}
