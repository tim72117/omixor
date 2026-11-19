package office

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 測試用的 places.json(格式同 office3d/places.json,只留需要的欄位)
const testPlaces = `{
  "agents": [
    {"id":"ceo","name":"CEO","aliases":["ceo","執行長","boss"],"seat":"seat_ceo"},
    {"id":"engineer","name":"工程師","aliases":["engineer","工程師","eng"],"seat":"seat_engineer"},
    {"id":"narrator","name":"旁白製作","aliases":["narrator","旁白"],"seat":"seat_narrator"}
  ],
  "places": [
    {"id":"seat_ceo","kind":"seat","owner":"ceo","name":"CEO的座位","aliases":["ceo seat"]},
    {"id":"seat_engineer","kind":"seat","owner":"engineer","name":"工程師的座位","aliases":[]},
    {"id":"seat_narrator","kind":"seat","owner":"narrator","name":"旁白製作的座位","aliases":[]},
    {"id":"meeting","kind":"shared","name":"會議室","aliases":["會議室","meeting","meeting room"]},
    {"id":"pantry","kind":"shared","name":"茶水間","aliases":["茶水間","pantry","coffee"]}
  ]
}`

func testDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "places.json"), []byte(testPlaces), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func testHub(t *testing.T, opt Options) *Hub {
	t.Helper()
	if opt.Dir == "" {
		opt.Dir = testDir(t)
	}
	opt.Logf = func(string, ...any) {}
	return NewHub(opt)
}

// recv 拿下一則訊息(最多等 1 秒)
func recv(t *testing.T, v *Viewer) Msg {
	t.Helper()
	select {
	case m := <-v.C:
		return m
	case <-time.After(time.Second):
		t.Fatal("沒有收到訊息")
	}
	return Msg{}
}

func submit(t *testing.T, h *Hub, req Request) *Command {
	t.Helper()
	cmds, err := h.Submit(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		t.Fatalf("要一個命令,拿到 %d 個", len(cmds))
	}
	return cmds[0]
}

func status(h *Hub, id string) string { return h.Command(id).Status }

func TestSubmitCommandLimit(t *testing.T) {
	h := testHub(t, Options{})
	v, _ := h.Subscribe()
	atLimit := strings.TrimSuffix(strings.Repeat("ceo停;", MaxCommandsPerSubmit), ";")
	cmds, err := h.Submit(Request{Text: atLimit})
	if err != nil || len(cmds) != MaxCommandsPerSubmit {
		t.Fatalf("剛好等於上限要通過:%d %v", len(cmds), err)
	}
	// 最壞情況:hello、snapshot、全部 command 事件都要放得進檢視器緩衝,不能被踢掉
	select {
	case <-v.Gone:
		t.Fatal("上限內的命令不應踢掉檢視器")
	default:
	}
	if MaxCommandsPerSubmit+3 >= viewerBuffer {
		t.Fatal("命令上限太大,會塞滿檢視器緩衝")
	}
	before := len(h.State(0)["commands"].([]*Command))
	_, err = h.Submit(Request{Text: atLimit + ";ceo停"})
	var ve ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "最多 20 條") {
		t.Fatalf("超過上限要被拒絕:%v", err)
	}
	if after := len(h.State(0)["commands"].([]*Command)); after != before {
		t.Fatal("被拒絕的請求不應留下命令")
	}
	// 全形分號與換行也算
	_, err = h.Submit(Request{Text: strings.Repeat("ceo停;\nceo停;", 11)})
	if !errors.As(err, &ve) {
		t.Fatalf("%v", err)
	}
	// 超長 text
	_, err = h.Submit(Request{Text: strings.Repeat("工", MaxTextRunes+1)})
	if !errors.As(err, &ve) {
		t.Fatalf("%v", err)
	}
}

