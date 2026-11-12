// Package client 連線上的 omixor server(ops-web 遠端模式、omixor CLI 用)。操作和 service 對應。
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// Error 伺服器回的錯誤(Status 是 HTTP 狀態碼;0 是連不上)
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// Status 錯誤的 HTTP 狀態碼(連不上或不是伺服器的錯誤是 0)
func Status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

type Client struct {
	Base string
	// Token 每次請求時取得目前的 omixor token
	Token func() string
	// Refresh 收到 401 時換一個新的 token(例如用 evoleap 的登入重新換);nil 表示不重試
	Refresh func(stale string) (string, error)
	HTTP    *http.Client
}

func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func esc(s string) string { return url.PathEscape(s) }

// Do 送出請求;out 給 nil 不解析。result 包在 {"result": …} 時自動拆開
func (c *Client) Do(method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	send := func(token string) (int, []byte, error) {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, c.Base+path, rd)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		hc := c.HTTP
		if hc == nil {
			hc = http.DefaultClient
		}
		resp, err := hc.Do(req)
		if err != nil {
			return 0, nil, &Error{Msg: fmt.Sprintf("連不到 omixor server(%s):%v", c.Base, err)}
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		return resp.StatusCode, b, err
	}
	token := ""
	if c.Token != nil {
		token = c.Token()
	}
	if token == "" && c.Refresh != nil {
		t, err := c.Refresh("")
		if err != nil {
			return err
		}
		token = t
	}
	status, b, err := send(token)
	if err != nil {
		return err
	}
	if status == 401 && c.Refresh != nil {
		t, err := c.Refresh(token)
		if err != nil {
			return err
		}
		if status, b, err = send(t); err != nil {
			return err
		}
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	_ = json.Unmarshal(b, &env)
	if env.Error != "" {
		return &Error{Status: status, Msg: env.Error}
	}
	if status/100 != 2 {
		return &Error{Status: status, Msg: fmt.Sprintf("omixor server 回應不對(HTTP %d)", status)}
	}
	if out == nil {
		return nil
	}
	if env.Result != nil {
		return json.Unmarshal(env.Result, out)
	}
	return json.Unmarshal(b, out)
}

// ---- 授權 ----

func (c *Client) LoginWithEvoleap(evoToken, device string) (*service.LoginResult, error) {
	var r service.LoginResult
	err := c.Do("POST", "/api/auth/evoleap", map[string]any{"token": evoToken, "device": device}, &r)
	return &r, err
}

type Me struct {
	User  model.User  `json:"user"`
	Token model.Token `json:"token"`
}

func (c *Client) Me() (*Me, error) {
	var m Me
	err := c.Do("GET", "/api/auth/me", nil, &m)
	return &m, err
}

func (c *Client) Logout() error { return c.Do("POST", "/api/auth/logout", map[string]any{}, nil) }

// ---- 組織 ----

func (c *Client) ListOrgs() ([]model.Org, error) {
	var l []model.Org
	return l, c.Do("GET", "/api/orgs", nil, &l)
}

func (c *Client) CreateOrg(id, name string) (*model.Org, error) {
	var o model.Org
	return &o, c.Do("POST", "/api/orgs", map[string]any{"id": id, "name": name}, &o)
}

func (c *Client) ListMembers(org string) ([]model.Member, error) {
	var l []model.Member
	return l, c.Do("GET", "/api/orgs/"+esc(org)+"/members", nil, &l)
}

func (c *Client) SaveMember(org, email, role string) (*model.Member, error) {
	var m model.Member
	return &m, c.Do("POST", "/api/orgs/"+esc(org)+"/members", map[string]any{"email": email, "role": role}, &m)
}

func (c *Client) RemoveMember(org, email string) error {
	return c.Do("POST", "/api/orgs/"+esc(org)+"/members/remove", map[string]any{"email": email}, nil)
}

// ---- Issue ----

func (c *Client) issues(org string) string { return "/api/orgs/" + esc(org) + "/issues" }

func (c *Client) ListIssues(org string) ([]model.Issue, error) {
	var l []model.Issue
	return l, c.Do("GET", c.issues(org), nil, &l)
}

func (c *Client) GetIssue(org, id string) (*model.Issue, error) {
	var i model.Issue
	return &i, c.Do("GET", c.issues(org)+"/"+esc(id), nil, &i)
}

func (c *Client) CreateIssue(org string, in service.IssueInput) (*model.Issue, error) {
	var i model.Issue
	return &i, c.Do("POST", c.issues(org), in, &i)
}

func (c *Client) UpdateIssue(org, id string, in service.IssuePatch) (*model.Issue, error) {
	var i model.Issue
	return &i, c.Do("POST", c.issues(org)+"/"+esc(id), in, &i)
}

func (c *Client) DeleteIssue(org, id string) error {
	return c.Do("POST", c.issues(org)+"/"+esc(id)+"/delete", map[string]any{}, nil)
}

func (c *Client) ExportIssues(org string) ([]model.Issue, error) {
	var l []model.Issue
	return l, c.Do("GET", c.issues(org)+"/export", nil, &l)
}

func (c *Client) ImportIssues(org string, list []model.Issue) (*model.ImportResult, error) {
	var r model.ImportResult
	return &r, c.Do("POST", c.issues(org)+"/import", map[string]any{"issues": list}, &r)
}

// ---- 伺服器管理 ----

func (c *Client) ListUsers() ([]model.User, error) {
	var l []model.User
	return l, c.Do("GET", "/api/admin/users", nil, &l)
}

func (c *Client) SaveUser(email string, role *string, disabled *bool) (*model.User, error) {
	body := map[string]any{"email": email}
	if role != nil {
		body["role"] = *role
	}
	if disabled != nil {
		body["disabled"] = *disabled
	}
	var u model.User
	return &u, c.Do("POST", "/api/admin/users", body, &u)
}

func (c *Client) ListTokens() ([]model.Token, error) {
	var l []model.Token
	return l, c.Do("GET", "/api/admin/tokens", nil, &l)
}

func (c *Client) RevokeToken(id string) error {
	return c.Do("POST", "/api/admin/tokens/"+esc(id)+"/revoke", map[string]any{}, nil)
}

func (c *Client) ListAudit(limit int) ([]model.AuditEntry, error) {
	var l []model.AuditEntry
	return l, c.Do("GET", fmt.Sprintf("/api/admin/audit?limit=%d", limit), nil, &l)
}
