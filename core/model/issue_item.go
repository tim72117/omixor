package model

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Issue 子項(issue_item,ISS-37):一張 Issue 底下可獨立領取、有狀態、有依賴與驗收證據的小工作。
// 方案 B:獨立表 omixor_issue_items(core/store/pg_issue_items.go);規則(授權、狀態機、回寫 Issue / 任務)在 service。
//
// 欄位
//
//	ID               內部 id(UUIDv7,同 Issue 的產生方式;store 的 CreateIssueItem 沒給就發)
//	IssueID          所屬 Issue 的內部 id(Issue 永久刪除時子項跟著刪;垃圾桶裡的 Issue 子項還在)
//	Seq              Issue 內的序號,從 1 起,omixor_seq 的 `item:<issueID>` scope,刪掉的號碼不再使用
//	Key              顯示用 <Issue key>.<seq>(例如 ISS-37.2);不存,service 輸出時填
//	Title            標題(必填,最長 MaxItemTitle)
//	Status           todo | doing | blocked | done | canceled
//	Assignee         相容字串(email 或 agent:<handle>;store 內是 actor id,空 = 沒指派);旁邊的 AssigneeActor 是 actor 物件
//	DependsOn        同一 Issue 內另一個子項的 id(單一前置;不可自指、不可成環);前置被刪就清成空
//	Files            會動到的檔案(最多 MaxItemFiles 個,每個最長 MaxItemFile)
//	Acceptance       驗收條件(最長 MaxItemAcceptance)
//	Evidence         完成證據 jsonb({commit, tests, unverified[]…};最大 MaxItemEvidence 位元組,必須是 JSON 物件)
//	BlockReason      卡住的原因;WaitForKind / WaitFor 在等什麼(user_decision:DEC-n、item:同 Issue 另一子項 id、external:自由文字)
//	ClaimedBy        領取者(actor id / 相容字串)與 LockedUntil 租約到期時間(沒領取是空 / nil)
//	TaskID           綁定的 agent_task id(可空;任務與子項互不 cascade,回寫由 service 處理)
//	PromotedIssueID  升級成獨立 Issue 時,新 Issue 的內部 id(Issue 永久刪除時清成空)
//	CreatedBy…       建立者(actor id / 相容字串)、時間
//
// 狀態:todo → doing(領取)→ done;doing/todo ↔ blocked;任一(非終態)→ canceled;done / canceled 可由 service 規則重開回 todo。
// 領取規則(store 的 ClaimIssueItem):status 是 todo,或 doing 但租約過期(locked_until <= 現在)且沒被別人持有的租約。

const (
	ItemTodo     = "todo"
	ItemDoing    = "doing"
	ItemBlocked  = "blocked"
	ItemDone     = "done"
	ItemCanceled = "canceled"

	// WaitFor 種類
	WaitUserDecision = "user_decision" // WaitFor = DEC-<n>
	WaitItem         = "item"          // WaitFor = 同 Issue 另一子項的 id
	WaitExternal     = "external"      // WaitFor = 自由文字

	MaxItemTitle      = 200
	MaxItemFiles      = 30
	MaxItemFile       = 300
	MaxItemAcceptance = 4000
	MaxItemEvidence   = 16 * 1024 // 位元組
	MaxItemReason     = 1000
	MaxItemWaitFor    = 300
	MaxItemsPerIssue  = 200
)

var (
	ItemStatuses  = []string{ItemTodo, ItemDoing, ItemBlocked, ItemDone, ItemCanceled}
	ItemWaitKinds = []string{WaitUserDecision, WaitItem, WaitExternal}
)

// ValidItemStatus 是不是合法的子項狀態
func ValidItemStatus(s string) bool { return slices.Contains(ItemStatuses, s) }

// ValidWaitKind 是不是合法的等待種類(空字串 = 沒在等,也算合法)
func ValidWaitKind(k string) bool { return k == "" || slices.Contains(ItemWaitKinds, k) }

// ItemTerminal done / canceled
func ItemTerminal(s string) bool { return s == ItemDone || s == ItemCanceled }

