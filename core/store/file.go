package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omixor-core/model"
)

// File 存在一個資料夾裡的存放處(App 的本機守護服務用;見 server/local.go)。
// 記憶體實作是 Mem;每次寫入成功後把整份資料存成 <資料夾>/store.json(先寫暫存檔再改名,原子寫入),啟動時讀回。
// 附件(圖片)一份一個檔:<資料夾>/attachments/<組織>/<id>(id 是內容雜湊,只寫一次,不放進 store.json)。
//
// 只給單一程式使用(同一個資料夾不能同時開兩個;守護服務另外用鎖檔擋)。
// 不存:已撤銷或過期的 token(讀回後本來就不能用);操作紀錄只留最近 auditKeep 筆。
// 刪除組織時,那個組織的附件檔留在磁碟上(不刪檔;id 是內容雜湊,不會被別的組織讀到)。
type File struct {
	*Mem
	dir    string
	saveMu sync.Mutex
	// gate 批次(Batch)進行中時擋住批次以外的寫入
	gate sync.RWMutex
	// tx 是批次裡給 fn 用的寫入入口(只改記憶體、不存檔);dirty 記錄批次裡有沒有寫入
	tx    bool
	dirty atomic.Bool
}

// auditKeep store.json 裡最多留幾筆操作紀錄
const auditKeep = 5000

// fileName 主檔名
const fileName = "store.json"

// fileToken token 加上雜湊與撤銷狀態(model.Token 的雜湊不輸出 JSON)
type fileToken struct {
	model.Token
	Hash []byte `json:"hash"`
}

// fileCred 角色憑證加上雜湊
type fileCred struct {
	model.AgentCredential
	Hash []byte `json:"hash"`
}

// fileSnapshot store.json 的格式
type fileSnapshot struct {
	Version  int                                                `json:"version"`
	SavedAt  time.Time                                          `json:"savedAt"`
	Users    []model.User                                       `json:"users"`
	Tokens   []fileToken                                        `json:"tokens"`
	Audit    []model.AuditEntry                                 `json:"audit"`
	Orgs     []model.Org                                        `json:"orgs"`
	Members  []model.Member                                     `json:"members"`
	Issues   map[string][]model.Issue                           `json:"issues"`
	Agents   map[string][]model.Agent                           `json:"agents"`
	Tasks    map[string][]model.AgentTask                       `json:"tasks"`
	Creds    []fileCred                                         `json:"credentials"`
	Layouts  map[string][]model.CabinetSection                  `json:"cabinetLayouts"`
	Docs     map[string][]model.CabinetDoc                      `json:"docs"`
	Chains   map[string]model.OrgChain                          `json:"chains"`
	Channels map[string][]model.Channel                         `json:"channels"`
	Messages map[string]map[string][]model.ChannelMessage       `json:"messages"`
	Reads    map[string]map[string]map[string]model.ChannelRead `json:"reads"`
	// Incidents 事件(寫入方法在 file_incident.go)
	Incidents map[string][]model.Incident `json:"incidents,omitempty"`
	// Integrations 組織的 GitHub、GitLab 設定(寫入方法在 file_integration.go)
	Integrations map[string]model.OrgIntegrations `json:"integrations,omitempty"`
	// System 組織的系統設定(功能開關;寫入方法在 file_system.go)
	System map[string]model.OrgSystemSettings `json:"system,omitempty"`
	// MCP 組織的外部 MCP 登記(寫入方法在 file_mcp.go;沒有任何密鑰)
	MCP map[string]model.OrgMCPConfig `json:"mcp,omitempty"`
}

// OpenFile 打開(沒有就建立)資料夾裡的存放處,讀回上次存的資料
func OpenFile(dir string) (*File, error) {
	if dir == "" {
		return nil, errors.New("要給資料夾")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f := &File{Mem: NewMem(), dir: dir}
	cleanTemps(dir)
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	var s fileSnapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s 讀不出來:%w", filepath.Join(dir, fileName), err)
	}
	f.restore(s)
	return f, nil
}

// cleanTemps 清掉上次寫到一半(程式被砍掉)留下的 .store.json.tmp-*(只有一個程式會開這個資料夾,
// 打開時不會有別人正在寫;改名前被砍斷的暫存檔不完整也沒用)
func cleanTemps(dir string) {
	list, _ := filepath.Glob(filepath.Join(dir, "."+fileName+".tmp-*"))
	for _, p := range list {
		_ = os.Remove(p)
	}
}

// Dir 資料夾
func (f *File) Dir() string { return f.dir }

func orEmpty[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}

