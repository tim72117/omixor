package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// 照 Swift 的 VoicePipelineTests 搬:用假的 shuttle 專案與假的 npm run voice(fakeVoiceRunner 模擬 voice.mjs 的
// --batch / --collect 輸出與檔案),不呼叫 Gemini、不上傳 GCS、不碰真正的 shuttle 與 Ops 資料。

// fakeVoiceRunner 模擬 Swift 測試裡的 scripts/voice.mjs
type fakeVoiceRunner struct {
	t     *testing.T
	calls [][]string
}

func (f *fakeVoiceRunner) Run(args []string, cwd string) VoiceShellResult {
	f.calls = append(f.calls, args)
	prefix := []string{"npm", "run", "--silent", "voice", "--"}
	if len(args) < len(prefix) || !slices.Equal(args[:len(prefix)], prefix) {
		f.t.Fatalf("不是 npm run voice:%v", args)
	}
	a := args[len(prefix):]
	has := func(flag string) bool { return slices.Contains(a, "--"+flag) }
	batch := filepath.Join(cwd, "recordings", "voice-batch.json")
	count := filepath.Join(cwd, "recordings", "count")
	live := filepath.Join(cwd, "src", "content", "live")
	die := func(m string) VoiceShellResult { return VoiceShellResult{Code: 1, Err: "錯誤:" + m + "\n"} }
	_ = os.MkdirAll(filepath.Join(cwd, "recordings"), 0o755)
	targets := []string{}
	for i, x := range a {
		if !strings.HasPrefix(x, "--") && !(i > 0 && a[i-1] == "--limit") {
			targets = append(targets, x)
		}
	}
	switch {
	case has("dry-run"):
		return VoiceShellResult{Out: "  launch/aaa.mp3  launch/1.1.json  新的一句\n共 2 段、10 字\n"}
	case has("batch"):
		if Exists(batch) {
			return die("已經有一批還沒收")
		}
		if slices.Contains(targets, "launch/empty.json") {
			return VoiceShellResult{Out: "沒有缺的旁白,不用送出\n"}
		}
		b, _ := json.Marshal(map[string]any{"name": "batches/fake1", "targets": targets,
			"items": []any{map[string]any{"key": "launch/aaa.mp3"}, map[string]any{"key": "launch/bbb.mp3"}}})
		_ = os.WriteFile(batch, b, 0o644)
		return VoiceShellResult{Out: "送出 2 段旁白(" + voiceTItoa(len(targets)) + " 份腳本),批次 batches/fake1。之後跑 npm run voice -- --collect 收結果\n"}
	case has("collect"):
		if !Exists(batch) {
			return die("沒有送出中的批次(recordings/voice-batch.json)")
		}
		n := 0
		if b, err := os.ReadFile(count); err == nil {
			n = voiceTAtoi(string(b))
		}
		_ = os.WriteFile(count, []byte(voiceTItoa(n+1)), 0o644)
		if n == 0 {
			return VoiceShellResult{Out: "批次 batches/fake1 還在處理(JOB_STATE_RUNNING),送出於 now。晚點再跑一次 --collect\n"}
		}
		_ = os.WriteFile(filepath.Join(live, "launch/1.1.voice.json"), []byte(`{
  "clips": {
    "舊的一句": {"file": "launch/old.mp3", "ms": 1200},
    "新的一句": {"file": "launch/aaa.mp3", "ms": 2500}
  }
}`), 0o644)
		_ = os.WriteFile(filepath.Join(live, "launch/1.2.voice.json"), []byte(`{"clips":{"另一句":{"file":"launch/bbb.mp3","ms":3000}}}`), 0o644)
		_ = os.Remove(batch)
		return VoiceShellResult{Out: "收到 2 段,1 段失敗(之後重送一次就會補上):\n  launch/ccc.mp3:{\"code\":500}\n"}
	}
	return die("不認得的參數")
}

func voiceTItoa(n int) string { b, _ := json.Marshal(n); return string(b) }
func voiceTAtoi(s string) int {
	var n int
	_ = json.Unmarshal([]byte(strings.TrimSpace(s)), &n)
	return n
}

type voiceTestEnv struct {
	t      *testing.T
	root   string
	runner *fakeVoiceRunner
}

