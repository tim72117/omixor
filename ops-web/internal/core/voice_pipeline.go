package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 旁白語音批次的流程,同 Swift 版 Voice/VoicePipeline.swift:
//
//	送出   npm run voice -- <腳本…> --batch    (Gemini Batch API,一次只能有一批)
//	輪詢   npm run voice -- --collect          (沒跑完不會動任何東西;跑完就下載、轉 mp3、上傳 GCS、改寫 .voice.json)
//	待發佈 比對收結果前後的 .voice.json,列出新增的段落,可試聽
//	發佈   人按下後 PUT /api/admin/scripts/<路徑>,線上立即生效(voice_publish.go)
//
// 外部程式(VoiceRunner)與發佈用的連線(VoiceConnector)都可以替換,測試用假的。
// voice-jobs.json 放在資料夾根目錄(全部組織共用),格式同 Swift 版。
//
// 和 Swift 版的設定(UserDefaults voice.shuttlePath / voice.pollMinutes / voice.connection)對應的是
// 資料夾根目錄的 voice-settings.json(Go 版自己的檔案)。

// VoiceSettings 旁白設定
type VoiceSettings struct {
	Connection  string `json:"connection"`  // 發佈到哪個連線,預設 evoleap
	PollMinutes int    `json:"pollMinutes"` // 每幾分鐘查一次,預設 10
	ShuttlePath string `json:"shuttlePath"` // shuttle 專案路徑,預設 ~/Documents/shuttle(OPS_SHUTTLE_PATH 優先)
}

// VoicePipeline 旁白批次。Jobs 與 Settings 要拿 App.Mu 讀寫。
type VoicePipeline struct {
	app      *App
	Runner   VoiceRunner    // 外部程式;預設真的執行
	Conn     VoiceConnector // 發佈用的連線;nil 時用 core.VoiceConn
	Jobs     []VoiceJob
	Settings VoiceSettings
	timerOn  bool
	tickMu   sync.Mutex // 同 Swift 的 busy:一次只跑一輪
	stop     chan struct{}
}

type voiceRefuseRunner struct{}

func (voiceRefuseRunner) Run(args []string, cwd string) VoiceShellResult {
	return VoiceShellResult{Code: -1, Err: "測試中不執行外部程式:" + strings.Join(args, " ")}
}

var voicePipes sync.Map // *App → *VoicePipeline

// VoiceOf 取得 app 的旁白批次(還沒啟動回傳 nil)
func VoiceOf(a *App) *VoicePipeline {
	if v, ok := voicePipes.Load(a); ok {
		return v.(*VoicePipeline)
	}
	return nil
}

// StartVoice 啟動旁白批次(同 Swift 的 startVoice(timer:));已經啟動就直接回傳。呼叫時不要拿著 Mu。
func StartVoice(a *App, timer bool) *VoicePipeline {
	if p := VoiceOf(a); p != nil {
		return p
	}
	p := &VoicePipeline{app: a, Runner: VoiceExecRunner{}}
	if testing.Testing() {
		// 測試裡絕不真的跑 npm run voice(會呼叫 Gemini 花錢);要測流程請換成假的 Runner
		p.Runner = voiceRefuseRunner{}
		p.Conn = voiceNoConn{}
	}
	if v, loaded := voicePipes.LoadOrStore(a, p); loaded {
		return v.(*VoicePipeline)
	}
	p.start(timer)
	return p
}

func init() {
	OnStart(func(a *App) { StartVoice(a, os.Getenv("OPS_NO_TIMERS") != "1") })
}

// MARK: - 設定

func voiceSettingsPath() string { return filepath.Join(DataRoot(), "voice-settings.json") }

func (p *VoicePipeline) loadSettings() {
	var s VoiceSettings
	_ = ReadJSON(voiceSettingsPath(), &s)
	p.Settings = s
}

// SaveSettings 存設定(呼叫時拿著 Mu)
func (p *VoicePipeline) SaveSettings() { _ = WriteJSON(voiceSettingsPath(), p.Settings) }

func (p *VoicePipeline) ShuttleRoot() string {
	if e := os.Getenv("OPS_SHUTTLE_PATH"); e != "" {
		return e
	}
	s := p.Settings.ShuttlePath
	home, _ := os.UserHomeDir()
	if s == "" {
		s = filepath.Join(home, "Documents", "shuttle")
	}
	if s == "~" {
		s = home
	} else if strings.HasPrefix(s, "~/") {
		s = filepath.Join(home, s[2:])
	}
	return s
}

