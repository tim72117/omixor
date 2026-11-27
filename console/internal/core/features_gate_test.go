package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// sysForceFeatures 測試用:把目前組織的功能開關換成「全部功能(系統層全部開放,含實驗)都開,off 列的關」,
// 標成剛從核心讀到。不打核心
func sysForceFeatures(t *testing.T, a *App, off ...string) {
	t.Helper()
	closed := map[string]bool{}
	for _, k := range off {
		closed[k] = true
	}
	v := &service.SystemSettingsView{Edition: model.EditionDev, CanEdit: true}
	for _, f := range model.Features {
		on := !closed[f.Key] && !closed[f.Parent]
		v.Features = append(v.Features, service.FeatureState{Feature: f, Enabled: on, On: !closed[f.Key]})
	}
	st := sysOf(a)
	a.Mu.Lock()
	st.mu.Lock()
	st.ensureKey(a.CurrentOrgID())
	st.view, st.source, st.err = v, SysSourceCore, ""
	st.mu.Unlock()
	a.Mu.Unlock()
}

// 徽章只算開著的分頁:關掉的實驗分頁(告警、待核准、執行紀錄)的範例資料不算;頁面整個關掉就不顯示
func TestBadgesSkipClosedFeatures(t *testing.T) {
	a := New()
	sysForceFeatures(t, a, "monitoring.alerts", "automation.inbox", "pipelines.runs")
	v, _ := View(a, "badges")
	b := v.(map[string]any)
	for _, k := range []string{"monitoring", "automation", "pipelines"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s 的分頁關了,不應該有徽章:%v", k, b)
		}
	}
	if b["incidents"] != float64(2) {
		t.Errorf("事件開著,徽章應該是 2:%v", b)
	}
	// 頁面整個關掉
	sysForceFeatures(t, a, "incidents")
	v, _ = View(a, "badges")
	if _, ok := v.(map[string]any)["incidents"]; ok {
		t.Errorf("事件頁關了,不應該有徽章:%v", v)
	}
}

// AI 助理:關掉的功能不建議;問到時回答「這個功能已關閉」並給「前往系統」
func TestAssistClosedFeatures(t *testing.T) {
	a := New()
	sysForceFeatures(t, a, "monitoring.alerts", "infrastructure.services", "development.board")
	a.Mu.Lock()
	s := NewAssistResponder(a, "monitoring").Suggestions()
	a.Mu.Unlock()
	for _, q := range s {
		if k := assistTopicFeature(q); k == "monitoring.alerts" || k == "infrastructure.services" {
			t.Fatalf("關掉的功能不應該建議:%q(%s)", q, k)
		}
	}
	a.Mu.Lock()
	all := NewAssistResponder(a, "monitoring").allSuggestions()
	a.Mu.Unlock()
	if len(s) >= len(all) {
		t.Fatalf("應該少掉關掉的建議:%v / %v", s, all)
	}

	out, err := Action(a, "assistant_ask", map[string]any{"section": "development", "question": "衝刺進度?"})
	if err != nil {
		t.Fatal(err)
	}
	m := Object(out).(map[string]any)
	text, _ := m["text"].(string)
	board, ok := model.FeatureByKey("development.board")
	if !ok {
		t.Fatal("核心沒登記 development.board")
	}
	if !strings.Contains(text, "這個功能已關閉") || !strings.Contains(text, board.Name) {
		t.Fatalf("問到關掉的功能要說明:%v", m)
	}
	acts, _ := m["actions"].([]any)
	if len(acts) != 1 || acts[0].(map[string]any)["section"] != "system" || acts[0].(map[string]any)["label"] != "前往系統" {
		t.Fatalf("要給「前往系統」:%v", acts)
	}
	// 開著的功能照常回答
	out, _ = Action(a, "assistant_ask", map[string]any{"section": "incidents", "question": "總結進行中的事件"})
	if text, _ := Object(out).(map[string]any)["text"].(string); strings.Contains(text, "已關閉") {
		t.Fatalf("事件開著不應該說關閉:%s", text)
	}
	// 和 App 的 topicFeature 一致的幾個分派
	for q, want := range map[string]string{
		"為什麼 api 延遲變高?": "infrastructure.services", "草擬事件公告": "", "今天有哪些部署?": "pipelines.deployments",
		"有什麼需要我核准?": "automation.inbox", "哪個自動化最常失敗?": "automation.runs", "列出觸發中的告警": "monitoring.alerts",
		"總結進行中的事件": "incidents.list", "誰在值班?": "incidents.oncall", "AI 自動化省了多少時間?": "automation.flows", "總結目前狀況": "",
	} {
		if got := assistTopicFeature(q); got != want {
			t.Errorf("%q → %q,應該是 %q", q, got, want)
		}
	}
}

