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
	// AuthorActor 作者的 actor 物件(ISS-36 C-3;服務層出口補,和相容字串 author 並存;查不到省略)
	AuthorActor *ActorView `json:"authorActor,omitempty"`
	ID          string     `json:"id"`
	Text        string     `json:"text"`
}

// Issue 的識別(規格:雲端 omixor DOC-10):
//   - ID 內部 id,UUIDv7(伺服器產生,永遠不變、不顯示);舊資料的 ISS-<n> 已在遷移時換成 UUID
//   - Key 顯示用編號:組織層級 ISS-<n>,專案裡 <專案代號>-<n>;每個範圍各自流水號(伺服器產生,用戶端不能指定)。
//     搬到別的專案會發新的 key,舊的留在別名表,所以舊編號仍然查得到。收 Issue id 的地方 UUID、目前或舊的 key 都接受
//   - Project 所屬專案 id(PRJ-<n>);空字串 = 組織層級
//   - Version 樂觀鎖的版本號:每次改本體(標題、內文、狀態、優先度、標籤、repo、指派、專案、刪除/還原)+1;新增留言不算
//   - DeletedAt 不是 nil = 在垃圾桶裡(軟刪除;一般列表、取得、修改都當作不存在)
type Issue struct {
	Body      string    `json:"body"`
	ClosedAt  *Time     `json:"closedAt,omitempty"`
	Comments  []Comment `json:"comments"`
	CreatedAt Time      `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
	// CreatedByActor、DeletedByActor、AssigneeActor 同名字串欄位旁邊的 actor 物件(ISS-36 C-3;服務層出口補,查不到或沒有省略)
	CreatedByActor *ActorView `json:"createdByActor,omitempty"`
	DeletedByActor *ActorView `json:"deletedByActor,omitempty"`
	AssigneeActor  *ActorView `json:"assigneeActor,omitempty"`
	ID             string     `json:"id"`
	Key            string     `json:"key"`
	Project        string     `json:"project"`
	// Num key 的號碼(所屬範圍內的流水號);見 Number()
	Num       int      `json:"number"`
	Version   int      `json:"version"`
	DeletedAt *Time    `json:"deletedAt,omitempty"`
	DeletedBy string   `json:"deletedBy,omitempty"`
	Labels    []string `json:"labels"`
	Link      string   `json:"link"`
	Priority  string   `json:"priority"`
	// Repo 這張 Issue 屬於哪個程式碼倉庫(空 = 未標記);見 CleanRepo
	Repo   string `json:"repo,omitempty"`
	Source string `json:"source"`
	// Assignee 指派給的虛擬角色 id(空 = 未指派);指派時會自動替該角色排任務(見 service/agents.go)
	Assignee  string `json:"assignee,omitempty"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	UpdatedAt Time   `json:"updatedAt"`
	// ItemsOpen、ItemsTotal 子項(issue_item)計數:未完成(todo/doing/blocked)與全部(不含取消);沒有子項省略。不存庫,服務層出口補
	ItemsOpen  int `json:"itemsOpen,omitempty"`
	ItemsTotal int `json:"itemsTotal,omitempty"`
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

// MaxNumber 編號的上限(資料庫的 number 是 int4);id 的數字超過它,Number() 回 0(當作非數字 id,不參與唯一編號)
const MaxNumber = 1<<31 - 1

// Number key 在所屬範圍內的號碼:有 Num 就是它;否則從 key 的「-」後面解析;都沒有時退回解析舊格式的 id(ISS-12 → 12)。格式不對回 0
func (i Issue) Number() int {
	if i.Num > 0 {
		return i.Num
	}
	if n := IssueKeyNumber(i.Key); n > 0 {
		return n
	}
	return idNumber(i.ID, IssueKeyPrefix+"-")
}

// Display 畫面顯示的編號:key,沒有 key(舊版伺服器)時退回 id
func (i Issue) Display() string {
	if i.Key != "" {
		return i.Key
	}
	return i.ID
}

// DedupExact 逐字去重(區分大小寫,保留第一次出現的順序),nil 回空陣列;
// 資料庫層成員、提及、標籤用它(對應區分大小寫的主鍵);標籤清理要不分大小寫用 CleanLabels
func DedupExact(l []string) []string {
	out := make([]string, 0, len(l))
	seen := make(map[string]bool, len(l))
	for _, s := range l {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
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
	// ActorID 這位使用者的 actor id(usr_…;ISS-36 B-2b)。只在 store 讀出時帶,不輸出到 JSON(對外格式由 C-3 決定)
	ActorID string `json:"-"`
	// ActorRef 這位成員的 actor 物件(ISS-36 C-3;服務層 ListMembers、SaveMember 補;和 userId、email 並存)
	ActorRef *ActorView `json:"actorRef,omitempty"`
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
	// ActorID 這位使用者的 actor id(omixor_actors,usr_<uuidv7>;ISS-36 B-2b)。SaveUser 存的時候空的會自動建立或取得
	// (依 email 對上既有的「未註冊使用者」actor),讀出時一定有(遷移補齊之前的舊列例外,下一次 SaveUser 補上)。
	// 不輸出到 JSON:對外的「誰」格式(D2:字串與物件並存)由 C-3 決定
	ActorID string `json:"-"`
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
	ID int64 `json:"id"`
	At Time  `json:"at"`
	// Actor 操作者的 actor id(使用者 usr_…、角色 agt_…);空 = 沒有(login.denied 等沒有登入的操作,存 NULL)。store 讀寫用,
	// 不輸出到 JSON(對外過渡期仍是 userId、email;actor 物件由 C-3 加)
	Actor string `json:"-"`
	// UserID 相容輸出的 userId:使用者 = omixor_users.id、角色 = agent:<handle>。store 不存這個欄位(ListAudit 回空),由服務層 ListAudit 依 Actor 補
	UserID string `json:"userId"`
	// Email 操作當時的 email 快照(使用者;角色是空)。使用者之後改 email,舊紀錄仍是當時的
	Email string `json:"email"`
	// Source 這個身分是怎麼來的(model.Via*:login、pair、mcp、local、agent-credential;空 = 沒標)。不要和 detail 裡的 via 鍵(登入方式,如 ldap)混淆
	Source string `json:"source,omitempty"`
	// ActorRef 操作者的 actor 物件(ISS-36 C-3;服務層 ListAudit 補,沒有操作者或查不到省略;和相容的 userId、email 並存)
	ActorRef *ActorView     `json:"actorRef,omitempty"`
	Action   string         `json:"action"`
	Org      string         `json:"org,omitempty"`
	Detail   map[string]any `json:"detail,omitempty"`
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
	// Actor 這次操作的「誰」(ISS-36 B-3):使用者登入 = 使用者 actor(usr_…),角色憑證 = 角色 actor(agt_…)。
	// 手工組出來的 Principal(測試、本機流程)可能是零值,取用處要自己判斷 Actor.ID 是否為空
	Actor ActorRef
	// Via 身分的來源,見 Via* 常數;空 = 沒標(舊路徑)
	Via string
	// ReadOnly 唯讀授權(OAuth 的 mcp:read):MCP 端點只開放登記為 ReadOnly 的工具(model.CoreTool.ReadOnly),REST 一律拒絕
	ReadOnly bool
}

// Principal.Via 的值:身分是怎麼來的
const (
	ViaLogin           = "login"            // 登入換來的 omixor token(含 LDAP、evoleap)
	ViaPair            = "pair"             // 配對碼換來的 token(手機)
	ViaMCP             = "mcp"              // MCP 端點
	ViaLocal           = "local"            // 本機模式(單機、不經登入)
	ViaOAuth           = "oauth"            // OAuth access token(Claude 自訂 connector;唯讀)
	ViaAgentCredential = "agent-credential" // 角色憑證(agentId + secret)或執行者代角色
)

func (p Principal) ServerAdmin() bool { return p.User.Role == "admin" && !p.User.Disabled }
