package server

// 本機守護服務(omixor-local):一台只聽 127.0.0.1 的 omixor server。有兩種啟動方式:
//
//   - App 模式:macOS App 的「本機模式」在背景啟動它,用環境變數給資料夾與這次的 token(有 OMIXOR_LOCAL_TOKEN 就是這個模式)
//   - 獨立模式:沒有 App 也能直接執行(例如 Windows 雙擊、命令列);沒給 OMIXOR_LOCAL_TOKEN 或加 -standalone 就是這個模式,
//     token 第一次啟動時產生、存在資料夾裡的 credentials.json,之後沿用(見 standalone.go)
//
// 和雲端版同一套核心(service、api、mcp 與路由都用 Handler),差別只在:
//
//   - 存放處是資料夾裡的 JSON(core/store/file.go),不用 PostgreSQL
//   - 只有一個本機使用者(伺服器管理員),用本機的 token 連線;不接受 evoleap 登入
//   - 多了 /local/… 端點:App 同步組織清單、第一次把 App 原本的本機 JSON 匯入
//
// 環境變數(token 一律走環境變數,不放命令列,ps 看不到):
//
//	OMIXOR_DATA_DIR              資料夾(App 模式必填;App 用 <App 資料夾>/core。
//	                             獨立模式預設 <使用者設定資料夾>/omixor/core,見 standalone.go)
//	OMIXOR_LOCAL_TOKEN           App 給的 token(omx_ 開頭);每次啟動換一把,上一次的作廢。沒給就是獨立模式
//	OMIXOR_LOCAL_USER            本機使用者的 email(預設 local;App 傳它的使用者 id,例如 tim,
//	                             訊息頻道與組織鏈的節點才會和 App 原本的本機資料一致:user:tim)
//	OMIXOR_LOCAL_NAME            本機使用者的顯示名稱(預設同 OMIXOR_LOCAL_USER)
//	OMIXOR_LOCAL_ORG             確保存在的預設組織(預設 default,名稱 evoleap)
//	OMIXOR_ADDR                  監聽位址(App 模式預設 127.0.0.1:0 = 隨機埠;獨立模式預設 127.0.0.1:8770);
//	                             只接受 127.0.0.1 / localhost / ::1
//	OMIXOR_EXIT_ON_STDIN_EOF=1   標準輸入關掉就結束(App 結束或當掉時守護服務跟著結束;獨立模式不理會)
//
// 命令列旗標(-h 看說明):-standalone、-data、-addr、-rotate-token、-version。
//
// 啟動好(已在監聽)時在標準輸出印一行「OMIXOR_LOCAL_READY <網址>」,App 讀這行得知網址。
//
// 跨平台:Unix 用 flock、Windows 用 LockFileEx 鎖資料夾(lock_unix.go、lock_windows.go);
// 檔案權限 0o600 / 0o700 在 Windows 沒有作用,靠使用者資料夾的 ACL;寫檔用「暫存檔 + os.Rename」,
// Windows 上 os.Rename 會取代既有的檔案(MoveFileEx + MOVEFILE_REPLACE_EXISTING)。
//
// /local/… 端點(Authorization 同 /api,要伺服器管理員):
//
//	POST /local/orgs                {orgs: [{id, name, symbol?, color?, note?}]} 確保組織存在、更新名稱等,本機使用者是每個組織的管理員
//	POST /local/import/{org}        {issues?, agents?, cabinetLayout?, docs?, chain?, channels?} 第一次匯入 App 的本機資料;
//	                                每一類各自記在 <資料夾>/app-import.json,匯入過的不再匯入(回傳 skipped)
//	                                每一類全有或全無(批次寫入、整類只存檔一次),成功後立刻記錄;
//	                                沒有紀錄但守護服務已有這類資料時不覆蓋,回傳 existing 並補寫紀錄
//	DELETE /local/orgs/{org}        刪除這個組織的全部資料與匯入紀錄,回傳 {deleted: true};
//	                                預設組織(OMIXOR_LOCAL_ORG)或最後一個組織回 400,找不到回 404
//	GET  /local/import              已經匯入過的紀錄

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// LocalDevice 本機 App 的 token 的裝置名稱(每次啟動先作廢同名的舊 token)
const LocalDevice = "omixor app(本機守護服務)"

