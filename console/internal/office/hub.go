// Package office 是 3D 虛擬辦公室(repo 的 office3d/)和 console 後端之間的橋:
//
//   - 後端下命令(HTTP POST /api/office/command、MCP office_move),驗證角色與地點(places.json)
//   - 用 SSE 推給所有連線的檢視器(瀏覽器負責尋路與走路)
//   - 檢視器回報收到命令、抵達、角色狀態;後端整理成每個角色的最後狀態與每個命令的狀態
//
// 多個檢視器同時開:命令推給全部(畫面都會動),但只採用「主檢視器」的回報。
// 主檢視器 = 目前連線中最早連上的那一個;它斷線時由下一個最早的接手(會收到 role 事件),
// 其他檢視器的回報回 accepted=false、不改狀態,所以各自模擬的小差異不會互相打架。
package office

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"omixor-core/model"
)

// 命令狀態(每個角色各有一個,命令的 status 是整理過的)
const (
	StatusPending    = "pending"    // 已建立、還沒寫進任何檢視器的連線
	StatusNoViewer   = "no_viewer"  // 建立時沒有檢視器連線,不會執行
	StatusDelivered  = "delivered"  // 已送達檢視器(SSE 已寫出)
	StatusRunning    = "running"    // 檢視器回報開始執行(走路中)
	StatusArrived    = "arrived"    // 抵達
	StatusDone       = "done"       // stop 已執行
	StatusPartial    = "partial"    // 有的角色抵達、有的失敗
	StatusFailed     = "failed"     // 失敗(找不到路、角色沒載入、檢視器斷線或沒回應)
	StatusSuperseded = "superseded" // 還沒抵達就被新的命令(或檢視器上的手動操作)取代
)

func rank(s string) int {
	switch s {
	case StatusPending:
		return 0
	case StatusDelivered:
		return 1
	case StatusRunning:
		return 2
	}
	return 3
}

func terminal(s string) bool { return rank(s) == 3 }

const (
	// MaxCommandsPerSubmit 一次 Submit 最多幾條命令(超過整段拒絕,不截斷)。
	// 每條命令廣播一個 command 事件;檢視器緩衝 viewerBuffer 格,
	// hello、snapshot 佔 2 格、role 可能再佔 1 格,所以上限 × 1 必須遠小於 viewerBuffer。
	MaxCommandsPerSubmit = 20
	// MaxTextRunes text 最多幾個字
	MaxTextRunes = 500
	viewerBuffer = 64
)

// Options 設定(零值用預設)
type Options struct {
	Dir         string        // 指定 office3d 資料夾(測試用);空字串 = ResolveDir
	MaxViewers  int           // 同時連線的檢視器上限,預設 8
	Heartbeat   time.Duration // SSE 心跳,預設 15 秒
	AckTimeout  time.Duration // 送出後多久沒回報算失敗,預設 15 秒
	KeepHistory int           // 保留幾筆命令,預設 200
	Logf        func(string, ...any)
}

// AgentResult 一個角色在一個命令裡的狀態
type AgentResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Where  string `json:"where,omitempty"`
	Place  string `json:"place,omitempty"`
}

// TargetView 命令目標(附名稱)
type TargetView struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Command 一個命令(一個動作;text 有多條時拆成多個命令)
type Command struct {
	ID          string                  `json:"id"`
	Text        string                  `json:"text,omitempty"`
	Action      string                  `json:"action"`
	Agents      []string                `json:"agents"`
	Target      *TargetView             `json:"target,omitempty"`
	Description string                  `json:"description"`
	Status      string                  `json:"status"`
	Error       string                  `json:"error,omitempty"`
	AgentStatus map[string]*AgentResult `json:"agentStatus"`
	Viewers     int                     `json:"viewers"` // 建立時連線的檢視器數
	CreatedAt   string                  `json:"createdAt"`
	UpdatedAt   string                  `json:"updatedAt"`
	FinishedAt  string                  `json:"finishedAt,omitempty"`

	created time.Time
	sentAt  time.Time
}

