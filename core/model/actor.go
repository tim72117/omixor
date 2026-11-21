package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ---- actor:「誰」的唯一識別(ISS-36;規格見雲端 omixor DOC-11、DOC-13) ----
//
// actor 是使用者(user)、虛擬角色(agent)、系統內建署名(system)三種「做事的人」的共同身分,存在 omixor_actors 表。
//
//	id 格式    使用者 usr_<uuidv7>、角色 agt_<uuidv7>、系統 sys_<名稱>(名稱固定,見下面的系統 actor 清單)
//	handle     對人顯示與輸入用的名字:使用者 = email(小寫;本機模式可以是 tim 這種非 email),
//	           角色 = 角色 id(組織內現役唯一),系統 = 名稱
//
// 型別名稱:model.Actor 已經是事件時間軸的執行者(incident.go,kind = person/ai/system),語意不同,
// 所以完整的一列叫 ActorRecord,對外精簡版叫 ActorRef;事件那個型別之後改名(DOC-13 §8.1 #7)。
// 本檔只有純函式與型別,不碰資料庫(資料表在 core/store/pg_actor.go)。

// ActorKind actor 的種類
type ActorKind string

const (
	ActorKindUser   ActorKind = "user"
	ActorKindAgent  ActorKind = "agent"
	ActorKindSystem ActorKind = "system"
)

// ActorKindList 全部種類(順序固定)
var ActorKindList = []ActorKind{ActorKindUser, ActorKindAgent, ActorKindSystem}

// Valid 是不是三種之一
func (k ActorKind) Valid() bool {
	switch k {
	case ActorKindUser, ActorKindAgent, ActorKindSystem:
		return true
	}
	return false
}

// actor id 的前綴
const (
	ActorIDPrefixUser   = "usr_"
	ActorIDPrefixAgent  = "agt_"
	ActorIDPrefixSystem = "sys_"
)

// ActorIDPrefix 種類對應的 id 前綴(種類不合法回 "")
func ActorIDPrefix(k ActorKind) string {
	switch k {
	case ActorKindUser:
		return ActorIDPrefixUser
	case ActorKindAgent:
		return ActorIDPrefixAgent
	case ActorKindSystem:
		return ActorIDPrefixSystem
	}
	return ""
}

// 系統 actor 的固定 id(DOC-13 §1.6;由 omixor_actors 的啟動遷移固定寫入,同一版內不改)
const (
	// SystemActorOmixor omixor 系統:SystemAuthor(領取逾時退回備註、轉移備註、範本模擬)、舊的 system 署名與事件的 system 種類
	SystemActorOmixor = "sys_omixor"
	// SystemActorRoutine 例行排程:RoutineAuthor(例行任務排的任務)
	SystemActorRoutine = "sys_routine"
	// SystemActorChain 組織鏈:ChainAuthor(範本與鏈的自動留言)
	SystemActorChain = "sys_chain"
)

// SystemActorDef 一個固定的系統 actor
type SystemActorDef struct {
	ID     string // sys_<名稱>
	Name   string // 顯示名稱
	Handle string // 名稱(不含 sys_)
	Note   string // 取代的舊值與用途(給維護的人看)
}

// SystemActors 固定寫入資料庫的系統 actor 清單(順序固定)。遷移時才建的 sys_legacy_* 不在這裡(DOC-13 §1.5,C-1)
var SystemActors = []SystemActorDef{
	{SystemActorOmixor, "omixor 系統", "omixor", "SystemAuthor(omixor)、舊的 system 與事件的 system 種類"},
	{SystemActorRoutine, "例行排程", "routine", "RoutineAuthor(routine)"},
	{SystemActorChain, "組織鏈", "chain", "ChainAuthor(chain)"},
}

// IsSystemActorID 是不是固定清單裡的系統 actor id
func IsSystemActorID(id string) bool {
	for _, s := range SystemActors {
		if s.ID == id {
			return true
		}
	}
	return false
}

// NewUserActorID 產生新的使用者 actor id:usr_<uuidv7>(時間用 t,毫秒;同毫秒以外的 id 依時間排序)
func NewUserActorID(t time.Time) string { return ActorIDPrefixUser + NewIssueID(t) }

// NewAgentActorID 產生新的角色 actor id:agt_<uuidv7>
func NewAgentActorID(t time.Time) string { return ActorIDPrefixAgent + NewIssueID(t) }

// NewActorID 依種類產生 actor id;system 的 id 是固定名稱,不能產生,回錯誤
func NewActorID(k ActorKind, t time.Time) (string, error) {
	switch k {
	case ActorKindUser:
		return NewUserActorID(t), nil
	case ActorKindAgent:
		return NewAgentActorID(t), nil
	}
	return "", fmt.Errorf("%q 種類的 actor id 不能自動產生", k)
}

var systemActorNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)

// isLowerUUID 小寫、8-4-4-4-12 的 UUID(actor id 只收這個寫法,同一個 id 不會有大小寫兩種)
func isLowerUUID(s string) bool { return IsUUID(s) && s == strings.ToLower(s) }

// ActorKindOfID 從 id 的前綴與格式判斷種類:usr_/agt_ 後面要是小寫 UUID,sys_ 後面要是 1 到 64 個小寫英數字或底線(第一個不是底線)。
// 格式不對回 ("", false)
func ActorKindOfID(id string) (ActorKind, bool) {
	switch {
	case strings.HasPrefix(id, ActorIDPrefixUser) && isLowerUUID(id[len(ActorIDPrefixUser):]):
		return ActorKindUser, true
	case strings.HasPrefix(id, ActorIDPrefixAgent) && isLowerUUID(id[len(ActorIDPrefixAgent):]):
		return ActorKindAgent, true
	case strings.HasPrefix(id, ActorIDPrefixSystem) && systemActorNameRe.MatchString(id[len(ActorIDPrefixSystem):]):
		return ActorKindSystem, true
	}
	return "", false
}

// ValidActorID id 格式是否正確(任何一種)
func ValidActorID(id string) bool { _, ok := ActorKindOfID(id); return ok }

// ValidActorIDOf id 格式正確而且是這個種類
func ValidActorIDOf(k ActorKind, id string) bool {
	got, ok := ActorKindOfID(id)
	return ok && got == k
}

// handle 的錯誤
var (
	ErrActorHandleEmpty   = errors.New("handle 不能是空的")
	ErrActorHandleInvalid = errors.New("handle 含有不允許的字元(不能有空白或控制字元)")
	ErrActorHandleTooLong = errors.New("handle 太長(最多 254 個字元)")
)

const maxActorHandleLen = 254 // email 的上限

// cleanHandleBase 去頭尾空白與一個前導 @(提及的寫法 @tim)
func cleanHandleBase(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimSpace(strings.TrimPrefix(s, "@"))
}

// CleanUserHandle 使用者 handle 的規範化:去頭尾空白與前導 @、轉小寫。內含空白、控制字元、超過 254 字元、空字串回錯誤。
// 不要求是 email(本機模式的使用者 handle 可以是 tim 這種名字);全伺服器唯一(不分大小寫)由資料庫保證
func CleanUserHandle(s string) (string, error) {
	h := strings.ToLower(cleanHandleBase(s))
	return h, checkHandle(h)
}

// CleanAgentHandle 角色 handle(= 角色 id)的規範化:去頭尾空白與前導 @、轉小寫,再要符合 ValidAgentID。
// 角色 id 在組織內現役唯一由資料庫保證
func CleanAgentHandle(s string) (string, error) {
	h := strings.ToLower(cleanHandleBase(s))
	if h == "" {
		return "", ErrActorHandleEmpty
	}
	if !ValidAgentID(h) {
		return "", fmt.Errorf("角色 handle %q 只能用小寫英文、數字與 -,第一個字不能是 -,最多 64 個字元", h)
	}
	return h, nil
}

// CleanSystemHandle 系統 actor 的名稱(sys_ 後面那段):去頭尾空白、轉小寫,要是 1 到 64 個英數字或底線
func CleanSystemHandle(s string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(s))
	if h == "" {
		return "", ErrActorHandleEmpty
	}
	if !systemActorNameRe.MatchString(h) {
		return "", fmt.Errorf("系統 actor 名稱 %q 只能用小寫英文、數字與底線,最多 64 個字元", h)
	}
	return h, nil
}

// CleanActorHandle 依種類規範化 handle
func CleanActorHandle(k ActorKind, s string) (string, error) {
	switch k {
	case ActorKindUser:
		return CleanUserHandle(s)
	case ActorKindAgent:
		return CleanAgentHandle(s)
	case ActorKindSystem:
		return CleanSystemHandle(s)
	}
	return "", fmt.Errorf("不認得的 actor 種類 %q", k)
}

func checkHandle(h string) error {
	switch {
	case h == "":
		return ErrActorHandleEmpty
	case len(h) > maxActorHandleLen:
		return ErrActorHandleTooLong
	}
	for _, r := range h {
		if r <= ' ' || r == 0x7f || r == 0x85 || r == 0xa0 || r == 0x3000 || r == 0x2028 || r == 0x2029 {
			return ErrActorHandleInvalid
		}
	}
	return nil
}

