package oauth

// 觀測 log:每個 OAuth 請求(以及帶 OAuth token 的 /mcp/org/{org} 請求)寫一行 JSON,用來了解 Claude 自訂 connector 實際怎麼連
// (有沒有走 DCR、resource 帶什麼、scope、PKCE 方法、token 請求格式、refresh 的時機)。
//
// 去識別規則全部在 logEvent 一處:
//   - 只記名稱與非機密欄位;配對碼、授權碼、code_verifier、code_challenge、CSRF、state、refresh / access token 一律不進 log
//     (各端點只放「有沒有帶」的布林值),logEvent 再用黑名單欄位與 omo_/omr_/omc_ 前綴雙重擋一次
//   - 來源 IP 只記雜湊前 8 碼;標頭只記名稱不記值
//   - 輸出走伺服器既有的 log(標準 logger,Cloud Run 從 stdout/stderr 收),不另開檔案、不加環境變數

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"omixor-core/mcp"
	"omixor-core/service"
)

type detailKey struct{}

// detail 取出這個請求的觀測欄位(各端點往裡放非機密欄位);沒掛 observe 的情況回一張用完即丟的 map
func detail(r *http.Request) map[string]any {
	if d, ok := r.Context().Value(detailKey{}).(map[string]any); ok {
		return d
	}
	return map[string]any{}
}

// 永遠不能進 log 的欄位名(就算哪個端點誤放進來,也在 logEvent 被丟掉)
var secretKeys = map[string]bool{
	"code": true, "code_verifier": true, "code_challenge": true, "csrf": true, "state": true, "pairing_code": true, "passcode": true,
	"refresh_token": true, "access_token": true, "token": true, "authorization": true, "client_secret": true, "arguments": true, "args": true,
}

var secretValRE = regexp.MustCompile(`\bom[orc]_[A-Za-z0-9_-]{8,}`)

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func scrub(v any) any {
	switch x := v.(type) {
	case string:
		return secretValRE.ReplaceAllString(trunc(x, 300), "[redacted]")
	case []string:
		out := make([]string, 0, len(x))
		for i, s := range x {
			if i >= 10 {
				break
			}
			out = append(out, secretValRE.ReplaceAllString(trunc(s, 300), "[redacted]"))
		}
		return out
	}
	return v
}

// logEvent 寫一行 JSON(唯一的出口,去識別規則在這裡)
func (h *Handler) logEvent(event map[string]any) {
	out := make(map[string]any, len(event))
	for k, v := range event {
		if secretKeys[k] {
			continue
		}
		out[k] = scrub(v)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	lg := h.Log
	if lg == nil {
		lg = log.New(log.Writer(), "", 0)
	}
	lg.Printf("%s", b)
}

// recorder 記狀態碼,並保留前 1 KB 的回應內容(只用來取出 JSON 錯誤碼與唯讀拒絕訊息,不進 log)
type recorder struct {
	http.ResponseWriter
	status int
	head   bytes.Buffer
}

func (w *recorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if room := 8192 - w.head.Len(); room > 0 {
		w.head.Write(b[:min(room, len(b))])
	}
	return w.ResponseWriter.Write(b)
}

var errCodeRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)

// observe 包一個 handler:跑完後寫一行事件。pre 在 handler 之前執行(可為 nil),post 在之後、寫 log 之前(可為 nil)
func (h *Handler) observe(next http.Handler, pre func(*http.Request, map[string]any) bool, post func(*recorder, map[string]any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		d := map[string]any{}
		r = r.WithContext(context.WithValue(r.Context(), detailKey{}, d))
		rec := &recorder{ResponseWriter: w}
		logIt := true
		if pre != nil {
			logIt = pre(r, d)
		}
		next.ServeHTTP(rec, r)
		if !logIt {
			return
		}
		if rec.status == 0 {
			rec.status = 200
		}
		// JSON 錯誤回應的 RFC 6749 error 字串
		if rec.status >= 400 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			var e struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(rec.head.Bytes(), &e) == nil && errCodeRE.MatchString(e.Error) {
				d["error"] = e.Error
			}
		}
		if post != nil {
			post(rec, d)
		}
		ev := map[string]any{
			"ts": time.Now().UTC().Format(time.RFC3339), "method": r.Method, "path": trunc(r.URL.Path, 200), "status": rec.status,
			"duration_ms": time.Since(t0).Milliseconds(), "content_type": trunc(r.Header.Get("Content-Type"), 120),
			"user_agent": trunc(r.Header.Get("User-Agent"), 120), "ip_hash": ipHash(service.ClientIP(r)), "header_names": headerNames(r),
		}
		for k, v := range d {
			ev[k] = v
		}
		h.logEvent(ev)
	})
}

func ipHash(ip string) string {
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])[:8]
}

func headerNames(r *http.Request) []string {
	out := make([]string, 0, len(r.Header))
	for k := range r.Header {
		out = append(out, strings.ToLower(k))
	}
	sort.Strings(out)
	return out
}

// ObserveMCP 包 /mcp/org/{org}:只記沒帶 token(missing)或帶 OAuth access token(omo_…)的請求,omixor 使用者 token(omx_)不記。
// 欄位:rpc(JSON-RPC method;tools/call 附工具名,不記參數)、token_state、resources/read 的 uri
func (h *Handler) ObserveMCP(next http.Handler) http.Handler {
	return h.observe(next, func(r *http.Request, d map[string]any) bool {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok = strings.TrimSpace(tok)
		if tok != "" && !strings.HasPrefix(tok, "omo_") {
			return false
		}
		d["token_state"] = h.Svc.OAuthAccessState(tok, h.MCPURL(r.PathValue("org")))
		if r.Method == http.MethodPost && r.Body != nil {
			b, _ := io.ReadAll(io.LimitReader(r.Body, mcp.MaxBody+1))
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), r.Body)) // 還給後面的 handler,行為不變
			rpcs, uri, calls := parseRPC(b)
			d["rpc"] = strings.Join(rpcs, ",")
			if uri != "" {
				d["uri"] = trunc(uri, 200)
			}
			d["_calls"] = calls
		}
		return true
	}, func(rec *recorder, d map[string]any) {
		calls, _ := d["_calls"].([]string)
		delete(d, "_calls")
		if d["token_state"] != "valid" {
			return
		}
		for _, name := range calls {
			if !mcp.ReadOnlyTools[name] || strings.Contains(rec.head.String(), mcp.ReadOnlyMessage) {
				d["token_state"] = "read_only_denied"
				return
			}
		}
	})
}

// parseRPC 取出 JSON-RPC 的 method(單一或批次,最多 5 個)、tools/call 的工具名、resources/read 的 uri;不碰 params 的其他內容
func parseRPC(b []byte) (rpcs []string, uri string, calls []string) {
	type msg struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"params"`
	}
	var list []msg
	if len(bytes.TrimSpace(b)) > 0 && bytes.TrimSpace(b)[0] == '[' {
		if json.Unmarshal(b, &list) != nil {
			return []string{"(invalid)"}, "", nil
		}
	} else {
		var m msg
		if json.Unmarshal(b, &m) != nil {
			return []string{"(invalid)"}, "", nil
		}
		list = []msg{m}
	}
	for i, m := range list {
		if i >= 5 {
			break
		}
		name := trunc(m.Method, 60)
		if name == "" {
			name = "(response)"
		}
		switch m.Method {
		case "tools/call":
			name += ":" + trunc(m.Params.Name, 60)
			calls = append(calls, m.Params.Name)
		case "resources/read":
			uri = m.Params.URI
		}
		rpcs = append(rpcs, name)
	}
	return
}