func (c *Command) action() Action {
	a := Action{Type: c.Action, Agents: c.Agents}
	if c.Target != nil {
		a.Target = &Target{Kind: c.Target.Kind, ID: c.Target.ID}
	}
	return a
}

// wire 推給檢視器的格式
func (c *Command) wire() map[string]any {
	return map[string]any{"id": c.ID, "action": c.action(), "description": c.Description}
}

func (c *Command) clone() *Command {
	x := *c
	x.Agents = append([]string(nil), c.Agents...)
	if c.Target != nil {
		t := *c.Target
		x.Target = &t
	}
	x.AgentStatus = map[string]*AgentResult{}
	for k, v := range c.AgentStatus {
		r := *v
		x.AgentStatus[k] = &r
	}
	return &x
}

// AgentState 檢視器回報的角色狀態
type AgentState struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Reported   bool           `json:"reported"` // false = 還沒有檢視器回報過
	State      string         `json:"state,omitempty"`
	StateLabel string         `json:"stateLabel,omitempty"`
	Seated     bool           `json:"seated"`
	Position   []float64      `json:"position,omitempty"`
	Yaw        float64        `json:"yaw"`
	Task       map[string]any `json:"task,omitempty"`
	Where      string         `json:"where,omitempty"`
	Place      string         `json:"place,omitempty"` // 最後抵達的地點 id(走路中清空)
	CommandID  string         `json:"commandId,omitempty"`
	UpdatedAt  string         `json:"updatedAt,omitempty"`
}

// Msg 一則 SSE 訊息
type Msg struct {
	Event string
	Data  []byte // 單行 JSON
	CmdID string // command 事件:寫出後要呼叫 Delivered
}

// FormatSSE 轉成 SSE 格式
func FormatSSE(m Msg) []byte {
	return []byte("event: " + m.Event + "\ndata: " + string(m.Data) + "\n\n")
}

// Viewer 一個 SSE 連線
type Viewer struct {
	ID          string
	C           chan Msg
	Gone        chan struct{} // 送不進去(太慢)時關掉,SSE 迴圈要結束
	connectedAt time.Time
	once        sync.Once
}

func (v *Viewer) kill() { v.once.Do(func() { close(v.Gone) }) }

// send 不擋:緩衝滿了(檢視器太慢)就斷掉它,它會重連拿 snapshot
func (v *Viewer) send(m Msg) bool {
	select {
	case v.C <- m:
		return true
	default:
		v.kill()
		return false
	}
}

// Hub 一個 console 一個
type Hub struct {
	opt  Options
	src  *dirSource
	boot string

	mu           sync.Mutex
	seq          int
	cmds         []*Command
	byID         map[string]*Command
	agents       map[string]*AgentState
	viewers      []*Viewer
	notify       chan struct{}
	lastReportAt time.Time
	lastReporter string
	now          func() time.Time
}

// NewHub 建立
func NewHub(opt Options) *Hub {
	if opt.MaxViewers <= 0 {
		opt.MaxViewers = 8
	}
	if opt.Heartbeat <= 0 {
		opt.Heartbeat = 15 * time.Second
	}
	if opt.AckTimeout <= 0 {
		opt.AckTimeout = 15 * time.Second
	}
	if opt.KeepHistory <= 0 {
		opt.KeepHistory = 200
	}
	return &Hub{
		opt:    opt,
		src:    &dirSource{fixed: opt.Dir, logf: opt.Logf, retryGap: 10 * time.Second},
		boot:   randHex(3),
		byID:   map[string]*Command{},
		agents: map[string]*AgentState{},
		notify: make(chan struct{}),
		now:    time.Now,
	}
}

var (
	hubsMu sync.Mutex
	hubs   = map[any]*Hub{}
)