func newVoiceTestEnv(t *testing.T) *voiceTestEnv {
	base := t.TempDir()
	e := &voiceTestEnv{t: t, root: filepath.Join(base, "shuttle"), runner: &fakeVoiceRunner{t: t}}
	data := filepath.Join(base, "data")
	_ = os.MkdirAll(data, 0o755)
	t.Setenv("OPS_SHUTTLE_PATH", e.root)
	t.Setenv("OPS_DATA_DIR", data)
	e.write("web/package.json", `{"name":"fake","private":true,"scripts":{"voice":"node scripts/voice.mjs"}}`)
	e.write("web/src/content/live/launch/1.1.json", `{"steps":[]}`)
	e.write("web/src/content/live/launch/1.2.json", `{"steps":[]}`)
	e.write("web/src/content/live/launch/empty.json", `{"steps":[]}`)
	e.write("web/src/content/live/launch/1.1.voice.json", `{"clips":{"舊的一句":{"file":"launch/old.mp3","ms":1200}}}`)
	e.write("server/internal/store/courses_seed.json",
		`[{"id":"launch","units":[{"id":"u1","lessons":[{"id":"1.1","script":"launch/1.1.json"}]}]}]`)
	return e
}

func (e *voiceTestEnv) write(rel, text string) {
	p := filepath.Join(e.root, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *voiceTestEnv) makeApp() (*App, *VoicePipeline) {
	a := New()
	p := StartVoice(a, false)
	p.Runner = e.runner
	return a, p
}

func voiceTDueNow(a *App, p *VoicePipeline) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	for i := range p.Jobs {
		p.Jobs[i].NextCheckAt = &Time{time.Unix(0, 0)}
	}
}

func voiceTJob(a *App, p *VoicePipeline, id string) VoiceJob {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	j := p.Job(id)
	if j == nil {
		return VoiceJob{}
	}
	return *j
}

func voiceTSubmit(t *testing.T, a *App, p *VoicePipeline, scripts []string, limit *int, actor Actor) VoiceJob {
	t.Helper()
	a.Mu.Lock()
	defer a.Mu.Unlock()
	j, err := p.Submit(scripts, false, limit, actor)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func voiceLogText(j VoiceJob) string {
	s := []string{}
	for _, l := range j.Log {
		s = append(s, l.Text)
	}
	return strings.Join(s, "\n")
}

func TestVoiceFullFlowSubmitPollCollect(t *testing.T) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	jb := voiceTSubmit(t, a, p, []string{"launch/1.1.json"}, nil, MCPActor)
	if jb.State != VoiceQueued || jb.By != MCPActorID {
		t.Fatalf("state=%s by=%s", jb.State, jb.By)
	}

	p.Tick() // 送出
	j := voiceTJob(a, p, jb.ID)
	if j.State != VoiceProcessing {
		t.Fatalf("送出後 state=%s\n%s", j.State, voiceLogText(j))
	}
	if j.BatchName == nil || *j.BatchName != "batches/fake1" || j.ClipCount == nil || *j.ClipCount != 2 {
		t.Fatalf("batch=%v clips=%v", j.BatchName, j.ClipCount)
	}
	if !slices.Equal(j.Baseline["launch/1.1.voice.json"].Files, []string{"launch/old.mp3"}) {
		t.Fatalf("baseline=%v", j.Baseline)
	}

	p.Tick() // 還沒到下次檢查時間:不動
	if voiceTJob(a, p, jb.ID).Checks != 0 {
		t.Fatal("還沒到時間就查了")
	}

	voiceTDueNow(a, p)
	p.Tick() // 第一次查:還在處理
	j = voiceTJob(a, p, jb.ID)
	if j.State != VoiceProcessing || j.BatchState == nil || *j.BatchState != "JOB_STATE_RUNNING" {
		t.Fatalf("state=%s batchState=%v", j.State, j.BatchState)
	}
	if j.NextCheckAt == nil || !j.NextCheckAt.After(time.Now()) {
		t.Fatal("nextCheckAt 應該在未來")
	}
	if !strings.Contains(voiceLogText(j), "還在處理(JOB_STATE_RUNNING),10 分鐘後再查") {
		t.Fatal(voiceLogText(j))
	}

	voiceTDueNow(a, p)
	p.Tick() // 第二次查:收完
	j = voiceTJob(a, p, jb.ID)
	if j.State != VoiceReady {
		t.Fatalf("state=%s\n%s", j.State, voiceLogText(j))
	}
	if len(j.Changes) != 2 {
		t.Fatalf("changes=%d", len(j.Changes))
	}
	var c11, c12 *VoiceFileChange
	for i := range j.Changes {
		switch j.Changes[i].Path {
		case "launch/1.1.voice.json":
			c11 = &j.Changes[i]
		case "launch/1.2.voice.json":
			c12 = &j.Changes[i]
		}
	}
	if c11 == nil || c12 == nil {
		t.Fatal("少了變動的清單")
	}
	if c11.IsNew || len(c11.Added) != 1 || c11.Added[0].File != "launch/aaa.mp3" || c11.Clips != 2 {
		t.Fatalf("c11=%+v", c11)
	}
	if c11.Course == nil || *c11.Course != "launch" || c11.Lesson == nil || *c11.Lesson != "1.1" {
		t.Fatalf("course/lesson=%v/%v", c11.Course, c11.Lesson)
	}
	if c11.Added[0].URL() != "https://storage.googleapis.com/shuttle-voice/launch/aaa.mp3" {
		t.Fatal(c11.Added[0].URL())
	}
	if !c12.IsNew || c12.Course != nil {
		t.Fatalf("c12=%+v", c12)
	}
	if len(j.FailedClips) != 1 {
		t.Fatalf("failedClips=%v", j.FailedClips)
	}
	b, _ := View(a, "badges")
	if n, _ := b.(map[string]any)["pipelines"].(float64); n < 1 {
		t.Fatalf("badges=%v", b)
	}

	// 存檔:重開 app 讀得回來
	a2, p2 := e.makeApp()
	a2.Mu.Lock()
	first := p2.Jobs[0].ID
	a2.Mu.Unlock()
	if first != jb.ID || voiceTJob(a2, p2, jb.ID).State != VoiceReady {
		t.Fatalf("重開後 first=%s state=%s", first, voiceTJob(a2, p2, jb.ID).State)
	}
}

