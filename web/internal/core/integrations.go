package core

import (
	"errors"
	"log"
	"strings"
	"sync"

	"omixor-core/model"
	"omixor-core/service"
)

// GitHub、GitLab 的「設定」是組織層級的核心資料(omixor-core 的 service/integration.go):
//
//	本機模式:內建核心 + 本機 PostgreSQL(omixor_org_integrations);遠端模式:omixor server 的 /api/orgs/{org}/integrations/github|gitlab
//
// 核心只存非機密設定:追蹤的 repo(GitHub)、GitLab 網址與追蹤的專案。
// 留在這台(各裝置)的:token(加密 vault)、用哪一個連線(github.json / gitlab.json 的 connection)、
// GitLab 的 CA 憑證檔與不驗證憑證(連線上)。讀 PR、Actions、MR、Pipeline 照舊在 web 用這台的 token 直接打。
//
// 這裡是轉接層:
//   - 讀:組織載入時(背景)、每次 GHRefresh / GLRefresh 前從核心讀,換掉記憶體裡的 s.GH.Repos、s.GL.Projects
//   - 寫:GHAddRepo、GHRemoveRepo、GLAddProject、GLRemoveProject、GLSetServer(網址)交給核心,成功後用回傳的內容
//   - 舊資料:核心還沒存過(saved = false)而這台的 github.json / gitlab.json 有 → 匯入一次(要組織管理員;原檔不刪不改)
//   - 本機的 github.json / gitlab.json 只再寫 connection,repos / projects 保留原本的內容(當成匯入來源與沒有核心時的唯讀備援)
//
// 呼叫這些函式時不要拿著 Mu(會打網路或資料庫)。

// integState 每個 App 一份:寫入次數(背景讀到舊資料時不要蓋掉剛寫的)、最近一次讀寫核心的錯誤
type integState struct {
	mu     sync.Mutex
	writes int
	ghErr  string
	glErr  string
}

var integStates sync.Map // *App → *integState

func integOf(a *App) *integState {
	v, _ := integStates.LoadOrStore(a, &integState{})
	return v.(*integState)
}

func (st *integState) wrote() {
	st.mu.Lock()
	st.writes++
	st.mu.Unlock()
}

func (st *integState) writeCount() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.writes
}

func (st *integState) setErr(gh bool, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if gh {
		if msg != "" && msg != st.ghErr {
			log.Printf("GitHub 設定(核心):%s", msg)
		}
		st.ghErr = msg
	} else {
		if msg != "" && msg != st.glErr {
			log.Printf("GitLab 設定(核心):%s", msg)
		}
		st.glErr = msg
	}
}

// GHSettingsError 最近一次讀寫核心 GitHub 設定的錯誤(沒有是 "")
func GHSettingsError(a *App) string {
	st := integOf(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.ghErr
}

// GLSettingsError 最近一次讀寫核心 GitLab 設定的錯誤(沒有是 "")
func GLSettingsError(a *App) string {
	st := integOf(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.glErr
}

// integErr 核心的錯誤 → 畫面看得懂的(權限、格式錯誤照原文)
func integErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errNoDB) {
		return UserError{"GitHub / GitLab 設定存在組織(核心):" + err.Error()}
	}
	return agentUserErr(err)
}

// ---- 本機檔案(只寫 connection,保留原本的 repos / projects) ----

func ghSaveConnection(a *App) {
	f := DefaultGHSettings()
	if b, err := ReadData(a.ghFile()); err == nil {
		_ = jsonUnmarshal(b, &f)
	}
	f.Connection = a.conns().GH.Connection
	_ = WriteJSON(a.ghFile(), f)
}

func glSaveConnection(a *App) {
	f := DefaultGLSettings()
	if b, err := ReadData(a.glFile()); err == nil {
		_ = jsonUnmarshal(b, &f)
	}
	f.Connection = a.conns().GL.Connection
	_ = WriteJSON(a.glFile(), f)
}

// ---- 讀 ----

// integSnapshot 背景讀之前記下:組織、Gen、寫入次數(讀回來時有變就不套用)
type integSnapshot struct {
	org    string
	gen    int
	writes int
}

func (a *App) integSnap() integSnapshot {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return integSnapshot{org: a.CurrentOrgID(), gen: a.conns().Gen, writes: integOf(a).writeCount()}
}

// stale 讀的期間換了組織或有人寫過(拿著 Mu 呼叫)
func (a *App) integStale(s integSnapshot) bool {
	return a.CurrentOrgID() != s.org || a.conns().Gen != s.gen || integOf(a).writeCount() != s.writes
}

