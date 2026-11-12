package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Issue 伺服器(同 Swift 的 Issues/IssueRemote.swift):設了 OPS_ISSUE_SERVER 時,Issue 不存本機也不存這台的資料庫,
// 改透過 omixor-issues 服務(或另一台 ops-web)的 /api/issues/<組織> 讀寫,它是唯一的資料來源。
//
//	OPS_ISSUE_SERVER        例如 https://omixor-issues-….run.app
//	OPS_ISSUE_SERVER_TOKEN  固定的 Bearer token(另一台 ops-web 的 OPS_API_TOKEN);沒設時用 omixor 自己的 token(omx_…,
//	                        存在鑰匙圈的 omixor-issues):沒有或被拒絕(401)時,拿 evoleap 連線的 token 去
//	                        /api/auth/evoleap 換一個再重送(evoleap 只證明身分,能不能用由 omixor 的使用者名單決定)
//
// 讀:組織載入時、每次修改前、ops_issues 查詢前重讀(最多每 2 秒一次)。寫:每個動作各自送出,用伺服器回傳的內容。
// 這些請求在拿著 Mu 時發出(Issue 的介面是同步的),逾時 8 秒。

const (
	issueRemoteEvoleapConn = "evoleap"
	issueRemoteTokenID     = "omixor-issues"
)

var issueRemoteClient = &http.Client{Timeout: 8 * time.Second}

// IssueRemoteURL 設定的 Issue 伺服器(沒設回傳 "")
func IssueRemoteURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("OPS_ISSUE_SERVER")), "/")
}

func issueRemoteOn() bool { return IssueRemoteURL() != "" }

// IssueRemoteError 最近一次 Issue 伺服器的錯誤(成功後清掉;拿著 Mu 呼叫)
func (a *App) IssueRemoteError() string { return a.issueSt().remoteErr }

// issueRemoteToken 固定的 token,或存著的 omixor token(沒有就回 "")
func issueRemoteToken(scope string) string {
	if t := os.Getenv("OPS_ISSUE_SERVER_TOKEN"); t != "" {
		return t
	}
	if t := vaultTokens(scope, issueRemoteTokenID); t != nil {
		return t.AccessToken
	}
	return ""
}

var issueRemoteExchangeMu sync.Mutex

