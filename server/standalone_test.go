package server

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// 預設資料夾:<使用者設定資料夾>/omixor/core(測試換掉 userConfigDir,不碰真的使用者資料夾)
func TestStandaloneDefaults(t *testing.T) {
	base := t.TempDir()
	orig := userConfigDir
	userConfigDir = func() (string, error) { return base, nil }
	defer func() { userConfigDir = orig }()

	o, err := parseLocalArgs(nil, envMap(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Standalone || o.Cfg.DataDir != filepath.Join(base, "omixor", "core") || o.Addr != StandaloneAddr || o.ExitOnEOF {
		t.Fatalf("沒給 token 要是獨立模式、預設資料夾與 8770:%+v", o)
	}
	// 旗標 > 環境變數
	o, err = parseLocalArgs([]string{"-data", "/x/flag", "-addr", "127.0.0.1:9"},
		envMap(map[string]string{"OMIXOR_DATA_DIR": "/x/env", "OMIXOR_ADDR": "127.0.0.1:8"}), io.Discard)
	if err != nil || o.Cfg.DataDir != "/x/flag" || o.Addr != "127.0.0.1:9" {
		t.Fatal(o, err)
	}
	o, err = parseLocalArgs(nil, envMap(map[string]string{"OMIXOR_DATA_DIR": "/x/env", "OMIXOR_ADDR": "127.0.0.1:8"}), io.Discard)
	if err != nil || o.Cfg.DataDir != "/x/env" || o.Addr != "127.0.0.1:8" {
		t.Fatal(o, err)
	}
	// 只接受本機位址
	if _, err := parseLocalArgs([]string{"-addr", "0.0.0.0:8770"}, envMap(nil), io.Discard); err == nil {
		t.Fatal("0.0.0.0 要拒絕")
	}
	// -standalone 時忽略 OMIXOR_LOCAL_TOKEN
	o, err = parseLocalArgs([]string{"-standalone"}, envMap(map[string]string{"OMIXOR_LOCAL_TOKEN": testTok, "OMIXOR_DATA_DIR": "/d"}), io.Discard)
	if err != nil || !o.Standalone || o.Cfg.Token != "" || o.Addr != StandaloneAddr {
		t.Fatal(o, err)
	}
	if _, err := parseLocalArgs([]string{"-h"}, envMap(nil), io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatal("-h", err)
	}
}

// App 模式(有 OMIXOR_LOCAL_TOKEN):行為和原本一樣
func TestAppModeArgs(t *testing.T) {
	env := map[string]string{"OMIXOR_LOCAL_TOKEN": testTok, "OMIXOR_DATA_DIR": "/app/core", "OMIXOR_EXIT_ON_STDIN_EOF": "1"}
	o, err := parseLocalArgs(nil, envMap(env), io.Discard)
	if err != nil || o.Standalone || o.Cfg.Token != testTok || o.Cfg.DataDir != "/app/core" || o.Addr != "127.0.0.1:0" || !o.ExitOnEOF {
		t.Fatal(o, err)
	}
	delete(env, "OMIXOR_DATA_DIR")
	if _, err := parseLocalArgs(nil, envMap(env), io.Discard); err == nil {
		t.Fatal("App 模式沒給資料夾要失敗")
	}
	env["OMIXOR_DATA_DIR"] = "/app/core"
	if _, err := parseLocalArgs([]string{"-rotate-token"}, envMap(env), io.Discard); err == nil {
		t.Fatal("App 模式不能 -rotate-token")
	}
}

// token 第一次產生、之後沿用;rotate 換新的、舊的作廢;App 模式與獨立模式的 token 互不作廢
func TestStandaloneToken(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenStandalone(LocalConfig{DataDir: dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	c1 := ReadStandaloneCred(dir)
	if !validLocalToken(c1.Token) || c1.TokenID == "" || c1.Email != "local" || c1.Token != l.Cfg.Token {
		t.Fatalf("第一次要產生 token 並存起來:%+v", c1)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(StandaloneCredPath(dir)); st.Mode().Perm() != 0o600 {
			t.Fatal("credentials.json 要 0600", st.Mode())
		}
	}
	if code, _ := localReq(t, l.Handler(), "GET", "/api/auth/me", c1.Token, nil); code != 200 {
		t.Fatal(code)
	}
	if err := l.SaveStandaloneCred("http://127.0.0.1:8770"); err != nil {
		t.Fatal(err)
	}

	// 重開:同一把 token,紀錄不重建
	l2, err := OpenStandalone(LocalConfig{DataDir: dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	c2 := ReadStandaloneCred(dir)
	if c2.Token != c1.Token || c2.TokenID != c1.TokenID || c2.Server != "http://127.0.0.1:8770" {
		t.Fatalf("重開要沿用:%+v %+v", c1, c2)
	}
	if code, _ := localReq(t, l2.Handler(), "GET", "/api/orgs", c1.Token, nil); code != 200 {
		t.Fatal(code)
	}

	// 同一個資料夾用 App 模式開(另一個裝置名稱):獨立模式的 token 不受影響
	appTok := testTok + "app"
	if _, err := OpenLocal(LocalConfig{DataDir: dir, Token: appTok}); err != nil {
		t.Fatal(err)
	}
	l3, err := OpenStandalone(LocalConfig{DataDir: dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	h3 := l3.Handler()
	for _, tok := range []string{c1.Token, appTok} {
		if code, _ := localReq(t, h3, "GET", "/api/orgs", tok, nil); code != 200 {
			t.Fatal("App 與獨立模式的 token 要互不作廢", tok == appTok, code)
		}
	}

	// rotate:換新的,舊的 401;App 的照樣能用
	l4, err := OpenStandalone(LocalConfig{DataDir: dir}, true)
	if err != nil {
		t.Fatal(err)
	}
	c4 := ReadStandaloneCred(dir)
	if c4.Token == c1.Token || !validLocalToken(c4.Token) || c4.TokenID == c1.TokenID {
		t.Fatalf("rotate 要換新的:%+v", c4)
	}
	h4 := l4.Handler()
	if code, _ := localReq(t, h4, "GET", "/api/orgs", c1.Token, nil); code != 401 {
		t.Fatal("舊的要作廢", code)
	}
	for _, tok := range []string{c4.Token, appTok} {
		if code, _ := localReq(t, h4, "GET", "/api/orgs", tok, nil); code != 200 {
			t.Fatal(code)
		}
	}
	// App 模式重開:作廢的只有 App 自己的舊 token
	if _, err := OpenLocal(LocalConfig{DataDir: dir, Token: appTok + "2"}); err != nil {
		t.Fatal(err)
	}
	l5, _ := OpenStandalone(LocalConfig{DataDir: dir}, false)
	if code, _ := localReq(t, l5.Handler(), "GET", "/api/orgs", appTok, nil); code != 401 {
		t.Fatal("App 舊的 token 要作廢", code)
	}
	if code, _ := localReq(t, l5.Handler(), "GET", "/api/orgs", c4.Token, nil); code != 200 {
		t.Fatal("獨立模式的不受 App 重開影響", code)
	}
}

// 實際啟動(隨機埠、暫存資料夾):印 READY、寫 credentials.json 的網址、同資料夾第二個被擋、stop 後正常結束
func TestRunLocalStandalone(t *testing.T) {
	t.Setenv("OMIXOR_PUBLIC_URL", "")
	dir := t.TempDir()
	env := envMap(map[string]string{"OMIXOR_DATA_DIR": dir, "OMIXOR_ADDR": "127.0.0.1:0"})
	pr, pw := io.Pipe()
	stop := make(chan struct{})
	res := make(chan error, 1)
	go func() { res <- runLocal(nil, env, pw, io.Discard, stop); pw.Close() }()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err, <-res)
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	url := strings.TrimSpace(strings.TrimPrefix(line, "OMIXOR_LOCAL_READY "))
	c := ReadStandaloneCred(dir)
	if c.Server != url || !validLocalToken(c.Token) {
		t.Fatalf("%q %+v", url, c)
	}
	req, _ := http.NewRequest("GET", url+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	r, err := http.DefaultClient.Do(req)
	if err != nil || r.StatusCode != 200 {
		t.Fatal(err, r)
	}
	r.Body.Close()
	// 同一個資料夾的第二個要被鎖擋下
	if err := runLocal(nil, env, io.Discard, io.Discard, nil); err == nil || !strings.Contains(err.Error(), "正在使用") {
		t.Fatal("第二個要被擋", err)
	}
	close(stop)
	select {
	case err := <-res:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("沒有結束")
	}
}
