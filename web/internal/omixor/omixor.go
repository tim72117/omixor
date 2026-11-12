// Package omixor 是 omixor-issues 服務自己的授權、管理與資料維護(不直連資料庫,維護一律走這裡的 API)。
//
// 身分:做法 A ── 用 evoleap 登入證明「這個人是誰」(把 evoleap 的 token 交給 /api/auth/evoleap,
// 伺服器拿它問一次 evoleap 的 /api/me),之後由 omixor 自己決定能不能用、能做什麼:
//   - 使用者名單與角色(admin / member)存在 omixor 自己的資料庫;不在名單上的人一律拒絕
//   - 使用者表是空的時,email 等於 OMIXOR_ADMIN_BOOTSTRAP_EMAIL 的人成為第一位管理員(只發生一次)
//   - omixor 發自己的 token(omx_…):資料庫只存雜湊,記裝置名稱、到期時間,可以撤銷
//   - 每個登入、管理與資料變更都記在操作紀錄
//
// API(除了 /api/auth/evoleap 都要 Authorization: Bearer omx_…):
//
//	POST /api/auth/evoleap                 {token, device} → {token, expiresAt, user}
//	GET  /api/auth/me                      目前的使用者
//	POST /api/auth/logout                  撤銷目前這個 token
//	     /api/issues/{org}…                Issue 讀寫(admin、member)
//	GET  /api/admin/users                  使用者名單(admin,以下同)
//	POST /api/admin/users                  {email, role?, disabled?} 新增或修改(以 email 找)
//	GET  /api/admin/tokens                 有效的 token
//	POST /api/admin/tokens/{id}/revoke     撤銷
//	GET  /api/admin/audit?limit=           操作紀錄(新的在前)
//	GET  /api/admin/issues/{org}/export    匯出整份 Issue(issues.json 格式)
//	POST /api/admin/issues/{org}/import    {issues} 第一次搬資料用:保留 id、作者與時間;伺服器上這個組織已經有 Issue 就拒絕
//	                                       (之後一律用 /api/issues 單筆新增或修改,不做差異比對與同步)
package omixor

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"omixor-web/internal/core"
)

const TokenPrefix = "omx_"

type User struct {
	ID          string     `json:"id"`
	EvoleapID   string     `json:"evoleapId,omitempty"` // 第一次登入時才對上;管理員先用 email 加進名單的人是空的
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"` // admin、member
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
}

type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Email      string     `json:"email,omitempty"`
	Device     string     `json:"device"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Hash       []byte     `json:"-"`
}

type AuditEntry struct {
	ID     int64          `json:"id"`
	At     time.Time      `json:"at"`
	UserID string         `json:"userId"`
	Email  string         `json:"email"`
	Action string         `json:"action"`
	Org    string         `json:"org,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

var ErrNotFound = errors.New("not found")

// Store 使用者、token、操作紀錄的存放處(正式是 PostgreSQL,測試用記憶體)
type Store interface {
	CountUsers() (int, error)
	ListUsers() ([]User, error)
	UserByEvoleapID(id string) (*User, error)
	UserByEmail(email string) (*User, error)
	SaveUser(u User) error // 以 ID 新增或更新
	CreateToken(t Token) error
	TokenByHash(h []byte) (*Token, *User, error) // 有效的(沒撤銷、沒過期)
	TouchToken(id string, at time.Time) error
	RevokeToken(id string, at time.Time) error
	ListTokens(now time.Time) ([]Token, error)
	AddAudit(e AuditEntry) error
	ListAudit(limit int) ([]AuditEntry, error)
}

// IssueBackend Issue 的整份讀取與匯入(正式是 core 的 PostgreSQL 函式)
type IssueBackend interface {
	Read(org string) ([]core.OpsIssue, error)
	Import(org string, list []core.OpsIssue, overwrite bool) (core.IssueImportResult, error)
}

type Server struct {
	Store     Store
	Issues    IssueBackend
	IssueAPI  http.Handler // /api/issues/… (web.IssueAPI)
	Evoleap   string       // evoleap 網址
	Bootstrap string       // 第一位管理員的 email
	TTL       time.Duration
	Client    *http.Client
	Now       func() time.Time

	limiterOnce sync.Once
	limiter     *loginLimiter
}

// LoginPerIP、LoginTotal 登入端點每分鐘的上限(每個來源 IP、整個服務)
var LoginPerIP, LoginTotal = 10, 60

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func randID(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func HashToken(tok string) []byte { h := sha256.Sum256([]byte(tok)); return h[:] }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func readBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(v)
}

