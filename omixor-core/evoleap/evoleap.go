// Package evoleap 用 evoleap 的瀏覽器核准登入拿一次性的 CLI token(omixor CLI 證明身分用),同 evoleap CLI 的 evoleap login --web:
// POST /api/cli-auth/start 登記本機回呼網址 → 瀏覽器開核准頁 → 回呼帶 id 與 code → POST /api/cli-auth/exchange 換 token。
package evoleap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// OpenBrowser 測試時可以換掉
var OpenBrowser = func(u string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", u).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		_ = exec.Command("xdg-open", u).Start()
	}
}

func post(u string, body any, token string) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", u, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode/100 != 2 {
		msg, _ := out["message"].(string)
		if msg == "" {
			msg, _ = out["error"].(string)
		}
		return nil, fmt.Errorf("evoleap 回應 HTTP %d %s", resp.StatusCode, msg)
	}
	return out, nil
}

// Login 回傳 evoleap 的 CLI token。base 是 evoleap API 網址;console 是核准頁網址(本機開發前端另一個埠時用,一般給 "")
func Login(base, console string, timeout time.Duration) (string, error) {
	base = strings.TrimRight(base, "/")
	if console == "" {
		console = base
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	redirect := fmt.Sprintf("http://%s/callback", ln.Addr().String())
	host, _ := os.Hostname()
	start, err := post(base+"/api/cli-auth/start", map[string]any{"redirect_uri": redirect, "name": "omixor CLI(" + strings.TrimSuffix(host, ".local") + ")"}, "")
	if err != nil {
		ln.Close()
		return "", err
	}
	id, _ := start["id"].(string)
	if id == "" {
		ln.Close()
		return "", errors.New("evoleap 沒有回傳登入 id")
	}
	got := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/callback" || q.Get("id") != id || q.Get("code") == "" {
			http.Error(w, "登入要求不符", 400)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>omixor</title><p style="font:16px -apple-system,sans-serif;margin:40px">已核准,可以關掉這個分頁回到終端機。</p>`)
		select {
		case got <- q.Get("code"):
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}()
	OpenBrowser(console + "/login/cli?id=" + url.QueryEscape(id))
	var code string
	select {
	case code = <-got:
	case <-time.After(timeout):
		return "", errors.New("等待核准逾時")
	}
	ex, err := post(base+"/api/cli-auth/exchange", map[string]any{"id": id, "code": code}, "")
	if err != nil {
		return "", err
	}
	tok, _ := ex["token"].(string)
	if tok == "" {
		return "", errors.New("換不到 token")
	}
	return tok, nil
}

// Logout 撤銷 evoleap CLI token
func Logout(base, token string) {
	_, _ = post(strings.TrimRight(base, "/")+"/api/cli-auth/logout", map[string]any{}, token)
}