// For 每個 key(console 傳 *core.App)一個 Hub;HTTP 與 MCP 共用同一個
func For(key any) *Hub {
	hubsMu.Lock()
	defer hubsMu.Unlock()
	h := hubs[key]
	if h == nil {
		h = NewHub(Options{})
		hubs[key] = h
	}
	return h
}

// Heartbeat SSE 心跳間隔
func (h *Hub) Heartbeat() time.Duration { return h.opt.Heartbeat }

// Dir office3d 資料夾
func (h *Hub) Dir() (string, error) { return h.src.Dir() }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// changedLocked 叫醒等待的人(拿著 h.mu)
func (h *Hub) changedLocked() {
	close(h.notify)
	h.notify = make(chan struct{})
}

// ---------------------------------------------------------------------- 下命令

// ValidationError 參數不合法(HTTP 400)
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return ValidationError{fmt.Sprintf(format, a...)} }

// Request 下命令的參數:Text,或 Action + Agents + Target
type Request struct {
	Text   string
	Action string
	Agents []string
	Target string
}

// RequestFromArgs JSON 物件 → Request(HTTP 與 MCP 共用)
func RequestFromArgs(args map[string]any) (Request, error) {
	var r Request
	str := func(k string) (string, error) {
		v, ok := args[k]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", invalid("%s 要是字串", k)
		}
		return strings.TrimSpace(s), nil
	}
	var err error
	if r.Text, err = str("text"); err != nil {
		return r, err
	}
	if r.Action, err = str("action"); err != nil {
		return r, err
	}
	if r.Target, err = str("target"); err != nil {
		return r, err
	}
	switch x := args["agents"].(type) {
	case nil:
	case string:
		if s := strings.TrimSpace(x); s != "" {
			r.Agents = []string{s}
		}
	case []any:
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return r, invalid("agents 要是字串陣列")
			}
			if s = strings.TrimSpace(s); s != "" {
				r.Agents = append(r.Agents, s)
			}
		}
	case []string:
		r.Agents = x
	default:
		return r, invalid("agents 要是字串陣列")
	}
	if _, ok := args["agent"]; ok && len(r.Agents) == 0 {
		if s, _ := args["agent"].(string); strings.TrimSpace(s) != "" {
			r.Agents = []string{strings.TrimSpace(s)}
		}
	}
	return r, nil
}