func TestStateHidesViewerID(t *testing.T) {
	h := testHub(t, Options{})
	v, _ := h.Subscribe()
	b, _ := json.Marshal(h.State(0))
	if strings.Contains(string(b), v.ID) {
		t.Fatalf("state 不能含 viewerId:%s", b)
	}
	if _, err := h.Report(Report{ViewerID: "v-forged"}); err != nil {
		t.Log(err)
	}
}

func TestValidate(t *testing.T) {
	h := testHub(t, Options{})
	bad := []Request{
		{},
		{Action: "fly", Agents: []string{"ceo"}},
		{Action: "goto", Agents: []string{"nobody"}, Target: "meeting"},
		{Action: "goto", Agents: []string{"ceo"}, Target: "mars"},
		{Action: "goto", Agents: []string{"ceo"}},
		{Action: "goto", Target: "meeting"},
		{Action: "find", Agents: []string{"ceo"}, Target: "meeting"},
		{Action: "find", Agents: []string{"ceo"}, Target: "ceo"},
		{Action: "return", Agents: []string{"ceo"}, Target: "meeting"},
		{Text: "工程師 去 火星"},
		{Text: "xyz go meeting"},
		{Text: "去 會議室"}, // 後端沒有選取的角色
		{Text: "ceo 找 ceo"},
		{Text: "工程師 去 會議室", Action: "goto"},
		{Text: strings.Repeat("工", 501)},
	}
	for _, r := range bad {
		_, err := h.Submit(r)
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%+v 應該是 ValidationError,拿到 %v", r, err)
		}
	}
	// 合法:id、名稱、別名、all;goto 角色 = find;find 自動去掉自己
	cases := []struct {
		req    Request
		action string
		agents []string
		target string
	}{
		{Request{Action: "goto", Agents: []string{"engineer"}, Target: "meeting"}, "goto", []string{"engineer"}, "meeting"},
		{Request{Action: "goto", Agents: []string{"工程師", "boss"}, Target: "會議室"}, "goto", []string{"engineer", "ceo"}, "meeting"},
		{Request{Action: "goto", Agents: []string{"engineer"}, Target: "ceo"}, "find", []string{"engineer"}, "ceo"},
		{Request{Action: "find", Agents: []string{"all"}, Target: "ceo"}, "find", []string{"engineer", "narrator"}, "ceo"},
		{Request{Action: "return", Agents: []string{"全部"}}, "return", []string{"ceo", "engineer", "narrator"}, ""},
		{Request{Action: "stop", Agents: []string{"eng"}}, "stop", []string{"engineer"}, ""},
		{Request{Text: "工程師 去 會議室"}, "goto", []string{"engineer"}, "meeting"},
		{Request{Text: "ceo find 旁白"}, "find", []string{"ceo"}, "narrator"},
		{Request{Text: "ceo 去 工程師的座位"}, "goto", []string{"ceo"}, "seat_engineer"},
	}
	for _, c := range cases {
		cmd := submit(t, h, c.req)
		tg := ""
		if cmd.Target != nil {
			tg = cmd.Target.ID
		}
		if cmd.Action != c.action || !reflect.DeepEqual(cmd.Agents, c.agents) || tg != c.target {
			t.Errorf("%+v → %s %v %s", c.req, cmd.Action, cmd.Agents, tg)
		}
		if cmd.Status != StatusNoViewer {
			t.Errorf("沒有檢視器時要是 no_viewer:%s", cmd.Status)
		}
	}
	// 多條命令拆成多個
	for _, text := range []string{"工程師 去 會議室;ceo 找 工程師", "工程師 去 會議室\uFF1Bceo 找 工程師", "工程師 去 會議室\nceo 找 工程師"} {
		cmds, err := h.Submit(Request{Text: text})
		if err != nil || len(cmds) != 2 || cmds[1].Action != "find" {
			t.Fatalf("多條命令 %q:%v %v", text, cmds, err)
		}
	}
	// 全形逗號、全形英數也認得(同 NFKC)
	c := submit(t, h, Request{Text: "工程師\uFF0Cceo 去 \uFF4D\uFF45\uFF45\uFF54\uFF49\uFF4E\uFF47"})
	if !reflect.DeepEqual(c.Agents, []string{"engineer", "ceo"}) || c.Target.ID != "meeting" {
		t.Fatalf("%+v", c)
	}
}