func TestVoiceQueueOneBatchAtATime(t *testing.T) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	ja := voiceTSubmit(t, a, p, []string{"launch/1.1.json"}, nil, Person(a.Me))
	jb := voiceTSubmit(t, a, p, []string{"launch/1.2.json"}, voicePtr(3), Person(a.Me))
	p.Tick()
	if voiceTJob(a, p, ja.ID).State != VoiceProcessing || voiceTJob(a, p, jb.ID).State != VoiceQueued {
		t.Fatalf("a=%s b=%s", voiceTJob(a, p, ja.ID).State, voiceTJob(a, p, jb.ID).State)
	}
	voiceTDueNow(a, p)
	p.Tick() // a 還在處理
	voiceTDueNow(a, p)
	p.Tick() // a 收完,接著送 b
	if voiceTJob(a, p, ja.ID).State != VoiceReady || voiceTJob(a, p, jb.ID).State != VoiceProcessing {
		t.Fatalf("a=%s b=%s", voiceTJob(a, p, ja.ID).State, voiceTJob(a, p, jb.ID).State)
	}
	// b 送出時帶了 --limit 3
	last := e.runner.calls[len(e.runner.calls)-1]
	if !slices.Equal(last[5:], []string{"launch/1.2.json", "--limit", "3", "--batch"}) {
		t.Fatalf("送出 b 的參數:%v", last)
	}
}

