package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"omixor-core/model"
	"omixor-core/store"
)

// 虛擬角色、任務佇列、任務綁定憑證、Issue 指派觸發。
//
// omixor 不自己跑 AI:任務排在這裡,由使用者的 Claude 領取(發一組憑證),再用憑證連角色端點(core/mcp)以角色身分工作、回報。
//
// 權限:
//   - 組織管理員:角色的新增、修改、刪除;派任務、取消、重新排隊
//   - 組織成員:列角色與任務、領取任務(代表角色的 Claude 用自己的 omixor token 領取)、回報
//   - 角色身分(Principal.Agent,憑證換來的):只能讀自己的任務與設定、回報憑證綁定的那一筆任務、
//     讀全部 Issue、建立 Issue、對 Issue 留言與改狀態;其他一律 403(orgRole 預設拒絕角色身分)
//
// 憑證:領取時產生 32 bytes 隨機密鑰(base64url),只回傳一次;資料庫只存 SHA-256 雜湊與到期時間。
// 一個任務同時只有一組有效憑證(重領舊的作廢);任務離開 inProgress、角色被刪、逾時都會失效。

// DefaultAgentCredTTL 角色憑證預設有效時間
const DefaultAgentCredTTL = 2 * time.Hour

// agentState Service 裡與角色有關的狀態(零值可用)
type agentState struct {
	mu       sync.Mutex // 任務的「讀了再改」、Issue 指派去重都在這把鎖裡(單一程式內;多個實例同時跑仍可能競爭,見回報的風險)
	failMu   sync.Mutex
	fails    map[string]*agentFail
	lastPrun time.Time
}

type agentFail struct {
	n     int
	reset time.Time
}

func (s *Service) credTTL() time.Duration {
	if s.AgentCredTTL > 0 {
		return s.AgentCredTTL
	}
	return DefaultAgentCredTTL
}

// ================= 角色 =================

// viewer 呼叫者可以看這個組織嗎:成員(含伺服器管理員)或該組織的角色身分
func (s *Service) viewer(p *model.Principal, org string) error {
	if p.Agent == nil {
		_, err := s.orgRole(p, org)
		return err
	}
	if p.Agent.Org != org {
		return fail(403, "這組角色憑證不能用在組織 %s", org)
	}
	return nil
}

// notAgent 這個操作不開放給角色身分
func notAgent(p *model.Principal) error {
	if p.Agent != nil {
		return fail(403, "角色身分不能做這件事")
	}
	return nil
}

// findAgent 依 id(不分大小寫)或名稱找角色
func (s *Service) findAgent(org, idOrName string) (*model.Agent, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return nil, store.ErrNotFound
	}
	list, err := s.Store.ListAgents(org)
	if err != nil {
		return nil, err
	}
	for _, g := range list {
		if strings.EqualFold(g.ID, idOrName) {
			return &g, nil
		}
	}
	for _, g := range list {
		if g.Name == idOrName {
			return &g, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Service) mustAgent(org, idOrName string) (*model.Agent, error) {
	g, err := s.findAgent(org, idOrName)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到角色 %s", idOrName)
	}
	return g, err
}

// ListAgents 組織的角色(成員;角色身分只看得到自己)
func (s *Service) ListAgents(p *model.Principal, org string) ([]model.Agent, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListAgents(org)
	if err != nil || p.Agent == nil {
		return list, err
	}
	out := []model.Agent{}
	for _, g := range list {
		if g.ID == p.Agent.AgentID {
			out = append(out, g)
		}
	}
	return out, nil
}

// GetAgent 依 id 或名稱取角色(角色身分只能取自己)
func (s *Service) GetAgent(p *model.Principal, org, id string) (*model.Agent, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	g, err := s.mustAgent(org, id)
	if err != nil {
		return nil, err
	}
	if p.Agent != nil && g.ID != p.Agent.AgentID {
		return nil, fail(404, "找不到角色 %s", id)
	}
	return g, nil
}