func (p *VoicePipeline) webDir() string  { return filepath.Join(p.ShuttleRoot(), "web") }
func (p *VoicePipeline) liveDir() string { return filepath.Join(p.webDir(), "src", "content", "live") }
func (p *VoicePipeline) batchFile() string {
	return filepath.Join(p.webDir(), "recordings", "voice-batch.json")
}
func (p *VoicePipeline) seedFile() string {
	return filepath.Join(p.ShuttleRoot(), "server", "internal", "store", "courses_seed.json")
}

func (p *VoicePipeline) PollMinutes() int {
	if p.Settings.PollMinutes == 0 {
		return 10
	}
	return max(1, p.Settings.PollMinutes)
}

func (p *VoicePipeline) TargetID() string {
	if p.Settings.Connection == "" {
		return "evoleap"
	}
	return p.Settings.Connection
}

func (p *VoicePipeline) pollDelay() time.Duration {
	return time.Duration(p.PollMinutes()) * time.Minute
}

// MARK: - 啟動與儲存

func voiceStorePath() string { return filepath.Join(DataRoot(), "voice-jobs.json") }

func (p *VoicePipeline) start(timer bool) {
	a := p.app
	a.Mu.Lock()
	p.loadSettings()
	p.load()
	// 重開 app 時,做到一半的步驟回到可以重試的狀態
	for i := range p.Jobs {
		j := &p.Jobs[i]
		switch j.State {
		case VoiceSubmitting:
			if Exists(p.batchFile()) {
				j.State = VoiceProcessing
			} else {
				j.State = VoiceQueued
			}
			j.NextCheckAt = voiceTimePtr(time.Now())
		case VoiceCollecting:
			j.State = VoiceProcessing
			j.NextCheckAt = voiceTimePtr(time.Now())
		case VoicePublishing:
			j.State = VoiceReady
		}
	}
	p.save()
	p.timerOn = timer
	a.Mu.Unlock()
	if !timer {
		return
	}
	p.stop = make(chan struct{})
	go func() {
		p.Tick()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p.Tick()
			case <-p.stop:
				return
			}
		}
	}()
}

func (p *VoicePipeline) load() {
	b, err := ReadData(voiceStorePath())
	if err != nil {
		return
	}
	var jobs []VoiceJob
	if json.Unmarshal(b, &jobs) != nil {
		jobs = nil
	}
	for i := range jobs {
		jobs[i].fix()
	}
	p.Jobs = jobs
}

// save 存檔並通知畫面(呼叫時拿著 Mu)
func (p *VoicePipeline) save() {
	jobs := p.Jobs
	if jobs == nil {
		jobs = []VoiceJob{}
	}
	_ = WriteJSON(voiceStorePath(), Object(jobs))
	go p.app.Changed("voice_jobs", "badges")
}

// update 改一個工作並存檔(呼叫時拿著 Mu)
func (p *VoicePipeline) update(id string, change func(j *VoiceJob)) {
	for i := range p.Jobs {
		if p.Jobs[i].ID == id {
			change(&p.Jobs[i])
			p.Jobs[i].UpdatedAt = Now()
			p.save()
			return
		}
	}
}

// Job 依 id 找工作(呼叫時拿著 Mu)
func (p *VoicePipeline) Job(id string) *VoiceJob {
	for i := range p.Jobs {
		if p.Jobs[i].ID == id {
			return &p.Jobs[i]
		}
	}
	return nil
}

func (p *VoicePipeline) nextID() string {
	n := 0
	for _, j := range p.Jobs {
		if len(j.ID) > 3 {
			if v, err := strconv.Atoi(j.ID[3:]); err == nil && v > n {
				n = v
			}
		}
	}
	return fmt.Sprintf("VJ-%d", n+1)
}

// MARK: - 送出