func TestRequestFromArgs(t *testing.T) {
	r, err := RequestFromArgs(map[string]any{"action": "goto", "agents": []any{"ceo", " engineer "}, "target": "meeting"})
	if err != nil || !reflect.DeepEqual(r.Agents, []string{"ceo", "engineer"}) || r.Target != "meeting" {
		t.Fatal(r, err)
	}
	r, _ = RequestFromArgs(map[string]any{"action": "stop", "agents": "ceo"})
	if !reflect.DeepEqual(r.Agents, []string{"ceo"}) {
		t.Fatal(r)
	}
	for _, bad := range []map[string]any{{"text": 3}, {"agents": []any{1}}, {"agents": 2}} {
		if _, err := RequestFromArgs(bad); err == nil {
			t.Error("應該失敗", bad)
		}
	}
}

// 沒有檢視器:no_viewer,之後有檢視器連上也不會補送
func TestNoViewer(t *testing.T) {
	h := testHub(t, Options{})
	c := submit(t, h, Request{Action: "goto", Agents: []string{"ceo"}, Target: "meeting"})
	if c.Status != StatusNoViewer || c.Error == "" || c.Viewers != 0 || c.AgentStatus["ceo"].Status != StatusNoViewer {
		t.Fatalf("%+v", c)
	}
	v, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	recv(t, v) // hello
	var snap struct {
		Active []any `json:"active"`
	}
	_ = json.Unmarshal(recv(t, v).Data, &snap)
	if len(snap.Active) != 0 {
		t.Fatal("no_viewer 的命令不該補送", snap.Active)
	}
	// Wait 馬上回(已經是結束狀態)
	out, done := h.Wait(context.Background(), []string{c.ID}, 5*time.Second)
	if !done || out[0].Status != StatusNoViewer {
		t.Fatal(out, done)
	}
}

// 完整流程:hello → snapshot → command → delivered → ack(running)→ arrived;state 有角色位置與地點
func TestCommandLifecycle(t *testing.T) {
	h := testHub(t, Options{})
	v, _ := h.Subscribe()
	hello := recv(t, v)
	var hi struct {
		ViewerID string `json:"viewerId"`
		Primary  bool   `json:"primary"`
	}
	_ = json.Unmarshal(hello.Data, &hi)
	if hello.Event != "hello" || !hi.Primary || hi.ViewerID != v.ID {
		t.Fatalf("%s %s", hello.Event, hello.Data)
	}
	if recv(t, v).Event != "snapshot" {
		t.Fatal("要有 snapshot")
	}
	c := submit(t, h, Request{Text: "工程師 去 會議室"})
	if c.Status != StatusPending || c.Viewers != 1 {
		t.Fatalf("%+v", c)
	}
	m := recv(t, v)
	var wire struct {
		ID     string `json:"id"`
		Action Action `json:"action"`
	}
	_ = json.Unmarshal(m.Data, &wire)
	if m.Event != "command" || m.CmdID != c.ID || wire.ID != c.ID || wire.Action.Type != "goto" || wire.Action.Target.ID != "meeting" {
		t.Fatalf("%s %s", m.Event, m.Data)
	}
	if !strings.HasPrefix(string(FormatSSE(m)), "event: command\ndata: {") {
		t.Fatalf("SSE 格式:%q", FormatSSE(m))
	}
	h.Delivered(v, c.ID)
	if s := status(h, c.ID); s != StatusDelivered {
		t.Fatal(s)
	}
	out, err := h.Report(Report{ViewerID: v.ID, Type: "ack", CommandID: c.ID, Results: []ReportResult{{Agent: "engineer", OK: true}},
		Agents: []json.RawMessage{json.RawMessage(`{"id":"engineer","state":"walking","seated":false,"position":[1,0,2],"yaw":0.5,"task":{"type":"goto","place":"meeting","x":1,"z":2},"where":""}`)}})
	if err != nil || !out.Accepted {
		t.Fatal(out, err)
	}
	if s := status(h, c.ID); s != StatusRunning {
		t.Fatal(s)
	}
	// 等抵達
	done := make(chan []*Command)
	go func() {
		cmds, _ := h.Wait(context.Background(), []string{c.ID}, 5*time.Second)
		done <- cmds
	}()
	time.Sleep(20 * time.Millisecond)
	_, _ = h.Report(Report{ViewerID: v.ID, Type: "arrived", CommandID: c.ID, Agent: "engineer",
		Arrived: &ArrivedDetail{Target: &struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}{"place", "meeting"}, Position: []float64{3, 0, 4}, Seated: true, Where: "會議室"}})
	select {
	case cmds := <-done:
		if cmds[0].Status != StatusArrived || cmds[0].AgentStatus["engineer"].Place != "meeting" || cmds[0].FinishedAt == "" {
			t.Fatalf("%+v", cmds[0])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait 沒有在抵達時回傳")
	}
	st := h.State(0)
	var eng *AgentState
	for _, a := range st["agents"].([]*AgentState) {
		if a.ID == "engineer" {
			eng = a
		}
	}
	if eng == nil || !eng.Reported || eng.State != "arrived" || eng.Place != "meeting" || !eng.Seated || eng.Position[0] != 3 || eng.CommandID != c.ID {
		t.Fatalf("%+v", eng)
	}
	if st["viewerCount"] != 1 || st["hasPrimary"] != true || st["lastReportAt"] == nil {
		t.Fatal(st)
	}
}