func TestVoiceNothingToDoAndValidation(t *testing.T) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	j := voiceTSubmit(t, a, p, []string{"launch/empty"}, nil, MCPActor) // 自動補 .json
	p.Tick()
	if voiceTJob(a, p, j.ID).State != VoiceNothingToDo {
		t.Fatalf("state=%s", voiceTJob(a, p, j.ID).State)
	}
	a.Mu.Lock()
	for _, tc := range []struct {
		scripts []string
		msg     string
	}{
		{[]string{"launch/nope.json"}, "找不到腳本 src/content/live/launch/nope.json"},
		{[]string{"launch/1.1.voice.json"}, "launch/1.1.voice.json 是產生出來的清單,請給腳本本身"},
		{[]string{}, "至少要指定一份腳本,或選「全部腳本」"},
	} {
		if _, err := p.Submit(tc.scripts, false, nil, MCPActor); err == nil || err.Error() != tc.msg {
			t.Errorf("%v:%v", tc.scripts, err)
		}
	}
	a.Mu.Unlock()
	out, err := p.DryRun([]string{"web/src/content/live/launch/1.1.json"}, false, nil)
	if err != nil || !strings.Contains(out, "共 2 段") {
		t.Fatalf("dryRun=%q err=%v", out, err)
	}
	last := e.runner.calls[len(e.runner.calls)-1]
	if !slices.Equal(last[5:], []string{"launch/1.1.json", "--batch", "--dry-run"}) {
		t.Fatalf("dry run 參數:%v", last)
	}
}

func TestVoiceFailedClipParsing(t *testing.T) {
	ok := "launch/2.1.json [█░░] 1/40\nlaunch/2.1.json:41 段旁白(新產生 1),共 15 秒\n收到 1 段"
	if got := VoiceFailedClips(ok); len(got) != 0 {
		t.Fatal(got)
	}
	bad := "進度…\n收到 2 段,1 段失敗(之後重送一次就會補上):\n  launch/ccc.mp3:{\"code\":500}"
	if got := VoiceFailedClips(bad); !slices.Equal(got, []string{`launch/ccc.mp3:{"code":500}`}) {
		t.Fatal(got)
	}
}

func TestVoiceAdoptsBatchSubmittedOutsideOps(t *testing.T) {
	e := newVoiceTestEnv(t)
	e.write("web/recordings/voice-batch.json", `{"name":"batches/term","targets":["launch/1.1.json"],"items":[{"key":"x"}]}`)
	a, p := e.makeApp()
	p.Tick()
	a.Mu.Lock()
	j := p.Jobs[0]
	a.Mu.Unlock()
	if j.By != "external" || j.BatchName == nil || *j.BatchName != "batches/term" || !slices.Equal(j.Scripts, []string{"launch/1.1.json"}) {
		t.Fatalf("%+v", j)
	}
}

// 以下是 Go 版另外加的測試

func TestVoiceTitleAndNormalize(t *testing.T) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	a.Mu.Lock()
	j, err := p.Submit([]string{"launch/1.1.json, launch/1.2\nlaunch/1.1.json", "launch/empty.json"}, false, voicePtr(5), MCPActor)
	a.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j.Scripts, []string{"launch/1.1.json", "launch/1.2.json", "launch/empty.json"}) {
		t.Fatal(j.Scripts)
	}
	if j.Title() != "launch/1.1.json、launch/1.2.json、launch/empty.json(每份前 5 段)" {
		t.Fatal(j.Title())
	}
	j.Scripts = append(j.Scripts, "x.json")
	if j.Title() != "launch/1.1.json、launch/1.2.json、launch/empty.json 等 4 份(每份前 5 段)" {
		t.Fatal(j.Title())
	}
	if (VoiceJob{All: true}).Title() != "全部腳本" {
		t.Fatal("all")
	}
	// 動態牆
	a.Mu.Lock()
	text := a.C["activity"][0]["text"]
	a.Mu.Unlock()
	if text != "建立旁白批次 VJ-1:launch/1.1.json、launch/1.2.json、launch/empty.json(每份前 5 段)" {
		t.Fatal(text)
	}
}

