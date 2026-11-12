package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// 連線用的 HTTP,同 Swift 的 ConnHTTP。
// 測試時把 ConnTransport 換成假的(例如 httptest 伺服器);跑 go test 時預設的 transport 一律拒絕連外。

// ConnTransport 所有連線請求走這個 RoundTripper;nil 表示用 http.DefaultTransport
var ConnTransport http.RoundTripper

func init() {
	if testing.Testing() {
		ConnTransport = refuseTransport{}
	}
}

type refuseTransport struct{}

func (refuseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("測試中不能連外:%s", r.URL)
}

// connClient 連線用的 client;連線有指定 CA 憑證檔或不驗證憑證時換掉 TLS 設定(conn_tls.go)
func connClient(c *Connection) (*http.Client, error) {
	t, err := connTransport(c)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: t, Timeout: 60 * time.Second, CheckRedirect: redirectGuard}, nil
}

// redirectGuard 轉址到別的網站時拿掉 Authorization,token 不會送到第三方(例如 GitHub 紀錄轉到暫時的下載網址)
func redirectGuard(req *http.Request, via []*http.Request) error {
	if len(via) >= 16 {
		return errors.New("轉址太多次")
	}
	if req.URL.Hostname() != via[0].URL.Hostname() {
		req.Header.Del("Authorization")
	}
	return nil
}

// ConnResponse 回應
type ConnResponse struct {
	Status int
	Data   []byte
	Header http.Header
}

func (r *ConnResponse) OK() bool { return r.Status >= 200 && r.Status < 300 }

// JSON 解析後的內容;不是 JSON 時回傳 nil
func (r *ConnResponse) JSON() any {
	var v any
	if err := json.Unmarshal(r.Data, &v); err != nil {
		return nil
	}
	return v
}

// connSend c 可以是 nil(不屬於任何連線的請求,用預設的 TLS 設定)
func connSend(c *Connection, method, rawURL string, headers map[string]string, body []byte) (*ConnResponse, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client, err := connClient(c)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, explainTLSError(c, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	return &ConnResponse{Status: resp.StatusCode, Data: data, Header: resp.Header}, nil
}

func connJSON(c *Connection, method, rawURL string, obj any, headers map[string]string) (*ConnResponse, error) {
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	return connSend(c, method, rawURL, h, Marshal(obj))
}

func connForm(c *Connection, rawURL string, fields map[string]string) (*ConnResponse, error) {
	return connSend(c, "POST", rawURL, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []byte(FormEncode(fields)))
}

// FormEncode 同 Swift 的 ConnHTTP.formEncode:key 排序,英數字與 -._~ 以外都編碼
func FormEncode(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+percentEncode(fields[k], "-._~"))
	}
	return strings.Join(parts, "&")
}

// percentEncode 英數字與 extra 以外的位元組都編成 %XX
func percentEncode(s, extra string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(extra, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// EscapePath 路徑的每一段各自編碼,保留斜線(同 Swift 的 ConnHTTP.escapePath,用 urlPathAllowed)
func EscapePath(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = percentEncode(s, "!$&'()*+,-.:;=@_~")
	}
	return strings.Join(segs, "/")
}

// connErrorMessage 伺服器錯誤訊息:常見的 {error:{message}}、{error_description}、{message}、{error}
func connErrorMessage(r *ConnResponse) string {
	o, _ := r.JSON().(map[string]any)
	if e, ok := o["error"].(map[string]any); ok {
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	if d, ok := o["error_description"].(string); ok {
		return d
	}
	if m, ok := o["message"].(string); ok {
		return m
	}
	if e, ok := o["error"].(string); ok {
		return e
	}
	data := r.Data
	if len(data) > 300 {
		data = data[:300]
	}
	text := strings.ToValidUTF8(string(data), "�")
	if text == "" {
		return strings.ToLower(http.StatusText(r.Status))
	}
	return text
}

// connCheck 不是 2xx 時轉成 ConnError
func connCheck(r *ConnResponse, err error) (*ConnResponse, error) {
	if err != nil {
		return nil, err
	}
	if !r.OK() {
		msg := connErrorMessage(r)
		if r.Status == 401 {
			msg = "登入已失效,請重新登入"
		}
		return nil, ConnError{Status: r.Status, Message: msg}
	}
	return r, nil
}

// accountLabel 從 JSON 找出可以顯示的帳號名稱(email、name、login、sub),也看 user 底下
func accountLabel(obj any) (string, bool) {
	d, ok := obj.(map[string]any)
	if !ok {
		return "", false
	}
	for _, k := range []string{"email", "preferred_username", "login", "name", "username", "sub"} {
		if s, ok := d[k].(string); ok && s != "" {
			return s, true
		}
	}
	for _, k := range []string{"user", "data", "account", "me"} {
		if inner, ok := d[k]; ok {
			if s, ok := accountLabel(inner); ok {
				return s, true
			}
		}
	}
	return "", false
}

// jwtClaims 解開 JWT 的 payload(不驗證簽章,只拿來顯示帳號)
func jwtClaims(jwt string) map[string]any {
	parts := []string{}
	for _, p := range strings.Split(jwt, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		return nil
	}
	b64 := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	for len(b64)%4 != 0 {
		b64 += "="
	}
	d, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(d, &m) != nil {
		return nil
	}
	return m
}

func base64url(d []byte) string { return base64.RawURLEncoding.EncodeToString(d) }
