package client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"omixor-core/service"
)

// GitLab 登入(伺服器 /api/version 的 features 含 gitlab_login 才有;契約見 core/api/gitlab.go)

// StartGitLabLogin 開始登入,回傳要用瀏覽器開的網址
func (c *Client) StartGitLabLogin(redirect, device, codeChallenge, state string) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	err := c.Do("POST", "/api/auth/gitlab/start", map[string]any{"redirect": redirect, "device": device, "codeChallenge": codeChallenge, "state": state}, &r)
	return r.URL, err
}

// LoginWithGitLab 用導回 loopback 的一次性碼換 omixor token
func (c *Client) LoginWithGitLab(code, codeVerifier string) (*service.LoginResult, error) {
	var r service.LoginResult
	err := c.Do("POST", "/api/auth/gitlab", map[string]any{"code": code, "codeVerifier": codeVerifier}, &r)
	return &r, err
}

func randomURLSafe() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GitLabBrowserLogin 整個客戶端流程:在本機開一個暫時的回呼伺服器、請 omixor 開始登入、用 open 開瀏覽器、
// 等導回(最多 timeout)、用一次性碼加 PKCE verifier 換 token
func (c *Client) GitLabBrowserLogin(device string, open func(string), timeout time.Duration) (*service.LoginResult, error) {
	verifier, err := randomURLSafe()
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	type answer struct{ code, err string }
	got := make(chan answer, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a := answer{code: q.Get("code"), err: q.Get("error_description")}
		if a.err == "" {
			a.err = q.Get("error")
		}
		if r.URL.Path != "/callback" || q.Get("state") != state || (a.code == "" && a.err == "") {
			http.Error(w, "登入要求不符", 400)
			return
		}
		msg := "已登入,可以關掉這個分頁回到終端機。"
		if a.code == "" {
			msg = "登入沒有完成,請回到終端機查看原因。"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>omixor</title><p style="font:16px sans-serif;margin:40px">%s</p>`, msg)
		select {
		case got <- a:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	loginURL, err := c.StartGitLabLogin("http://"+ln.Addr().String()+"/callback", device, base64.RawURLEncoding.EncodeToString(sum[:]), state)
	if err != nil {
		return nil, err
	}
	// 只開網頁網址:伺服器回的其他 scheme 不交給作業系統開
	if u, err := url.Parse(loginURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("伺服器回傳的登入網址不對")
	}
	open(loginURL)
	select {
	case a := <-got:
		time.Sleep(100 * time.Millisecond) // 讓回應頁面送完
		if a.code == "" {
			return nil, errors.New(a.err)
		}
		return c.LoginWithGitLab(a.code, verifier)
	case <-time.After(timeout):
		return nil, errors.New("等待 GitLab 登入逾時")
	}
}