// StandaloneDevice 獨立模式的 token 的裝置名稱(和 LocalDevice 分開:兩種模式各自作廢自己的舊 token,互不影響)
const StandaloneDevice = "omixor 獨立守護服務"

// LocalConfig 本機守護服務的設定
type LocalConfig struct {
	DataDir string
	Token   string
	User    string
	Name    string
	Org     string
	// Device token 的裝置名稱(空的是 LocalDevice = App 模式;獨立模式用 StandaloneDevice)
	Device string
}

// Local 本機守護服務(store、service、本機使用者與匯入紀錄)
type Local struct {
	Cfg     LocalConfig
	Store   *store.File
	Svc     *service.Service
	user    model.User
	tokenID string // 這次用的 token 的紀錄 id(獨立模式寫進 credentials.json)
	// regenerated 獨立模式:credentials.json 的 token 沒有有效紀錄(撤銷了等),這次換了一把新的
	regenerated bool
	impMu       sync.Mutex
	// importing 進行中的匯入數(結束時等匯入做完,見 local_run.go)
	importing atomic.Int32
}

// OpenLocal 打開資料夾、確保本機使用者、預設組織與這次的 token
func OpenLocal(cfg LocalConfig) (*Local, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("要設 OMIXOR_DATA_DIR")
	}
	if !strings.HasPrefix(cfg.Token, service.TokenPrefix) || len(cfg.Token) < len(service.TokenPrefix)+20 {
		return nil, errors.New("OMIXOR_LOCAL_TOKEN 要是 omx_ 開頭的隨機字串")
	}
	if cfg.User = strings.ToLower(strings.TrimSpace(cfg.User)); cfg.User == "" {
		cfg.User = "local"
	}
	if cfg.Device == "" {
		cfg.Device = LocalDevice
	}
	if cfg.Org = strings.TrimSpace(cfg.Org); cfg.Org == "" {
		cfg.Org = "default"
	}
	if !model.ValidOrgID(cfg.Org) {
		return nil, fmt.Errorf("OMIXOR_LOCAL_ORG 不對:%s", cfg.Org)
	}
	st, err := store.OpenFile(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	l := &Local{Cfg: cfg, Store: st, Svc: &service.Service{Store: st}}
	_ = os.Remove(l.importLogPath() + ".tmp") // 上次寫匯入紀錄寫到一半留下的暫存檔(資料夾已上鎖,不會有別人在寫)
	if err := l.ensureUser(); err != nil {
		return nil, err
	}
	if err := l.ensureOrg(model.Org{ID: cfg.Org, Name: map[bool]string{true: "evoleap", false: cfg.Org}[cfg.Org == "default"]}, false); err != nil {
		return nil, err
	}
	return l, nil
}

// ensureUser 本機使用者(伺服器管理員)與這次的 token:
//
//   - 同裝置名稱的舊 token 一律作廢(不管是哪個使用者的):換 OMIXOR_LOCAL_USER 後,舊使用者在這個裝置名稱下的 token 不能再用;
//     獨立模式 -rotate-token 時,這台守護服務發過的所有獨立模式 token 都作廢
//   - 這次的 token 已經有本機使用者、同裝置名稱的有效紀錄就沿用(獨立模式重開)
//   - 獨立模式找不到有效紀錄(被撤銷了、紀錄不見了、或換了使用者)就換一把新的,不用 credentials.json 的舊 token 重建紀錄:
//     store.json 不存已撤銷的 token,用舊雜湊重建等於讓撤銷的 token 復活
//   - 同一個雜湊只會對到一個使用者:別的裝置或使用者剛好有同雜湊的有效紀錄,也一併作廢
func (l *Local) ensureUser() error {
	now := model.Now()
	u, err := l.Store.UserByEmail(l.Cfg.User)
	if errors.Is(err, store.ErrNotFound) {
		u = &model.User{ID: service.RandID("u_", 9), Email: l.Cfg.User, Name: l.Cfg.User, Role: "admin", CreatedAt: now}
	} else if err != nil {
		return err
	}
	u.Role, u.Disabled = "admin", false
	if n := strings.TrimSpace(l.Cfg.Name); n != "" {
		u.Name = n
	}
	if err := l.Store.SaveUser(*u); err != nil {
		return err
	}
	l.user = *u
	toks, err := l.Store.ListTokens(time.Now())
	if err != nil {
		return err
	}
	hash := service.HashToken(l.Cfg.Token)
	for _, t := range toks {
		same := bytes.Equal(t.Hash, hash)
		if t.Device != l.Cfg.Device && !same {
			continue // 別的裝置名稱(例如 App 模式與獨立模式互不影響、CLI 登入)
		}
		if l.tokenID == "" && same && t.UserID == u.ID && t.Device == l.Cfg.Device {
			l.tokenID = t.ID
			continue
		}
		if err := l.Store.RevokeToken(t.ID, time.Now()); err != nil {
			return err
		}
	}
	if l.tokenID != "" {
		return nil
	}
	if l.Cfg.Device == StandaloneDevice {
		// 獨立模式:舊 token 沒有有效紀錄,換新的(OpenStandalone 會寫回 credentials.json)
		l.Cfg.Token = service.RandID(service.TokenPrefix, 32)
		hash = service.HashToken(l.Cfg.Token)
		l.regenerated = true
	}
	l.tokenID = service.RandID("t_", 9)
	return l.Store.CreateToken(model.Token{ID: l.tokenID, UserID: u.ID, Device: l.Cfg.Device, CreatedAt: now, Hash: hash})
}

