package model

import (
	"strconv"
	"strings"
)

// 待決定(decision)與需要協助(help):把「要人做的選擇、要人做的事」從 Issue 描述與留言裡搬出來,變成有狀態的結構。
// 兩種共用一張表、一個狀態機:標題、背景、來源 Issue、blocks、建立者、狀態、處理者、處理時間。
//
//	decision  有選項,要人選一個(或選其他附說明);options 至少 2 個
//	help      需要人做某件事或提供資訊(Cloud SQL 備份、Mac 上編譯、提供憑證、做一次實機驗證…),沒有選項
//
// 狀態:open → decided(help 畫面顯示「已處理」)或 withdrawn;decided 可由管理員或原處理人 reopen 回 open。
// 「回答」:decision 是選項 id(或 other + 說明);help 是 done(附完成備註與可選證據字串)或 declined(附原因)。
// 規則(誰能做什麼、可見性、稽核、來源 Issue 留言)在 service/decision.go。

const (
	// DecisionPrefix 編號 DEC-<n>(一個組織一起編號,decision 與 help 共用)
	DecisionPrefix = "DEC-"

	DecisionKindDecision = "decision"
	DecisionKindHelp     = "help"

	DecisionOpen      = "open"
	DecisionDecided   = "decided"
	DecisionWithdrawn = "withdrawn"

	// DecisionOther decision 的回答不是任何既有選項時用(要附 answerNote)
	DecisionOther = "other"
	// HelpDone、HelpDeclined help 的處理結果
	HelpDone     = "done"
	HelpDeclined = "declined"

	MaxDecisionTitle   = 200
	MaxDecisionText    = 20000
	MaxDecisionOptions = 12
)

var (
	DecisionKinds    = []string{DecisionKindDecision, DecisionKindHelp}
	DecisionStatuses = []string{DecisionOpen, DecisionDecided, DecisionWithdrawn}
)

// DecisionOption decision 的一個選項
type DecisionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// Decision 待決定 / 需要協助
type Decision struct {
	ID      string           `json:"id"` // DEC-3
	Kind    string           `json:"kind"`
	Title   string           `json:"title"`
	Body    string           `json:"body"`
	Options []DecisionOption `json:"options"` // help 一律是空陣列
	Status  string           `json:"status"`
	// Answer decision:選到的選項 id 或 "other";help:"done" 或 "declined";沒處理是空字串
	Answer     string `json:"answer"`
	AnswerNote string `json:"answerNote"`
	// Evidence help 完成時的證據字串(連結、輸出、版本號…;選填)
	Evidence string `json:"evidence,omitempty"`
	// SourceIssue 來源 Issue 的 key(例如 ISS-37;空 = 沒有);Issue 刪除時清成空
	SourceIssue string `json:"sourceIssue"`
	// Blocks 被它擋住的 Issue key 或自由字串(日後子項上線後改成 FK)
	Blocks []string `json:"blocks"`
	// CreatedBy、DecidedBy:store 與服務層內部是 actor id;對外輸出換成相容字串(人 = email、角色 = agent:<handle>;沒處理是空字串),
	// 旁邊的 CreatedByActor、DecidedByActor 是 actor 物件(ISS-36 C-3;查不到或沒有省略)
	CreatedBy      string     `json:"createdBy"`
	CreatedByActor *ActorView `json:"createdByActor,omitempty"`
	DecidedBy      string     `json:"decidedBy"`
	DecidedByActor *ActorView `json:"decidedByActor,omitempty"`
	CreatedAt      Time       `json:"createdAt"`
	DecidedAt      *Time      `json:"decidedAt,omitempty"`
	UpdatedAt      Time       `json:"updatedAt"`
}

// Number DEC-<n> 的 n(格式不對是 0)
func (d Decision) Number() int {
	if !strings.HasPrefix(d.ID, DecisionPrefix) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(d.ID, DecisionPrefix))
	if err != nil {
		return 0
	}
	return n
}

// Clone 深拷貝
func (d Decision) Clone() Decision {
	d.Options = append([]DecisionOption{}, d.Options...)
	d.Blocks = append([]string{}, d.Blocks...)
	if d.DecidedAt != nil {
		t := *d.DecidedAt
		d.DecidedAt = &t
	}
	return d
}

// DecisionQuery 列表條件(零值 = 全部)
type DecisionQuery struct {
	Status string // open、decided、withdrawn
	Kind   string // decision、help
	Issue  string // 來源 Issue(store 層是內部 id,服務層先解析成內部 id 再比對)
}

// Match 符合條件
func (q DecisionQuery) Match(d Decision) bool {
	return (q.Status == "" || d.Status == q.Status) &&
		(q.Kind == "" || d.Kind == q.Kind) &&
		(q.Issue == "" || strings.EqualFold(d.SourceIssue, q.Issue))
}

// DecisionSummary 一張 Issue 的決定摘要
type DecisionSummary struct {
	Open    int `json:"open"`
	Decided int `json:"decided"`
}
