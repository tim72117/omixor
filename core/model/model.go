// Package model 是核心共用的資料型別。JSON 格式和 Swift app、omixor-web 一樣(時間是到秒的 ISO 8601)。
package model

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// Time JSON 是 "2006-01-02T15:04:05Z"(到秒);讀的時候也接受小數秒
type Time struct{ time.Time }

func Now() Time { return Time{time.Now().UTC().Truncate(time.Second)} }

func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.UTC().Truncate(time.Second).Format(time.RFC3339) + `"`), nil
}

func (t *Time) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*t = Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	p, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	*t = Time{p.UTC()}
	return nil
}

var (
	IssueStatuses   = []string{"open", "closed"}
	IssuePriorities = []string{"urgent", "high", "normal", "low"}
	Roles           = []string{"admin", "member"}
)

func OneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type Comment struct {
	At     Time   `json:"at"`
	Author string `json:"author"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

type Issue struct {
	Body      string    `json:"body"`
	ClosedAt  *Time     `json:"closedAt,omitempty"`
	Comments  []Comment `json:"comments"`
	CreatedAt Time      `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
	ID        string    `json:"id"`
	Labels    []string  `json:"labels"`
	Link      string    `json:"link"`
	Priority  string    `json:"priority"`
	// Repo 這張 Issue 屬於哪個程式碼倉庫(空 = 未標記);見 CleanRepo
	Repo   string `json:"repo,omitempty"`
	Source string `json:"source"`
	// Assignee 指派給的虛擬角色 id(空 = 未指派);指派時會自動替該角色排任務(見 service/agents.go)
	Assignee  string `json:"assignee,omitempty"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	UpdatedAt Time   `json:"updatedAt"`
}

// RepoMaxLen Issue repo 標記的長度上限
const RepoMaxLen = 200

// CleanRepo 整理 Issue 的 repo 標記:去頭尾空白;只允許英數、-、_、.、/、:,最長 RepoMaxLen。
// 格式慣例是 owner/name(GitHub)或 group/sub/project(GitLab),可在前面加 github:、gitlab: 平台前綴;
// 空字串 = 未標記。不合格回傳 ok=false
func CleanRepo(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) > RepoMaxLen {
		return "", false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == ':':
		default:
			return "", false
		}
	}
	return s, true
}

// RepoMatch 依 repo 篩選用:不分大小寫比對;平台前綴(github:、gitlab:)有一邊沒寫就忽略前綴比較
func RepoMatch(repo, filter string) bool {
	a, b := strings.ToLower(strings.TrimSpace(repo)), strings.ToLower(strings.TrimSpace(filter))
	if a == b {
		return true
	}
	strip := func(x string) (string, bool) {
		for _, pf := range []string{"github:", "gitlab:"} {
			if r, ok := strings.CutPrefix(x, pf); ok {
				return r, true
			}
		}
		return x, false
	}
	ra, pa := strip(a)
	rb, pb := strip(b)
	return ra == rb && ra != "" && (!pa || !pb)
}

// Number ISS-12 → 12(格式不對回傳 0)
func (i Issue) Number() int {
	n := 0
	rest, ok := strings.CutPrefix(strings.ToUpper(i.ID), "ISS-")
	if !ok || rest == "" {
		return 0
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// CleanLabels 去頭尾空白、去掉空的與重複的(不分大小寫,保留第一次出現的寫法),同 app
func CleanLabels(labels []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range labels {
		t := strings.TrimFunc(l, func(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) })
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// ValidOrgID 組織 id 只能是英數字、- 與 _(1~100 字)
func ValidOrgID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !(r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')) {
			return false
		}
	}
	return true
}

type Org struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt Time   `json:"createdAt"`
	// 畫面用的外觀與說明(圖示名稱、顏色、備註);沒設是空字串
	Symbol string `json:"symbol,omitempty"`
	Color  string `json:"color,omitempty"`
	Note   string `json:"note,omitempty"`
	// 目前使用者在這個組織的角色(列出「我的組織」時才有;伺服器管理員是 "admin")
	MyRole string `json:"myRole,omitempty"`
}

type Member struct {
	Org     string `json:"org"`
	UserID  string `json:"userId"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"` // admin、member
	AddedAt Time   `json:"addedAt"`
}

type User struct {
	ID          string `json:"id"`
	EvoleapID   string `json:"evoleapId,omitempty"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"` // admin(伺服器管理員)、member
	Disabled    bool   `json:"disabled"`
	CreatedAt   Time   `json:"createdAt"`
	LastLoginAt *Time  `json:"lastLoginAt,omitempty"`
}

type Token struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	Email      string `json:"email,omitempty"`
	Device     string `json:"device"`
	CreatedAt  Time   `json:"createdAt"`
	LastUsedAt *Time  `json:"lastUsedAt,omitempty"`
	ExpiresAt  *Time  `json:"expiresAt,omitempty"`
	Hash       []byte `json:"-"`
}

type AuditEntry struct {
	ID     int64          `json:"id"`
	At     Time           `json:"at"`
	UserID string         `json:"userId"`
	Email  string         `json:"email"`
	Action string         `json:"action"`
	Org    string         `json:"org,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

// ImportResult 匯入結果
type ImportResult struct {
	Org      string `json:"org"`
	Inserted int    `json:"inserted"`
}

// Principal 目前呼叫的人(服務層用它判斷權限)
type Principal struct {
	User  User
	Token Token
	// Agent 不是 nil 表示這是「角色憑證」換來的身分(只能做 service 明確開放給角色的事,見 service/agents.go)
	Agent *AgentIdentity
}

func (p Principal) ServerAdmin() bool { return p.User.Role == "admin" && !p.User.Disabled }