// ensureMember 本機使用者是組織管理員
func (l *Local) ensureMember(org, userID, role string) error {
	if r, err := l.Store.MemberRole(org, userID); err == nil && r == role {
		return nil
	}
	return l.Store.SaveMember(model.Member{Org: org, UserID: userID, Role: role, AddedAt: model.Now()})
}

// ensureOrg 組織不存在就建立;update 時更新名稱、圖示、顏色、備註
func (l *Local) ensureOrg(o model.Org, update bool) error {
	if !model.ValidOrgID(o.ID) {
		return fmt.Errorf("組織 id 不對:%s", o.ID)
	}
	if strings.TrimSpace(o.Name) == "" {
		o.Name = o.ID
	}
	cur, err := l.Store.GetOrg(o.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if o.CreatedAt.IsZero() {
			o.CreatedAt = model.Now()
		}
		o.MyRole = ""
		if err := l.Store.CreateOrg(o); err != nil {
			return err
		}
	case err != nil:
		return err
	case update && (cur.Name != o.Name || cur.Symbol != o.Symbol || cur.Color != o.Color || cur.Note != o.Note):
		if err := l.Store.UpdateOrg(o); err != nil {
			return err
		}
	}
	return l.ensureMember(o.ID, l.user.ID, "admin")
}

// principal 本機使用者的身分(匯入用)
func (l *Local) principal() *model.Principal { return &model.Principal{User: l.user} }

// ---- 匯入 App 的本機資料 ----

// AppImport App 送來的本機資料(格式同 App 的 issues.json、agents.json、cabinet*.json、org-chain.json、channels.json)
type AppImport struct {
	Issues        *[]model.Issue          `json:"issues,omitempty"`
	Agents        *service.AgentImport    `json:"agents,omitempty"`
	CabinetLayout *[]model.CabinetSection `json:"cabinetLayout,omitempty"`
	Docs          *[]model.CabinetDoc     `json:"docs,omitempty"`
	Chain         *model.OrgChain         `json:"chain,omitempty"`
	Channels      *AppChannels            `json:"channels,omitempty"`
}

// AppChannels App 的 channels.json
type AppChannels struct {
	Channels []model.Channel        `json:"channels"`
	Messages []model.ChannelMessage `json:"messages"`
	Reads    []model.ChannelRead    `json:"reads"`
}

// ImportSection 一類資料的匯入結果
type ImportSection struct {
	// Status done(匯入了或沒有資料)、skipped(之前匯入過)、existing(沒有匯入紀錄但守護服務已經有這類資料:
	// 視為已匯入、補寫紀錄,不用 App 的資料覆蓋)、error(這一類什麼都沒寫,之後可以重送)
	Status string `json:"status"`
	Count  int    `json:"count"`
	Error  string `json:"error,omitempty"`
	Note   string `json:"note,omitempty"`
}