func (h *Hub) validate(cat *Catalog, req Request) ([]Action, error) {
	if utf8.RuneCountInString(req.Text) > MaxTextRunes {
		return nil, invalid("text 太長(最多 %d 字)", MaxTextRunes)
	}
	if req.Text != "" {
		if req.Action != "" || len(req.Agents) > 0 || req.Target != "" {
			return nil, invalid("text 與 action/agents/target 擇一")
		}
		if n := countParts(req.Text); n > MaxCommandsPerSubmit {
			return nil, invalid("一次最多 %d 條命令,你給了 %d 條,請分次送", MaxCommandsPerSubmit, n)
		}
		acts, errs := cat.p.parse(req.Text)
		if len(errs) > 0 {
			return nil, invalid("%s", strings.Join(errs, ";"))
		}
		return acts, nil
	}
	switch req.Action {
	case "goto", "find", "return", "stop":
	case "":
		return nil, invalid("要給 text,或 action(goto、find、return、stop)")
	default:
		return nil, invalid("action 只能是 goto、find、return、stop:%s", req.Action)
	}
	if len(req.Agents) == 0 {
		return nil, invalid("agents 至少要一個角色 id(可用:%s;或 all)", agentIDs(cat))
	}
	if len(req.Agents) > 50 {
		return nil, invalid("agents 太多")
	}
	var ids []string
	for _, x := range req.Agents {
		if n := Normalize(x); contains(normAll(allWords), n) {
			ids = ids[:0]
			for _, a := range cat.Agents {
				ids = append(ids, a.ID)
			}
			break
		}
		id := x
		if cat.agent(id) == nil {
			id = cat.p.resolveAgent(x)
		}
		if id == "" {
			return nil, invalid("沒有角色 %q(可用:%s)", x, agentIDs(cat))
		}
		if !contains(ids, id) {
			ids = append(ids, id)
		}
	}
	act := Action{Type: req.Action, Agents: ids}
	switch req.Action {
	case "return", "stop":
		if req.Target != "" {
			return nil, invalid("%s 不用 target", req.Action)
		}
	case "goto", "find":
		if req.Target == "" {
			return nil, invalid("%s 要給 target(地點 id 或角色 id)", req.Action)
		}
		var tg *Target
		if cat.place(req.Target) != nil {
			tg = &Target{Kind: "place", ID: req.Target}
		} else if cat.agent(req.Target) != nil {
			tg = &Target{Kind: "agent", ID: req.Target}
		} else if id := cat.p.resolvePlace(req.Target); id != "" {
			tg = &Target{Kind: "place", ID: id}
		} else if id := cat.p.resolveAgent(req.Target); id != "" {
			tg = &Target{Kind: "agent", ID: id}
		}
		if tg == nil {
			return nil, invalid("沒有地點或角色 %q(地點用 office_state 或 GET /api/office/state 查)", req.Target)
		}
		if req.Action == "find" && tg.Kind != "agent" {
			return nil, invalid("find 的 target 要是角色 id;走到地點用 goto")
		}
		if tg.Kind == "agent" {
			act.Type = "find"
			others := []string{}
			for _, id := range ids {
				if id != tg.ID {
					others = append(others, id)
				}
			}
			if len(others) == 0 {
				return nil, invalid("%s 不能去找自己", cat.agent(tg.ID).Name)
			}
			act.Agents = others
		} else {
			act.Type = "goto"
		}
		act.Target = tg
	}
	return []Action{act}, nil
}

func agentIDs(cat *Catalog) string {
	ids := make([]string, 0, len(cat.Agents))
	for _, a := range cat.Agents {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, "、")
}

// Submit 驗證並下命令;沒有檢視器時命令的狀態是 no_viewer(不會補送)
func (h *Hub) Submit(req Request) ([]*Command, error) {
	cat, err := h.src.Catalog()
	if err != nil {
		return nil, err
	}
	acts, err := h.validate(cat, req)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	var out []*Command
	for _, a := range acts {
		h.seq++
		c := &Command{
			ID: fmt.Sprintf("oc-%s-%d", h.boot, h.seq), Text: req.Text, Action: a.Type, Agents: a.Agents,
			Description: cat.p.describe(a), AgentStatus: map[string]*AgentResult{}, Viewers: len(h.viewers),
			CreatedAt: ts(now), UpdatedAt: ts(now), created: now,
		}
		if a.Target != nil {
			name := a.Target.ID
			if a.Target.Kind == "place" {
				name = cat.place(a.Target.ID).Name
			} else {
				name = cat.agent(a.Target.ID).Name
			}
			c.Target = &TargetView{Kind: a.Target.Kind, ID: a.Target.ID, Name: name}
		}
		st := StatusPending
		if len(h.viewers) == 0 {
			st = StatusNoViewer
			c.Error = "沒有檢視器連線,命令沒有執行(開 /office/ 頁面後再下一次)"
		} else {
			// 同一個角色還沒結束的舊命令:被取代
			for _, old := range h.cmds {
				for _, id := range a.Agents {
					if r := old.AgentStatus[id]; r != nil && !terminal(r.Status) {
						r.Status = StatusSuperseded
						r.Error = "被命令 " + c.ID + " 取代"
						h.refreshLocked(old, now)
					}
				}
			}
		}
		for _, id := range a.Agents {
			c.AgentStatus[id] = &AgentResult{Status: st}
		}
		h.refreshLocked(c, now)
		h.cmds = append(h.cmds, c)
		h.byID[c.ID] = c
		if st == StatusPending {
			c.sentAt = now
			h.broadcastLocked(Msg{Event: "command", Data: mustJSON(c.wire()), CmdID: c.ID})
		}
		out = append(out, c.clone())
	}
	h.trimLocked()
	h.changedLocked()
	return out, nil
}