type ctxKey struct{}

type caller struct {
	user  User
	token Token
}

func (s *Server) audit(c *caller, action, org string, detail map[string]any) {
	e := AuditEntry{At: s.now(), Action: action, Org: org, Detail: detail}
	if c != nil {
		e.UserID, e.Email = c.user.ID, c.user.Email
	}
	_ = s.Store.AddAudit(e)
}

// Handler 整個服務的路由(/health 在外層)
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/evoleap", s.handleEvoleapLogin)
	mux.HandleFunc("GET /api/auth/me", s.authed("", func(w http.ResponseWriter, r *http.Request, c *caller) {
		writeJSON(w, 200, map[string]any{"user": c.user, "token": c.token})
	}))
	mux.HandleFunc("POST /api/auth/logout", s.authed("", func(w http.ResponseWriter, r *http.Request, c *caller) {
		_ = s.Store.RevokeToken(c.token.ID, s.now())
		s.audit(c, "logout", "", map[string]any{"token": c.token.ID})
		writeJSON(w, 200, map[string]any{"result": true})
	}))
	mux.Handle("/api/issues/", s.authed("", func(w http.ResponseWriter, r *http.Request, c *caller) {
		if r.Method != "POST" {
			s.IssueAPI.ServeHTTP(w, r)
			return
		}
		// 路徑:/api/issues/{org}[/{id}[/delete]]
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/issues/"), "/")
		action, d := "issue.create", map[string]any{}
		if len(parts) >= 2 {
			action, d["id"] = "issue.update", parts[1]
			if len(parts) == 3 && parts[2] == "delete" {
				action = "issue.delete"
			}
		}
		if c.user.Role != "admin" {
			// 成員不能冒用別人的名字:作者一律是自己,既有留言的作者保持原樣、新留言的作者是自己
			if err := s.pinAuthor(r, parts, c.user.Email); err != nil {
				fail(w, 400, err.Error())
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		s.IssueAPI.ServeHTTP(rec, r)
		if rec.code < 300 {
			s.audit(c, action, parts[0], d)
		}
	}))
	admin := func(pattern string, h func(http.ResponseWriter, *http.Request, *caller)) {
		mux.HandleFunc(pattern, s.authed("admin", h))
	}
	admin("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request, c *caller) {
		list, err := s.Store.ListUsers()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"result": list})
	})
	admin("POST /api/admin/users", s.handleUserSave)
	admin("GET /api/admin/tokens", func(w http.ResponseWriter, r *http.Request, c *caller) {
		list, err := s.Store.ListTokens(s.now())
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"result": list})
	})
	admin("POST /api/admin/tokens/{id}/revoke", func(w http.ResponseWriter, r *http.Request, c *caller) {
		id := r.PathValue("id")
		if err := s.Store.RevokeToken(id, s.now()); err != nil {
			fail(w, 404, "找不到 token "+id)
			return
		}
		s.audit(c, "token.revoke", "", map[string]any{"token": id})
		writeJSON(w, 200, map[string]any{"result": true})
	})
	admin("GET /api/admin/audit", func(w http.ResponseWriter, r *http.Request, c *caller) {
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if n <= 0 || n > 1000 {
			n = 100
		}
		list, err := s.Store.ListAudit(n)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"result": list})
	})
	admin("GET /api/admin/issues/{org}/export", s.handleExport)
	admin("POST /api/admin/issues/{org}/import", s.handleImport)
	return mux
}