func newAgentID(name string, taken func(string) bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	parts := []string{}
	for _, p := range strings.Split(b.String(), "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	root := strings.Join(parts, "-")
	if len(root) > 40 {
		root = strings.Trim(root[:40], "-")
	}
	if root == "" {
		root = "agent"
	}
	id, n := root, 2
	for taken(id) {
		id = fmt.Sprintf("%s-%d", root, n)
		n++
	}
	return id
}

// SaveAgent 新增或整筆修改角色(組織管理員)。id 空白 = 新增,由名稱產生 id;
// 有給 id 且不存在 = 用這個 id 新增(只能用小寫英文、數字與 -);存在 = 取代(建立時間不變)。
func (s *Service) SaveAgent(p *model.Principal, org string, in model.Agent) (*model.Agent, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fail(400, "要給角色名稱")
	}
	in.Color = strings.TrimSpace(in.Color)
	if in.Color != "" && !model.OneOf(in.Color, model.AgentColors) {
		return nil, fail(400, "color 必須是 %s 之一", strings.Join(model.AgentColors, "、"))
	}
	acc := map[string]string{}
	for k, v := range in.DocAccess {
		if !model.OneOf(v, model.DocAccessLevels) {
			return nil, fail(400, "docAccess 的值必須是 read、write 或 none")
		}
		if v != "none" {
			acc[k] = v
		}
	}
	in.DocAccess = acc
	routines := []model.AgentRoutine{}
	for _, r := range in.Routines {
		if strings.TrimSpace(r.Title) == "" {
			continue
		}
		if r.ID == "" {
			r.ID = uuid()
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = s.mt()
		}
		if !model.OneOf(r.Kind, model.RoutineKinds) {
			r.Kind = "daily"
		}
		if !model.OneOf(r.Priority, model.IssuePriorities) {
			r.Priority = "normal"
		}
		routines = append(routines, r)
	}
	in.Routines = routines

	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	id := strings.ToLower(strings.TrimSpace(in.ID))
	var cur *model.Agent
	if id != "" {
		g, err := s.Store.GetAgent(org, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		cur = g
		if cur == nil && !model.ValidAgentID(id) {
			return nil, fail(400, "新角色的 id 只能用小寫英文、數字與 -")
		}
	} else {
		list, err := s.Store.ListAgents(org)
		if err != nil {
			return nil, err
		}
		id = newAgentID(in.Name, func(x string) bool {
			for _, g := range list {
				if g.ID == x {
					return true
				}
			}
			return false
		})
	}
	in.ID = id
	if cur != nil {
		in.CreatedAt = cur.CreatedAt
	} else {
		in.CreatedAt = s.mt()
	}
	if err := s.Store.SaveAgent(org, in); err != nil {
		return nil, err
	}
	action := "agent.update"
	if cur == nil {
		action = "agent.create"
	}
	s.Audit(p, action, org, map[string]any{"agent": id, "name": in.Name})
	return &in, nil
}

// DeleteAgent 刪除角色(組織管理員):它未結束的任務一併取消、憑證作廢,指派給它的 Issue 清掉指派
func (s *Service) DeleteAgent(p *model.Principal, org, id string) error {
	if err := s.needOrgAdmin(p, org); err != nil {
		return err
	}
	g, err := s.mustAgent(org, id)
	if err != nil {
		return err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		return err
	}
	now := s.mt()
	for _, t := range tasks {
		if t.AgentID == g.ID && model.TaskIsOpen(t.Status) {
			t.Status, t.UpdatedAt, t.CompletedAt = "canceled", now, &now
			if err := s.Store.UpdateAgentTask(org, t); err != nil {
				return err
			}
		}
	}
	if err := s.Store.DeleteAgentCredentials(org, "", g.ID); err != nil {
		return err
	}
	if err := s.Store.DeleteAgent(org, g.ID); err != nil {
		return err
	}
	issues, err := s.Store.ListIssues(org)
	if err == nil {
		for _, i := range issues {
			if i.Assignee == g.ID {
				i.Assignee, i.UpdatedAt = "", now
				_ = s.Store.UpdateIssue(org, i)
			}
		}
	}
	s.Audit(p, "agent.delete", org, map[string]any{"agent": g.ID})
	return nil
}

// ================= 任務 =================

// TaskInput 派任務
type TaskInput struct {
	AgentID      string   `json:"agentId"`
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	Priority     string   `json:"priority"`
	Links        []string `json:"links"`
	// Author 任務的建立者署名;只有伺服器管理員(例如 omixor-web 內建核心)給的才算數,其他人一律是自己的 email
	Author string `json:"author,omitempty"`
}

// TaskFilter 列任務的條件:AgentID 空 = 全部角色;Status 空或 all = 全部,open = 未結束,或單一狀態
type TaskFilter struct {
	AgentID string `json:"agentId,omitempty"`
	Status  string `json:"status,omitempty"`
}

// TaskReport 回報任務;nil 表示沒給
type TaskReport struct {
	Status *string  `json:"status,omitempty"`
	Result *string  `json:"result,omitempty"`
	Note   *string  `json:"note,omitempty"`
	Links  []string `json:"links,omitempty"`
}

// ClaimInput 領取任務:TaskID 空 = 領該角色最優先、最早的待領取任務
type ClaimInput struct {
	AgentID string `json:"agentId"`
	TaskID  string `json:"taskId,omitempty"`
}

// ClaimResult 領取結果;沒有可領的任務時 Task 與 Credential 都是 nil
type ClaimResult struct {
	Agent      *model.Agent      `json:"agent,omitempty"`
	Task       *model.AgentTask  `json:"task"`
	Credential *model.AgentGrant `json:"credential,omitempty"`
}

func priorityRank(p string) int {
	for i, v := range model.IssuePriorities {
		if v == p {
			return i
		}
	}
	return len(model.IssuePriorities)
}

// insertTask 排一筆新任務(呼叫端要拿著 agents.mu)
func (s *Service) insertTask(org string, g *model.Agent, title, instructions, priority, by string, links []string) (*model.AgentTask, error) {
	return s.insertTaskFor(org, g, title, instructions, priority, by, links, nil)
}

// insertTaskFor 同 insertTask,多一個例行任務的 id(例行任務排的任務才有)
func (s *Service) insertTaskFor(org string, g *model.Agent, title, instructions, priority, by string, links []string, routineID *string) (*model.AgentTask, error) {
	if priority == "" {
		priority = "normal"
	}
	if !model.OneOf(priority, model.IssuePriorities) {
		return nil, fail(400, "priority 必須是 %s 之一", strings.Join(model.IssuePriorities, "、"))
	}
	now := s.mt()
	t := model.AgentTask{Title: title, Instructions: instructions, AgentID: g.ID, Status: "queued", Priority: priority,
		CreatedBy: by, RoutineID: routineID, CreatedAt: now, UpdatedAt: now, Links: model.CleanLabels(links), Notes: []model.AgentTaskNote{}}
	saved, err := s.Store.CreateAgentTask(org, t)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// AssignTask 派任務給角色(組織管理員)
func (s *Service) AssignTask(p *model.Principal, org string, in TaskInput) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fail(400, "要給任務標題")
	}
	g, err := s.mustAgent(org, in.AgentID)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.insertTask(org, g, title, in.Instructions, in.Priority, author(p, in.Author), in.Links)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "agent.task.assign", org, map[string]any{"task": t.ID, "agent": g.ID})
	return t, nil
}