func (f *File) restore(s fileSnapshot) {
	m := f.Mem
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range s.Users {
		m.users[u.ID] = u
	}
	for _, t := range s.Tokens {
		tk := t.Token
		tk.Hash = t.Hash
		m.tokens[tk.ID] = &memToken{Token: tk}
	}
	m.audit = append([]model.AuditEntry{}, s.Audit...)
	for _, o := range s.Orgs {
		m.orgs[o.ID] = o
	}
	for _, mm := range s.Members {
		if m.members[mm.Org] == nil {
			m.members[mm.Org] = map[string]model.Member{}
		}
		m.members[mm.Org][mm.UserID] = mm
	}
	m.issues = orEmpty(s.Issues)
	m.agents = orEmpty(s.Agents)
	m.tasks = orEmpty(s.Tasks)
	for _, c := range s.Creds {
		x := c.AgentCredential
		x.Hash = c.Hash
		m.creds = append(m.creds, x)
	}
	m.layouts = orEmpty(s.Layouts)
	m.docs = orEmpty(s.Docs)
	m.chains = orEmpty(s.Chains)
	m.channels = orEmpty(s.Channels)
	m.messages = orEmpty(s.Messages)
	m.reads = orEmpty(s.Reads)
	m.incidents = orEmpty(s.Incidents)
	m.integrations = orEmpty(s.Integrations)
	m.system = orEmpty(s.System)
	m.mcp = orEmpty(s.MCP)
}

// snapshot 在 Mem 的鎖裡編成 JSON(編碼本身就是深拷貝)
func (f *File) snapshot() ([]byte, error) {
	m := f.Mem
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	s := fileSnapshot{Version: 1, SavedAt: now, Users: []model.User{}, Tokens: []fileToken{}, Orgs: []model.Org{},
		Members: []model.Member{}, Creds: []fileCred{},
		Issues: m.issues, Agents: m.agents, Tasks: m.tasks, Layouts: m.layouts, Docs: m.docs, Chains: m.chains,
		Channels: m.channels, Messages: m.messages, Reads: m.reads, Incidents: m.incidents, Integrations: m.integrations, System: m.system, MCP: m.mcp}
	for _, u := range m.users {
		s.Users = append(s.Users, u)
	}
	for _, t := range m.tokens {
		if t.revoked || (t.ExpiresAt != nil && !t.ExpiresAt.After(now)) {
			continue
		}
		s.Tokens = append(s.Tokens, fileToken{Token: t.Token, Hash: t.Hash})
	}
	s.Audit = m.audit
	if len(s.Audit) > auditKeep {
		s.Audit = s.Audit[len(s.Audit)-auditKeep:]
	}
	for _, o := range m.orgs {
		s.Orgs = append(s.Orgs, o)
	}
	for _, byUser := range m.members {
		for _, mm := range byUser {
			s.Members = append(s.Members, mm)
		}
	}
	for _, c := range m.creds {
		s.Creds = append(s.Creds, fileCred{AgentCredential: c, Hash: c.Hash})
	}
	return json.Marshal(s)
}

// writeAtomic 先寫同資料夾的暫存檔、同步到磁碟,再改名蓋過去
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o600)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name) // 只清掉自己剛建的暫存檔
	}
	return werr
}

// save 存整份資料;同一時間只有一個在存,而且存的一定是最新的狀態(快照與寫檔都在 saveMu 裡)
func (f *File) save() error {
	f.saveMu.Lock()
	defer f.saveMu.Unlock()
	b, err := f.snapshot()
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(f.dir, fileName), b); err != nil {
		return fmt.Errorf("存檔失敗:%w", err)
	}
	return nil
}

// after 寫入成功才存檔(批次裡不存,批次結束時一起存)
func (f *File) after(err error) error {
	if err != nil {
		return err
	}
	if f.tx {
		f.dirty.Store(true)
		return nil
	}
	return f.save()
}

// write 一筆寫入:改記憶體、成功就存檔。批次進行中時,批次以外的寫入在這裡等批次結束
// (批次失敗會把記憶體還原到批次開始前,不能把別人的寫入一起還原掉)
func (f *File) write(op func() error) error {
	if f.tx {
		return f.after(op())
	}
	f.gate.RLock()
	defer f.gate.RUnlock()
	return f.after(op())
}

