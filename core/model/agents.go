package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 虛擬角色與任務:omixor 不自己跑 AI,任務排在這裡,由使用者的 Claude 以角色身分領取、回報。
// JSON 欄位同 omixor-web(web/internal/core/agents.go),兩邊的資料格式一致。

// AgentPrefix 角色的署名前綴:Issue 與留言的作者是 "agent:<角色 id>"
const AgentPrefix = "agent:"

// RoutineAuthor 例行任務排的任務,createdBy 是這個
const RoutineAuthor = "routine"

// SystemAuthor 系統自動留的任務備註(例如領取逾時退回)的署名
const SystemAuthor = "omixor"

var (
	AgentColors       = []string{"blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"}
	RoutineKinds      = []string{"daily", "weekly", "hourly"}
	AgentTaskStatuses = []string{"queued", "inProgress", "blocked", "done", "canceled"}
	DocAccessLevels   = []string{"none", "read", "write"}
)

var agentIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidAgentID 角色 id 只能用小寫英文、數字與 -(也因此 "<id>:<secret>" 的格式不會混淆)
func ValidAgentID(id string) bool { return agentIDRe.MatchString(id) }

// AgentRoutine 例行任務:到時間由核心(service.RunDueRoutines)替角色排一筆任務;LastRunAt 記上次排的時間
type AgentRoutine struct {
	CreatedAt    Time   `json:"createdAt"`
	Enabled      bool   `json:"enabled"`
	EveryHours   int    `json:"everyHours"`
	Hour         int    `json:"hour"`
	ID           string `json:"id"`
	Instructions string `json:"instructions"`
	Kind         string `json:"kind"`
	LastRunAt    *Time  `json:"lastRunAt,omitempty"`
	Minute       int    `json:"minute"`
	Priority     string `json:"priority"`
	Title        string `json:"title"`
	Weekday      int    `json:"weekday"`
}

