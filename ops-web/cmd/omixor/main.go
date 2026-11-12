// omixor:omixor-issues 服務的管理 CLI。資料維護一律走服務的 API,不直連資料庫。
//
//	omixor login [--server 網址] [--device 名稱]   用 evoleap 登入證明身分,換 omixor 自己的 token
//	omixor logout                                  撤銷這台的 token
//	omixor whoami
//	omixor users                                   使用者名單(管理員)
//	omixor users add <email> [--role admin|member]
//	omixor users role <email> <admin|member>
//	omixor users disable|enable <email>
//	omixor tokens                                  有效的 token(管理員)
//	omixor tokens revoke <id>
//	omixor audit [--limit 100]                     操作紀錄(管理員)
//	omixor issues export [--org default] [-o 檔案]  匯出整份 Issue(issues.json 格式;沒給 -o 印到畫面)
//	omixor issues import [--org default] <檔案>     第一次搬資料用(保留 id、作者與時間);伺服器上已經有 Issue 就會拒絕。
//	                                               之後一律在 app、網頁或 MCP 單筆新增或修改,不做差異比對與同步
//
// 登入資料存在 ~/.config/omixor/credentials.json(權限 600)。登入時的伺服器網址:--server > OMIXOR_SERVER > 上次登入的 > 正式站;
// 之後的指令一律送回發 token 的那台(OMIXOR_SERVER 指到別台時拒絕,不把 token 送出去)。
// 登入流程:瀏覽器開 evoleap 的核准頁(要 evoleap 管理員帳號)→ 拿到 evoleap 的 CLI token → 交給 omixor 換 omixor 的 token
// → 立刻撤銷 evoleap 的 CLI token(只用來證明一次身分,不留著)。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opsweb/internal/core"
)

const defaultServer = "https://omixor-issues-340121279179.asia-east1.run.app"

type credentials struct {
	Server    string     `json:"server"`
	Token     string     `json:"token"`
	TokenID   string     `json:"tokenId"`
	Email     string     `json:"email"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

func credPath() string {
	if p := os.Getenv("OMIXOR_CREDENTIALS"); p != "" {
		return p
	}
	dir, _ := os.UserConfigDir()
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "omixor", "credentials.json")
}

func loadCred() credentials {
	var c credentials
	if b, err := os.ReadFile(credPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func saveCred(c credentials) error {
	p := credPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

func server(flagVal string) string {
	for _, s := range []string{flagVal, os.Getenv("OMIXOR_SERVER"), loadCred().Server, defaultServer} {
		if s != "" {
			return strings.TrimRight(s, "/")
		}
	}
	return defaultServer
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// call 呼叫 API;回傳 result(或整個回應)
func call(srv, token, method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("連不到 %s:%v", srv, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out.Result != nil {
		return out.Result, nil
	}
	return raw, nil
}

// credServer 存著的 token 只送回發它的伺服器;OMIXOR_SERVER 指到別台時拒絕,避免把 token 送給別人
func credServer(c credentials) string {
	srv := strings.TrimRight(c.Server, "/")
	if srv == "" {
		srv = defaultServer
	}
	if env := strings.TrimRight(os.Getenv("OMIXOR_SERVER"), "/"); env != "" && env != srv {
		die("登入資料是 %s 發的,但 OMIXOR_SERVER 指到 %s;請先 omixor login --server %s", srv, env, env)
	}
	return srv
}

func authed(method, path string, body any) json.RawMessage {
	c := loadCred()
	if c.Token == "" {
		die("還沒登入:先執行 omixor login")
	}
	r, err := call(credServer(c), c.Token, method, path, body)
	if err != nil {
		die("%v", err)
	}
	return r
}

func printJSON(raw json.RawMessage) {
	var v any
	_ = json.Unmarshal(raw, &v)
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func readIssues(file string) []core.OpsIssue {
	b, err := os.ReadFile(file)
	if err != nil {
		die("讀不到 %s:%v", file, err)
	}
	var list []core.OpsIssue
	if err := json.Unmarshal(b, &list); err != nil {
		die("%s 不是 issues.json 格式(要是 Issue 陣列):%v", file, err)
	}
	return list
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "login":
		login(args)
	case "logout":
		c := loadCred()
		if c.Token == "" {
			fmt.Println("本來就沒有登入")
			return
		}
		if _, err := call(credServer(c), c.Token, "POST", "/api/auth/logout", map[string]any{}); err != nil {
			fmt.Fprintln(os.Stderr, "伺服器上撤銷失敗(本機的登入照樣刪掉):", err)
		}
		c.Token, c.TokenID, c.ExpiresAt = "", "", nil
		_ = saveCred(c)
		fmt.Println("已登出")
	case "whoami":
		printJSON(authed("GET", "/api/auth/me", nil))
	case "users":
		users(args)
	case "tokens":
		if len(args) >= 2 && args[0] == "revoke" {
			authed("POST", "/api/admin/tokens/"+url.PathEscape(args[1])+"/revoke", map[string]any{})
			fmt.Println("已撤銷", args[1])
			return
		}
		printJSON(authed("GET", "/api/admin/tokens", nil))
	case "audit":
		fs := flag.NewFlagSet("audit", flag.ExitOnError)
		limit := fs.Int("limit", 100, "筆數")
		_ = fs.Parse(args)
		printJSON(authed("GET", fmt.Sprintf("/api/admin/audit?limit=%d", *limit), nil))
	case "issues":
		issues(args)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:omixor login|logout|whoami|users|tokens|audit|issues …(說明見 ops-web/cmd/omixor/main.go)`)
	os.Exit(2)
}