// Batch 批次寫入(全有或全無):fn 用 tx 寫入時只改記憶體、不存檔;fn 成功後整份只存一次檔。
// fn 回傳錯誤或存檔失敗時,記憶體還原到批次開始前,磁碟上的 store.json 也還是批次開始前的樣子。
// 批次進行中,其他寫入(不是透過 tx 的)會等批次結束;讀取不受影響(可能讀到批次做到一半的資料)。
// fn 裡只能用 tx 寫入(用 f 會等自己而卡住)。tx 只在 fn 執行期間有效
func (f *File) Batch(fn func(tx *File) error) error {
	if f.tx {
		return fn(f)
	}
	f.gate.Lock()
	defer f.gate.Unlock()
	before, err := f.snapshot()
	if err != nil {
		return err
	}
	tx := &File{Mem: f.Mem, dir: f.dir, tx: true}
	if err := fn(tx); err != nil {
		if rerr := f.rollback(before); rerr != nil {
			return fmt.Errorf("%v(還原記憶體也失敗:%v)", err, rerr)
		}
		return err
	}
	if !tx.dirty.Load() {
		return nil
	}
	if err := f.save(); err != nil {
		if rerr := f.rollback(before); rerr != nil {
			return fmt.Errorf("%v(還原記憶體也失敗:%v)", err, rerr)
		}
		return err
	}
	return nil
}

// rollback 把記憶體換回批次開始前的快照
func (f *File) rollback(before []byte) error {
	var s fileSnapshot
	if err := json.Unmarshal(before, &s); err != nil {
		return err
	}
	fresh := &File{Mem: NewMem()}
	fresh.restore(s)
	f.Mem.replaceFrom(fresh.Mem)
	return nil
}

// replaceFrom 用 src 的內容取代 m 的(鎖不換;附件 File 存在磁碟上,不在記憶體)
func (m *Mem) replaceFrom(src *Mem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users, m.tokens, m.audit, m.orgs, m.members = src.users, src.tokens, src.audit, src.orgs, src.members
	m.issues, m.agents, m.tasks, m.creds = src.issues, src.agents, src.tasks, src.creds
	m.layouts, m.docs, m.chains = src.layouts, src.docs, src.chains
	m.channels, m.messages, m.reads = src.channels, src.messages, src.reads
	m.incidents, m.integrations, m.system = src.incidents, src.integrations, src.system
	m.mcp = src.mcp
}

// w1 有回傳值的寫入
func w1[T any](f *File, op func() (T, error)) (T, error) {
	var out T
	err := f.write(func() error {
		var err error
		out, err = op()
		return err
	})
	return out, err
}

// ---- 寫入:先改記憶體,成功就存檔 ----

func (f *File) SaveUser(u model.User) error {
	return f.write(func() error { return f.Mem.SaveUser(u) })
}
func (f *File) CreateToken(t model.Token) error {
	return f.write(func() error { return f.Mem.CreateToken(t) })
}
func (f *File) TouchToken(id string, at time.Time) error {
	return f.write(func() error { return f.Mem.TouchToken(id, at) })
}
func (f *File) RevokeToken(id string, at time.Time) error {
	return f.write(func() error { return f.Mem.RevokeToken(id, at) })
}
func (f *File) AddAudit(e model.AuditEntry) error {
	return f.write(func() error { return f.Mem.AddAudit(e) })
}
func (f *File) CreateOrg(o model.Org) error {
	return f.write(func() error { return f.Mem.CreateOrg(o) })
}
func (f *File) UpdateOrg(o model.Org) error {
	return f.write(func() error { return f.Mem.UpdateOrg(o) })
}
func (f *File) DeleteOrg(id string) error {
	return f.write(func() error { return f.Mem.DeleteOrg(id) })
}
func (f *File) SaveMember(m model.Member) error {
	return f.write(func() error { return f.Mem.SaveMember(m) })
}
func (f *File) RemoveMember(org, userID string) error {
	return f.write(func() error { return f.Mem.RemoveMember(org, userID) })
}

func (f *File) CreateIssue(org string, i model.Issue) (model.Issue, error) {
	return w1(f, func() (model.Issue, error) { return f.Mem.CreateIssue(org, i) })
}
func (f *File) UpdateIssue(org string, i model.Issue) error {
	return f.write(func() error { return f.Mem.UpdateIssue(org, i) })
}
func (f *File) DeleteIssue(org, id string) error {
	return f.write(func() error { return f.Mem.DeleteIssue(org, id) })
}
func (f *File) ImportIssues(org string, list []model.Issue) (int, error) {
	return w1(f, func() (int, error) { return f.Mem.ImportIssues(org, list) })
}