// pinAuthor 把請求內容的 author 換成 email;修改時,留言陣列裡既有留言的作者照伺服器上的,新留言的作者換成 email
func (s *Server) pinAuthor(r *http.Request, parts []string, email string) error {
	var body map[string]any
	if err := readBody(r, &body); err != nil || body == nil {
		body = map[string]any{}
	}
	body["author"] = email
	if cs, ok := body["comments"].([]any); ok && len(parts) >= 2 {
		existing := map[string]string{}
		list, err := s.Issues.Read(parts[0])
		if err != nil {
			return err
		}
		for _, i := range list {
			if strings.EqualFold(i.ID, parts[1]) {
				for _, cm := range i.Comments {
					existing[cm.ID] = cm.Author
				}
			}
		}
		for _, x := range cs {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if a, ok := existing[id]; ok {
				m["author"] = a
			} else {
				m["author"] = email
			}
		}
	}
	b, _ := json.Marshal(body)
	r.Body = io.NopCloser(bytes.NewReader(b))
	r.ContentLength = int64(len(b))
	return nil
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(c int) { r.code = c; r.ResponseWriter.WriteHeader(c) }

// authed 驗證 omixor token;role 是 "admin" 時只給管理員
func (s *Server) authed(role string, h func(http.ResponseWriter, *http.Request, *caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok = strings.TrimSpace(tok)
		if !ok || !strings.HasPrefix(tok, TokenPrefix) {
			fail(w, 401, "要帶 omixor 的登入(Authorization: Bearer omx_…);用 omixor login 或在 app 登入")
			return
		}
		t, u, err := s.Store.TokenByHash(HashToken(tok))
		if err != nil || t == nil || u == nil {
			fail(w, 401, "omixor 的登入無效、已過期或已撤銷,請重新登入")
			return
		}
		if u.Disabled {
			fail(w, 403, "這個帳號已停用")
			return
		}
		if role == "admin" && u.Role != "admin" {
			fail(w, 403, "要 omixor 管理員才能做")
			return
		}
		now := s.now()
		if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > time.Minute {
			_ = s.Store.TouchToken(t.ID, now)
		}
		h(w, r, &caller{user: *u, token: *t})
	}
}

type evoleapMe struct {
	User *struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		Name   string `json:"name"`
		Google bool   `json:"google"`
	} `json:"user"`
}

func (s *Server) handleEvoleapLogin(w http.ResponseWriter, r *http.Request) {
	s.limiterOnce.Do(func() { s.limiter = newLoginLimiter(LoginPerIP, LoginTotal) })
	if !s.limiter.allow(clientIP(r), s.now()) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "登入嘗試太頻繁,請一分鐘後再試")
		return
	}
	var in struct {
		Token  string `json:"token"`
		Device string `json:"device"`
	}
	if readBody(r, &in) != nil || strings.TrimSpace(in.Token) == "" {
		fail(w, 400, "要給 evoleap 的 token")
		return
	}
	device := strings.TrimSpace(in.Device)
	if device == "" {
		device = "未命名裝置"
	}
	if len([]rune(device)) > 80 {
		device = string([]rune(device)[:80])
	}
	// 1. 用 evoleap 證明身分
	req, _ := http.NewRequest("GET", strings.TrimRight(s.Evoleap, "/")+"/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(in.Token))
	req.Header.Set("Accept", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		fail(w, 502, "連不到 evoleap 驗證身分:"+err.Error())
		return
	}
	var me evoleapMe
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != 200 || err != nil {
		fail(w, 502, fmt.Sprintf("evoleap 驗證身分失敗(HTTP %d)", resp.StatusCode))
		return
	}
	if me.User == nil || me.User.ID == "" {
		fail(w, 401, "evoleap 的登入無效或已過期")
		return
	}
	if !me.User.Google || me.User.Email == "" {
		fail(w, 403, "evoleap 帳號要用 Google 登入(email 經過驗證)才能用 omixor")
		return
	}
	email := strings.ToLower(strings.TrimSpace(me.User.Email))
	now := s.now()

	// 2. omixor 自己決定能不能用
	u, err := s.Store.UserByEvoleapID(me.User.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		fail(w, 500, err.Error())
		return
	}
	if u == nil {
		// 管理員先用 email 加進名單、還沒對上 evoleap 帳號的人
		if p, err := s.Store.UserByEmail(email); err == nil && p.EvoleapID == "" {
			u = p
			u.EvoleapID = me.User.ID
		}
	}
	if u == nil {
		n, err := s.Store.CountUsers()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		boot := strings.ToLower(strings.TrimSpace(s.Bootstrap))
		if n == 0 && boot != "" && email == boot {
			u = &User{ID: randID("u_", 9), EvoleapID: me.User.ID, Email: email, Role: "admin", CreatedAt: now}
			s.audit(&caller{user: *u}, "bootstrap.admin", "", map[string]any{"evoleapId": me.User.ID})
		} else {
			s.audit(nil, "login.denied", "", map[string]any{"email": email, "evoleapId": me.User.ID})
			fail(w, 403, "這個帳號("+email+")沒有 omixor 的權限,請 omixor 管理員用 omixor users add 加入")
			return
		}
	}
	if u.Disabled {
		s.audit(&caller{user: *u}, "login.denied", "", map[string]any{"reason": "disabled"})
		fail(w, 403, "這個帳號已停用")
		return
	}
	// evoleap 上改了 email:新的 email 已經被名單上另一個人用了,就保留原本的(避免 email 重複,登入失敗)
	if email != u.Email {
		if other, err := s.Store.UserByEmail(email); err == nil && other.ID != u.ID {
			s.audit(&caller{user: *u}, "email.conflict", "", map[string]any{"evoleapEmail": email, "other": other.ID})
			email = u.Email
		}
	}
	u.Email, u.LastLoginAt = email, &now
	if me.User.Name != "" {
		u.Name = me.User.Name
	}
	if err := s.Store.SaveUser(*u); err != nil {
		fail(w, 500, err.Error())
		return
	}
	// 3. 發 omixor 自己的 token(只存雜湊)
	raw := randID(TokenPrefix, 32)
	t := Token{ID: randID("t_", 9), UserID: u.ID, Device: device, CreatedAt: now, Hash: HashToken(raw)}
	if s.TTL > 0 {
		exp := now.Add(s.TTL)
		t.ExpiresAt = &exp
	}
	if err := s.Store.CreateToken(t); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.audit(&caller{user: *u, token: t}, "login", "", map[string]any{"token": t.ID, "device": device})
	writeJSON(w, 200, map[string]any{"token": raw, "tokenId": t.ID, "expiresAt": t.ExpiresAt, "user": u})
}