func (s *Service) findTask(org, id string) (*model.AgentTask, error) {
	t, err := s.Store.GetAgentTask(org, strings.ToUpper(strings.TrimSpace(id)))
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到任務 %s", id)
	}
	return t, err
}

// ListAgentTasks 列任務(新的在前)。角色身分只列自己的
func (s *Service) ListAgentTasks(p *model.Principal, org string, f TaskFilter) ([]model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(f.AgentID)
	if p.Agent != nil {
		agentID = p.Agent.AgentID
	} else if agentID != "" {
		g, err := s.mustAgent(org, agentID)
		if err != nil {
			return nil, err
		}
		agentID = g.ID
	}
	st := strings.TrimSpace(f.Status)
	if st != "" && st != "all" && st != "open" && !model.OneOf(st, model.AgentTaskStatuses) {
		return nil, fail(400, "status 不正確:%s", st)
	}
	all, err := s.Store.ListAgentTasks(org)
	if err != nil {
		return nil, err
	}
	out := []model.AgentTask{}
	for _, t := range all {
		if agentID != "" && t.AgentID != agentID {
			continue
		}
		switch st {
		case "", "all":
		case "open":
			if !model.TaskIsOpen(t.Status) {
				continue
			}
		default:
			if t.Status != st {
				continue
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// GetAgentTask 取一筆任務(角色身分只能取自己的;不是自己的一律 404,不洩漏存在與否)
func (s *Service) GetAgentTask(p *model.Principal, org, id string) (*model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if p.Agent != nil && t.AgentID != p.Agent.AgentID {
		return nil, fail(404, "找不到任務 %s", id)
	}
	return t, nil
}

// saveTaskChange 套用修改並存檔:狀態變了補領取/完成時間;離開 inProgress 就作廢憑證(呼叫端要拿著 agents.mu)
func (s *Service) saveTaskChange(org string, before model.AgentTask, change func(*model.AgentTask)) (*model.AgentTask, error) {
	t := before.Clone()
	change(&t)
	if t.Status != before.Status {
		now := s.mt()
		if t.Status == "inProgress" && t.ClaimedAt == nil {
			t.ClaimedAt = &now
		}
		if t.Status == "done" || t.Status == "canceled" {
			t.CompletedAt = &now
		} else {
			t.CompletedAt = nil
		}
	}
	t.UpdatedAt = s.mt()
	if err := s.Store.UpdateAgentTask(org, t); err != nil {
		return nil, err
	}
	if t.Status != "inProgress" {
		if err := s.Store.DeleteAgentCredentials(org, t.ID, ""); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ClaimTask 領取任務(組織成員;角色身分不能領):任務改成進行中,回傳任務與一次性憑證。
// 給 TaskID 就領那筆(已經進行中、需要協助的可以重領,舊憑證作廢);否則領該角色最優先、最早的待領取任務。沒有任務時 Task 是 nil。
func (s *Service) ClaimTask(p *model.Principal, org string, in ClaimInput) (*ClaimResult, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	g, err := s.mustAgent(org, in.AgentID)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	var pick *model.AgentTask
	if strings.TrimSpace(in.TaskID) != "" {
		t, err := s.findTask(org, in.TaskID)
		if err != nil {
			return nil, err
		}
		if t.AgentID != g.ID {
			owner := t.AgentID
			if o, err := s.Store.GetAgent(org, t.AgentID); err == nil {
				owner = o.Name
			}
			return nil, fail(409, "%s 是指派給 %s 的,不是 %s", t.ID, owner, g.Name)
		}
		if !model.TaskIsOpen(t.Status) {
			return nil, fail(409, "%s 已經%s,不能再領", t.ID, model.TaskStatusLabel(t.Status))
		}
		pick = t
	} else {
		all, err := s.Store.ListAgentTasks(org)
		if err != nil {
			return nil, err
		}
		queued := []model.AgentTask{}
		for _, t := range all {
			if t.AgentID == g.ID && t.Status == "queued" {
				queued = append(queued, t)
			}
		}
		sort.SliceStable(queued, func(i, j int) bool {
			pi, pj := priorityRank(queued[i].Priority), priorityRank(queued[j].Priority)
			if pi != pj {
				return pi < pj
			}
			if !queued[i].CreatedAt.Equal(queued[j].CreatedAt.Time) {
				return queued[i].CreatedAt.Before(queued[j].CreatedAt.Time)
			}
			return queued[i].Number() < queued[j].Number()
		})
		if len(queued) > 0 {
			c := queued[0]
			pick = &c
		}
	}
	if pick == nil {
		return &ClaimResult{Agent: g}, nil
	}
	claimed, err := s.saveTaskChange(org, *pick, func(x *model.AgentTask) { x.Status = "inProgress" })
	if err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, err
	}
	now := s.now()
	exp := now.Add(s.credTTL())
	cred := model.AgentCredential{Org: org, AgentID: g.ID, TaskID: claimed.ID, Hash: HashToken(secret),
		IssuedAt: model.Time{Time: now.Truncate(time.Second)}, ExpiresAt: model.Time{Time: exp.Truncate(time.Second)}}
	if err := s.Store.SaveAgentCredential(cred); err != nil { // 同任務舊的憑證一併作廢
		return nil, err
	}
	s.Audit(p, "agent.task.claim", org, map[string]any{"task": claimed.ID, "agent": g.ID, "expiresAt": cred.ExpiresAt.Time.Format(time.RFC3339)})
	return &ClaimResult{Agent: g, Task: claimed, Credential: &model.AgentGrant{AgentID: g.ID, TaskID: claimed.ID, Secret: secret,
		ExpiresAt: cred.ExpiresAt.Time.UTC().Format(time.RFC3339)}}, nil
}

// ReportTask 回報任務(進度、結果、狀態)。組織成員可回報任何任務;角色身分只能回報憑證綁定的那一筆(id 空白就是那一筆),
// 而且狀態只能是 inProgress、blocked、done。離開 inProgress(done、blocked、canceled)憑證立刻作廢。
func (s *Service) ReportTask(p *model.Principal, org, id string, in TaskReport) (*model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if p.Agent != nil {
		if strings.TrimSpace(id) == "" {
			id = p.Agent.TaskID
		} else if !strings.EqualFold(strings.TrimSpace(id), p.Agent.TaskID) {
			return nil, fail(403, "這組憑證只能回報任務 %s", p.Agent.TaskID)
		}
	}
	if in.Status != nil {
		ok := []string{"inProgress", "blocked", "done"}
		if p.Agent == nil {
			ok = append(ok, "canceled")
		}
		if !model.OneOf(*in.Status, ok) {
			return nil, fail(400, "status 必須是 %s 之一", strings.Join(ok, "、"))
		}
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if p.Agent != nil && t.AgentID != p.Agent.AgentID {
		return nil, fail(404, "找不到任務 %s", id)
	}
	if t.Status == "canceled" {
		return nil, fail(409, "%s 已取消", t.ID)
	}
	if p.Agent != nil && t.Status != "inProgress" {
		return nil, fail(409, "%s 目前不是進行中,不能回報", t.ID)
	}
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	updated, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) {
		if note != "" {
			x.Notes = append(x.Notes, model.AgentTaskNote{ID: uuid(), Author: model.AgentPrefix + t.AgentID, Text: note, At: s.mt()})
		}
		if in.Status != nil {
			x.Status = *in.Status
		}
		if in.Result != nil {
			x.Result = *in.Result
		}
		if in.Links != nil {
			x.Links = model.CleanLabels(append(append([]string{}, x.Links...), in.Links...))
		}
	})
	if err != nil {
		return nil, err
	}
	if in.Status != nil && *in.Status != t.Status {
		s.Audit(p, "agent.task."+*in.Status, org, map[string]any{"task": t.ID, "agent": t.AgentID})
	}
	return updated, nil
}

// CancelTask 取消未結束的任務(組織管理員);憑證作廢
func (s *Service) CancelTask(p *model.Principal, org, id string) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if !model.TaskIsOpen(t.Status) {
		return nil, fail(409, "%s 已經%s,不能取消", t.ID, model.TaskStatusLabel(t.Status))
	}
	u, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) { x.Status = "canceled" })
	if err == nil {
		s.Audit(p, "agent.task.cancel", org, map[string]any{"task": t.ID})
	}
	return u, err
}

// RequeueTask 把任務重新排回待領取(組織管理員;已完成、已取消的也可以);憑證作廢
func (s *Service) RequeueTask(p *model.Principal, org, id string) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if t.Status == "queued" {
		return t, nil
	}
	u, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) { x.Status = "queued" })
	if err == nil {
		s.Audit(p, "agent.task.requeue", org, map[string]any{"task": t.ID})
	}
	return u, err
}