// 失敗、stop、部分失敗、被新命令取代
func TestStatuses(t *testing.T) {
	h := testHub(t, Options{})
	v, _ := h.Subscribe()
	c := submit(t, h, Request{Action: "goto", Agents: []string{"ceo", "engineer"}, Target: "pantry"})
	_, _ = h.Report(Report{ViewerID: v.ID, Type: "ack", CommandID: c.ID, Results: []ReportResult{{Agent: "ceo", OK: false, Error: "CEO 找不到路到 茶水間"}, {Agent: "engineer", OK: true}}})
	if s := status(h, c.ID); s != StatusRunning {
		t.Fatal(s)
	}
	_, _ = h.Report(Report{ViewerID: v.ID, Type: "arrived", CommandID: c.ID, Agent: "engineer", Arrived: &ArrivedDetail{Where: "茶水間"}})
	got := h.Command(c.ID)
	if got.Status != StatusPartial || !strings.Contains(got.Error, "找不到路") || got.AgentStatus["ceo"].Status != StatusFailed {
		t.Fatalf("%+v", got)
	}
	// stop:ack 就完成
	s := submit(t, h, Request{Action: "stop", Agents: []string{"ceo"}})
	_, _ = h.Report(Report{ViewerID: v.ID, Type: "ack", CommandID: s.ID, Results: []ReportResult{{Agent: "ceo", OK: true}}})
	if st := status(h, s.ID); st != StatusDone {
		t.Fatal(st)
	}
	// 取代
	a := submit(t, h, Request{Action: "goto", Agents: []string{"narrator"}, Target: "meeting"})
	b := submit(t, h, Request{Action: "return", Agents: []string{"narrator"}})
	if st := status(h, a.ID); st != StatusSuperseded {
		t.Fatal(st)
	}
	if st := status(h, b.ID); st != StatusPending {
		t.Fatal(st)
	}
	// 檢視器回報被手動操作取代
	_, _ = h.Report(Report{ViewerID: v.ID, Type: "superseded", CommandID: b.ID, Agent: "narrator"})
	if st := status(h, b.ID); st != StatusSuperseded {
		t.Fatal(st)
	}
	// 結束的命令不會被之後的回報改掉
	_, _ = h.Report(Report{ViewerID: v.ID, Type: "arrived", CommandID: b.ID, Agent: "narrator"})
	if st := status(h, b.ID); st != StatusSuperseded {
		t.Fatal(st)
	}
	// 不合法的回報
	if _, err := h.Report(Report{ViewerID: v.ID, Type: "hack"}); err == nil {
		t.Fatal("type 不合法要錯")
	}
}