func TestVoiceRestartRecoversStates(t *testing.T) {
	e := newVoiceTestEnv(t)
	// Swift 版寫的檔(縮排、" : "、沒有小數秒)
	raw := `[
  {
    "all" : false,
    "baseline" : {},
    "by" : "tim",
    "changes" : [],
    "checks" : 0,
    "createdAt" : "2026-11-10T01:00:00Z",
    "failedClips" : [],
    "id" : "VJ-3",
    "log" : [],
    "scripts" : ["launch/1.1.json"],
    "state" : "submitting",
    "updatedAt" : "2026-11-10T01:00:00Z"
  },
  {
    "all" : false, "baseline" : {}, "by" : "mcp", "changes" : [], "checks" : 2,
    "createdAt" : "2026-11-10T00:00:00Z", "failedClips" : [], "id" : "VJ-2", "log" : [],
    "scripts" : [], "state" : "collecting", "updatedAt" : "2026-11-10T00:00:00Z"
  },
  {
    "all" : true, "baseline" : {}, "by" : "tim", "changes" : [], "checks" : 1,
    "createdAt" : "2026-11-09T00:00:00Z", "failedClips" : [], "id" : "VJ-1", "log" : [],
    "scripts" : [], "state" : "publishing", "updatedAt" : "2026-11-09T00:00:00Z"
  }
]`
	if err := os.WriteFile(filepath.Join(DataRoot(), "voice-jobs.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	a, p := e.makeApp()
	if s := voiceTJob(a, p, "VJ-3").State; s != VoiceQueued {
		t.Fatal("submitting 沒有 batch 檔應該回到 queued:", s)
	}
	if s := voiceTJob(a, p, "VJ-2").State; s != VoiceProcessing {
		t.Fatal(s)
	}
	if s := voiceTJob(a, p, "VJ-1").State; s != VoiceReady {
		t.Fatal(s)
	}
	// 寫回去的檔 Swift 讀得懂:key 排序、時間沒有小數秒
	b, _ := os.ReadFile(filepath.Join(DataRoot(), "voice-jobs.json"))
	var back []map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back[0]["createdAt"] != "2026-11-10T01:00:00Z" || back[0]["nextCheckAt"] == nil || strings.Contains(back[0]["nextCheckAt"].(string), ".") {
		t.Fatal(back[0])
	}
	if _, ok := back[0]["limit"]; ok {
		t.Fatal("nil 的欄位不應該輸出")
	}
	// 新的 id 接著最大的號碼
	a.Mu.Lock()
	j, _ := p.Submit([]string{"launch/1.2.json"}, false, nil, MCPActor)
	a.Mu.Unlock()
	if j.ID != "VJ-4" {
		t.Fatal(j.ID)
	}
}

func TestVoiceCollectErrors(t *testing.T) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	jb := voiceTSubmit(t, a, p, []string{"launch/1.1.json"}, nil, MCPActor)
	p.Tick()
	// 紀錄被手動刪掉:失敗
	_ = os.Remove(filepath.Join(e.root, "web/recordings/voice-batch.json"))
	voiceTDueNow(a, p)
	p.Tick()
	j := voiceTJob(a, p, jb.ID)
	if j.State != VoiceFailed || j.Error == nil || *j.Error != "錯誤:沒有送出中的批次(recordings/voice-batch.json)" {
		t.Fatalf("%s %v", j.State, j.Error)
	}
	// 失敗的可以取消
	a.Mu.Lock()
	p.Cancel(jb.ID)
	a.Mu.Unlock()
	if voiceTJob(a, p, jb.ID).State != VoiceCanceled {
		t.Fatal("cancel")
	}

	// 其他錯誤:下一輪重試
	jc := voiceTSubmit(t, a, p, []string{"launch/1.2.json"}, nil, MCPActor)
	p.Tick()
	p.Runner = voiceRunnerFunc(func(args []string, cwd string) VoiceShellResult {
		return VoiceShellResult{Code: 1, Err: "network down"}
	})
	voiceTDueNow(a, p)
	p.Tick()
	j = voiceTJob(a, p, jc.ID)
	if j.State != VoiceProcessing || j.Error == nil || *j.Error != "network down" ||
		!strings.Contains(voiceLogText(j), "收結果時出錯,10 分鐘後重試:network down") {
		t.Fatalf("%s %v\n%s", j.State, j.Error, voiceLogText(j))
	}
	// 送出失敗
	p.Runner = e.runner
	_ = os.Remove(filepath.Join(e.root, "web/recordings/voice-batch.json"))
	a.Mu.Lock()
	p.Jobs[0].State = VoiceCanceled
	a.Mu.Unlock()
	jd := voiceTSubmit(t, a, p, []string{"launch/1.1.json"}, nil, MCPActor)
	p.Runner = voiceRunnerFunc(func(args []string, cwd string) VoiceShellResult {
		return VoiceShellResult{Code: 1, Out: "quota"}
	})
	p.Tick()
	if j := voiceTJob(a, p, jd.ID); j.State != VoiceFailed || !strings.Contains(voiceLogText(j), "送出失敗:quota") {
		t.Fatalf("%s\n%s", j.State, voiceLogText(j))
	}
}

type voiceRunnerFunc func(args []string, cwd string) VoiceShellResult

func (f voiceRunnerFunc) Run(args []string, cwd string) VoiceShellResult { return f(args, cwd) }

// MARK: 發佈(假的連線)

type voiceFakeConn struct {
	target   *VoiceTarget
	token    bool
	reqs     []string
	stored   map[string][]byte
	failPut  error
	mangle   bool
	lastBody []byte
}

func (f *voiceFakeConn) Target(a *App, id string) (VoiceTarget, bool) {
	if f.target == nil || f.target.ID != id {
		return VoiceTarget{}, false
	}
	return *f.target, true
}
func (f *voiceFakeConn) HasToken(a *App, id string) bool       { return f.token }
func (f *voiceFakeConn) HasTokenCached(a *App, id string) bool { return f.token }
func (f *voiceFakeConn) Request(a *App, id, method, path string, body []byte) (any, error) {
	f.reqs = append(f.reqs, method+" "+path)
	switch method {
	case "PUT":
		if f.failPut != nil {
			return nil, f.failPut
		}
		key := strings.TrimPrefix(strings.SplitN(path, "?", 2)[0], "/api/admin/scripts/")
		f.stored[key] = body
		return map[string]any{"ok": true}, nil
	case "GET":
		key := strings.TrimPrefix(path, "/api/scripts/")
		var v any
		_ = json.Unmarshal(f.stored[key], &v)
		if f.mangle {
			v = map[string]any{"clips": map[string]any{}}
		}
		return v, nil
	}
	return nil, errors.New("bad method")
}

func voiceReadyJob(t *testing.T) (*voiceTestEnv, *App, *VoicePipeline, string) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	jb := voiceTSubmit(t, a, p, []string{"launch/1.1.json"}, nil, MCPActor)
	p.Tick()
	voiceTDueNow(a, p)
	p.Tick()
	voiceTDueNow(a, p)
	p.Tick()
	if s := voiceTJob(a, p, jb.ID).State; s != VoiceReady {
		t.Fatal(s)
	}
	return e, a, p, jb.ID
}