func (h *Hub) trimLocked() {
	for len(h.cmds) > h.opt.KeepHistory {
		delete(h.byID, h.cmds[0].ID)
		h.cmds = h.cmds[1:]
	}
}

// refreshLocked 依每個角色的狀態整理命令的狀態
func (h *Hub) refreshLocked(c *Command, now time.Time) {
	low := 3
	var failed, ok, superseded, noViewer int
	var errs []string
	for _, id := range c.Agents {
		r := c.AgentStatus[id]
		if r == nil {
			continue
		}
		if rk := rank(r.Status); rk < low {
			low = rk
		}
		switch r.Status {
		case StatusFailed:
			failed++
			if r.Error != "" {
				errs = append(errs, r.Error)
			}
		case StatusArrived, StatusDone:
			ok++
		case StatusSuperseded:
			superseded++
		case StatusNoViewer:
			noViewer++
		}
	}
	switch {
	case noViewer > 0 && noViewer == len(c.Agents):
		c.Status = StatusNoViewer
	case low < 3:
		c.Status = []string{StatusPending, StatusDelivered, StatusRunning}[low]
	case failed > 0 && ok > 0:
		c.Status = StatusPartial
	case failed > 0:
		c.Status = StatusFailed
	case ok == 0 && superseded > 0:
		c.Status = StatusSuperseded
	case c.Action == "stop":
		c.Status = StatusDone
	default:
		c.Status = StatusArrived
	}
	if len(errs) > 0 {
		c.Error = strings.Join(model.DedupExact(errs), ";")
	}
	c.UpdatedAt = ts(now)
	if terminal(c.Status) && c.FinishedAt == "" && c.Status != StatusNoViewer {
		c.FinishedAt = ts(now)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func (h *Hub) broadcastLocked(m Msg) {
	for _, v := range h.viewers {
		v.send(m)
	}
}

// ---------------------------------------------------------------------- 檢視器連線

// ErrTooManyViewers 連線數到上限
var ErrTooManyViewers = errors.New("檢視器連線數已到上限")

// Subscribe 新的 SSE 連線:先送 hello(檢視器 id、是不是主檢視器)與 snapshot(角色最後狀態、還在進行的命令)
func (h *Hub) Subscribe() (*Viewer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.viewers) >= h.opt.MaxViewers {
		return nil, ErrTooManyViewers
	}
	v := &Viewer{ID: "v-" + randHex(8), C: make(chan Msg, viewerBuffer), Gone: make(chan struct{}), connectedAt: h.now()}
	h.viewers = append(h.viewers, v)
	primary := h.viewers[0] == v
	active := []any{}
	for _, c := range h.cmds {
		if !terminal(c.Status) {
			active = append(active, c.wire())
		}
	}
	agents := []*AgentState{}
	for _, a := range h.agentListLocked(nil) {
		if a.Reported {
			agents = append(agents, a)
		}
	}
	v.send(Msg{Event: "hello", Data: mustJSON(map[string]any{"viewerId": v.ID, "primary": primary, "strategy": "primary"})})
	v.send(Msg{Event: "snapshot", Data: mustJSON(map[string]any{"viewerId": v.ID, "primary": primary, "agents": agents, "active": active})})
	h.changedLocked()
	return v, nil
}

// Unsubscribe 連線結束:主檢視器斷了就換下一個;一個都不剩時,還沒結束的命令算失敗
func (h *Hub) Unsubscribe(v *Viewer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	idx := -1
	for i, x := range h.viewers {
		if x == v {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	v.kill()
	h.viewers = append(h.viewers[:idx], h.viewers[idx+1:]...)
	now := h.now()
	if idx == 0 && len(h.viewers) > 0 {
		h.viewers[0].send(Msg{Event: "role", Data: mustJSON(map[string]any{"primary": true})})
	}
	if len(h.viewers) == 0 {
		for _, c := range h.cmds {
			if terminal(c.Status) {
				continue
			}
			for _, r := range c.AgentStatus {
				if !terminal(r.Status) {
					r.Status, r.Error = StatusFailed, "檢視器斷線"
				}
			}
			h.refreshLocked(c, now)
		}
	}
	h.changedLocked()
}

// Delivered SSE 已把命令寫給這個檢視器
func (h *Hub) Delivered(v *Viewer, cmdID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.byID[cmdID]
	if c == nil {
		return
	}
	for _, r := range c.AgentStatus {
		if rank(r.Status) < 1 {
			r.Status = StatusDelivered
		}
	}
	h.refreshLocked(c, h.now())
	h.changedLocked()
}

// Viewers 連線中的檢視器數
func (h *Hub) Viewers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.viewers)
}

// ---------------------------------------------------------------------- 回報

// Report 檢視器的回報
type Report struct {
	ViewerID  string            `json:"viewerId"`
	Type      string            `json:"type"` // ack | arrived | superseded | state
	CommandID string            `json:"commandId"`
	Agent     string            `json:"agent"`
	Results   []ReportResult    `json:"results"`
	Arrived   *ArrivedDetail    `json:"arrived"`
	Agents    []json.RawMessage `json:"agents"` // office.state() 的內容
	Error     string            `json:"error"`
}

// ReportResult World.runAction 的每個角色結果
type ReportResult struct {
	Agent   string `json:"agent"`
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Already bool   `json:"already"`
}

// ArrivedDetail office:arrived 的 detail
type ArrivedDetail struct {
	Target *struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"target"`
	Position []float64 `json:"position"`
	Seated   bool      `json:"seated"`
	Where    string    `json:"where"`
}

// ReportOutcome 回報的結果
type ReportOutcome struct {
	Accepted bool   `json:"accepted"`
	Primary  bool   `json:"primary"`
	Reason   string `json:"reason,omitempty"`
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func finite(xs []float64) bool {
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1e6 {
			return false
		}
	}
	return true
}

var stateLabels = map[string]string{"idle": "閒置", "walking": "走路中", "arrived": "抵達", "waiting": "等待讓路"}

// Report 處理回報;不是主檢視器(或檢視器沒連線)的回報不採用
func (h *Hub) Report(rep Report) (ReportOutcome, error) {
	cat, err := h.src.Catalog()
	if err != nil {
		return ReportOutcome{}, err
	}
	switch rep.Type {
	case "ack", "arrived", "superseded", "state":
	default:
		return ReportOutcome{}, invalid("type 只能是 ack、arrived、superseded、state")
	}
	if len(rep.Agents) > 64 || len(rep.Results) > 64 {
		return ReportOutcome{}, invalid("回報太大")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.viewers) == 0 || rep.ViewerID == "" {
		return ReportOutcome{Reason: "檢視器沒有連線(先連 /api/office/events)"}, nil
	}
	if h.viewers[0].ID != rep.ViewerID {
		for _, v := range h.viewers {
			if v.ID == rep.ViewerID {
				return ReportOutcome{Reason: "不是主檢視器,回報不採用"}, nil
			}
		}
		return ReportOutcome{Reason: "檢視器沒有連線(先連 /api/office/events)"}, nil
	}
	now := h.now()
	h.lastReportAt, h.lastReporter = now, rep.ViewerID
	// 角色狀態
	for _, raw := range rep.Agents {
		h.applyStateLocked(cat, raw, now)
	}
	c := h.byID[rep.CommandID]
	switch rep.Type {
	case "ack":
		if c == nil {
			break
		}
		for _, r := range rep.Results {
			ar := c.AgentStatus[r.Agent]
			if ar == nil || terminal(ar.Status) {
				continue
			}
			switch {
			case !r.OK:
				ar.Status, ar.Error = StatusFailed, clip(r.Error, 200)
				if ar.Error == "" {
					ar.Error = "檢視器執行失敗"
				}
			case c.Action == "stop":
				ar.Status = StatusDone
			default:
				ar.Status = StatusRunning
			}
		}
		h.refreshLocked(c, now)
	case "arrived":
		a := cat.agent(rep.Agent)
		if a == nil {
			break
		}
		st := h.agentLocked(a)
		if d := rep.Arrived; d != nil {
			if len(d.Position) == 3 && finite(d.Position) {
				st.Position = d.Position
			}
			st.Seated, st.Where = d.Seated, clip(d.Where, 120)
			st.Place = ""
			if d.Target != nil && d.Target.Kind == "place" && cat.place(d.Target.ID) != nil {
				st.Place = d.Target.ID
			}
			st.State, st.StateLabel = "arrived", stateLabels["arrived"]
			st.Reported, st.UpdatedAt = true, ts(now)
		}
		if c != nil {
			if ar := c.AgentStatus[rep.Agent]; ar != nil && !terminal(ar.Status) {
				ar.Status = StatusArrived
				ar.Where, ar.Place = st.Where, st.Place
				st.CommandID = c.ID
				h.refreshLocked(c, now)
			}
		}
	case "superseded":
		if c == nil {
			break
		}
		if ar := c.AgentStatus[rep.Agent]; ar != nil && !terminal(ar.Status) {
			ar.Status, ar.Error = StatusSuperseded, clip(rep.Error, 200)
			if ar.Error == "" {
				ar.Error = "被檢視器上的操作取代"
			}
			h.refreshLocked(c, now)
		}
	}
	h.changedLocked()
	return ReportOutcome{Accepted: true, Primary: true}, nil
}

func (h *Hub) agentLocked(a *AgentInfo) *AgentState {
	st := h.agents[a.ID]
	if st == nil {
		st = &AgentState{ID: a.ID, Name: a.Name}
		h.agents[a.ID] = st
	}
	return st
}

// applyStateLocked office.state() 的一筆
func (h *Hub) applyStateLocked(cat *Catalog, raw json.RawMessage, now time.Time) {
	var in struct {
		ID       string         `json:"id"`
		State    string         `json:"state"`
		Seated   bool           `json:"seated"`
		Position []float64      `json:"position"`
		Yaw      float64        `json:"yaw"`
		Task     map[string]any `json:"task"`
		Where    *string        `json:"where"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return
	}
	a := cat.agent(in.ID)
	if a == nil {
		return
	}
	st := h.agentLocked(a)
	if _, ok := stateLabels[in.State]; ok {
		st.State, st.StateLabel = in.State, stateLabels[in.State]
	}
	if st.State == "walking" || st.State == "waiting" {
		st.Place = ""
	}
	st.Seated = in.Seated
	if len(in.Position) == 3 && finite(in.Position) {
		st.Position = in.Position
	}
	if finite([]float64{in.Yaw}) {
		st.Yaw = in.Yaw
	}
	st.Task = sanitizeTask(in.Task, cat)
	st.Where = "" // office.state() 走路中或停在半路是 null
	if in.Where != nil {
		st.Where = clip(*in.Where, 120)
	}
	st.Reported, st.UpdatedAt = true, ts(now)
}