// 送出後沒回報:逾時算失敗
func TestAckTimeout(t *testing.T) {
	h := testHub(t, Options{AckTimeout: time.Second})
	now := time.Now()
	h.now = func() time.Time { return now }
	_, _ = h.Subscribe()
	c := submit(t, h, Request{Action: "goto", Agents: []string{"ceo"}, Target: "meeting"})
	now = now.Add(2 * time.Second)
	got := h.Command(c.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Error, "沒有回應") {
		t.Fatalf("%+v", got)
	}
}

// 多檢視器:只採用主檢視器(最早連上的)的回報;主檢視器斷線由下一個接手
func TestPrimaryViewer(t *testing.T) {
	h := testHub(t, Options{})
	v1, _ := h.Subscribe()
	v2, _ := h.Subscribe()
	recv(t, v2)
	var snap struct {
		Primary bool `json:"primary"`
	}
	_ = json.Unmarshal(recv(t, v2).Data, &snap)
	if snap.Primary {
		t.Fatal("第二個不是主檢視器")
	}
	c := submit(t, h, Request{Action: "goto", Agents: []string{"ceo"}, Target: "meeting"})
	// 兩個都收到命令
	for _, v := range []*Viewer{v1, v2} {
		for {
			if m := recv(t, v); m.Event == "command" {
				break
			}
		}
	}
	out, _ := h.Report(Report{ViewerID: v2.ID, Type: "ack", CommandID: c.ID, Results: []ReportResult{{Agent: "ceo", OK: false, Error: "x"}}})
	if out.Accepted || out.Primary {
		t.Fatal("副檢視器的回報不該採用", out)
	}
	if s := status(h, c.ID); s == StatusFailed {
		t.Fatal("副檢視器改到狀態了")
	}
	out, _ = h.Report(Report{ViewerID: "v-unknown", Type: "state"})
	if out.Accepted {
		t.Fatal("沒連線的檢視器不該採用")
	}
	_, _ = h.Report(Report{ViewerID: v1.ID, Type: "ack", CommandID: c.ID, Results: []ReportResult{{Agent: "ceo", OK: true}}})
	h.Unsubscribe(v1)
	if m := recv(t, v2); m.Event != "role" || !strings.Contains(string(m.Data), `"primary":true`) {
		t.Fatalf("%s %s", m.Event, m.Data)
	}
	// 主檢視器斷了,還有別的檢視器:進行中的命令照舊
	if s := status(h, c.ID); s != StatusRunning {
		t.Fatal(s)
	}
	out, _ = h.Report(Report{ViewerID: v2.ID, Type: "arrived", CommandID: c.ID, Agent: "ceo"})
	if !out.Accepted || status(h, c.ID) != StatusArrived {
		t.Fatal("接手的檢視器回報要採用", out)
	}
	// snapshot 帶最後狀態與進行中的命令(重連用)
	d := submit(t, h, Request{Action: "goto", Agents: []string{"engineer"}, Target: "pantry"})
	v3, _ := h.Subscribe()
	recv(t, v3)
	var snap3 struct {
		Active []struct {
			ID string `json:"id"`
		} `json:"active"`
	}
	_ = json.Unmarshal(recv(t, v3).Data, &snap3)
	if len(snap3.Active) != 1 || snap3.Active[0].ID != d.ID {
		t.Fatal("snapshot 要帶進行中的命令", snap3)
	}
}

