package cli

// login.go 登入與登出,以及每台伺服器各自的登入資料檔

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/client"
	"omixor-core/config"
	"omixor-core/evoleap"
	"omixor-core/model"
	"omixor-core/service"
)

type credentials struct {
	Server    string      `json:"server"`
	Token     string      `json:"token"`
	TokenID   string      `json:"tokenId"`
	Email     string      `json:"email"`
	ExpiresAt *model.Time `json:"expiresAt,omitempty"`
}

// configDir 設定資料夾(OMIXOR_CONFIG_DIR 可改,測試用)
func configDir() string {
	if d := config.Current().Str(config.ConfigDir); d != "" {
		return d
	}
	dir, _ := os.UserConfigDir()
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "omixor")
}

func legacyCredPath() string { return filepath.Join(configDir(), "credentials.json") }
func currentSrvPath() string { return filepath.Join(configDir(), "current-server") }

// serverKey 登入資料的檔名:網址的 host(:port),只留英數字與 . _ -
func serverKey(srv string) string {
	k := srv
	if u, err := url.Parse(srv); err == nil && u.Host != "" {
		k = u.Host
	}
	var b strings.Builder
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// credPath 這台伺服器的登入資料
func credPath(srv string) string {
	if p := config.Current().Str(config.Credentials); p != "" {
		return p
	}
	return filepath.Join(configDir(), "credentials", serverKey(trim(srv))+".json")
}

func readCred(p string) credentials {
	var c credentials
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// loadCred 這台伺服器的登入資料;新的檔案沒有時,舊版 credentials.json 是這台的就沿用(複製成新的檔案,舊的不刪)
func loadCred(srv string) credentials {
	srv = trim(srv)
	if c := readCred(credPath(srv)); c.Token != "" && (c.Server == "" || trim(c.Server) == srv) {
		return c
	}
	if config.Current().Str(config.Credentials) != "" {
		return credentials{}
	}
	if c := readCred(legacyCredPath()); c.Token != "" && trim(c.Server) == srv {
		_ = saveCred(c)
		return c
	}
	return credentials{}
}

func saveCred(c credentials) error {
	p := credPath(c.Server)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// pickServer 這個指令連哪一台:--server > OMIXOR_SERVER > 上次登入的 > 舊版登入資料的 > 正式站
func pickServer(flagSrv string) string {
	for _, s := range []string{flagSrv, config.Current().Str(config.ServerURL)} {
		if s = trim(s); s != "" {
			return s
		}
	}
	if config.Current().Str(config.Credentials) == "" {
		if b, err := os.ReadFile(currentSrvPath()); err == nil {
			if s := trim(string(b)); s != "" {
				return s
			}
		}
	}
	p := legacyCredPath()
	if e := config.Current().Str(config.Credentials); e != "" {
		p = e
	}
	if c := readCred(p); c.Server != "" {
		return trim(c.Server)
	}
	return DefaultServer
}

func setCurrentServer(srv string) error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(currentSrvPath(), []byte(trim(srv)+"\n"), 0o600)
}

func trim(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

// authed 用這台伺服器存著的 token 連它(token 只送回發它的那台)
func authed(srv string) (*client.Client, error) {
	srv = trim(srv)
	c := loadCred(srv)
	if c.Token == "" {
		return nil, fmt.Errorf("還沒登入 %s:先執行 omixor login --server %s", srv, srv)
	}
	cl := client.New(srv)
	tok := c.Token
	cl.Token = func() string { return tok }
	return cl, nil
}

// runLogin omixor login:預設用 evoleap,--gitlab 用伺服器的自建 GitLab
func runLogin(out io.Writer, srv, device, evo, console string, gitlab bool) error {
	dev := device
	if dev == "" {
		h, _ := os.Hostname()
		dev = "omixor CLI(" + strings.TrimSuffix(h, ".local") + ")"
	}
	var res *service.LoginResult
	var err error
	if gitlab {
		res, err = gitlabLogin(out, srv, dev)
	} else {
		res, err = evoleapLogin(out, srv, dev, evo, console)
	}
	if err != nil {
		return err
	}
	if err := saveCred(credentials{Server: srv, Token: res.Token, TokenID: res.TokenID, Email: res.User.Email, ExpiresAt: res.ExpiresAt}); err != nil {
		return err
	}
	if config.Current().Str(config.Credentials) == "" {
		_ = setCurrentServer(srv) // 之後的指令沒指定 --server 就連這台
	}
	exp := "不到期"
	if res.ExpiresAt != nil {
		exp = res.ExpiresAt.Local().Format("2006-01-02") + " 到期"
	}
	fmt.Fprintf(out, "已登入 %s(%s,%s;%s)\n", srv, res.User.Email, res.User.Role, exp)
	return nil
}

func evoleapLogin(out io.Writer, srv, dev, evo, console string) (*service.LoginResult, error) {
	fmt.Fprintln(out, "瀏覽器會打開 evoleap 的核准頁,核准後回到這裡…")
	// 也把網址印出來:不在這台電腦旁時,可以用別的裝置打開核准
	openBrowser := evoleap.OpenBrowser
	evoleap.OpenBrowser = func(u string) {
		fmt.Fprintln(out, "核准頁:"+u)
		if f, ok := out.(interface{ Sync() error }); ok {
			_ = f.Sync()
		}
		openBrowser(u)
	}
	defer func() { evoleap.OpenBrowser = openBrowser }()
	evoTok, err := evoleap.Login(evo, console, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("evoleap 登入失敗:%v", err)
	}
	res, err := client.New(srv).LoginWithEvoleap(evoTok, dev)
	// evoleap 的 token 只用來證明一次身分:成功或失敗都立刻撤銷
	evoleap.Logout(evo, evoTok)
	if err != nil {
		return nil, fmt.Errorf("omixor 拒絕登入:%v", err)
	}
	return res, nil
}

func runLogout(out io.Writer, srvSel string) error {
	c := loadCred(srvSel)
	if c.Token == "" {
		fmt.Fprintf(out, "本來就沒有登入 %s\n", srvSel)
		return nil
	}
	if cl, err := authed(srvSel); err == nil {
		if err := cl.Logout(); err != nil {
			fmt.Fprintln(out, "伺服器上撤銷失敗(本機的登入照樣刪掉):", err)
		}
	}
	c.Token, c.TokenID, c.ExpiresAt = "", "", nil
	if err := saveCred(c); err != nil {
		return err
	}
	fmt.Fprintln(out, "已登出")
	return nil
}
