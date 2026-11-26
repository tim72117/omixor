package core

import (
	"fmt"
	"strings"

	"omixor-core/model"
)

// AI 助理面板的回答,同 Swift 版 Features/Assistant/AssistResponder.swift(與 AssistModels.swift)。
// 規則式回應器:只從範例資料組出固定格式的文字,不連網、不呼叫模型。
//
//	Action assistant_briefing {section}           → AssistBriefingResult(目前狀況卡片 + 建議問題)
//	Action assistant_ask      {section, question} → AssistReplyResult(回答文字 + 動作按鈕)
//
// section 沒給時用目前導覽的頁面(a.Nav.Section)。格式寫在 omixor 文件櫃 DOC-32《console/docs/PORTING.md》 的 Action 表。

// AssistBriefLine 「目前狀況」卡片的一行;color 是 Swift 的顏色名稱(green、orange、red、blue、purple、indigo、teal、gray、secondary)
type AssistBriefLine struct {
	Color string `json:"color"`
	Icon  string `json:"icon"`
	Text  string `json:"text"`
}

type AssistBriefing struct {
	Headline string            `json:"headline"`
	Lines    []AssistBriefLine `json:"lines"`
	Tone     string            `json:"tone"`
}

// AssistAction 回答裡可以按的動作按鈕(同 Swift 的 AssistAction)。
//
//	kind = acknowledge {alertId, title}  確認告警
//	       approve     {approvalId}      核准
//	       reject      {approvalId}      拒絕
//	       retry       {runId}           重新執行 CI
//	       go          {section, focus}  前往頁面(focus 可能是 null)
//	       copy        {text}            複製文字
//
// key 同 Swift 的 id(按過就停用用的);prominent = 會改變狀態的動作用醒目樣式。
type AssistAction map[string]any

type AssistReply struct {
	Actions []AssistAction `json:"actions"`
	Text    string         `json:"text"`
}

var assistSections = []string{"overview", "development", "pipelines", "infrastructure", "monitoring", "incidents", "automation", "cabinet", "settings", "channels", "system"}

var assistSectionTitle = map[string]string{
	"overview": "總覽", "development": "開發進度", "pipelines": "CI/CD", "infrastructure": "伺服器與服務",
	"monitoring": "監控與告警", "incidents": "事件", "automation": "AI 自動化", "cabinet": "文件櫃", "settings": "設定",
	"channels": "對話", "system": "系統",
}

var assistSectionIcon = map[string]string{
	"overview": "square.grid.2x2", "development": "hammer", "pipelines": "arrow.triangle.branch", "infrastructure": "server.rack",
	"monitoring": "waveform.path.ecg", "incidents": "flame", "automation": "sparkles", "cabinet": "archivebox", "settings": "gearshape",
	"channels": "bubble.left.and.bubble.right", "system": "switch.2",
}

// MARK: - 動作

func assistAck(alertID, title string) AssistAction {
	return AssistAction{"kind": "acknowledge", "alertId": alertID, "title": title, "key": "ack-" + alertID,
		"label": "確認告警:" + title, "icon": "bell.badge", "prominent": true}
}

func assistApprove(id string) AssistAction {
	return AssistAction{"kind": "approve", "approvalId": id, "key": "approve-" + id, "label": "核准", "icon": "checkmark.seal", "prominent": true}
}

func assistReject(id string) AssistAction {
	return AssistAction{"kind": "reject", "approvalId": id, "key": "reject-" + id, "label": "拒絕", "icon": "xmark.seal", "prominent": false}
}

func assistRetry(runID int) AssistAction {
	return AssistAction{"kind": "retry", "runId": runID, "key": fmt.Sprintf("retry-%d", runID),
		"label": fmt.Sprintf("重新執行 #%d", runID), "icon": "arrow.clockwise", "prominent": true}
}

// assistGo focus 是 nil 時輸出 null
func assistGo(section string, focus *string, label string) AssistAction {
	f := ""
	var fv any
	if focus != nil {
		f, fv = *focus, *focus
	}
	return AssistAction{"kind": "go", "section": section, "focus": fv, "key": "go-" + section + "-" + f,
		"label": label, "icon": assistSectionIcon[section], "prominent": false}
}

func assistCopy(text string) AssistAction {
	return AssistAction{"kind": "copy", "text": text, "key": "copy-" + VoiceSha([]byte(text))[:16],
		"label": "複製文字", "icon": "doc.on.doc", "prominent": false}
}

func assistSP(s string) *string { return &s }

// MARK: - Action

// AssistBriefingResult assistant_briefing 的回傳
type AssistBriefingResult struct {
	AssistBriefing
	FocusLabel   *string  `json:"focusLabel"`
	Section      string   `json:"section"`
	SectionTitle string   `json:"sectionTitle"`
	Suggestions  []string `json:"suggestions"`
}

// AssistReplyResult assistant_ask 的回傳
type AssistReplyResult struct {
	AssistReply
	Question string `json:"question"`
	Section  string `json:"section"`
}

// assistVisibleOnly 拿掉「前往」系統設定裡關掉的頁面的按鈕(同 App 的 AssistResponder 依開關)。要拿著 Mu
func assistVisibleOnly(a *App, r AssistReply) AssistReply {
	if len(r.Actions) == 0 {
		return r
	}
	out := make([]AssistAction, 0, len(r.Actions))
	for _, x := range r.Actions {
		if k, _ := x["kind"].(string); k == "go" {
			if s, _ := x["section"].(string); s != "" && !a.SectionVisible(s) {
				continue
			}
		}
		out = append(out, x)
	}
	r.Actions = out
	return r
}

func assistSection(a *App, args map[string]any) (string, error) {
	s, _ := args["section"].(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return a.NavOf(CallerOf(args)).Section, nil
	}
	for _, x := range assistSections {
		if x == s {
			return s, nil
		}
	}
	return "", UserError{fmt.Sprintf("section 必須是 %s 之一,收到「%s」", strings.Join(assistSections, "、"), s)}
}

// assistOffErr 系統設定把 AI 助理(實驗功能)關掉時,assistant_* 動作回這個錯誤
var assistOffErr = UserError{"AI 助理在系統設定裡關閉了(實驗功能,到「系統」打開)"}

func init() {
	RegisterAction("assistant_briefing", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if !a.FeatureOn(model.FeatureAssistant) {
			return nil, assistOffErr
		}
		sec, err := assistSection(a, args)
		if err != nil {
			return nil, err
		}
		r := NewAssistResponder(a, sec)
		r.focus = a.NavOf(CallerOf(args)).FocusID
		return AssistBriefingResult{AssistBriefing: r.Briefing(), FocusLabel: r.FocusLabel(), Section: sec,
			SectionTitle: assistSectionTitle[sec], Suggestions: r.Suggestions()}, nil
	})
	RegisterAction("assistant_ask", func(a *App, args map[string]any) (any, error) {
		q, _ := args["question"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, UserError{"缺少參數 question"}
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if !a.FeatureOn(model.FeatureAssistant) {
			return nil, assistOffErr
		}
		sec, err := assistSection(a, args)
		if err != nil {
			return nil, err
		}
		// 問到關掉的功能:回答「這個功能已關閉」並給「前往系統」(系統頁永遠開著)
		if closed, ok := assistClosedReply(a, q); ok {
			return AssistReplyResult{AssistReply: closed, Question: q, Section: sec}, nil
		}
		r := NewAssistResponder(a, sec)
		r.focus = a.NavOf(CallerOf(args)).FocusID
		return AssistReplyResult{AssistReply: assistVisibleOnly(a, r.Reply(q)), Question: q, Section: sec}, nil
	})
}