// Submit 建立一個批次工作(排隊,輪到時送出)。呼叫時拿著 Mu;回傳的錯誤是 UserError。
func (p *VoicePipeline) Submit(scripts []string, all bool, limit *int, actor Actor) (VoiceJob, error) {
	scripts, err := p.normalize(scripts)
	if err != nil {
		return VoiceJob{}, err
	}
	if !all && len(scripts) == 0 {
		return VoiceJob{}, UserError{"至少要指定一份腳本,或選「全部腳本」"}
	}
	if all {
		scripts = []string{}
	}
	now := Now()
	job := VoiceJob{ID: p.nextID(), Scripts: scripts, All: all, Limit: limit, State: VoiceQueued,
		CreatedAt: now, UpdatedAt: now, By: VoiceActorID(actor)}
	job.fix()
	job.Note("建立:" + job.Title())
	p.Jobs = append([]VoiceJob{job}, p.Jobs...)
	p.save()
	p.app.Log(actor, "waveform", fmt.Sprintf("建立旁白批次 %s:%s", job.ID, job.Title()), "pipelines")
	if p.timerOn {
		go p.Tick()
	}
	return job, nil
}

// DryRun 試算:列出會送哪些段落,不呼叫 API、不花錢(但會執行 npm run voice -- --dry-run)。呼叫時不要拿著 Mu。
func (p *VoicePipeline) DryRun(scripts []string, all bool, limit *int) (string, error) {
	p.app.Mu.Lock()
	scripts, err := p.normalize(scripts)
	p.app.Mu.Unlock()
	if err != nil {
		return "", err
	}
	if !all && len(scripts) == 0 {
		return "", UserError{"至少要指定一份腳本,或選「全部腳本」"}
	}
	r := p.npm(append(voiceArgs(scripts, all, limit), "--batch", "--dry-run"))
	if !r.OK() {
		return "", UserError{r.Message()}
	}
	return strings.TrimSpace(r.Out), nil
}

// Cancel 取消排隊中或失敗的工作(呼叫時拿著 Mu)
func (p *VoicePipeline) Cancel(id string) {
	j := p.Job(id)
	if j == nil || (j.State != VoiceQueued && j.State != VoiceFailed) {
		return
	}
	p.update(id, func(j *VoiceJob) { j.State = VoiceCanceled; j.Note("已取消") })
}

// CheckNow 立刻查一次,不等下一輪(Swift 的「立即檢查」;呼叫時不要拿著 Mu)
func (p *VoicePipeline) CheckNow(id string) {
	p.app.Mu.Lock()
	p.update(id, func(j *VoiceJob) { j.NextCheckAt = voiceTimePtr(time.Now()) })
	p.app.Mu.Unlock()
	go p.Tick()
}