// GHSyncSettings 從核心讀 GitHub 設定;核心沒存過而這台有舊設定 → 匯入一次(不拿 Mu)
func GHSyncSettings(a *App) {
	snap := a.integSnap()
	if snap.org == "" {
		return
	}
	st := integOf(a)
	be, err := agentBackend()
	if err != nil {
		st.setErr(true, err)
		return
	}
	x, err := be.GetGitHubSettings(snap.org)
	if err != nil {
		st.setErr(true, err)
		return
	}
	if !x.Saved {
		local := DefaultGHSettings()
		if b, err := ReadData(a.ghFile()); err == nil {
			_ = jsonUnmarshal(b, &local)
		}
		if len(local.Repos) > 0 {
			r, err := be.ImportGitHubSettings(snap.org, service.GitHubImport{Repos: local.Repos})
			if err != nil {
				// 不是管理員不能匯入:先照核心的(空的)顯示,等管理員連上時再匯入
				st.setErr(true, errors.New("這台的舊 GitHub 設定還沒匯入組織(要組織管理員):"+err.Error()))
			} else {
				x = &r.Settings
				if len(r.Skipped) > 0 {
					log.Printf("GitHub 設定匯入核心時略過格式不對的 repo:%s", strings.Join(r.Skipped, "、"))
				}
				st.setErr(true, nil)
			}
		} else {
			st.setErr(true, nil)
		}
	} else {
		st.setErr(true, nil)
	}
	a.Mu.Lock()
	if a.integStale(snap) {
		a.Mu.Unlock()
		return
	}
	changed := !equalStrings(a.conns().GH.Repos, x.Repos)
	a.conns().GH.Repos = append([]string{}, x.Repos...)
	a.Mu.Unlock()
	if changed {
		a.Changed("github")
	}
}

// GLSyncSettings 從核心讀 GitLab 設定(網址與專案);核心沒存過而這台有 → 匯入一次。
// 核心有網址而這台的連線不一樣(或還沒有連線)→ 套用到這台的連線(網址換了,舊的登入作廢)
func GLSyncSettings(a *App) {
	snap := a.integSnap()
	if snap.org == "" {
		return
	}
	st := integOf(a)
	be, err := agentBackend()
	if err != nil {
		st.setErr(false, err)
		return
	}
	x, err := be.GetGitLabSettings(snap.org)
	if err != nil {
		st.setErr(false, err)
		return
	}
	st.setErr(false, nil)
	if !x.Saved {
		local := DefaultGLSettings()
		if b, err := ReadData(a.glFile()); err == nil {
			_ = jsonUnmarshal(b, &local)
		}
		a.Mu.Lock()
		base := ""
		if c, ok := GLConnection(a); ok {
			base = c.BaseURL
		}
		a.Mu.Unlock()
		if len(local.Projects) > 0 || base != "" {
			r, err := be.ImportGitLabSettings(snap.org, service.GitLabImport{BaseURL: base, Projects: local.Projects})
			if err != nil {
				st.setErr(false, errors.New("這台的舊 GitLab 設定還沒匯入組織(要組織管理員):"+err.Error()))
			} else {
				x = &r.Settings
				if len(r.Skipped) > 0 {
					log.Printf("GitLab 設定匯入核心時略過格式不對的項目:%s", strings.Join(r.Skipped, "、"))
				}
			}
		}
	}
	glApplyCore(a, snap, *x)
}

// glApplyCore 把核心的 GitLab 設定套用到記憶體與這台的連線(不拿 Mu)
func glApplyCore(a *App, snap integSnapshot, x model.GitLabSettings) {
	a.Mu.Lock()
	if a.integStale(snap) {
		a.Mu.Unlock()
		return
	}
	s := a.conns()
	changed := !equalStrings(s.GL.Projects, x.Projects)
	s.GL.Projects = append([]string{}, x.Projects...)
	c, hasConn := GLConnection(a)
	a.Mu.Unlock()
	if x.BaseURL != "" && (!hasConn || c.BaseURL != x.BaseURL) {
		if _, err := glSetLocalServer(a, &x.BaseURL, nil, nil); err != nil {
			integOf(a).setErr(false, err)
		}
		changed = true
	}
	if changed {
		a.Changed("gitlab")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- 寫 ----

func ghSaveCore(a *App, in service.GitHubSettingsInput) error {
	org := a.currentOrgNow()
	be, err := agentBackend()
	if err != nil {
		return integErr(err)
	}
	st := integOf(a)
	st.wrote()
	x, err := be.SaveGitHubSettings(org, in)
	if err != nil {
		return integErr(err)
	}
	st.setErr(true, nil)
	a.Mu.Lock()
	if a.CurrentOrgID() == org {
		a.conns().GH.Repos = append([]string{}, x.Repos...)
	}
	a.Mu.Unlock()
	a.Changed("github")
	return nil
}

func glSaveCore(a *App, in service.GitLabSettingsInput) (*model.GitLabSettings, error) {
	org := a.currentOrgNow()
	be, err := agentBackend()
	if err != nil {
		return nil, integErr(err)
	}
	st := integOf(a)
	st.wrote()
	x, err := be.SaveGitLabSettings(org, in)
	if err != nil {
		return nil, integErr(err)
	}
	st.setErr(false, nil)
	a.Mu.Lock()
	if a.CurrentOrgID() == org {
		a.conns().GL.Projects = append([]string{}, x.Projects...)
	}
	a.Mu.Unlock()
	a.Changed("gitlab")
	return x, nil
}