// 系統設定關掉「自動執行」:不領新任務;畫面(agent_runner)說明
func TestRunnerStopsWhenFeatureOff(t *testing.T) {
	a := connTestApp(t)
	sysForceFeatures(t, a, runnerFeature)
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	// 設定檔寫了 AutoRun 也不看(不會讀設定、不會領任務)
	if _, err := SaveRunnerConfig(org, "g1", AgentRunConfig{AutoRun: true}); err != nil {
		t.Fatal(err)
	}
	if a.runnerTick(context.Background(), org) {
		t.Fatal("關掉自動執行時不應該領任務")
	}
	a.Mu.Lock()
	v := a.runnerView()
	a.Mu.Unlock()
	if !v.Disabled || !strings.Contains(v.DisabledReason, "系統設定") {
		t.Fatalf("畫面要說明關閉了:%+v", v)
	}
	sysForceFeatures(t, a)
	a.Mu.Lock()
	v = a.runnerView()
	a.Mu.Unlock()
	if v.Disabled {
		t.Fatal("打開後不應該標關閉")
	}
}

// system_save 帶畫面顯示的組織:和目前的組織不同時拒絕(409),核心沒有被呼叫
func TestSystemSaveRejectsOtherOrg(t *testing.T) {
	a := connTestApp(t)
	calls := 0
	systemUseBackend(t, func() (backend.Backend, error) { calls++; return nil, nil })
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	_, err := Action(a, "system_save", map[string]any{"org": org + "-other", "features": map[string]any{"cabinet": false}})
	var se StatusError
	if err == nil || !errors.As(err, &se) || se.Status != 409 || !strings.Contains(se.Message, "重新整理") {
		t.Fatalf("應該 409:%v", err)
	}
	if calls != 0 {
		t.Fatal("組織不同時不應該呼叫核心")
	}
	a.Mu.Lock()
	v := a.systemViewData()
	a.Mu.Unlock()
	if v.Org != org {
		t.Fatalf("system view 要帶目前的組織:%q", v.Org)
	}
}

// 背景重讀:比較晚開始的先回來,較早那次的(過期)結果不能蓋掉;讀的期間換了組織也不套用
func TestSystemRefreshStaleResult(t *testing.T) {
	a := connTestApp(t)
	release := make(chan struct{})
	var mu sync.Mutex
	n := 0
	viewWith := func(on bool) *service.SystemSettingsView {
		v := SystemFallback()
		for i := range v.Features {
			if v.Features[i].Key == "cabinet" {
				v.Features[i].Enabled, v.Features[i].On = on, on
			}
		}
		return v
	}
	systemUseBackend(t, func() (backend.Backend, error) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			return sysSlowBackend{wait: release, view: viewWith(false)}, nil // 第一次(舊):慢,cabinet 關
		}
		return sysSlowBackend{view: viewWith(true)}, nil // 第二次(新):馬上回,cabinet 開
	})
	done := make(chan bool)
	go func() { done <- a.SystemRefresh() }()
	// 等第一次已經開始讀
	for i := 0; ; i++ {
		mu.Lock()
		started := n == 1
		mu.Unlock()
		if started {
			break
		}
		if i > 200 {
			t.Fatal("第一次沒有開始")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.SystemRefresh() // 第二次:套用
	close(release)
	if <-done {
		t.Fatal("過期的結果不應該套用")
	}
	if !a.FeatureEnabledNow("cabinet") {
		t.Fatal("新的結果(cabinet 開)被舊的蓋掉了")
	}
}

type sysSlowBackend struct {
	backend.Unsupported
	wait chan struct{}
	view *service.SystemSettingsView
}

func (b sysSlowBackend) GetSystemSettings(string) (*service.SystemSettingsView, error) {
	if b.wait != nil {
		<-b.wait
	}
	return b.view, nil
}

// withWebOnlyTab 測試用:暫時登記 web 獨有分頁(像選用模組 integrations 登記開發進度 › GitLab),測完拿掉
func withWebOnlyTab(t *testing.T, section, tab string) {
	t.Helper()
	saved := WebOnlyTabs()
	RegisterWebOnlyTab(section, tab)
	t.Cleanup(func() { webOnlyTabs = saved })
}

// 開發進度有 web 獨有的 GitLab 分頁(選用模組 integrations 登記的):核心登記的分頁全關、頁面自己開著時頁面照樣顯示(只用 GitLab);
// 頁面自己關掉才不顯示
func TestDevelopmentVisibleWithOnlyGitLab(t *testing.T) {
	withWebOnlyTab(t, "development", "gitlab")
	a := New()
	off := []string{}
	for _, f := range model.Features {
		if f.Parent == "development" {
			off = append(off, f.Key)
		}
	}
	sysForceFeatures(t, a, off...)
	if !a.FeatureEnabledNow("development") {
		t.Fatal("只剩 GitLab 時開發進度要顯示")
	}
	if a.FeatureEnabledNow("development.issues") {
		t.Fatal("Issue 分頁關了")
	}
	a.Mu.Lock()
	to, notice := a.GuardSection("development")
	a.Mu.Unlock()
	if to != "development" || notice != "" {
		t.Fatalf("不應該被擋:%s %s", to, notice)
	}
	sysForceFeatures(t, a, "development")
	if a.FeatureEnabledNow("development") {
		t.Fatal("頁面自己關掉就不顯示")
	}
}