// importLog <資料夾>/app-import.json:組織 → 類別 → 完成時間
type importLog struct {
	Orgs map[string]map[string]time.Time `json:"orgs"`
}

func (l *Local) importLogPath() string { return filepath.Join(l.Cfg.DataDir, "app-import.json") }

// readImportLog 讀匯入紀錄;檔案損毀時當成沒有紀錄(各類資料靠「已經有資料就不覆蓋」保護,見 Import)
func (l *Local) readImportLog() importLog {
	lg := importLog{Orgs: map[string]map[string]time.Time{}}
	if b, err := os.ReadFile(l.importLogPath()); err == nil {
		if err := json.Unmarshal(b, &lg); err != nil {
			log.Printf("%s 讀不出來,當成沒有匯入紀錄:%v", l.importLogPath(), err)
			lg = importLog{}
		}
		if lg.Orgs == nil {
			lg.Orgs = map[string]map[string]time.Time{}
		}
	}
	return lg
}

// writeImportLog 寫匯入紀錄:暫存檔寫入並 fsync、改名,再 fsync 資料夾(Windows 不支援資料夾 fsync,略過)
func (l *Local) writeImportLog(lg importLog) error {
	b, err := json.MarshalIndent(lg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileSync(l.importLogPath(), b)
}

// writeFileSync 原子寫檔:同資料夾的暫存檔 → 寫入 → fsync → 改名 → fsync 資料夾
func writeFileSync(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp) // 只清掉自己剛建的暫存檔
		return werr
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync() // 讓改名本身也落到磁碟;Windows 會回錯誤,不影響
		_ = d.Close()
	}
	return nil
}