func (p *VoicePipeline) normalize(scripts []string) ([]string, error) {
	out := []string{}
	for _, s0 := range scripts {
		for _, raw := range strings.FieldsFunc(s0, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' }) {
			s := strings.Trim(raw, " \t")
			if s == "" {
				continue
			}
			if i := strings.Index(s, "src/content/live/"); i >= 0 {
				s = s[i+len("src/content/live/"):]
			}
			if !strings.HasSuffix(s, ".json") {
				s += ".json"
			}
			if strings.HasSuffix(s, ".voice.json") || strings.HasSuffix(s, ".mix.json") {
				return nil, UserError{s + " 是產生出來的清單,請給腳本本身"}
			}
			if !Exists(filepath.Join(p.liveDir(), s)) {
				return nil, UserError{"找不到腳本 src/content/live/" + s}
			}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func voiceArgs(scripts []string, all bool, limit *int) []string {
	a := append([]string{}, scripts...)
	if all {
		a = []string{"--all"}
	}
	if limit != nil && *limit > 0 {
		a = append(a, "--limit", strconv.Itoa(*limit))
	}
	return a
}

func (p *VoicePipeline) npm(args []string) VoiceShellResult {
	p.app.Mu.Lock()
	dir, runner := p.webDir(), p.Runner
	p.app.Mu.Unlock()
	return runner.Run(append([]string{"npm", "run", "--silent", "voice", "--"}, args...), dir)
}

// MARK: - 輪詢

// Tick 跑一輪:接手外部批次、查進度、送出排隊的下一批。呼叫時不要拿著 Mu。
func (p *VoicePipeline) Tick() {
	if !p.tickMu.TryLock() {
		return
	}
	defer p.tickMu.Unlock()
	a := p.app
	a.Mu.Lock()
	p.adoptExternalBatch()
	due := ""
	for _, j := range p.Jobs {
		if j.State == VoiceProcessing {
			if j.NextCheckAt == nil || !j.NextCheckAt.After(time.Now()) {
				due = j.ID
			}
			break
		}
	}
	a.Mu.Unlock()
	if due != "" {
		p.collect(due)
	}
	// 收完就空出位置,同一輪接著送排隊的下一批
	a.Mu.Lock()
	holds, next := false, ""
	for _, j := range p.Jobs {
		if j.State.HoldsBatch() {
			holds = true
		}
		if j.State == VoiceQueued {
			next = j.ID // 最後一個(最早建立的)
		}
	}
	a.Mu.Unlock()
	if !holds && next != "" {
		p.send(next)
	}
}

// adoptExternalBatch Ops 以外(終端機)送出的批次也接手追蹤(呼叫時拿著 Mu)
func (p *VoicePipeline) adoptExternalBatch() {
	for _, j := range p.Jobs {
		if j.State.HoldsBatch() {
			return
		}
	}
	saved := p.readBatchFile()
	name, ok := saved["name"].(string)
	if saved == nil || !ok {
		return
	}
	now := Now()
	scripts := []string{}
	if t, ok := saved["targets"].([]any); ok {
		ss := []string{}
		allStrings := true
		for _, x := range t {
			s, ok := x.(string)
			if !ok {
				allStrings = false
				break
			}
			ss = append(ss, s)
		}
		if allStrings {
			scripts = ss
		}
	}
	job := VoiceJob{ID: p.nextID(), Scripts: scripts, State: VoiceProcessing, CreatedAt: now, UpdatedAt: now, By: "external"}
	job.fix()
	job.BatchName = &name
	if items, ok := saved["items"].([]any); ok {
		job.ClipCount = voicePtr(len(items))
	}
	job.Baseline = p.Snapshot()
	job.NextCheckAt = voiceTimePtr(time.Now())
	job.Note("接手 Ops 以外送出的批次 " + name + "(送出時的清單沒有記錄,改用現在的狀態比對)")
	p.Jobs = append([]VoiceJob{job}, p.Jobs...)
	p.save()
}

func (p *VoicePipeline) readBatchFile() map[string]any {
	b, err := os.ReadFile(p.batchFile())
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func (p *VoicePipeline) send(id string) {
	a := p.app
	a.Mu.Lock()
	jp := p.Job(id)
	if jp == nil {
		a.Mu.Unlock()
		return
	}
	j := *jp
	if Exists(p.batchFile()) {
		p.update(id, func(j *VoiceJob) { j.Note("已經有一批還沒收,等它收完再送") })
		a.Mu.Unlock()
		return
	}
	p.update(id, func(j *VoiceJob) {
		j.State = VoiceSubmitting
		j.Baseline = p.Snapshot()
		j.Note("送出批次…")
	})
	a.Mu.Unlock()

	r := p.npm(append(voiceArgs(j.Scripts, j.All, j.Limit), "--batch"))

	a.Mu.Lock()
	defer a.Mu.Unlock()
	if !r.OK() {
		msg := r.Message()
		p.update(id, func(j *VoiceJob) { j.State = VoiceFailed; j.Error = &msg; j.Note("送出失敗:" + msg) })
		return
	}
	if strings.Contains(r.Out, "沒有缺的旁白") {
		p.update(id, func(j *VoiceJob) { j.State = VoiceNothingToDo; j.Note("沒有缺的旁白,不用送出") })
		return
	}
	saved := p.readBatchFile()
	var count *int
	if items, ok := saved["items"].([]any); ok {
		count = voicePtr(len(items))
	}
	p.update(id, func(j *VoiceJob) {
		j.State = VoiceProcessing
		j.BatchName = nil
		if n, ok := saved["name"].(string); ok {
			j.BatchName = &n
		}
		j.ClipCount = count
		j.NextCheckAt = voiceTimePtr(time.Now().Add(p.pollDelay()))
		j.Note(strings.TrimSpace(r.Out))
	})
	n := 0
	if count != nil {
		n = *count
	}
	a.Log(VoiceActor(j.By), "paperplane", fmt.Sprintf("送出旁白批次 %s(%d 段)", id, n), "pipelines")
}

var voiceStillRe = regexp.MustCompile(`還在處理\(([^)]*)\)`)

func (p *VoicePipeline) collect(id string) {
	a := p.app
	a.Mu.Lock()
	p.update(id, func(j *VoiceJob) { j.State = VoiceCollecting; j.LastCheckAt = voiceTimePtr(time.Now()); j.Checks++ })
	a.Mu.Unlock()

	r := p.npm([]string{"--collect"})
	out := strings.TrimSpace(r.Out)

	a.Mu.Lock()
	defer a.Mu.Unlock()
	poll := p.PollMinutes()
	if r.OK() && strings.Contains(out, "還在處理") {
		var state *string
		if m := voiceStillRe.FindString(out); m != "" {
			s := strings.ReplaceAll(strings.ReplaceAll(m, "還在處理(", ""), ")", "")
			state = &s
		}
		p.update(id, func(j *VoiceJob) {
			j.State = VoiceProcessing
			j.BatchState = state
			j.Error = nil
			j.NextCheckAt = voiceTimePtr(time.Now().Add(p.pollDelay()))
			if j.Checks == 1 || j.Checks%6 == 0 {
				st := "?"
				if state != nil {
					st = *state
				}
				j.Note(fmt.Sprintf("還在處理(%s),%d 分鐘後再查", st, poll))
			}
		})
		return
	}
	if r.OK() {
		failed := VoiceFailedClips(out)
		baseline := map[string]VoiceBaseline{}
		if j := p.Job(id); j != nil {
			baseline = j.Baseline
		}
		changes := p.Diff(baseline)
		p.update(id, func(j *VoiceJob) {
			if len(changes) == 0 {
				j.State = VoiceNothingToDo
			} else {
				j.State = VoiceReady
			}
			j.Changes = changes
			j.FailedClips = failed
			j.BatchState = voicePtr("JOB_STATE_SUCCEEDED")
			j.Error = nil
			j.Note(out)
			if len(changes) == 0 {
				j.Note("收完了,但沒有任何旁白清單有變動")
			} else {
				j.Note(fmt.Sprintf("收完了:%d 份旁白清單有變動,等你發佈", len(changes)))
			}
		})
		if len(changes) > 0 {
			a.Log(SystemActor, "waveform.badge.checkmark", fmt.Sprintf("旁白批次 %s 完成,%d 份清單待發佈", id, len(changes)), "pipelines")
		}
		return
	}
	msg := r.Message()
	if strings.Contains(msg, "沒有完成") || strings.Contains(msg, "沒有送出中的批次") {
		// 批次失敗、取消或過期(voice.mjs 已清掉紀錄),或紀錄被手動刪掉
		p.update(id, func(j *VoiceJob) { j.State = VoiceFailed; j.Error = &msg; j.Note(msg) })
	} else {
		// 網路、額度、上傳失敗等:下一輪重試(voice.mjs 每段完成就寫入清單,重跑不會重複計費)
		p.update(id, func(j *VoiceJob) {
			j.State = VoiceProcessing
			j.Error = &msg
			j.NextCheckAt = voiceTimePtr(time.Now().Add(p.pollDelay()))
			j.Note(fmt.Sprintf("收結果時出錯,%d 分鐘後重試:%s", poll, msg))
		})
	}
}

// VoiceFailedClips voice.mjs --collect 的最後一段:「收到 N 段,M 段失敗(…):」後面縮排的每一行是一段失敗的旁白
func VoiceFailedClips(out string) []string {
	lines := strings.Split(out, "\n")
	idx := -1
	for i, l := range lines {
		if strings.Contains(l, "段失敗") {
			idx = i
		}
	}
	res := []string{}
	if idx < 0 {
		return res
	}
	for _, l := range lines[idx+1:] {
		if strings.HasPrefix(l, "  ") {
			res = append(res, strings.Trim(l, " \t"))
		}
	}
	return res
}

// MARK: - 比對

// Snapshot 所有 .voice.json 的內容雜湊與音檔清單
func (p *VoicePipeline) Snapshot() map[string]VoiceBaseline {
	out := map[string]VoiceBaseline{}
	for _, m := range p.manifests() {
		files := []string{}
		for _, c := range VoiceClips(m.data) {
			files = append(files, c.File)
		}
		out[m.path] = VoiceBaseline{Sha: VoiceSha(m.data), Files: files}
	}
	return out
}

type voiceManifest struct {
	path string
	data []byte
}

func (p *VoicePipeline) manifests() []voiceManifest {
	base := p.liveDir()
	out := []voiceManifest{}
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".voice.json") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		out = append(out, voiceManifest{filepath.ToSlash(rel), b})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// Diff 和送出前的狀態比,列出有變動的旁白清單
func (p *VoicePipeline) Diff(baseline map[string]VoiceBaseline) []VoiceFileChange {
	lessons := p.LessonMap()
	out := []VoiceFileChange{}
	for _, m := range p.manifests() {
		sha := VoiceSha(m.data)
		old, had := baseline[m.path]
		if had && old.Sha == sha {
			continue
		}
		clips := VoiceClips(m.data)
		oldFiles := map[string]bool{}
		for _, f := range old.Files {
			oldFiles[f] = true
		}
		nowFiles := map[string]bool{}
		total := 0
		added := []VoiceClip{}
		for _, c := range clips {
			nowFiles[c.File] = true
			total += c.Ms
			if !oldFiles[c.File] {
				added = append(added, c)
			}
		}
		removed := 0
		for f := range oldFiles {
			if !nowFiles[f] {
				removed++
			}
		}
		script := strings.TrimSuffix(m.path, ".voice.json") + ".json"
		ch := VoiceFileChange{Path: m.path, IsNew: !had, Clips: len(clips), TotalMs: total, Added: added,
			Removed: removed, Sha: sha}
		if owner, ok := lessons[script]; ok {
			ch.Course, ch.Lesson = voicePtr(owner[0]), voicePtr(owner[1])
		}
		out = append(out, ch)
	}
	return out
}

// LessonMap 腳本路徑 → [課程 id, 課 id],從 courses_seed.json 的 script / files 查
func (p *VoicePipeline) LessonMap() map[string][2]string {
	res := map[string][2]string{}
	b, err := os.ReadFile(p.seedFile())
	if err != nil {
		return res
	}
	var courses []map[string]any
	if json.Unmarshal(b, &courses) != nil {
		return res
	}
	for _, c := range courses {
		cid, ok := c["id"].(string)
		if !ok {
			continue
		}
		units, _ := c["units"].([]any)
		for _, u := range units {
			um, _ := u.(map[string]any)
			lessons, _ := um["lessons"].([]any)
			for _, l := range lessons {
				lm, _ := l.(map[string]any)
				lid, ok := lm["id"].(string)
				if !ok {
					continue
				}
				paths := []string{}
				if s, ok := lm["script"].(string); ok {
					paths = append(paths, s)
				}
				if fl, ok := lm["files"].([]any); ok {
					strs := []string{}
					allStr := true
					for _, f := range fl {
						s, ok := f.(string)
						if !ok {
							allStr = false
							break
						}
						strs = append(strs, s)
					}
					if allStr {
						paths = append(paths, strs...)
					}
				}
				for _, pth := range paths {
					res[pth] = [2]string{cid, lid}
				}
			}
		}
	}
	return res
}

// VoiceClips 解析 .voice.json 的 clips(依檔名排序)
func VoiceClips(data []byte) []VoiceClip {
	var obj map[string]any
	if json.Unmarshal(data, &obj) != nil {
		return []VoiceClip{}
	}
	clips, ok := obj["clips"].(map[string]any)
	if !ok {
		return []VoiceClip{}
	}
	out := []VoiceClip{}
	for text, v := range clips {
		c, ok := v.(map[string]any)
		if !ok {
			continue
		}
		file, ok := c["file"].(string)
		if !ok {
			continue
		}
		ms := 0
		switch n := c["ms"].(type) {
		case float64:
			ms = int(n)
		case bool:
			if n {
				ms = 1
			}
		}
		out = append(out, VoiceClip{Text: text, File: file, Ms: ms})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

func VoiceSha(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// MARK: - 執行者

func VoiceActorID(a Actor) string {
	switch a.Kind {
	case "person", "ai":
		return a.ID
	}
	return "system"
}

func VoiceActor(id string) Actor {
	switch id {
	case MCPActorID:
		return MCPActor
	case "system", "external":
		return SystemActor
	}
	return Person(id)
}