func TestVoicePublish(t *testing.T) {
	_, a, p, id := voiceReadyJob(t)
	conn := &voiceFakeConn{target: &VoiceTarget{ID: "evoleap", Name: "evoleap", BaseURL: "https://evoleap.example"}, stored: map[string][]byte{}}
	p.Conn = conn

	conn.token = false
	p.Publish(id)
	if j := voiceTJob(a, p, id); j.State != VoiceReady || !strings.HasSuffix(voiceLogText(j), "發佈前要先登入「evoleap」") || len(conn.reqs) != 0 {
		t.Fatal(voiceLogText(j))
	}
	conn.token = true
	p.Publish(id)
	j := voiceTJob(a, p, id)
	if j.State != VoicePublished || j.PublishedTo == nil || *j.PublishedTo != "evoleap" || j.PublishedAt == nil {
		t.Fatalf("%s\n%s", j.State, voiceLogText(j))
	}
	want := []string{
		"PUT /api/admin/scripts/launch/1.1.voice.json?course=launch&lesson=1.1",
		"GET /api/scripts/launch/1.1.voice.json",
		"PUT /api/admin/scripts/launch/1.2.voice.json",
		"GET /api/scripts/launch/1.2.voice.json",
	}
	if !slices.Equal(conn.reqs, want) {
		t.Fatal(conn.reqs)
	}
	for _, c := range j.Changes {
		if c.Publish == nil || *c.Publish != "ok" {
			t.Fatal(c.Path, c.Publish)
		}
	}
	a.Mu.Lock()
	text := a.C["activity"][0]["text"]
	a.Mu.Unlock()
	if text != "發佈旁白 "+id+" 到「evoleap」(2 份)" {
		t.Fatal(text)
	}
	// 已發佈的不會再發佈
	p.Publish(id)
	if len(conn.reqs) != 4 {
		t.Fatal(conn.reqs)
	}
}