// ensureNodeMembers 組織鏈與頻道裡的 user:<x> 都要是組織成員(App 本機模式的人是 App 的人員 id,不一定是 email):
// 不存在的建一個本機的使用者紀錄,加成一般成員。st 是批次的寫入入口(匯入時)或 l.Store
func (l *Local) ensureNodeMembers(st store.Store, org string, nodes []string) error {
	for _, n := range nodes {
		e, ok := strings.CutPrefix(n, model.UserPrefix)
		e = strings.ToLower(strings.TrimSpace(e))
		if !ok || e == "" || e == l.user.Email {
			continue
		}
		u, err := st.UserByEmail(e)
		if errors.Is(err, store.ErrNotFound) {
			u = &model.User{ID: service.RandID("u_", 9), Email: e, Name: e, Role: "member", CreatedAt: model.Now()}
			if err := st.SaveUser(*u); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := st.MemberRole(org, u.ID); errors.Is(err, store.ErrNotFound) {
			if err := st.SaveMember(model.Member{Org: org, UserID: u.ID, Role: "member", AddedAt: model.Now()}); err != nil {
				return err
			}
		}
	}
	return nil
}

// importStep 一類資料的匯入:existing 回傳守護服務裡已經有幾筆這類資料(> 0 就不匯入);
// run 在批次裡寫入(tx 與用 tx 的 service),回傳匯入幾筆
type importStep struct {
	name     string
	has      bool
	existing func() (int, error)
	run      func(tx *store.File, svc *service.Service) (int, error)
}

// Importing 有沒有進行中的匯入(結束時要等它做完)
func (l *Local) Importing() bool { return l.importing.Load() > 0 }

// Import 第一次匯入 App 的本機資料(每一類只匯入一次;順序:Issue、角色與任務、文件櫃、組織鏈、頻道)。
//
// 每一類「全有或全無」:在 store 的批次裡寫入,整類成功才存檔一次,失敗就整類還原;成功後立刻寫匯入紀錄。
// 中途被打斷(程式被砍掉)時,做完的類別已經有紀錄,沒做完的什麼都沒寫,之後重送會接著做。
// 匯入紀錄遺失(檔案壞了、寫紀錄前被砍斷)時,守護服務已經有的資料不會被 App 的舊資料覆蓋:
// 視為已匯入、補寫紀錄,回傳 existing。回傳的錯誤只有組織不存在或寫匯入紀錄失敗
func (l *Local) Import(org string, in AppImport) (map[string]ImportSection, error) {
	l.importing.Add(1)
	defer l.importing.Add(-1)
	l.impMu.Lock()
	defer l.impMu.Unlock()
	if _, err := l.Store.GetOrg(org); err != nil {
		return nil, fmt.Errorf("找不到組織 %s(先用 /local/orgs 建立)", org)
	}
	lg := l.readImportLog()
	if lg.Orgs[org] == nil {
		lg.Orgs[org] = map[string]time.Time{}
	}
	done := lg.Orgs[org]
	p := l.principal()
	out := map[string]ImportSection{}
	var logErr error
	mark := func(name string) {
		done[name] = time.Now().UTC()
		if err := l.writeImportLog(lg); err != nil && logErr == nil {
			logErr = fmt.Errorf("寫匯入紀錄失敗:%w", err)
		}
	}
	for _, st := range l.importSteps(org, in, p) {
		if _, ok := done[st.name]; ok {
			out[st.name] = ImportSection{Status: "skipped"}
			continue
		}
		if !st.has {
			continue // 這次沒送這一類:不記錄,之後送了還會匯入
		}
		n, err := st.existing()
		if err != nil {
			out[st.name] = ImportSection{Status: "error", Error: err.Error()}
			continue
		}
		if n > 0 {
			mark(st.name)
			out[st.name] = ImportSection{Status: "existing", Count: n,
				Note: fmt.Sprintf("沒有匯入紀錄,但守護服務的 %s 已經有 %d 筆這類資料;視為已匯入、補寫紀錄,沒有用 App 的資料覆蓋", org, n)}
			log.Printf("匯入 %s 的 %s:已經有 %d 筆資料,視為已匯入", org, st.name, n)
			continue
		}
		var cnt int
		err = l.Store.Batch(func(tx *store.File) error {
			var err error
			cnt, err = st.run(tx, &service.Service{Store: tx})
			return err
		})
		if err != nil {
			out[st.name] = ImportSection{Status: "error", Error: err.Error()}
			log.Printf("匯入 %s 的 %s 失敗(這一類沒有寫入):%v", org, st.name, err)
			continue
		}
		mark(st.name)
		out[st.name] = ImportSection{Status: "done", Count: cnt}
	}
	return out, logErr
}

// importSteps 各類資料的匯入步驟
func (l *Local) importSteps(org string, in AppImport, p *model.Principal) []importStep {
	return []importStep{
		{name: "issues", has: in.Issues != nil,
			existing: func() (int, error) { list, err := l.Store.ListIssues(org); return len(list), err },
			run: func(tx *store.File, svc *service.Service) (int, error) {
				if len(*in.Issues) == 0 {
					return 0, nil
				}
				r, err := svc.ImportIssues(p, org, *in.Issues)
				if err != nil {
					return 0, err
				}
				return r.Inserted, nil
			}},
		{name: "agents", has: in.Agents != nil,
			existing: func() (int, error) { list, err := l.Store.ListAgents(org); return len(list), err },
			run: func(tx *store.File, svc *service.Service) (int, error) {
				if len(in.Agents.Agents) == 0 {
					return 0, nil
				}
				r, err := svc.ImportAgents(p, org, *in.Agents)
				if err != nil {
					return 0, err
				}
				return r.Agents + r.Tasks, nil
			}},
		{name: "cabinet", has: in.CabinetLayout != nil || in.Docs != nil,
			existing: func() (int, error) {
				docs, err := l.Store.ListDocs(org)
				if err != nil {
					return 0, err
				}
				n := len(docs)
				if _, err := l.Store.GetCabinetLayout(org); err == nil {
					n++ // 存過的結構也算一筆
				} else if !errors.Is(err, store.ErrNotFound) {
					return 0, err
				}
				return n, nil
			},
			run: func(tx *store.File, svc *service.Service) (int, error) {
				ci := service.CabinetImport{}
				if in.CabinetLayout != nil {
					ci.Layout = *in.CabinetLayout
				}
				if in.Docs != nil {
					ci.Docs = *in.Docs
				}
				if ci.Layout == nil && len(ci.Docs) == 0 {
					return 0, nil
				}
				r, err := svc.ImportCabinet(p, org, ci)
				if err != nil {
					return 0, err
				}
				return r.Docs, nil
			}},
		{name: "chain", has: in.Chain != nil,
			// 組織鏈是整份取代:守護服務已經有邊就不用 App 的舊版蓋掉
			existing: func() (int, error) {
				c, err := l.Store.GetOrgChain(org)
				if errors.Is(err, store.ErrNotFound) {
					return 0, nil
				}
				if err != nil {
					return 0, err
				}
				return len(c.Edges), nil
			},
			run: func(tx *store.File, svc *service.Service) (int, error) {
				if len(in.Chain.Edges) == 0 {
					return 0, nil
				}
				nodes := []string{}
				for _, e := range in.Chain.Edges {
					nodes = append(nodes, e.From, e.To)
				}
				if err := l.ensureNodeMembers(tx, org, nodes); err != nil {
					return 0, err
				}
				c, err := svc.SaveChain(p, org, in.Chain.Edges)
				if err != nil {
					return 0, err
				}
				return len(c.Edges), nil
			}},
		{name: "channels", has: in.Channels != nil,
			existing: func() (int, error) { list, err := l.Store.ListChannels(org); return len(list), err },
			run: func(tx *store.File, _ *service.Service) (int, error) {
				return l.importChannels(tx, org, *in.Channels)
			}},
	}
}

// importFault 測試用:模擬匯入做到一半出錯(正式執行是 nil)
var importFault func(section string, n int) error

// importChannels 頻道、訊息與已讀照原本的順序寫進 store(核心沒有頻道的匯入 API;編號由 store 重新指定,
// App 的編號本來就從 1 連續,通常不變;已讀依新編號換算)。在批次裡執行(tx),整類只存檔一次
func (l *Local) importChannels(tx *store.File, org string, in AppChannels) (int, error) {
	if len(in.Channels) == 0 {
		return 0, nil
	}
	cur, err := tx.ListChannels(org)
	if err != nil {
		return 0, err
	}
	if len(cur) > 0 {
		return 0, fmt.Errorf("%s 已經有 %d 個頻道;匯入只在第一次搬資料時用", org, len(cur))
	}
	nodes := []string{}
	for _, c := range in.Channels {
		nodes = append(nodes, c.Members...)
		nodes = append(nodes, c.CreatedBy)
	}
	for _, m := range in.Messages {
		nodes = append(nodes, m.Author)
	}
	if err := l.ensureNodeMembers(tx, org, nodes); err != nil {
		return 0, err
	}
	chs := append([]model.Channel{}, in.Channels...)
	sort.SliceStable(chs, func(i, j int) bool { return chs[i].Number() < chs[j].Number() })
	ids := map[string]string{} // 舊頻道 id → 新的
	for _, c := range chs {
		old := c.ID
		if c.Members == nil {
			c.Members = []string{}
		}
		if c.UpdatedAt.IsZero() {
			c.UpdatedAt = c.CreatedAt
		}
		saved, err := tx.CreateChannel(org, c)
		if err != nil {
			return 0, err
		}
		ids[old] = saved.ID
	}
	byCh := map[string][]model.ChannelMessage{}
	for _, m := range in.Messages {
		byCh[m.ChannelID] = append(byCh[m.ChannelID], m)
	}
	nums := map[string]map[int]int{} // 舊頻道 id → 舊號碼 → 新號碼
	for old, list := range byCh {
		nid, ok := ids[old]
		if !ok {
			continue // 頻道不在了的訊息不搬
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].Number() < list[j].Number() })
		nums[old] = map[int]int{}
		for _, m := range list {
			n := m.Number()
			m.ChannelID = nid
			if m.Mentions == nil {
				m.Mentions = []string{}
			}
			if importFault != nil {
				if err := importFault("channels", len(nums[old])); err != nil {
					return 0, err
				}
			}
			saved, err := tx.CreateChannelMessage(org, m)
			if err != nil {
				return 0, err
			}
			nums[old][n] = saved.Number()
		}
	}
	for _, r := range in.Reads {
		nid, ok := ids[r.ChannelID]
		if !ok {
			continue
		}
		last := 0 // 新號碼裡,原本號碼 <= lastRead 的最大值
		for o, n := range nums[r.ChannelID] {
			if o <= r.LastRead && n > last {
				last = n
			}
		}
		r.ChannelID, r.LastRead = nid, last
		if err := tx.SaveChannelRead(org, r); err != nil {
			return 0, err
		}
	}
	return len(chs), nil
}