func sanitizeTask(t map[string]any, cat *Catalog) map[string]any {
	if t == nil {
		return nil
	}
	out := map[string]any{}
	switch t["type"] {
	case "goto":
		out["type"] = "goto"
		if p, _ := t["place"].(string); p != "" && cat.place(p) != nil {
			out["place"] = p
		}
		for _, k := range []string{"x", "z"} {
			if f, ok := t[k].(float64); ok && finite([]float64{f}) {
				out[k] = f
			}
		}
	case "find":
		out["type"] = "find"
		if p, _ := t["target"].(string); cat.agent(p) != nil {
			out["target"] = p
		}
	default:
		return nil
	}
	return out
}

// ---------------------------------------------------------------------- 查詢

// sweepLocked 送出太久沒回報的命令算失敗
func (h *Hub) sweepLocked() {
	now := h.now()
	changed := false
	for _, c := range h.cmds {
		if terminal(c.Status) || c.sentAt.IsZero() || now.Sub(c.sentAt) < h.opt.AckTimeout {
			continue
		}
		hit := false
		for _, r := range c.AgentStatus {
			if rank(r.Status) < 2 {
				r.Status, r.Error = StatusFailed, "檢視器沒有回應(主檢視器可能還在載入或卡住)"
				hit = true
			}
		}
		if hit {
			h.refreshLocked(c, now)
			changed = true
		}
	}
	if changed {
		h.changedLocked()
	}
}

