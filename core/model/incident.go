package model

import "strings"

// 事件(Incident):線上服務出狀況時宣告的事件,含嚴重度、狀態、時間軸、AI 摘要與後續工作。
// JSON 欄位和 Swift app(Core/Models.swift 的 Incident、IncidentEvent)、omixor-web 的畫面一樣;
// 多的欄位(alertIds、createdBy)沒有值時不輸出。規則(權限、編號、時間軸)在 service/incident.go。
//
// 事後檢討(postmortem)由畫面依時間軸、告警與後續工作即時起草,不另外存。

// IncidentPrefix 事件 id 的前綴(INC-<n>,一個組織一起編號)
const IncidentPrefix = "INC-"

// IncidentNumberBase 編號從這個數字之後開始(第一筆是 INC-2001,同 app)
const IncidentNumberBase = 2000

const (
	MaxIncidentTitle = 200   // 標題最多幾個字
	MaxIncidentText  = 20000 // 影響、AI 摘要、時間軸一則最多幾個字
	MinIncidentSev   = 1     // SEV1 最嚴重
	MaxIncidentSev   = 4
)

// IncidentStatuses 事件狀態(順序同 app 的 IncidentStatus.allCases)
var IncidentStatuses = []string{"investigating", "identified", "monitoring", "resolved"}

// IncidentStatusLabel 狀態的中文(時間軸的「狀態改為「…」」用)
var IncidentStatusLabel = map[string]string{
	"investigating": "調查中", "identified": "已找到原因", "monitoring": "觀察中", "resolved": "已解決",
}

// IncidentEventKinds 時間軸項目的種類
var IncidentEventKinds = []string{"status", "note", "action", "alert", "deploy", "aiFinding"}

// ActorKinds 執行者的種類:人(id 是人員 id 或 email)、AI(id 是自動化 id;"mcp" 是 MCP 用戶端)、系統
var ActorKinds = []string{"person", "ai", "system"}

// MCPActorID 透過 MCP 做的變更,執行者記為 ai/mcp(畫面顯示「MCP 用戶端」)
const MCPActorID = "mcp"

// Actor 執行者(JSON 同 app:{"kind": "person", "id": "tim"}、{"kind": "system"})
type Actor struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

func PersonActor(id string) Actor { return Actor{Kind: "person", ID: id} }
func AIActor(id string) Actor     { return Actor{Kind: "ai", ID: id} }
func SystemActor() Actor          { return Actor{Kind: "system"} }

// Valid 種類要認得;person、ai 要有 id
func (a Actor) Valid() bool {
	switch a.Kind {
	case "system":
		return true
	case "person", "ai":
		return strings.TrimSpace(a.ID) != ""
	}
	return false
}

// IncidentEvent 時間軸的一筆
type IncidentEvent struct {
	ID    string `json:"id"` // UUID
	Date  Time   `json:"date"`
	Actor Actor  `json:"actor"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
}

type Incident struct {
	ID         string          `json:"id"` // INC-2041
	Title      string          `json:"title"`
	Sev        int             `json:"sev"` // 1…4,1 最嚴重
	Status     string          `json:"status"`
	Services   []string        `json:"services"`
	Commander  string          `json:"commander"` // 指揮官(人員 id 或 email)
	StartedAt  Time            `json:"startedAt"`
	ResolvedAt *Time           `json:"resolvedAt,omitempty"`
	Impact     string          `json:"impact"`
	Timeline   []IncidentEvent `json:"timeline"`
	AISummary  *string         `json:"aiSummary,omitempty"`
	FollowUps  []string        `json:"followUps"` // 後續工作(工作項目或 Issue 的 id)
	// AlertIDs 關聯的告警 id(告警在 app/web 端;核心只記 id)
	AlertIDs []string `json:"alertIds,omitempty"`
	// CreatedBy 宣告者的署名(email、人員 id 或 mcp;匯入的舊資料可能沒有)
	CreatedBy string `json:"createdBy,omitempty"`
}

// Clone 深拷貝
func (i Incident) Clone() Incident {
	i.Services = append([]string{}, i.Services...)
	i.Timeline = append([]IncidentEvent{}, i.Timeline...)
	i.FollowUps = append([]string{}, i.FollowUps...)
	if i.AlertIDs != nil {
		i.AlertIDs = append([]string{}, i.AlertIDs...)
	}
	if i.ResolvedAt != nil {
		t := *i.ResolvedAt
		i.ResolvedAt = &t
	}
	if i.AISummary != nil {
		s := *i.AISummary
		i.AISummary = &s
	}
	return i
}

// Number INC-<n> 的 n(格式不對是 0)
func (i Incident) Number() int { return idNumber(i.ID, IncidentPrefix) }

// IsOpen 還沒解決
func (i Incident) IsOpen() bool { return i.Status != "resolved" }

// CleanList 去頭尾空白、去掉空的與重複的(保留順序);沒有回傳空陣列(不是 nil)
func CleanList(list []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