// ================= 憑證驗證 =================

var errAgentAuth = &Error{Code: 401, Msg: "角色憑證無效或已失效"}

func (s *Service) agentBlocked(ip string) bool {
	st := &s.agents
	st.failMu.Lock()
	defer st.failMu.Unlock()
	f := st.fails[ip]
	return f != nil && s.now().Before(f.reset) && f.n > 10
}

// agentFailed 記一次失敗,回傳這一分鐘內第幾次
func (s *Service) agentFailed(ip string) int {
	st := &s.agents
	st.failMu.Lock()
	defer st.failMu.Unlock()
	now := s.now()
	if st.fails == nil || len(st.fails) > 1000 {
		st.fails = map[string]*agentFail{}
	}
	f := st.fails[ip]
	if f == nil || !now.Before(f.reset) {
		f = &agentFail{reset: now.Add(time.Minute)}
		st.fails[ip] = f
	}
	f.n++
	return f.n
}

// AuthenticateAgent 驗證角色憑證(agentId + secret),通過回傳角色身分的 Principal(Agent 欄位有組織、角色、綁定任務)。
//   - 失敗一律回同一個 401(不洩漏角色或憑證存不存在、是逾時還是任務結束);同一來源 ip 一分鐘內失敗超過 10 次回 429
//   - 逾時、任務不在進行中(done、blocked、canceled、重新排隊)、角色被刪都會失敗
//   - 雜湊與角色 id 都用 constant-time 比對
func (s *Service) AuthenticateAgent(agentID, secret, ip string) (*model.Principal, error) {
	if s.agentBlocked(ip) {
		return nil, fail(429, "驗證失敗次數過多,請稍後再試")
	}
	deny := func() (*model.Principal, error) {
		if n := s.agentFailed(ip); n <= 3 { // 避免被灌爆操作紀錄:每分鐘每來源只記前 3 筆(不記角色 id 與密鑰)
			s.Audit(nil, "agent.auth.denied", "", map[string]any{"ip": ip})
		}
		return nil, errAgentAuth
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || secret == "" || len(secret) > 256 || len(agentID) > 100 {
		return deny()
	}
	now := s.now()
	s.agents.failMu.Lock()
	prune := now.Sub(s.agents.lastPrun) > time.Minute
	if prune {
		s.agents.lastPrun = now
	}
	s.agents.failMu.Unlock()
	if prune {
		_ = s.Store.PruneAgentCredentials(now)
	}
	h := HashToken(secret)
	cr, err := s.Store.AgentCredentialByHash(h)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, fail(500, "驗證時發生錯誤")
	}
	if cr == nil {
		return deny()
	}
	ok := subtle.ConstantTimeCompare(cr.Hash, h) & subtle.ConstantTimeCompare([]byte(cr.AgentID), []byte(agentID))
	if ok != 1 || !now.Before(cr.ExpiresAt.Time) {
		return deny()
	}
	t, err := s.Store.GetAgentTask(cr.Org, cr.TaskID)
	if err != nil || t.Status != "inProgress" || t.AgentID != cr.AgentID {
		return deny()
	}
	g, err := s.Store.GetAgent(cr.Org, cr.AgentID)
	if err != nil {
		return deny()
	}
	name := "agent:" + g.ID
	return &model.Principal{
		User:  model.User{ID: name, Email: name, Name: g.Name, Role: "member"},
		Agent: &model.AgentIdentity{Org: cr.Org, AgentID: g.ID, TaskID: cr.TaskID},
	}, nil
}