// DeleteOrg 刪除守護服務裡這個組織的全部資料(service 的刪組織)與它的匯入紀錄。
// 預設組織(OMIXOR_LOCAL_ORG)與最後一個組織不能刪:回傳 400 類的錯誤
func (l *Local) DeleteOrg(org string) (int, error) {
	l.impMu.Lock() // 和匯入互斥:不會刪到匯入一半的組織,也不會和匯入同時改 app-import.json
	defer l.impMu.Unlock()
	if _, err := l.Store.GetOrg(org); err != nil {
		return 404, fmt.Errorf("找不到組織 %s", org)
	}
	if org == l.Cfg.Org {
		return 400, fmt.Errorf("%s 是守護服務的預設組織(OMIXOR_LOCAL_ORG),不能刪除", org)
	}
	orgs, err := l.Store.ListOrgs()
	if err != nil {
		return 500, err
	}
	if len(orgs) <= 1 {
		return 400, fmt.Errorf("%s 是最後一個組織,不能刪除", org)
	}
	if err := l.Svc.DeleteOrg(l.principal(), org); err != nil {
		return 500, err
	}
	lg := l.readImportLog()
	if _, ok := lg.Orgs[org]; ok {
		delete(lg.Orgs, org)
		if err := l.writeImportLog(lg); err != nil {
			// 組織已經刪了;紀錄沒刪掉的話,之後同名組織重建時那幾類會被略過(不會讓舊資料回來)
			return 500, fmt.Errorf("組織已刪除,但清除匯入紀錄失敗:%w", err)
		}
	}
	return 200, nil
}