// ActorRecord omixor_actors 的一列(完整)
type ActorRecord struct {
	ID     string    `json:"id"`
	Kind   ActorKind `json:"kind"`
	Org    string    `json:"org,omitempty"` // 只有 agent 有
	Name   string    `json:"name"`          // 顯示名稱
	Handle string    `json:"handle"`
	Avatar string    `json:"avatar,omitempty"` // 使用者頭像網址
	Owner  string    `json:"owner,omitempty"`  // agent 的負責人(使用者 actor id);可以是空的(遷移時找不到人)
	// DisabledAt 退役時間;nil = 現役。角色被刪 = 退役(不實際刪列,舊紀錄仍指向它)
	DisabledAt *Time `json:"disabledAt,omitempty"`
	CreatedAt  Time  `json:"createdAt"`
}

// Disabled 是否已退役、停用
func (a ActorRecord) Disabled() bool { return a.DisabledAt != nil && !a.DisabledAt.IsZero() }

// Ref 對外的精簡物件
func (a ActorRecord) Ref() ActorRef {
	return ActorRef{ID: a.ID, Kind: a.Kind, Name: a.Name, Handle: a.Handle}
}

// ActorRef 對外(API、MCP)輸出的「誰」:{id, kind, name, handle}。name 與 handle 是當下的值,不是留言當時的值
type ActorRef struct {
	ID     string    `json:"id"`
	Kind   ActorKind `json:"kind"`
	Name   string    `json:"name"`
	Handle string    `json:"handle"`
}

// ActorView 對外輸出的「誰」完整物件(ISS-36 C-3):{id, kind, handle, name, email?, status}。
// GET /actors 的一筆,以及各資料「誰」欄位旁邊並存的 actor 物件(authorActor、createdByActor…;相容字串欄位照舊)都是這個型別。
// name 與 handle 是「現在」的值,不是留言當時的值;查不到的 actor(舊資料、已清掉)物件欄位省略(omitempty),不是錯誤
type ActorView struct {
	ID     string    `json:"id"`
	Kind   ActorKind `json:"kind"`
	Handle string    `json:"handle"` // 使用者 = email(小寫)、角色 = 角色 id、系統 = 名稱
	Name   string    `json:"name"`   // 顯示名稱
	Email  string    `json:"email,omitempty"`
	Status string    `json:"status"` // ActorStatus*
}

// ActorView 的 Status 值
const (
	ActorStatusActive    = "active"
	ActorStatusDisabled  = "disabled"  // 使用者帳號被停用
	ActorStatusSuspended = "suspended" // 角色暫停(Active=false)
	ActorStatusRetired   = "retired"   // 角色已刪(actor 退役,舊紀錄仍指向它)
)

// ValidateActorRecord 檢查一筆要寫入的 actor(不含資料庫唯一性):id 格式與種類一致、handle 已是規範化的值、
// org 只有 agent 有且必填、owner 只有 agent 有而且要是使用者 actor id、system 的 handle 要等於 id 去掉 sys_
// (id 不限定在固定清單裡:sys_legacy_* 這類遷移才建的也合格)
func ValidateActorRecord(a ActorRecord) error {
	if !a.Kind.Valid() {
		return fmt.Errorf("不認得的 actor 種類 %q", a.Kind)
	}
	if !ValidActorIDOf(a.Kind, a.ID) {
		return fmt.Errorf("actor id %q 的格式不對(%s 要是 %s<小寫 UUID>)", a.ID, a.Kind, ActorIDPrefix(a.Kind))
	}
	h, err := CleanActorHandle(a.Kind, a.Handle)
	if err != nil {
		return err
	}
	if h != a.Handle {
		return fmt.Errorf("handle %q 不是規範化的值(應為 %q)", a.Handle, h)
	}
	switch a.Kind {
	case ActorKindAgent:
		if a.Org == "" {
			return errors.New("角色 actor 要有組織")
		}
		if a.Owner != "" && !ValidActorIDOf(ActorKindUser, a.Owner) {
			return fmt.Errorf("owner %q 要是使用者 actor id", a.Owner)
		}
	default:
		if a.Org != "" {
			return fmt.Errorf("%s actor 不能有組織", a.Kind)
		}
		if a.Owner != "" {
			return fmt.Errorf("%s actor 不能有 owner", a.Kind)
		}
	}
	if a.Kind == ActorKindSystem && a.ID != ActorIDPrefixSystem+a.Handle {
		return fmt.Errorf("系統 actor 的 handle 要等於 id 去掉 %s(id %q、handle %q)", ActorIDPrefixSystem, a.ID, a.Handle)
	}
	return nil
}