// ================= Issue 指派觸發 =================

// resolveAssignee 把 Issue 的 assignee 換成角色 id;空白是不指派;角色不存在回 400
func (s *Service) resolveAssignee(org, assignee string) (string, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return "", nil
	}
	g, err := s.findAgent(org, assignee)
	if errors.Is(err, store.ErrNotFound) {
		return "", fail(400, "assignee 指定的角色不存在:%s", assignee)
	}
	if err != nil {
		return "", err
	}
	return g.ID, nil
}

func hasLink(links []string, id string) bool {
	for _, l := range links {
		if strings.EqualFold(l, id) {
			return true
		}
	}
	return false
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// syncAssignee Issue 的指派變了之後:舊角色還沒領取(queued)的這個 Issue 的任務取消(已在進行中的不動);
// 新角色(存在、active、Issue 還開著)沒有「未結束」的這個 Issue 的任務就排一筆。
// 失敗只記操作紀錄,不讓 Issue 的儲存失敗(Issue 已經存好,重試會重複建立)。
func (s *Service) syncAssignee(p *model.Principal, org string, issue model.Issue, old, by string) {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": err.Error()})
		return
	}
	if old != "" && old != issue.Assignee {
		for _, t := range tasks {
			if t.AgentID == old && t.Status == "queued" && hasLink(t.Links, issue.ID) {
				if _, err := s.saveTaskChange(org, t, func(x *model.AgentTask) { x.Status = "canceled" }); err != nil {
					s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": err.Error()})
				}
			}
		}
	}
	if issue.Assignee == "" || issue.Status != "open" {
		return
	}
	g, err := s.Store.GetAgent(org, issue.Assignee)
	if err != nil || !g.Active {
		return
	}
	for _, t := range tasks {
		if t.AgentID == g.ID && model.TaskIsOpen(t.Status) && hasLink(t.Links, issue.ID) {
			return // 這個 Issue 已經有一筆未結束的任務,不重複排
		}
	}
	instr := fmt.Sprintf("請處理 Issue %s「%s」(優先度 %s)。\n\n%s\n\n用 ops_issues(id: %s)讀完整內容與留言;處理完在 Issue 留言說明結果,再用 ops_agent_task_report 回報這筆任務。",
		issue.ID, issue.Title, issue.Priority, truncRunes(strings.TrimSpace(issue.Body), 1000), issue.ID)
	t, err := s.insertTask(org, g, fmt.Sprintf("處理 %s:%s", issue.ID, issue.Title), instr, issue.Priority, by, []string{issue.ID})
	if err != nil {
		s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": err.Error()})
		return
	}
	s.Audit(p, "issue.assign", org, map[string]any{"issue": issue.ID, "agent": g.ID, "task": t.ID})
}