func (h *Hub) agentListLocked(cat *Catalog) []*AgentState {
	out := []*AgentState{}
	seen := map[string]bool{}
	if cat != nil {
		for i := range cat.Agents {
			a := &cat.Agents[i]
			st := h.agents[a.ID]
			if st == nil {
				st = &AgentState{ID: a.ID, Name: a.Name}
			}
			x := *st
			out = append(out, &x)
			seen[a.ID] = true
		}
	}
	for id, st := range h.agents {
		if !seen[id] {
			x := *st
			out = append(out, &x)
		}
	}
	return out
}

// State GET /api/office/state 與 office_state 的內容
func (h *Hub) State(limit int) map[string]any {
	cat, catErr := h.src.Catalog()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepLocked()
	if limit <= 0 || limit > h.opt.KeepHistory {
		limit = 50
	}
	// 不輸出 viewerId:它是 /api/office/report 的憑證,只在檢視器自己的 SSE hello 給
	viewers := []map[string]any{}
	for i, v := range h.viewers {
		viewers = append(viewers, map[string]any{"primary": i == 0, "connectedAt": ts(v.connectedAt)})
	}
	cmds := []*Command{}
	for i := len(h.cmds) - 1; i >= 0 && len(cmds) < limit; i-- {
		cmds = append(cmds, h.cmds[i].clone())
	}
	out := map[string]any{
		"available":    catErr == nil,
		"viewers":      viewers,
		"viewerCount":  len(h.viewers),
		"hasPrimary":   len(h.viewers) > 0,
		"strategy":     "primary(最早連上、還連著的檢視器;只採用它的回報)",
		"agents":       h.agentListLocked(cat),
		"commands":     cmds,
		"lastReportAt": nil,
	}
	if catErr != nil {
		out["error"] = catErr.Error()
	}
	if !h.lastReportAt.IsZero() {
		out["lastReportAt"] = ts(h.lastReportAt)
	}
	return out
}