func login(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	srvFlag := fs.String("server", "", "omixor-issues 網址")
	device := fs.String("device", "", "這台的名稱(顯示在 omixor tokens)")
	evoleap := fs.String("evoleap", "https://evoleap.io", "evoleap 網址")
	console := fs.String("evoleap-console", "", "evoleap 核准頁網址(本機開發時用)")
	_ = fs.Parse(args)
	srv := server(*srvFlag)
	name := *device
	if name == "" {
		h, _ := os.Hostname()
		name = "omixor CLI(" + strings.TrimSuffix(h, ".local") + ")"
	}
	fmt.Println("瀏覽器會打開 evoleap 的核准頁,用 evoleap 的帳號核准後回到這裡…")
	evoTok, err := core.EvoleapLogin(*evoleap, *console)
	if err != nil {
		die("evoleap 登入失敗:%v", err)
	}
	raw, err := call(srv, "", "POST", "/api/auth/evoleap", map[string]any{"token": evoTok, "device": name})
	// evoleap 的 token 只用來證明一次身分:不管換成功或失敗都立刻撤銷(die 會直接結束程式,不能用 defer)
	core.EvoleapLogout(*evoleap, evoTok)
	if err != nil {
		die("omixor 拒絕登入:%v", err)
	}
	var out struct {
		Token     string     `json:"token"`
		TokenID   string     `json:"tokenId"`
		ExpiresAt *time.Time `json:"expiresAt"`
		User      struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		die("omixor 回應不對")
	}
	if err := saveCred(credentials{Server: srv, Token: out.Token, TokenID: out.TokenID, Email: out.User.Email, ExpiresAt: out.ExpiresAt}); err != nil {
		die("存不了登入資料:%v", err)
	}
	exp := "不到期"
	if out.ExpiresAt != nil {
		exp = out.ExpiresAt.Local().Format("2006-01-02") + " 到期"
	}
	fmt.Printf("已登入 %s(%s,%s;%s)\n", srv, out.User.Email, out.User.Role, exp)
}

func users(args []string) {
	if len(args) == 0 || args[0] == "list" {
		printJSON(authed("GET", "/api/admin/users", nil))
		return
	}
	sub := args[0]
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	role := fs.String("role", "", "admin 或 member")
	_ = fs.Parse(reorder(args[1:]))
	rest := fs.Args()
	if len(rest) < 1 {
		die("要給 email")
	}
	body := map[string]any{"email": rest[0]}
	switch sub {
	case "add":
		if *role != "" {
			body["role"] = *role
		}
	case "role":
		if len(rest) < 2 {
			die("用法:omixor users role <email> <admin|member>")
		}
		body["role"] = rest[1]
	case "disable":
		body["disabled"] = true
	case "enable":
		body["disabled"] = false
	default:
		usage()
	}
	printJSON(authed("POST", "/api/admin/users", body))
}

// reorder 讓旗標可以放在位置參數後面(flag 套件遇到第一個非旗標就停)
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && needsValue(a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func needsValue(f string) bool {
	switch strings.TrimLeft(f, "-") {
	case "role", "org", "o", "limit", "server", "device":
		return true
	}
	return false
}

func issues(args []string) {
	if len(args) == 0 {
		usage()
	}
	sub := args[0]
	fs := flag.NewFlagSet("issues", flag.ExitOnError)
	org := fs.String("org", "default", "組織 id")
	outFile := fs.String("o", "", "輸出檔案")
	_ = fs.Parse(reorder(args[1:]))
	switch sub {
	case "export":
		raw := authed("GET", "/api/admin/issues/"+url.PathEscape(*org)+"/export", nil)
		var v any
		_ = json.Unmarshal(raw, &v)
		b, _ := json.MarshalIndent(v, "", "  ")
		if *outFile == "" {
			fmt.Println(string(b))
			return
		}
		if err := os.WriteFile(*outFile, b, 0o600); err != nil {
			die("寫不了 %s:%v", *outFile, err)
		}
		fmt.Printf("已匯出 %s 的 Issue 到 %s\n", *org, *outFile)
	case "import":
		if fs.NArg() < 1 {
			die("要給 issues.json")
		}
		raw := authed("POST", "/api/admin/issues/"+url.PathEscape(*org)+"/import", map[string]any{"issues": readIssues(fs.Arg(0))})
		var r core.IssueImportResult
		_ = json.Unmarshal(raw, &r)
		fmt.Printf("已匯入 %s:%d 筆\n", *org, r.Inserted)
	default:
		usage()
	}
}
