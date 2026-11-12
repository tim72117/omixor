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
	Source    string    `json:"source"`
	Status    string    `json:"status"`
	Title     string    `json:"title"`
	UpdatedAt Time      `json:"updatedAt"`
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
}

func (p Principal) ServerAdmin() bool { return p.User.Role == "admin" && !p.User.Disabled }