// ================= 例行任務 =================

// RunDueRoutines 到時間的例行任務各排一筆任務(全部組織;停用的角色、停用的例行任務不排)。
// 一個例行任務上一筆還沒做完(待領取、進行中、需要協助)就不重複排,但仍然記下這次的時間(lastRunAt),
// 下次排程從這個時間往後算。回傳這次新排的任務。
// 由使用核心的程式定時呼叫(server 與 omixor-web 本機核心各自每 60 秒一次);同一個資料庫有多個程式同時排程時可能重複排。
func (s *Service) RunDueRoutines(now time.Time) ([]model.AgentTask, error) {
	_ = s.Store.PruneAgentCredentials(now)
	orgs, err := s.Store.ListOrgs()
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	created := []model.AgentTask{}
	var firstErr error
	for _, o := range orgs {
		agents, err := s.Store.ListAgents(o.ID)
		if err != nil {
			firstErr = err
			continue
		}
		var tasks []model.AgentTask
		loaded := false
		for _, g := range agents {
			if !g.Active {
				continue
			}
			changed := false
			for ri := range g.Routines {
				r := g.Routines[ri]
				if !r.Enabled || r.NextRun().After(now) {
					continue
				}
				if !loaded {
					if tasks, err = s.Store.ListAgentTasks(o.ID); err != nil {
						firstErr = err
						break
					}
					loaded = true
				}
				t := model.Time{Time: now.UTC().Truncate(time.Second)}
				g.Routines[ri].LastRunAt = &t
				changed = true
				pending := false
				for _, x := range tasks {
					if x.RoutineID != nil && *x.RoutineID == r.ID && model.TaskIsOpen(x.Status) {
						pending = true
					}
				}
				if pending {
					continue
				}
				rid := r.ID
				gg := g
				nt, err := s.insertTaskFor(o.ID, &gg, r.Title, r.Instructions, r.Priority, model.RoutineAuthor, nil, &rid)
				if err != nil {
					firstErr = err
					continue
				}
				tasks = append([]model.AgentTask{*nt}, tasks...)
				created = append(created, *nt)
			}
			if changed {
				if err := s.Store.SaveAgent(o.ID, g); err != nil {
					firstErr = err
				}
			}
		}
	}
	return created, firstErr
}

