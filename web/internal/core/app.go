package core

import (
	"encoding/json"
	"regexp"
	"sync"
	"time"

	"omixor-web/internal/templates"
)

// App 是全域狀態,對應 Swift 版的 AppState。
//
// 併發規則(很重要):
//   - 讀寫任何欄位前都要拿 Mu(對應 Swift 的 @MainActor)。
//   - 呼叫外部網路(GitHub、GCP、evoleap…)時不能拿著 Mu:先在鎖內讀好需要的設定,放開鎖再發請求,回來再拿鎖寫結果。
//   - 改完資料呼叫 Changed(主題…),畫面會收到通知重新讀。Changed 不需要拿鎖。
type App struct {
	Mu sync.Mutex

	// 目前登入的使用者(和 Swift 版一樣固定是 tim)
	Me string

	// 範例資料:和 Swift 版 MockData 一模一樣的 JSON 物件(從 templates/seed/ 載入,時間平移到現在)。
	// key 是 ops_list 的集合名稱(people、services…);sprint 是單一物件,放在 Sprint。
	C      map[string][]map[string]any
	Sprint map[string]any

	// 導覽狀態(ops_navigate 會改)
	Nav Nav

	// MCP 伺服器狀態(設定頁顯示)
	MCP MCPStatus

	// 各功能的狀態,由各自的檔案定義與初始化(issues.go、agents.go…)
	Ext map[string]any

	// 目前這個動作是誰做的(畫面有登入時是登入者);見 As
	acting *Actor

	version uint64
	subsMu  sync.Mutex
	subs    map[chan Event]struct{}
}

type Nav struct {
	Section   string `json:"section"`
	FocusID   string `json:"focusId,omitempty"`
	Env       string `json:"env"`
	TimeRange string `json:"timeRange"`
}

type MCPStatus struct {
	State         string `json:"state"` // stopped、starting、listening、failed
	Error         string `json:"error,omitempty"`
	Port          int    `json:"port"`
	Requests      int    `json:"requests"`
	LastRequestAt *Time  `json:"lastRequestAt,omitempty"`
	LastClient    string `json:"lastClient,omitempty"`
	LastTool      string `json:"lastTool,omitempty"`
}

// Event 送給畫面的變更通知
type Event struct {
	Version uint64   `json:"version"`
	Topics  []string `json:"topics"`
}

// 各功能啟動時要做的事(載入檔案、開計時器)。各檔案在 init() 裡用 OnStart 登記。
var starters []func(*App)

// OnStart 登記 app 啟動時要跑的初始化(依登記順序執行,執行時沒有拿 Mu)
func OnStart(f func(*App)) { starters = append(starters, f) }

// New 建立 app 並載入範例資料。不會啟動計時器與各功能,要另外呼叫 Start。
func New() *App {
	a := &App{
		Me:   "tim",
		C:    map[string][]map[string]any{},
		Nav:  Nav{Section: "overview", Env: "production", TimeRange: "24h"},
		MCP:  MCPStatus{State: "stopped", Port: 3765},
		Ext:  map[string]any{},
		subs: map[chan Event]struct{}{},
	}
	a.loadSeed()
	if pgPool != nil {
		a.loadPersisted()
	}
	return a
}

// Start 跑各功能的初始化
func (a *App) Start() {
	for _, f := range starters {
		f(a)
	}
}

var isoRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)

func (a *App) loadSeed() {
	exportedAt, collections := templates.Seed() // 範本 templates/seed/
	exported, _ := ParseTime(exportedAt)
	shift := time.Since(exported).Truncate(time.Second)
	for name, raw := range collections {
		var v any
		_ = json.Unmarshal(raw, &v)
		v = shiftTimes(v, shift)
		if name == "sprint" {
			a.Sprint, _ = v.(map[string]any)
			continue
		}
		list, _ := v.([]any)
		items := make([]map[string]any, 0, len(list))
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				items = append(items, m)
			}
		}
		a.C[name] = items
	}
}

// shiftTimes 把所有 ISO 時間字串往後平移(範例資料的時間都是相對於匯出時刻)
func shiftTimes(v any, d time.Duration) any {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			x[k] = shiftTimes(vv, d)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = shiftTimes(vv, d)
		}
		return x
	case string:
		if isoRe.MatchString(x) {
			if t, err := ParseTime(x); err == nil {
				return ISO(t.Add(d))
			}
		}
		return x
	}
	return v
}

