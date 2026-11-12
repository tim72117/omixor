package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Facebook 粉絲專頁(Meta Graph API):設定、草稿與畫面上的資料。同 Swift 版 Facebook/FacebookModels.swift。
// 權杖(使用者權杖、各專頁的 Page access token)存在 core.Vault,帳號名稱帶組織前綴(<前綴>facebook:token、<前綴>facebook:page:<id>);
// 專頁清單、要管理哪些專頁與草稿存在目前組織資料夾的 facebook.json,解碼寬鬆。

// FBPage 粉絲專頁(不含權杖;欄位依字母排序,同 Swift 的 sortedKeys)
type FBPage struct {
	Category       string `json:"category"`
	FanCount       *int   `json:"fanCount,omitempty"`
	FollowersCount *int   `json:"followersCount,omitempty"`
	ID             string `json:"id"`
	Link           string `json:"link"`
	// Managed 要在 Ops 管理這個專頁
	Managed bool   `json:"managed"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// FBDraftStatus:draft、scheduled、published
const (
	FBDraftDraft     = "draft"
	FBDraftScheduled = "scheduled"
	FBDraftPublished = "published"
)

var FBDraftStatuses = []string{FBDraftDraft, FBDraftScheduled, FBDraftPublished}

// FBStatusLabel 同 Swift 的 FBDraftStatus.label
func FBStatusLabel(s string) string {
	switch s {
	case FBDraftScheduled:
		return "已排程"
	case FBDraftPublished:
		return "已發佈"
	}
	return "草稿"
}

// FBDraft 貼文草稿。AI(角色)透過 MCP 只能新增或修改草稿;發佈與排程只能由人在畫面上按
type FBDraft struct {
	Author    string `json:"author"` // person id、"mcp" 或 "agent:<角色 id>"
	CreatedAt Time   `json:"createdAt"`
	ID        string `json:"id"` // FD-1
	ImageURL  string `json:"imageURL"`
	Link      string `json:"link"`
	// LocalImage 本機圖片的完整路徑(只有人在畫面上能選)
	LocalImage string  `json:"localImage"`
	Message    string  `json:"message"`
	PageID     string  `json:"pageId"`
	PostID     *string `json:"postId,omitempty"`
	// ProposedTime 建議的發佈時間(角色可以提議;真正排程由人決定)
	ProposedTime *Time   `json:"proposedTime,omitempty"`
	PublishedAt  *Time   `json:"publishedAt,omitempty"`
	PublishedBy  *string `json:"publishedBy,omitempty"`
	ScheduledFor *Time   `json:"scheduledFor,omitempty"`
	// Source 從哪裡帶入,例如 doc:DOC-3、task:T-5
	Source    string `json:"source"`
	Status    string `json:"status"`
	UpdatedAt Time   `json:"updatedAt"`
}

// NewFBDraft 同 Swift 的 FBDraft(id:author:)
func NewFBDraft(id, author string) FBDraft {
	now := nowPrecise()
	return FBDraft{ID: id, Author: author, Status: FBDraftDraft, CreatedAt: now, UpdatedAt: now}
}

// Number 同 Swift 的 number:id 裡的數字
func (d FBDraft) Number() int {
	var b strings.Builder
	for _, r := range d.ID {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	n, _ := strconv.Atoi(b.String())
	return n
}

func (d FBDraft) IsEmpty() bool {
	for _, s := range []string{d.Message, d.Link, d.ImageURL, d.LocalImage} {
		if strings.TrimSpace(s) != "" {
			return false
		}
	}
	return true
}

// TitleLine 同 Swift 的 titleLine:第一行(超過 40 字截斷),沒有文字就是連結或(圖片)/(空白草稿)
func (d FBDraft) TitleLine() string {
	first := ""
	for _, line := range strings.Split(d.Message, "\n") {
		if line != "" { // Swift 的 split 會略過空的段落
			first = line
			break
		}
	}
	if first != "" {
		r := []rune(first)
		if len(r) > 40 {
			return string(r[:40]) + "…"
		}
		return first
	}
	if d.Link == "" {
		if d.ImageURL == "" && d.LocalImage == "" {
			return "(空白草稿)"
		}
		return "(圖片)"
	}
	return d.Link
}

func cloneFBDraft(d FBDraft) FBDraft {
	n := d
	if d.PostID != nil {
		n.PostID = strPtr(*d.PostID)
	}
	if d.PublishedBy != nil {
		n.PublishedBy = strPtr(*d.PublishedBy)
	}
	for _, p := range []**Time{&n.ProposedTime, &n.PublishedAt, &n.ScheduledFor} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return n
}

// MARK: 寬鬆解碼(同 Swift 的 init(from:):型別不對或缺欄位用預設值)

type rawObj map[string]json.RawMessage

func (o rawObj) str(k string) (string, bool) {
	var s string
	if v, ok := o[k]; ok && json.Unmarshal(v, &s) == nil && string(v) != "null" {
		return s, true
	}
	return "", false
}

func (o rawObj) strOr(k, def string) string {
	if s, ok := o.str(k); ok {
		return s
	}
	return def
}

func (o rawObj) boolOr(k string, def bool) bool {
	var b bool
	if v, ok := o[k]; ok && string(v) != "null" && json.Unmarshal(v, &b) == nil {
		return b
	}
	return def
}

func (o rawObj) int(k string) *int {
	var f float64
	if v, ok := o[k]; ok && string(v) != "null" && json.Unmarshal(v, &f) == nil && f == float64(int(f)) {
		n := int(f)
		return &n
	}
	return nil
}

func (o rawObj) time(k string) *Time {
	s, ok := o.str(k)
	if !ok {
		return nil
	}
	t, err := ParseTime(s)
	if err != nil {
		return nil
	}
	return &Time{t}
}

func (p *FBPage) UnmarshalJSON(b []byte) error {
	var o rawObj
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	id := o.strOr("id", "")
	*p = FBPage{ID: id, Name: o.strOr("name", id), Category: o.strOr("category", ""), Link: o.strOr("link", ""),
		Picture: o.strOr("picture", ""), FanCount: o.int("fanCount"), FollowersCount: o.int("followersCount"),
		Managed: o.boolOr("managed", true)}
	return nil
}

func (d *FBDraft) UnmarshalJSON(b []byte) error {
	var o rawObj
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	status := o.strOr("status", FBDraftDraft)
	if !iaOneOf(status, FBDraftStatuses) {
		status = FBDraftDraft
	}
	created := nowPrecise()
	if t := o.time("createdAt"); t != nil {
		created = *t
	}
	updated := created
	if t := o.time("updatedAt"); t != nil {
		updated = *t
	}
	*d = FBDraft{ID: o.strOr("id", ""), PageID: o.strOr("pageId", ""), Message: o.strOr("message", ""), Link: o.strOr("link", ""),
		ImageURL: o.strOr("imageURL", ""), LocalImage: o.strOr("localImage", ""), ProposedTime: o.time("proposedTime"),
		Source: o.strOr("source", ""), Author: o.strOr("author", "tim"), Status: status,
		ScheduledFor: o.time("scheduledFor"), PublishedAt: o.time("publishedAt"), CreatedAt: created, UpdatedAt: updated}
	if s, ok := o.str("postId"); ok {
		d.PostID = &s
	}
	if s, ok := o.str("publishedBy"); ok {
		d.PublishedBy = &s
	}
	return nil
}

// FBDefaultVersion Graph API 版本(Meta 每個版本約支援兩年)
const FBDefaultVersion = "v24.0"

// FBSettings facebook.json
type FBSettings struct {
	APIVersion string    `json:"apiVersion"`
	Drafts     []FBDraft `json:"drafts"`
	// Mode 權杖怎麼來的:oauth(Meta 登入)或 page_token(直接貼)
	Mode  string   `json:"mode"`
	Pages []FBPage `json:"pages"`
}

func DefaultFBSettings() FBSettings {
	return FBSettings{APIVersion: FBDefaultVersion, Drafts: []FBDraft{}, Pages: []FBPage{}}
}

func (s *FBSettings) UnmarshalJSON(b []byte) error {
	var o rawObj
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*s = DefaultFBSettings()
	if v := o.strOr("apiVersion", ""); FBIsVersion(v) {
		s.APIVersion = v
	}
	s.Mode = o.strOr("mode", "")
	var pages []json.RawMessage
	if json.Unmarshal(o["pages"], &pages) == nil {
		for _, raw := range pages {
			var p FBPage
			if json.Unmarshal(raw, &p) != nil { // 元素不是物件:Swift 整個陣列解碼失敗
				s.Pages = []FBPage{}
				break
			}
			if p.ID != "" {
				s.Pages = append(s.Pages, p)
			}
		}
	}
	var drafts []json.RawMessage
	if json.Unmarshal(o["drafts"], &drafts) == nil {
		for _, raw := range drafts {
			var d FBDraft
			if json.Unmarshal(raw, &d) != nil {
				s.Drafts = []FBDraft{}
				break
			}
			if d.ID != "" {
				s.Drafts = append(s.Drafts, d)
			}
		}
	}
	return nil
}

// ManagedPages 管理中的專頁
func (s FBSettings) ManagedPages() []FBPage {
	out := []FBPage{}
	for _, p := range s.Pages {
		if p.Managed {
			out = append(out, p)
		}
	}
	return out
}

func cloneFBSettings(s FBSettings) FBSettings {
	n := s
	n.Pages = append([]FBPage{}, s.Pages...)
	n.Drafts = make([]FBDraft, len(s.Drafts))
	for i, d := range s.Drafts {
		n.Drafts[i] = cloneFBDraft(d)
	}
	return n
}

// MARK: - 從 Graph API 讀到的資料(畫面與 MCP 用,不存檔)

// FBMetric 一個成效指標。Meta 會淘汰指標,取不到時 status 是 unavailable(沒有資料)或 deprecated(已淘汰 / 不再支援)
type FBMetric struct {
	Key       string
	Label     string
	Metric    *string // 實際用到的 Graph 指標名稱
	Status    string  // ok、unavailable、deprecated
	Total     *float64
	Values    []FBPoint
	Breakdown map[string]float64 // 例如心情:like、love…
	Note      *string
}

type FBPoint struct {
	Date  time.Time
	Value float64
}

// StatusLabel 同 Swift 的 statusLabel
func (m FBMetric) StatusLabel() string {
	switch m.Status {
	case "unavailable":
		return "無資料"
	case "deprecated":
		return "已淘汰"
	}
	return ""
}

type FBPost struct {
	ID            string
	PageID        string
	Message       string
	CreatedTime   *time.Time
	ScheduledTime *time.Time
	Permalink     string
	Picture       string
	IsPublished   bool
	StatusType    string
	Reactions     *int
	Comments      *int
	Shares        *int
	Insights      []FBMetric
}

// Engagement 互動 = 心情 + 留言 + 分享(貼文欄位,不受 insights 淘汰影響)
func (p FBPost) Engagement() *int {
	if p.Reactions == nil && p.Comments == nil && p.Shares == nil {
		return nil
	}
	n := 0
	for _, v := range []*int{p.Reactions, p.Comments, p.Shares} {
		if v != nil {
			n += *v
		}
	}
	return &n
}

type FBPageInsights struct {
	PageID  string
	Period  string
	Since   time.Time
	Until   time.Time
	Metrics []FBMetric
}

// FBSnapshot 畫面上的 Facebook 狀態
type FBSnapshot struct {
	Posts     map[string][]FBPost
	Scheduled map[string][]FBPost
	Insights  map[string]FBPageInsights
	Errors    map[string]string
	Error     *string
	Busy      bool
	FetchedAt *time.Time
	// TokenExpired 權杖過期(code 190):要請人重新連線
	TokenExpired bool
}

func emptyFBSnapshot() FBSnapshot {
	return FBSnapshot{Posts: map[string][]FBPost{}, Scheduled: map[string][]FBPost{}, Insights: map[string]FBPageInsights{}, Errors: map[string]string{}}
}

// FBError Graph API 的錯誤;Error() 等於 Swift 的 errorDescription
type FBError struct {
	Message string
	Code    int
	Subcode int
	Status  int
}

func (e FBError) IsTokenExpired() bool { return e.Code == 190 || e.Code == 102 }

// IsInvalidMetric 指標不存在或已淘汰:(#100) The value must be a valid insights metric
func (e FBError) IsInvalidMetric() bool {
	return e.Code == 100 && (strings.Contains(strings.ToLower(e.Message), "metric") || e.Subcode == 0)
}

func (e FBError) Error() string {
	if e.IsTokenExpired() {
		return fmt.Sprintf("Facebook 權杖已過期或被撤銷(code %d),請到 omixor 的 AI 自動化 › 粉絲專頁 重新連線。%s", e.Code, e.Message)
	}
	if e.Code > 0 {
		return fmt.Sprintf("Facebook:%s(code %d)", e.Message, e.Code)
	}
	return e.Message
}

func fbErr(msg string) error { return FBError{Message: msg} }

// MARK: - JSON(同 Swift MCPHandler 的 fbPageJSON / fbMetricJSON / fbPostJSON / fbDraftJSON)

func optInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func optStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func optISO(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ISO(*t)
}

func optTimeISO(t *Time) any {
	if t == nil {
		return nil
	}
	return ISO(t.Time)
}

// FBPageJSON hasToken 由呼叫的人先查好(鑰匙圈不能在鎖內讀)
func FBPageJSON(p FBPage, hasToken bool) map[string]any {
	return map[string]any{"id": p.ID, "name": p.Name, "category": p.Category, "link": p.Link, "picture": p.Picture,
		"fanCount": optInt(p.FanCount), "followersCount": optInt(p.FollowersCount), "managed": p.Managed, "hasToken": hasToken}
}

func FBMetricJSON(m FBMetric) map[string]any {
	values := []any{}
	for _, v := range m.Values {
		values = append(values, map[string]any{"date": ISO(v.Date), "value": v.Value})
	}
	var total, breakdown any
	if m.Total != nil {
		total = *m.Total
	}
	if m.Breakdown != nil {
		breakdown = m.Breakdown
	}
	return map[string]any{"key": m.Key, "label": m.Label, "metric": optStr(m.Metric), "status": m.Status, "total": total,
		"values": values, "breakdown": breakdown, "note": optStr(m.Note)}
}

func FBPostJSON(p FBPost) map[string]any {
	ins := []any{}
	for _, m := range p.Insights {
		ins = append(ins, FBMetricJSON(m))
	}
	return map[string]any{"id": p.ID, "pageId": p.PageID, "message": p.Message, "createdTime": optISO(p.CreatedTime),
		"scheduledTime": optISO(p.ScheduledTime), "permalink": p.Permalink, "picture": p.Picture,
		"isPublished": p.IsPublished, "statusType": p.StatusType, "reactions": optInt(p.Reactions),
		"comments": optInt(p.Comments), "shares": optInt(p.Shares), "engagement": optInt(p.Engagement()), "insights": ins}
}

// FBDraftJSON 拿著鎖呼叫
func FBDraftJSON(a *App, d FBDraft) map[string]any {
	var pageName, localName any
	if p := FBFindPage(a, d.PageID); p != nil {
		pageName = p.Name
	}
	if d.LocalImage != "" {
		localName = filepath.Base(d.LocalImage)
	}
	return map[string]any{"id": d.ID, "pageId": d.PageID, "pageName": pageName, "message": d.Message, "link": d.Link,
		"imageURL": d.ImageURL, "hasLocalImage": d.LocalImage != "", "localImageName": localName,
		"proposedTime": optTimeISO(d.ProposedTime), "source": d.Source, "author": d.Author,
		"authorName": FBAuthorName(a, d.Author), "status": d.Status, "postId": optStr(d.PostID),
		"scheduledFor": optTimeISO(d.ScheduledFor), "publishedAt": optTimeISO(d.PublishedAt),
		"publishedBy": optStr(d.PublishedBy), "createdAt": ISO(d.CreatedAt.Time), "updatedAt": ISO(d.UpdatedAt.Time)}
}

func fbInsightsJSON(r FBPageInsights) map[string]any {
	ms := []any{}
	for _, m := range r.Metrics {
		ms = append(ms, FBMetricJSON(m))
	}
	return map[string]any{"pageId": r.PageID, "period": r.Period, "since": ISO(r.Since), "until": ISO(r.Until), "metrics": ms}
}