// RunRoutine 立刻替角色排一筆例行任務(組織管理員;不管上一筆做完沒有),並把這次當作上次排程的時間。
// by 是署名(同 TaskInput.Author:只有伺服器管理員給的才算數)。
func (s *Service) RunRoutine(p *model.Principal, org, agentID, routineID, by string) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	g, err := s.mustAgent(org, agentID)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	for ri := range g.Routines {
		r := g.Routines[ri]
		if r.ID != routineID {
			continue
		}
		t := s.mt()
		g.Routines[ri].LastRunAt = &t
		if err := s.Store.SaveAgent(org, *g); err != nil {
			return nil, err
		}
		rid := r.ID
		nt, err := s.insertTaskFor(org, g, r.Title, r.Instructions, r.Priority, author(p, by), nil, &rid)
		if err != nil {
			return nil, err
		}
		s.Audit(p, "agent.routine.run", org, map[string]any{"task": nt.ID, "agent": g.ID, "routine": r.ID})
		return nt, nil
	}
	return nil, fail(404, "找不到例行任務 %s", routineID)
}

// StartRoutines 啟動背景排程:立刻檢查一次,之後每 interval(0 = 60 秒)檢查一次。
// 新排了任務時呼叫 onCreated(可以是 nil)。回傳停止的函式。
func (s *Service) StartRoutines(interval time.Duration, onCreated func([]model.AgentTask)) (stop func()) {
	if interval <= 0 {
		interval = time.Minute
	}
	done := make(chan struct{})
	run := func() {
		list, err := s.RunDueRoutines(s.now())
		if err != nil {
			log.Printf("例行任務排程:%v", err)
		}
		if len(list) > 0 && onCreated != nil {
			onCreated(list)
		}
	}
	go func() {
		run()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				run()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