// Agent 虛擬角色。DocAccess 的 key 是文件櫃的區塊 id 或頁籤網址(文件櫃不在核心,核心只保存)
type Agent struct {
	Active       bool              `json:"active"`
	Color        string            `json:"color"`
	CreatedAt    Time              `json:"createdAt"`
	DocAccess    map[string]string `json:"docAccess"`
	ID           string            `json:"id"`
	Instructions string            `json:"instructions"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	Routines     []AgentRoutine    `json:"routines"`
	Symbol       string            `json:"symbol"`
}

var weekdayNames = []string{"日", "一", "二", "三", "四", "五", "六"}

// ScheduleLabel 排程的文字(例如「每天 09:00」「每週一 09:00」「每 6 小時」)
func (r AgentRoutine) ScheduleLabel() string {
	t := fmt.Sprintf("%02d:%02d", r.Hour, r.Minute)
	switch r.Kind {
	case "weekly":
		return "每週" + weekdayNames[min(7, max(1, r.Weekday))-1] + " " + t
	case "hourly":
		return "每 " + strconv.Itoa(r.EveryHours) + " 小時"
	}
	return "每天 " + t
}

// NextRun 上次排程(沒有就從建立時間)之後的下一個時間點(本地時區);時間設定不正確就回傳起算點
func (r AgentRoutine) NextRun() time.Time {
	base := r.CreatedAt.Time
	if r.LastRunAt != nil {
		base = r.LastRunAt.Time
	}
	if r.Kind == "hourly" {
		return base.Add(time.Duration(max(1, r.EveryHours)) * time.Hour)
	}
	if r.Hour < 0 || r.Hour > 23 || r.Minute < 0 || r.Minute > 59 || (r.Kind == "weekly" && (r.Weekday < 1 || r.Weekday > 7)) {
		return base
	}
	lb := base.In(time.Local)
	for d := 0; d <= 8; d++ {
		day := lb.AddDate(0, 0, d)
		c := time.Date(day.Year(), day.Month(), day.Day(), r.Hour, r.Minute, 0, 0, time.Local)
		if !c.After(base) {
			continue
		}
		if r.Kind == "weekly" && int(c.Weekday())+1 != r.Weekday {
			continue
		}
		return c
	}
	return base
}

// Access 這個角色對文件櫃某個位置的權限("" = 不能存取);頁籤的設定優先於區塊
func (g Agent) Access(section, tab string) string {
	if tab != "" {
		if a, ok := g.DocAccess[tab]; ok {
			if a == "none" {
				return ""
			}
			return a
		}
	}
	if a, ok := g.DocAccess[section]; ok && a != "none" {
		return a
	}
	return ""
}

func (g Agent) CanRead(section, tab string) bool  { return g.Access(section, tab) != "" }
func (g Agent) CanWrite(section, tab string) bool { return g.Access(section, tab) == "write" }

// Clone 深拷貝(map 與 slice)
func (g Agent) Clone() Agent {
	g.Routines = append([]AgentRoutine{}, g.Routines...)
	acc := map[string]string{}
	for k, v := range g.DocAccess {
		acc[k] = v
	}
	g.DocAccess = acc
	return g
}

type AgentTaskNote struct {
	At     Time   `json:"at"`
	Author string `json:"author"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

// AgentTask 角色的任務(id 是 T-<n>,一個組織內流水號)
type AgentTask struct {
	AgentID      string          `json:"agentId"`
	ClaimedAt    *Time           `json:"claimedAt,omitempty"` // 最近一次領取(租約開始)的時間
	ClaimedBy    string          `json:"claimedBy,omitempty"` // 領取者(email 或名稱,只供畫面顯示)
	CompletedAt  *Time           `json:"completedAt,omitempty"`
	CreatedAt    Time            `json:"createdAt"`
	CreatedBy    string          `json:"createdBy"`
	ID           string          `json:"id"`
	Instructions string          `json:"instructions"`
	Links        []string        `json:"links"`
	LockedUntil  *Time           `json:"lockedUntil,omitempty"` // 領取租約的到期時間(進行中才有);鎖定期間不能被別人領取
	Notes        []AgentTaskNote `json:"notes"`
	Priority     string          `json:"priority"`
	Result       string          `json:"result"`
	RoutineID    *string         `json:"routineId,omitempty"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	UpdatedAt    Time            `json:"updatedAt"`
}

// Number T-12 → 12(格式不對是 0)
func (t AgentTask) Number() int {
	rest, ok := strings.CutPrefix(strings.ToUpper(t.ID), "T-")
	if !ok || rest == "" {
		return 0
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// Clone 深拷貝
func (t AgentTask) Clone() AgentTask {
	t.Links = append([]string{}, t.Links...)
	t.Notes = append([]AgentTaskNote{}, t.Notes...)
	if t.LockedUntil != nil {
		l := *t.LockedUntil
		t.LockedUntil = &l
	}
	return t
}

// Locked 任務現在被領取鎖住:進行中且 LockedUntil 還沒到
func (t AgentTask) Locked(now time.Time) bool {
	return t.Status == "inProgress" && t.LockedUntil != nil && now.Before(t.LockedUntil.Time)
}

// LeaseExpired 進行中,但領取租約已逾時(原領取者沒有回報也沒有續約)
func (t AgentTask) LeaseExpired(now time.Time) bool {
	return t.Status == "inProgress" && t.LockedUntil != nil && !now.Before(t.LockedUntil.Time)
}

// TaskIsOpen 還沒結束(待領取、進行中、需要協助)
func TaskIsOpen(status string) bool {
	return status == "queued" || status == "inProgress" || status == "blocked"
}

// TaskStatusLabel 狀態的中文名稱
func TaskStatusLabel(s string) string {
	switch s {
	case "queued":
		return "待領取"
	case "inProgress":
		return "進行中"
	case "blocked":
		return "需要協助"
	case "done":
		return "完成"
	case "canceled":
		return "已取消"
	}
	return s
}

// AgentCredential 任務綁定的角色憑證(只存雜湊與到期時間,不存明文)
type AgentCredential struct {
	Org       string `json:"org"`
	AgentID   string `json:"agentId"`
	TaskID    string `json:"taskId"`
	Hash      []byte `json:"-"` // SHA-256(secret)
	IssuedAt  Time   `json:"issuedAt"`
	ExpiresAt Time   `json:"expiresAt"`
}

// AgentGrant 領取任務當下回給呼叫端的憑證(Secret 明文只有這一次看得到)
// 連角色端點用 Authorization: Bearer <agentId>:<secret>
type AgentGrant struct {
	AgentID   string `json:"agentId"`
	TaskID    string `json:"taskId"`
	Secret    string `json:"secret"`
	ExpiresAt string `json:"expiresAt"`
}

// AgentIdentity 角色憑證驗證通過後的身分(掛在 Principal.Agent)
type AgentIdentity struct {
	Org     string
	AgentID string
	TaskID  string
}
