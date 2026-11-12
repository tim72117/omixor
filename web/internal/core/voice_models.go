package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// 旁白語音批次的資料模型,同 Swift 版 Voice/VoiceModels.swift。
// 送出(npm run voice -- --batch)→ 定時查(--collect)→ 收完 → 人按發佈(PUT /api/admin/scripts)。
// 流程在 voice_pipeline.go。

// VoiceJobState 同 Swift 的 VoiceJobState(rawValue 一樣)
type VoiceJobState string

const (
	VoiceQueued      VoiceJobState = "queued"
	VoiceSubmitting  VoiceJobState = "submitting"
	VoiceProcessing  VoiceJobState = "processing"
	VoiceCollecting  VoiceJobState = "collecting"
	VoiceReady       VoiceJobState = "ready"
	VoicePublishing  VoiceJobState = "publishing"
	VoicePublished   VoiceJobState = "published"
	VoiceNothingToDo VoiceJobState = "nothingToDo"
	VoiceFailed      VoiceJobState = "failed"
	VoiceCanceled    VoiceJobState = "canceled"
)

func (s VoiceJobState) Label() string {
	switch s {
	case VoiceQueued:
		return "排隊中"
	case VoiceSubmitting:
		return "送出中"
	case VoiceProcessing:
		return "產生中"
	case VoiceCollecting:
		return "收結果中"
	case VoiceReady:
		return "待發佈"
	case VoicePublishing:
		return "發佈中"
	case VoicePublished:
		return "已發佈"
	case VoiceNothingToDo:
		return "沒有缺的旁白"
	case VoiceFailed:
		return "失敗"
	case VoiceCanceled:
		return "已取消"
	}
	return string(s)
}

func (s VoiceJobState) Icon() string {
	switch s {
	case VoiceQueued:
		return "clock"
	case VoiceSubmitting, VoiceCollecting, VoicePublishing:
		return "arrow.triangle.2.circlepath"
	case VoiceProcessing:
		return "waveform"
	case VoiceReady:
		return "hand.raised.fill"
	case VoicePublished:
		return "checkmark.seal.fill"
	case VoiceNothingToDo:
		return "checkmark.circle"
	case VoiceFailed:
		return "xmark.circle.fill"
	case VoiceCanceled:
		return "slash.circle"
	}
	return ""
}

// IsFinal 已經結束,不會再變
func (s VoiceJobState) IsFinal() bool {
	return slices.Contains([]VoiceJobState{VoicePublished, VoiceNothingToDo, VoiceFailed, VoiceCanceled}, s)
}

// HoldsBatch 占著「一次只能有一批」的位置
func (s VoiceJobState) HoldsBatch() bool {
	return s == VoiceSubmitting || s == VoiceProcessing || s == VoiceCollecting
}

// VoiceClip 一段旁白(url 是計算出來的,不存檔)
type VoiceClip struct {
	File string `json:"file"` // GCS 物件路徑,例如 launch/ab12cd34ef56.mp3
	Ms   int    `json:"ms"`
	Text string `json:"text"`
}

func (c VoiceClip) URL() string { return "https://storage.googleapis.com/shuttle-voice/" + c.File }

// VoiceFileChange 收結果後有變動的一份旁白清單(.voice.json)
type VoiceFileChange struct {
	Added   []VoiceClip `json:"added"` // 這次新增或換掉的段落
	Clips   int         `json:"clips"`
	Course  *string     `json:"course,omitempty"` // 從 courses_seed.json 查到這個腳本屬於哪一課
	IsNew   bool        `json:"isNew"`            // 送出前本機沒有這個檔
	Lesson  *string     `json:"lesson,omitempty"`
	Path    string      `json:"path"`              // web/src/content/live/ 底下的路徑
	Publish *string     `json:"publish,omitempty"` // nil 還沒發佈、"ok"、或錯誤訊息
	Removed int         `json:"removed"`
	Sha     string      `json:"sha"` // 收結果當下的內容雜湊
	TotalMs int         `json:"totalMs"`
}