// 斷線清理:全部斷了,進行中的命令算失敗;連線上限;太慢的連線被踢掉
func TestDisconnectAndLimits(t *testing.T) {
	h := testHub(t, Options{MaxViewers: 2})
	v1, _ := h.Subscribe()
	v2, _ := h.Subscribe()
	if _, err := h.Subscribe(); !errors.Is(err, ErrTooManyViewers) {
		t.Fatal("超過上限要錯", err)
	}
	c := submit(t, h, Request{Action: "goto", Agents: []string{"ceo"}, Target: "meeting"})
	h.Unsubscribe(v1)
	h.Unsubscribe(v2)
	h.Unsubscribe(v2) // 重複呼叫沒事
	if h.Viewers() != 0 {
		t.Fatal(h.Viewers())
	}
	got := h.Command(c.ID)
	if got.Status != StatusFailed || !strings.Contains(got.Error, "斷線") {
		t.Fatalf("%+v", got)
	}
	select {
	case <-v1.Gone:
	default:
		t.Fatal("Unsubscribe 要關 Gone")
	}
	// 太慢:緩衝滿了就斷掉
	v3, _ := h.Subscribe()
	for i := 0; i < cap(v3.C)+5; i++ {
		if _, err := h.Submit(Request{Action: "stop", Agents: []string{"ceo"}}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-v3.Gone:
	default:
		t.Fatal("緩衝滿了要斷掉連線")
	}
}

func TestHistoryTrim(t *testing.T) {
	h := testHub(t, Options{KeepHistory: 3})
	var first string
	for i := 0; i < 5; i++ {
		c := submit(t, h, Request{Action: "stop", Agents: []string{"ceo"}})
		if i == 0 {
			first = c.ID
		}
	}
	if h.Command(first) != nil || len(h.State(0)["commands"].([]*Command)) != 3 {
		t.Fatal("只保留最近 3 個")
	}
}

func TestMissingDir(t *testing.T) {
	h := NewHub(Options{Dir: filepath.Join(t.TempDir(), "nope"), Logf: func(string, ...any) {}})
	if _, err := h.Submit(Request{Text: "ceo 回座位"}); err == nil {
		t.Fatal("沒有 places.json 要錯")
	}
	st := h.State(0)
	if st["available"] != false || st["error"] == nil {
		t.Fatal(st)
	}
	// ResolveDir:OMIXOR_OFFICE_DIR 指錯就是錯(不亂找)
	t.Setenv("OMIXOR_OFFICE_DIR", filepath.Join(t.TempDir(), "nope"))
	if _, err := ResolveDir(); err == nil {
		t.Fatal("OMIXOR_OFFICE_DIR 沒有 places.json 要錯")
	}
	d := testDir(t)
	t.Setenv("OMIXOR_OFFICE_DIR", d)
	if got, err := ResolveDir(); err != nil || got != d {
		t.Fatal(got, err)
	}
}

func TestStaticPath(t *testing.T) {
	good := map[string]string{
		"office.glb": "office.glb", "places.json": "places.json", "viewer/": "viewer/index.html",
		"viewer/index.html": "viewer/index.html", "viewer/js/office.js": "viewer/js/office.js", "viewer/js/bridge.js": "viewer/js/bridge.js",
	}
	for in, want := range good {
		if got, _, ok := staticPath(in); !ok || got != want {
			t.Errorf("%s → %s %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "office.blend", "build_office.py", "../places.json", "viewer/../places.json", "viewer/js/../../office.blend",
		"viewer/js/a/b.js", "viewer/test/run_tests.mjs", "viewer/README.md", "/etc/passwd", "viewer\\js\\office.js", "renders/overview.png",
		"viewer/js/.hidden.js", "places.json\x00", "./places.json", "viewer//index.html"} {
		if _, _, ok := staticPath(bad); ok {
			t.Errorf("%q 不該開放", bad)
		}
	}
}

// ---------------------------------------------------------------------- 和 command.js 一致

func repoOffice(t *testing.T) string {
	t.Helper()
	d, _ := filepath.Abs(filepath.Join("..", "..", "..", "office3d"))
	if !validDir(d) {
		t.Skip("沒有 office3d/(repo 最外層)")
	}
	return d
}

// 用真的 places.json:結果要和 command.js 一樣(有 node 時直接跑 command.js 比對)
func TestParseParityWithJS(t *testing.T) {
	dir := repoOffice(t)
	h := NewHub(Options{Dir: dir, Logf: func(string, ...any) {}})
	cat, err := h.src.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{
		"工程師 去 會議室", "engineer go to the meeting room", "工程師去會議室", "ceo 找 旁白", "ceo find narrator", "CEO 去找 旁白製作",
		"全部 回座位", "all return", "行銷 去 ceo 的座位", "marketing go ceo seat", "marketing go to ceo's seat", "行銷 去 CEO 辦公室",
		"旁白 回 錄音間", "訪客分析師 去 茶水間", "visitor-analyst go lounge", "visitor analyst go pantry", "Issue 整理員 去 入口櫃台",
		"triager go reception", "產品經理、工程師 去 會議室", "pm and engineer go meeting", "課程開發 去 西側公共角落",
		"課程開發 去 工程師 的工作區", "行銷 去 ceo", "工程師 停", "大家 去 休息區", "all go entrance", "工程師 到 窗邊",
		"工程師 去 火星", "xyz go meeting", "工程師", "ceo 找 ceo", "ENGINEER GO MEETING", "工程師,ceo 去 印表機", "pm 去 看板",
		"分析師 去 儀表板", "工程師 去 咖啡", "工程師\uFF0Cpm 去 \uFF4D\uFF45\uFF45\uFF54\uFF49\uFF4E\uFF47", "ceo 去 茶水間！", "（工程師） 去 窗邊", "ceo 跟著 工程師", "整理員 walk to window bench", "所有人 停下",
	}
	type res struct {
		Type   string   `json:"type"`
		Agents []string `json:"agents"`
		Target string   `json:"target"`
		Err    bool     `json:"err"`
	}
	goRes := make([]res, len(texts))
	for i, tx := range texts {
		acts, errs := cat.p.parse(tx)
		if len(errs) > 0 || len(acts) == 0 {
			goRes[i] = res{Err: true}
			continue
		}
		a := acts[0]
		goRes[i] = res{Type: a.Type, Agents: a.Agents}
		if a.Target != nil {
			goRes[i].Target = a.Target.ID
		}
	}
	// 基本預期(不需要 node)
	if goRes[0].Type != "goto" || goRes[0].Target != "meeting" || goRes[3].Type != "find" || goRes[3].Target != "narrator" || !goRes[27].Err {
		t.Fatal(goRes[0], goRes[3], goRes[27])
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("沒有 node,略過和 command.js 的比對")
		return
	}
	script := `
import { createParser } from ` + jsString("file://"+filepath.Join(dir, "viewer", "js", "command.js")) + `;
import { readFileSync } from 'node:fs';
const pj = JSON.parse(readFileSync(` + jsString(filepath.Join(dir, "places.json")) + `, 'utf8'));
const P = createParser({ agents: pj.agents, places: pj.places });
const texts = JSON.parse(process.argv[2]);
console.log(JSON.stringify(texts.map(t => { const r = P.parse(t); const a = r.actions[0];
  return (!r.ok || !a) ? { type: '', agents: null, target: '', err: true } : { type: a.type, agents: a.agents, target: a.target ? a.target.id : '', err: false }; })));`
	f := filepath.Join(t.TempDir(), "parity.mjs")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	tj, _ := json.Marshal(texts)
	out, err := exec.Command(node, f, string(tj)).Output()
	if err != nil {
		t.Fatalf("node:%v", err)
	}
	var jsRes []res
	if err := json.Unmarshal(out, &jsRes); err != nil {
		t.Fatalf("%v:%s", err, out)
	}
	for i := range texts {
		if goRes[i].Err && jsRes[i].Err {
			continue
		}
		if !reflect.DeepEqual(goRes[i], jsRes[i]) {
			t.Errorf("%q:Go %+v ≠ JS %+v", texts[i], goRes[i], jsRes[i])
		}
	}
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