// ---- HTTP ----

// Handler 核心的 Handler 加上 /local/… 端點
func (l *Local) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", Handler(l.Svc))
	admin := func(h func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			p, err := l.Svc.Authenticate(tok)
			if err == nil && !p.ServerAdmin() {
				err = errors.New("要伺服器管理員")
			}
			if err != nil {
				writeLocal(w, 401, map[string]any{"error": err.Error()})
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /local/orgs", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Orgs []model.Org `json:"orgs"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in); err != nil {
			writeLocal(w, 400, map[string]any{"error": "格式錯誤:" + err.Error()})
			return
		}
		res := map[string]string{}
		for _, o := range in.Orgs {
			if err := l.ensureOrg(o, true); err != nil {
				res[o.ID] = err.Error()
			} else {
				res[o.ID] = "ok"
			}
		}
		writeLocal(w, 200, map[string]any{"result": res})
	}))
	mux.HandleFunc("POST /local/import/{org}", admin(func(w http.ResponseWriter, r *http.Request) {
		l.importing.Add(1) // 讀請求內容的時間也算(結束時要等)
		defer l.importing.Add(-1)
		var in AppImport
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<20)).Decode(&in); err != nil {
			writeLocal(w, 400, map[string]any{"error": "格式錯誤:" + err.Error()})
			return
		}
		res, err := l.Import(r.PathValue("org"), in)
		if err != nil && res == nil {
			writeLocal(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if err != nil {
			// 資料已經寫進去了,只是匯入紀錄沒寫成;重送時已有資料的類別會回 existing,不會重複匯入
			writeLocal(w, 500, map[string]any{"error": err.Error(), "result": res})
			return
		}
		writeLocal(w, 200, map[string]any{"result": res})
	}))
	mux.HandleFunc("DELETE /local/orgs/{org}", admin(func(w http.ResponseWriter, r *http.Request) {
		code, err := l.DeleteOrg(r.PathValue("org"))
		if err != nil {
			writeLocal(w, code, map[string]any{"error": err.Error()})
			return
		}
		writeLocal(w, 200, map[string]any{"deleted": true})
	}))
	mux.HandleFunc("GET /local/import", admin(func(w http.ResponseWriter, r *http.Request) {
		l.impMu.Lock()
		lg := l.readImportLog()
		l.impMu.Unlock()
		writeLocal(w, 200, map[string]any{"result": lg.Orgs})
	}))
	return mux
}

func writeLocal(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// loopbackAddr 只接受本機位址
func loopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("本機守護服務只能聽 127.0.0.1(不是 %s)", addr)
}