type VoiceLogLine struct {
	Date Time   `json:"date"`
	Text string `json:"text"`
}

type VoiceBaseline struct {
	Files []string `json:"files"`
	Sha   string   `json:"sha"`
}

// VoiceJob 同 Swift 的 VoiceJob(欄位名與 Codable 一樣;nil 的選填欄位不輸出)
type VoiceJob struct {
	All         bool                     `json:"all"`
	Baseline    map[string]VoiceBaseline `json:"baseline"`
	BatchName   *string                  `json:"batchName,omitempty"`
	BatchState  *string                  `json:"batchState,omitempty"`
	By          string                   `json:"by"` // person id、"mcp" 或 "external"
	Changes     []VoiceFileChange        `json:"changes"`
	Checks      int                      `json:"checks"`
	ClipCount   *int                     `json:"clipCount,omitempty"`
	CreatedAt   Time                     `json:"createdAt"`
	Error       *string                  `json:"error,omitempty"`
	FailedClips []string                 `json:"failedClips"`
	ID          string                   `json:"id"`
	LastCheckAt *Time                    `json:"lastCheckAt,omitempty"`
	Limit       *int                     `json:"limit,omitempty"`
	Log         []VoiceLogLine           `json:"log"`
	NextCheckAt *Time                    `json:"nextCheckAt,omitempty"`
	PublishedAt *Time                    `json:"publishedAt,omitempty"`
	PublishedTo *string                  `json:"publishedTo,omitempty"`
	Scripts     []string                 `json:"scripts"`
	State       VoiceJobState            `json:"state"`
	UpdatedAt   Time                     `json:"updatedAt"`
}

// fix 把 nil 的陣列/字典補成空的(Swift 版一定會輸出 [] 與 {})
func (j *VoiceJob) fix() {
	if j.Baseline == nil {
		j.Baseline = map[string]VoiceBaseline{}
	}
	if j.Changes == nil {
		j.Changes = []VoiceFileChange{}
	}
	for i := range j.Changes {
		if j.Changes[i].Added == nil {
			j.Changes[i].Added = []VoiceClip{}
		}
	}
	if j.FailedClips == nil {
		j.FailedClips = []string{}
	}
	if j.Log == nil {
		j.Log = []VoiceLogLine{}
	}
	if j.Scripts == nil {
		j.Scripts = []string{}
	}
	for k, b := range j.Baseline {
		if b.Files == nil {
			b.Files = []string{}
			j.Baseline[k] = b
		}
	}
}

func (j VoiceJob) limitSuffix() string {
	if j.Limit == nil {
		return ""
	}
	return fmt.Sprintf("(每份前 %d 段)", *j.Limit)
}

// Title 同 Swift 的 title
func (j VoiceJob) Title() string {
	if j.All {
		return "全部腳本" + j.limitSuffix()
	}
	head := j.Scripts
	if len(head) > 3 {
		head = head[:3]
	}
	more := ""
	if len(j.Scripts) > 3 {
		more = fmt.Sprintf(" 等 %d 份", len(j.Scripts))
	}
	return strings.Join(head, "、") + more + j.limitSuffix()
}

func (j VoiceJob) TotalAddedMs() int {
	n := 0
	for _, c := range j.Changes {
		for _, a := range c.Added {
			n += a.Ms
		}
	}
	return n
}

// Note 加一行紀錄(最多留 200 行)
func (j *VoiceJob) Note(text string) {
	j.Log = append(j.Log, VoiceLogLine{Date: Now(), Text: text})
	if len(j.Log) > 200 {
		j.Log = append([]VoiceLogLine{}, j.Log[len(j.Log)-200:]...)
	}
	j.UpdatedAt = Now()
}

func voicePtr[T any](v T) *T { return &v }

func voiceTimePtr(t time.Time) *Time { return &Time{t.UTC().Truncate(time.Second)} }