// MARK: 變更通知

// Changed 通知畫面這些主題的資料變了
func (a *App) Changed(topics ...string) {
	markDirty(topics)
	a.subsMu.Lock()
	a.version++
	ev := Event{Version: a.version, Topics: topics}
	for ch := range a.subs {
		select {
		case ch <- ev:
		default: // 畫面來不及收就丟掉,下一次通知會帶最新版本
		}
	}
	a.subsMu.Unlock()
}

func (a *App) Version() uint64 {
	a.subsMu.Lock()
	defer a.subsMu.Unlock()
	return a.version
}

func (a *App) Subscribe() chan Event {
	ch := make(chan Event, 32)
	a.subsMu.Lock()
	a.subs[ch] = struct{}{}
	a.subsMu.Unlock()
	return ch
}

func (a *App) Unsubscribe(ch chan Event) {
	a.subsMu.Lock()
	delete(a.subs, ch)
	a.subsMu.Unlock()
}

// MARK: 共用查詢(呼叫時要拿著 Mu)

// Find 依 id 找集合裡的一筆;id 比對 JSON 的 id 欄位(數字也行)
func (a *App) Find(collection, id string) (int, map[string]any) {
	for i, it := range a.C[collection] {
		if s, ok := ScalarString(it["id"]); ok && s == id {
			return i, it
		}
	}
	return -1, nil
}

// NameResolvers:各功能可以登記把 ai id 轉成名稱的方法(例如虛擬角色)
var aiNameResolvers []func(a *App, id string) (string, bool)

func OnAIName(f func(a *App, id string) (string, bool)) { aiNameResolvers = append(aiNameResolvers, f) }

// Name 顯示用名稱,同 Swift 的 name(of:)
func (a *App) Name(actor Actor) string {
	switch actor.Kind {
	case "person":
		if _, p := a.Find("people", actor.ID); p != nil {
			if n, ok := p["name"].(string); ok {
				return n
			}
		}
		return actor.ID
	case "ai":
		if _, au := a.Find("automations", actor.ID); au != nil {
			if n, ok := au["name"].(string); ok {
				return n
			}
		}
		for _, f := range aiNameResolvers {
			if n, ok := f(a, actor.ID); ok {
				return n
			}
		}
		if actor.ID == MCPActorID {
			return "MCP 用戶端"
		}
		return "AI"
	}
	return "系統"
}

// Log 寫一筆動態(最新的在最前面),同 Swift 的 log(_:_:_:_:)。呼叫時要拿著 Mu。
func (a *App) Log(actor Actor, icon, text, section string) {
	item := map[string]any{
		"id": UUID(), "date": ISO(time.Now()), "actor": Object(actor),
		"icon": icon, "text": text, "section": section,
	}
	// 動態屬於目前組織(預設組織不加,和 Swift 版格式一樣)
	if org := a.mockOrg(); org != DefaultOrgID {
		item["org"] = org
	}
	a.C["activity"] = append([]map[string]any{item}, a.C["activity"]...)
	if pgPool != nil {
		go activityInsert(item)
	}
	go a.Changed("activity")
}

// ScalarString 和 Swift 的 scalarString 一樣:字串、數字、布林轉字串
func ScalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case float64:
		if x == float64(int64(x)) {
			return formatInt(int64(x)), true
		}
		return formatFloat(x), true
	case json.Number:
		return x.String(), true
	case int:
		return formatInt(int64(x)), true
	case int64:
		return formatInt(x), true
	}
	return "", false
}

// As 以 actor 的身分執行 f:f 裡面用 a.Actor() 署名。呼叫時要拿著 Mu(f 執行期間不能放鎖)。
func (a *App) As(actor Actor, f func()) {
	prev := a.acting
	a.acting = &actor
	defer func() { a.acting = prev }()
	f()
}

// Actor 目前動作的執行者:在 As 裡面是指定的人,否則是 a.Me(呼叫時要拿著 Mu)
func (a *App) Actor() Actor {
	if a.acting != nil {
		return *a.acting
	}
	return Person(a.Me)
}