func (s *Server) handleUserSave(w http.ResponseWriter, r *http.Request, c *caller) {
	var in struct {
		Email    string  `json:"email"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if readBody(r, &in) != nil {
		fail(w, 400, "格式錯誤")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") {
		fail(w, 400, "要給 email")
		return
	}
	if in.Role != nil && *in.Role != "admin" && *in.Role != "member" {
		fail(w, 400, "role 必須是 admin 或 member")
		return
	}
	u, err := s.Store.UserByEmail(email)
	created := false
	if errors.Is(err, ErrNotFound) {
		u, created = &User{ID: randID("u_", 9), Email: email, Role: "member", CreatedAt: s.now()}, true
	} else if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if in.Role != nil {
		u.Role = *in.Role
	}
	if in.Disabled != nil {
		u.Disabled = *in.Disabled
	}
	// 不能把自己降級或停用(避免沒有管理員)
	if u.ID == c.user.ID && (u.Role != "admin" || u.Disabled) {
		fail(w, 400, "不能把自己降級或停用")
		return
	}
	if err := s.Store.SaveUser(*u); err != nil {
		fail(w, 500, err.Error())
		return
	}
	action := "user.update"
	if created {
		action = "user.add"
	}
	s.audit(c, action, "", map[string]any{"email": email, "role": u.Role, "disabled": u.Disabled})
	writeJSON(w, 200, map[string]any{"result": u})
}

func (s *Server) org(w http.ResponseWriter, r *http.Request) (string, bool) {
	o := r.PathValue("org")
	if !core.ValidOrgID(o) {
		fail(w, 400, "組織 id 不對:"+o)
		return "", false
	}
	return o, true
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, c *caller) {
	org, ok := s.org(w, r)
	if !ok {
		return
	}
	list, err := s.Issues.Read(org)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.audit(c, "issues.export", org, map[string]any{"count": len(list)})
	writeJSON(w, 200, map[string]any{"result": list})
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request, c *caller) {
	org, ok := s.org(w, r)
	if !ok {
		return
	}
	var in struct {
		Issues []core.OpsIssue `json:"issues"`
	}
	if err := readBody(r, &in); err != nil {
		fail(w, 400, "格式錯誤:"+err.Error())
		return
	}
	if len(in.Issues) == 0 {
		fail(w, 400, "沒有要匯入的 Issue")
		return
	}
	seen := map[string]bool{}
	for _, i := range in.Issues {
		if i.ID == "" || strings.TrimSpace(i.Title) == "" {
			fail(w, 400, "每一筆都要有 id 與 title")
			return
		}
		if seen[i.ID] {
			fail(w, 400, "id 重複:"+i.ID)
			return
		}
		seen[i.ID] = true
	}
	// 只在第一次搬資料時用:已經有資料就不碰(之後用單筆新增或修改)
	cur, err := s.Issues.Read(org)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if len(cur) > 0 {
		fail(w, 409, fmt.Sprintf("伺服器上 %s 已經有 %d 筆 Issue;匯入只在第一次搬資料時用,之後請用單筆新增或修改", org, len(cur)))
		return
	}
	res, err := s.Issues.Import(org, in.Issues, false)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.audit(c, "issues.import", org, map[string]any{"inserted": res.Inserted})
	writeJSON(w, 200, map[string]any{"result": res})
}