// IssueItem Issue 子項(欄位說明見檔頭)
type IssueItem struct {
	Org     string `json:"-"`
	ID      string `json:"id"`
	IssueID string `json:"issueId"`
	Seq     int    `json:"seq"`
	Key     string `json:"key"` // service 填:<Issue key>.<seq>
	Title   string `json:"title"`
	Status  string `json:"status"`

	Assignee      string     `json:"assignee,omitempty"`
	AssigneeActor *ActorView `json:"assigneeActor,omitempty"`

	DependsOn  string          `json:"dependsOn,omitempty"`
	Files      []string        `json:"files,omitempty"`
	Acceptance string          `json:"acceptance,omitempty"`
	Evidence   json.RawMessage `json:"evidence,omitempty"`

	BlockReason string `json:"blockReason,omitempty"`
	WaitForKind string `json:"waitForKind,omitempty"`
	WaitFor     string `json:"waitFor,omitempty"`

	ClaimedBy      string     `json:"claimedBy,omitempty"`
	ClaimedByActor *ActorView `json:"claimedByActor,omitempty"`
	LockedUntil    *Time      `json:"lockedUntil,omitempty"`

	TaskID          string `json:"taskId,omitempty"`
	PromotedIssueID string `json:"promotedIssueId,omitempty"`

	CreatedBy      string     `json:"createdBy"`
	CreatedByActor *ActorView `json:"createdByActor,omitempty"`
	CreatedAt      Time       `json:"createdAt"`
	UpdatedAt      Time       `json:"updatedAt"`
	CompletedAt    *Time      `json:"completedAt,omitempty"`
}

// Clone 深拷貝(Files、Evidence、時間指標)
func (it IssueItem) Clone() IssueItem {
	it.Files = slices.Clone(it.Files)
	it.Evidence = slices.Clone(it.Evidence)
	if it.LockedUntil != nil {
		t := *it.LockedUntil
		it.LockedUntil = &t
	}
	if it.CompletedAt != nil {
		t := *it.CompletedAt
		it.CompletedAt = &t
	}
	return it
}

// Locked 現在被領取鎖住:進行中且 LockedUntil 還沒到
func (it IssueItem) Locked(now Time) bool {
	return it.Status == ItemDoing && it.LockedUntil != nil && now.Before(it.LockedUntil.Time)
}

// Clean 整理並驗證可由使用者輸入的欄位(標題與檔案去頭尾空白、檔案去空與重複、空 evidence 清掉),不合法回錯誤。
// 不檢查跨資料的規則(依賴屬於同 Issue、無環、授權):那些在 store(依賴)與 service(其餘)。
func (it *IssueItem) Clean() error {
	it.Title = strings.TrimSpace(it.Title)
	if it.Title == "" {
		return fmt.Errorf("子項標題不能空白")
	}
	if utf8.RuneCountInString(it.Title) > MaxItemTitle {
		return fmt.Errorf("子項標題最長 %d 字", MaxItemTitle)
	}
	if !ValidItemStatus(it.Status) {
		return fmt.Errorf("子項狀態不合法:%q(%s)", it.Status, strings.Join(ItemStatuses, "、"))
	}
	if !ValidWaitKind(it.WaitForKind) {
		return fmt.Errorf("等待種類不合法:%q(%s)", it.WaitForKind, strings.Join(ItemWaitKinds, "、"))
	}
	if (it.WaitForKind == "") != (it.WaitFor == "") {
		return fmt.Errorf("等待種類與等待對象要一起給或一起不給")
	}
	files := make([]string, 0, len(it.Files))
	for _, f := range it.Files {
		f = strings.TrimSpace(f)
		if f == "" || slices.Contains(files, f) {
			continue
		}
		if utf8.RuneCountInString(f) > MaxItemFile {
			return fmt.Errorf("檔案路徑最長 %d 字", MaxItemFile)
		}
		files = append(files, f)
	}
	if len(files) > MaxItemFiles {
		return fmt.Errorf("檔案最多 %d 個", MaxItemFiles)
	}
	it.Files = files
	if utf8.RuneCountInString(it.Acceptance) > MaxItemAcceptance {
		return fmt.Errorf("驗收條件最長 %d 字", MaxItemAcceptance)
	}
	if utf8.RuneCountInString(it.BlockReason) > MaxItemReason {
		return fmt.Errorf("卡住原因最長 %d 字", MaxItemReason)
	}
	if utf8.RuneCountInString(it.WaitFor) > MaxItemWaitFor {
		return fmt.Errorf("等待對象最長 %d 字", MaxItemWaitFor)
	}
	if err := CleanItemEvidence(&it.Evidence); err != nil {
		return err
	}
	if it.DependsOn != "" && it.DependsOn == it.ID {
		return fmt.Errorf("子項不能依賴自己")
	}
	return nil
}

// CleanItemEvidence 驗證 evidence:空(含 null、{})清成 nil;否則要是 JSON 物件且不超過 MaxItemEvidence 位元組;並壓縮空白
func CleanItemEvidence(e *json.RawMessage) error {
	s := strings.TrimSpace(string(*e))
	if s == "" || s == "null" || s == "{}" {
		*e = nil
		return nil
	}
	if len(s) > MaxItemEvidence {
		return fmt.Errorf("證據最大 %d 位元組", MaxItemEvidence)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return fmt.Errorf("證據要是 JSON 物件")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	*e = b
	return nil
}
