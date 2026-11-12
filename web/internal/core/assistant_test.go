package core

import (
	"strings"
	"testing"
	"time"
)

// AI 助理的回答。輸出曾和 Swift 版 AssistResponder 用同一份範例資料逐字比對過(9 個頁面 × 33 個問題,
// 只有值班結束時間因為範例資料的時間平移方式不同而差幾分鐘)。這裡固定幾個代表性的結果。

func TestAssistFmtRelative(t *testing.T) {
	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d    float64
		want string
	}{
		{-0.4, "0秒後"}, {-1, "1秒前"}, {-59, "59秒前"}, {-60, "1分鐘前"}, {-119, "1分鐘前"}, {-3599, "59分鐘前"},
		{-3600, "1小時前"}, {-86399, "23小時前"}, {-86400, "1天前"}, {-6 * 86400, "6天前"}, {-7 * 86400, "1週前"},
		{-30 * 86400, "4週前"}, {-45 * 86400, "1個月前"}, {-400 * 86400, "1年前"},
		{0, "0秒後"}, {30, "30秒後"}, {90, "1分鐘後"}, {2700, "45分鐘後"}, {5400, "1小時後"}, {172800, "2天後"},
	} {
		got := AssistFmtRelative(now.Add(time.Duration(tc.d*float64(time.Second))), now)
		if got != tc.want {
			t.Errorf("%v 秒:%q,應該是 %q", tc.d, got, tc.want)
		}
	}
	if AssistFmtPercent(0.06, 2) != "0.06%" || AssistFmtNumber(640) != "640" || AssistFmtNumber(1250) != "1.2k" || AssistFmtNumber(2.5) != "2" {
		t.Fatal("Fmt")
	}
}

func TestAssistBriefingAndReplies(t *testing.T) {
	a := New()
	a.Mu.Lock()
	defer a.Mu.Unlock()

	b := NewAssistResponder(a, "overview").Briefing()
	if b.Headline != "有事件正在處理" || b.Tone != "red" || len(b.Lines) == 0 {
		t.Fatalf("%+v", b)
	}
	if s := NewAssistResponder(a, "monitoring").Suggestions(); len(s) != 3 || !strings.HasPrefix(s[0], "為什麼 ") {
		t.Fatal(s)
	}
	r := NewAssistResponder(a, "incidents")

	// 公告草稿:有複製與前往事件兩個按鈕
	rep := r.Reply("草擬事件公告")
	if !strings.HasPrefix(rep.Text, "以下是 INC-2041 的公告草稿") || len(rep.Actions) != 2 ||
		rep.Actions[0]["kind"] != "copy" || rep.Actions[1]["kind"] != "go" || rep.Actions[1]["focus"] != "INC-2041" {
		t.Fatalf("%+v", rep)
	}
	// 服務診斷(名稱最長者優先)
	rep = r.Reply("為什麼 payments 延遲變高?")
	if !strings.HasPrefix(rep.Text, "**payments** 目前") || rep.Actions[0]["kind"] != "acknowledge" || rep.Actions[0]["prominent"] != true {
		t.Fatalf("%+v", rep)
	}
	last := rep.Actions[len(rep.Actions)-1]
	if last["section"] != "infrastructure" || last["focus"] != "payments" || last["label"] != "查看服務" || last["key"] != "go-infrastructure-payments" {
		t.Fatal(last)
	}
	// CI 失敗:重新執行按鈕帶 runId(整數)
	rep = r.Reply("最近哪些執行失敗?為什麼?")
	if rep.Actions[0]["kind"] != "retry" || rep.Actions[0]["runId"] != 1286 || rep.Actions[0]["label"] != "重新執行 #1286" {
		t.Fatalf("%+v", rep.Actions)
	}
	// 核准
	rep = r.Reply("有什麼需要我核准?")
	if rep.Actions[0]["kind"] != "approve" || rep.Actions[1]["kind"] != "reject" {
		t.Fatalf("%+v", rep.Actions)
	}
	// 和 Swift 一樣:「Sprint」含有 pr,先被當成 PR 問題
	if rep = r.Reply("這個 Sprint 進度如何?"); !strings.Contains(rep.Text, "個 PR 未合併") {
		t.Fatal(rep.Text)
	}
	// 不會回答
	rep = NewAssistResponder(a, "cabinet").Reply("hello")
	if rep.Text != "這個問題我還不會回答。我可以從目前的資料回答告警、事件、部署、CI 失敗、伺服器資源、PR、Sprint、值班和 AI 核准相關的問題,例如 「總結目前狀況」。" || len(rep.Actions) != 0 {
		t.Fatalf("%q", rep.Text)
	}
	// 狀態總結用目前頁面的標題
	rep = NewAssistResponder(a, "pipelines").Reply("總結目前狀況")
	if !strings.HasPrefix(rep.Text, "**CI/CD:") {
		t.Fatal(rep.Text)
	}
	// go 的 focus 是 nil 時輸出 null
	rep = NewAssistResponder(a, "settings").Reply("哪些整合還沒連接?")
	if m := Object(rep).(map[string]any)["actions"].([]any)[0].(map[string]any); m["focus"] != nil || m["key"] != "go-settings-" {
		t.Fatal(m)
	}
}

func TestAssistActions(t *testing.T) {
	a := New()
	out, err := Action(a, "assistant_briefing", map[string]any{"section": "automation"})
	if err != nil {
		t.Fatal(err)
	}
	m := Object(out).(map[string]any)
	for _, k := range []string{"headline", "tone", "lines", "suggestions", "section", "sectionTitle", "focusLabel"} {
		if _, ok := m[k]; !ok {
			t.Fatal("少了", k, m)
		}
	}
	if m["sectionTitle"] != "AI 自動化" || m["focusLabel"] != nil {
		t.Fatal(m)
	}
	a.Mu.Lock()
	a.Nav.FocusID = "ALT-5121"
	a.Mu.Unlock()
	out, _ = Action(a, "assistant_briefing", map[string]any{})
	if m := Object(out).(map[string]any); m["section"] != "overview" || m["focusLabel"] != "Paddle webhook 處理延遲過高" {
		t.Fatal(m)
	}
	out, err = Action(a, "assistant_ask", map[string]any{"section": "development", "question": "  衝刺進度?  "})
	if err != nil {
		t.Fatal(err)
	}
	if m := Object(out).(map[string]any); !strings.HasPrefix(m["text"].(string), "**Sprint 47**") || m["question"] != "衝刺進度?" {
		t.Fatal(m)
	}
	if _, err := Action(a, "assistant_ask", map[string]any{"question": " "}); err == nil || err.Error() != "缺少參數 question" {
		t.Fatal(err)
	}
	if _, err := Action(a, "assistant_briefing", map[string]any{"section": "nope"}); err == nil || !strings.Contains(err.Error(), "收到「nope」") {
		t.Fatal(err)
	}
}