// Places 地點清單(id、名稱、種類)
func (h *Hub) Places() ([]map[string]any, error) {
	cat, err := h.src.Catalog()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range cat.Places {
		m := map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind}
		if p.Owner != "" {
			m["owner"] = p.Owner
		}
		out = append(out, m)
	}
	return out, nil
}

// Command 一個命令的目前狀態
func (h *Hub) Command(id string) *Command {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepLocked()
	if c := h.byID[id]; c != nil {
		return c.clone()
	}
	return nil
}

// Wait 等這些命令都結束(抵達、失敗、被取代…)或逾時;回傳目前狀態與是否都結束了
func (h *Hub) Wait(ctx context.Context, ids []string, d time.Duration) ([]*Command, bool) {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		h.mu.Lock()
		h.sweepLocked()
		out := make([]*Command, 0, len(ids))
		done := true
		for _, id := range ids {
			c := h.byID[id]
			if c == nil {
				continue
			}
			if !terminal(c.Status) {
				done = false
			}
			out = append(out, c.clone())
		}
		ch := h.notify
		h.mu.Unlock()
		if done {
			return out, true
		}
		select {
		case <-ch:
		case <-tick.C:
		case <-deadline.C:
			return out, false
		case <-ctx.Done():
			return out, false
		}
	}
}