func TestVoicePublishFailures(t *testing.T) {
	e, a, p, id := voiceReadyJob(t)
	// 沒有連線
	p.Conn = &voiceFakeConn{}
	p.Publish(id)
	if j := voiceTJob(a, p, id); !strings.HasSuffix(voiceLogText(j), "找不到發佈目的地的連線,請在設定 › 連線 確認") {
		t.Fatal(voiceLogText(j))
	}
	conn := &voiceFakeConn{target: &VoiceTarget{ID: "evoleap", Name: "正式站"}, token: true, stored: map[string][]byte{}, mangle: true}
	p.Conn = conn
	// 1.2 收結果後又被改過
	e.write("web/src/content/live/launch/1.2.voice.json", `{"clips":{}}`)
	p.Publish(id)
	j := voiceTJob(a, p, id)
	if j.State != VoiceReady {
		t.Fatal(j.State)
	}
	res := map[string]string{}
	for _, c := range j.Changes {
		res[c.Path] = *c.Publish
	}
	if res["launch/1.1.voice.json"] != "寫入成功,但讀回來的內容和本機不同" ||
		res["launch/1.2.voice.json"] != "收結果後這個檔又被改過,請重新檢查後再發佈" {
		t.Fatal(res)
	}
	if !strings.HasSuffix(voiceLogText(j), "有檔案發佈失敗,修好後可以再按一次發佈(已成功的不會重送)") {
		t.Fatal(voiceLogText(j))
	}
	a.Mu.Lock()
	text := a.C["activity"][0]["text"]
	a.Mu.Unlock()
	if text != "部分發佈旁白 "+id+" 到「正式站」(2 份)" {
		t.Fatal(text)
	}
	// 請求錯誤
	conn.failPut = errors.New("HTTP 401:未登入")
	conn.mangle = false
	p.Publish(id)
	j = voiceTJob(a, p, id)
	if *j.Changes[0].Publish != "HTTP 401:未登入" {
		t.Fatal(*j.Changes[0].Publish)
	}
}

func TestVoiceEscape(t *testing.T) {
	if got := VoiceEscapePath("launch/1 1#?.voice.json"); got != "launch/1%201%23%3F.voice.json" {
		t.Fatal(got)
	}
	if got := VoiceEscapePath("課程/a.json"); got != "%E8%AA%B2%E7%A8%8B/a.json" {
		t.Fatal(got)
	}
	if got := VoiceFormEncode(map[string]string{"lesson": "1.1 a&b", "course": "launch"}); got != "course=launch&lesson=1.1%20a%26b" {
		t.Fatal(got)
	}
}

func TestVoiceActions(t *testing.T) {
	e := newVoiceTestEnv(t)
	a, p := e.makeApp()
	j := voiceTSubmit(t, a, p, []string{"launch/1.1.json"}, nil, Person(a.Me))
	if _, err := Action(a, "voice_cancel", map[string]any{"id": "VJ-9"}); err == nil || err.Error() != "找不到旁白批次 VJ-9" {
		t.Fatal(err)
	}
	out, err := Action(a, "voice_cancel", map[string]any{"id": j.ID})
	if err != nil || out.(VoiceJob).State != VoiceCanceled {
		t.Fatal(out, err)
	}
	if _, err := Action(a, "voice_discard", map[string]any{"id": j.ID}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	n := len(p.Jobs)
	a.Mu.Unlock()
	if n != 0 {
		t.Fatal("discard 沒有移除")
	}
	v, _ := View(a, "voice_jobs")
	if l, ok := v.([]any); !ok || len(l) != 0 {
		t.Fatal(v)
	}
	s, err := Action(a, "voice_settings", map[string]any{"pollMinutes": float64(3)})
	if err != nil || s.(map[string]any)["pollMinutes"] != 3 {
		t.Fatal(s, err)
	}
	if _, err := Action(a, "voice_settings", map[string]any{"pollMinutes": float64(0)}); err == nil {
		t.Fatal("0 分鐘應該不行")
	}
}