func (f *File) SaveAgent(org string, a model.Agent) error {
	return f.write(func() error { return f.Mem.SaveAgent(org, a) })
}
func (f *File) DeleteAgent(org, id string) error {
	return f.write(func() error { return f.Mem.DeleteAgent(org, id) })
}
func (f *File) CreateAgentTask(org string, t model.AgentTask) (model.AgentTask, error) {
	return w1(f, func() (model.AgentTask, error) { return f.Mem.CreateAgentTask(org, t) })
}
func (f *File) UpdateAgentTask(org string, t model.AgentTask) error {
	return f.write(func() error { return f.Mem.UpdateAgentTask(org, t) })
}
func (f *File) ClaimAgentTask(org string, t model.AgentTask, now time.Time) error {
	return f.write(func() error { return f.Mem.ClaimAgentTask(org, t, now) })
}
func (f *File) ExtendAgentCredential(org, taskID string, expires time.Time) error {
	return f.write(func() error { return f.Mem.ExtendAgentCredential(org, taskID, expires) })
}
func (f *File) SaveAgentCredential(c model.AgentCredential) error {
	return f.write(func() error { return f.Mem.SaveAgentCredential(c) })
}
func (f *File) DeleteAgentCredentials(org, taskID, agentID string) error {
	return f.write(func() error { return f.Mem.DeleteAgentCredentials(org, taskID, agentID) })
}
func (f *File) ImportAgents(org string, agents []model.Agent, tasks []model.AgentTask) error {
	return f.write(func() error { return f.Mem.ImportAgents(org, agents, tasks) })
}

// PruneAgentCredentials 例行清理:沒有到期的憑證就不存檔
func (f *File) PruneAgentCredentials(now time.Time) error {
	if !f.tx {
		f.gate.RLock()
		defer f.gate.RUnlock()
	}
	f.Mem.mu.Lock()
	n := len(f.Mem.creds)
	f.Mem.mu.Unlock()
	if err := f.Mem.PruneAgentCredentials(now); err != nil {
		return err
	}
	f.Mem.mu.Lock()
	changed := len(f.Mem.creds) != n
	f.Mem.mu.Unlock()
	if !changed {
		return nil
	}
	return f.after(nil)
}

func (f *File) SaveCabinetLayout(org string, layout []model.CabinetSection) error {
	return f.write(func() error { return f.Mem.SaveCabinetLayout(org, layout) })
}
func (f *File) CreateDoc(org string, d model.CabinetDoc) (model.CabinetDoc, error) {
	return w1(f, func() (model.CabinetDoc, error) { return f.Mem.CreateDoc(org, d) })
}
func (f *File) UpdateDoc(org string, d model.CabinetDoc) error {
	return f.write(func() error { return f.Mem.UpdateDoc(org, d) })
}
func (f *File) DeleteDoc(org, id string) error {
	return f.write(func() error { return f.Mem.DeleteDoc(org, id) })
}
func (f *File) ImportDocs(org string, list []model.CabinetDoc) (int, error) {
	return w1(f, func() (int, error) { return f.Mem.ImportDocs(org, list) })
}

func (f *File) SaveOrgChain(org string, c model.OrgChain) error {
	return f.write(func() error { return f.Mem.SaveOrgChain(org, c) })
}

func (f *File) CreateChannel(org string, c model.Channel) (model.Channel, error) {
	return w1(f, func() (model.Channel, error) { return f.Mem.CreateChannel(org, c) })
}
func (f *File) UpdateChannel(org string, c model.Channel) error {
	return f.write(func() error { return f.Mem.UpdateChannel(org, c) })
}
func (f *File) CreateChannelMessage(org string, m model.ChannelMessage) (model.ChannelMessage, error) {
	return w1(f, func() (model.ChannelMessage, error) { return f.Mem.CreateChannelMessage(org, m) })
}
func (f *File) SaveChannelRead(org string, r model.ChannelRead) error {
	return f.write(func() error { return f.Mem.SaveChannelRead(org, r) })
}

// ---- 附件:一份一個檔 ----

// attachPath 附件檔的位置;組織與 id 只能是安全的檔名(不能跳出資料夾)
func (f *File) attachPath(org, id string) (string, error) {
	for _, s := range []string{org, id} {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".") {
			return "", fmt.Errorf("附件的組織或 id 不對:%q", s)
		}
	}
	return filepath.Join(f.dir, "attachments", org, id), nil
}

func (f *File) PutAttachment(org, id string, data []byte) error {
	p, err := f.attachPath(org, id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return nil // 同 id 再存不覆蓋
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return writeAtomic(p, data)
}

func (f *File) GetAttachment(org, id string) ([]byte, error) {
	p, err := f.attachPath(org, id)
	if err != nil {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

var _ Store = (*File)(nil)