// issueRemoteExchange 用 evoleap 連線的登入,向 omixor-issues 換一個 omixor 自己的 token(存進鑰匙圈)。
// 同時只換一次:別的請求剛換好(鑰匙圈裡已經有、而且不是 stale 那一把)就直接用,不重複換
func issueRemoteExchange(scope, stale string) (string, error) {
	issueRemoteExchangeMu.Lock()
	defer issueRemoteExchangeMu.Unlock()
	if t := vaultTokens(scope, issueRemoteTokenID); t != nil && t.AccessToken != "" && t.AccessToken != stale {
		return t.AccessToken, nil
	}
	evo := vaultTokens(scope, issueRemoteEvoleapConn)
	if evo == nil || evo.AccessToken == "" {
		return "", errors.New("還沒登入 evoleap 連線:請在 設定 › 連線 登入「evoleap 正式站」,omixor 會用它證明身分")
	}
	b, _ := json.Marshal(map[string]any{"token": evo.AccessToken, "device": clientLabel() + " 網頁版"})
	req, err := http.NewRequest("POST", IssueRemoteURL()+"/api/auth/evoleap", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := issueRemoteClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("連不到 Issue 伺服器:%v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Token     string `json:"token"`
		ExpiresAt *Time  `json:"expiresAt"`
		Error     string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if out.Error != "" {
		return "", errors.New("omixor 拒絕登入:" + out.Error)
	}
	if resp.StatusCode != 200 || out.Token == "" {
		return "", fmt.Errorf("omixor 登入失敗(HTTP %d)", resp.StatusCode)
	}
	vaultSetTokens(scope, issueRemoteTokenID, &TokenSet{AccessToken: out.Token, ExpiresAt: out.ExpiresAt, TokenType: strPtr("Bearer")})
	return out.Token, nil
}

// issueRemoteCall 拿著 Mu 呼叫
func (a *App) issueRemoteCall(method, org string, path []string, body any) (json.RawMessage, error) {
	return issueRemoteCallScoped(a.conns().Scope, method, org, path, body)
}

// issueRemoteCallScoped 不用拿鎖(scope 是鑰匙圈的組織前綴)
func issueRemoteCallScoped(scope, method, org string, path []string, body any) (json.RawMessage, error) {
	u := IssueRemoteURL() + "/api/issues/" + url.PathEscape(org)
	for _, p := range path {
		u += "/" + url.PathEscape(p)
	}
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	send := func(token string) (*http.Response, error) {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, u, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := issueRemoteClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("連不到 Issue 伺服器:%v", err)
		}
		return resp, nil
	}
	fixed := os.Getenv("OPS_ISSUE_SERVER_TOKEN") != ""
	token := issueRemoteToken(scope)
	if token == "" && !fixed {
		t, err := issueRemoteExchange(scope, "")
		if err != nil {
			return nil, err
		}
		token = t
	}
	resp, err := send(token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 && !fixed {
		// omixor token 過期或被撤銷:換一個再送一次
		resp.Body.Close()
		t, err := issueRemoteExchange(scope, token)
		if err != nil {
			return nil, err
		}
		if resp, err = send(t); err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out) != nil {
		return nil, fmt.Errorf("Issue 伺服器回應不對(HTTP %d)", resp.StatusCode)
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	if resp.StatusCode/100 != 2 || out.Result == nil {
		return nil, fmt.Errorf("Issue 伺服器回應不對(HTTP %d)", resp.StatusCode)
	}
	return out.Result, nil
}

func (a *App) issueRemoteFailed(err error) {
	s := a.issueSt()
	if s.remoteErr != err.Error() {
		log.Printf("Issue 伺服器:%v", err)
	}
	s.remoteErr = err.Error()
}

// issueRemoteLoad 從伺服器讀 s.Org 的全部 Issue;失敗時保留原本的清單
func (a *App) issueRemoteLoad(s *issueState) {
	raw, err := a.issueRemoteCall("GET", s.Org, nil, nil)
	var list []OpsIssue
	if err == nil {
		err = json.Unmarshal(raw, &list)
	}
	if err != nil {
		a.issueRemoteFailed(err)
		if s.Issues == nil {
			s.Issues = []OpsIssue{}
		}
		return
	}
	if list == nil {
		list = []OpsIssue{}
	}
	s.Issues, s.remoteErr, s.fetched = list, "", time.Now()
	s.gen++
}

// RefreshIssuesIfStale Issue 伺服器模式:超過 maxAge 沒讀就重讀(拿著 Mu 呼叫;不是伺服器模式不做事)
func (a *App) RefreshIssuesIfStale(maxAge time.Duration) {
	if !issueRemoteOn() {
		return
	}
	s := a.issueSt()
	if time.Since(s.fetched) >= maxAge {
		a.issueRemoteLoad(s)
	}
}

func (a *App) issueRemoteCreate(title, body, priority string, labels []string, source, link, author string) (OpsIssue, error) {
	raw, err := a.issueRemoteCall("POST", a.issueSt().Org, nil, map[string]any{
		"title": title, "body": body, "priority": priority, "labels": labels, "source": source, "link": link, "author": author,
	})
	var i OpsIssue
	if err == nil {
		err = json.Unmarshal(raw, &i)
	}
	return i, err
}

func (a *App) issueRemoteUpdate(n OpsIssue, author string) (OpsIssue, error) {
	raw, err := a.issueRemoteCall("POST", a.issueSt().Org, []string{n.ID}, map[string]any{
		"author": author, "title": n.Title, "body": n.Body, "status": n.Status, "priority": n.Priority,
		"labels": n.Labels, "source": n.Source, "link": n.Link, "comments": n.Comments,
	})
	var i OpsIssue
	if err == nil {
		err = json.Unmarshal(raw, &i)
	}
	return i, err
}

// issueRemoteSync 在鎖外讀 org 的 Issue,讀到就換上並通知畫面(組織已經換掉就丟掉)
func (a *App) issueRemoteSync(org, scope string, gen int) {
	raw, err := issueRemoteCallScoped(scope, "GET", org, nil, nil)
	var list []OpsIssue
	if err == nil {
		err = json.Unmarshal(raw, &list)
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.issueSt()
	if s.Org != org || s.gen != gen {
		return
	}
	if err != nil {
		a.issueRemoteFailed(err)
		return
	}
	if list == nil {
		list = []OpsIssue{}
	}
	changed := string(Marshal(s.Issues)) != string(Marshal(list))
	s.Issues, s.fetched, s.remoteErr = list, time.Now(), ""
	s.gen++
	if changed {
		a.changedIssues()
	}
}

// 伺服器模式:每 5 秒在背景重讀一次(網路請求在鎖外),有變就通知畫面,別處(Swift app、其他 ops-web)的修改才看得到
func init() {
	OnStart(func(a *App) {
		if !issueRemoteOn() || os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(5 * time.Second) {
				a.Mu.Lock()
				st := a.issueSt()
				org, scope, gen := st.Org, a.conns().Scope, st.gen
				a.Mu.Unlock()
				a.issueRemoteSync(org, scope, gen)
			}
		}()
	})
}
