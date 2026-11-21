// agents.go 虛擬角色本身:說明、共用狀態與輔助、列出/取得/新增/修改角色與 docAccess

package service

import (
	"errors"
	"fmt"
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
	// docAccess 先正規化:"<專案 id>:<位置>" 適用該專案(id 存成大寫);沒有前綴的只適用未分專案。
	// 正規化後同一個 key 出現兩次且值不同就拒絕(不靠 map 的走訪順序決定);none 直接拿掉,不檢查專案
	acc := map[string]string{}
	for k, v := range in.DocAccess {
		if !model.OneOf(v, model.DocAccessLevels) {
			return nil, fail(400, "docAccess 的值必須是 read、write 或 none")
		}
		k = model.NormalizeDocAccessKey(k)
		if pid, rest := model.SplitDocAccessKey(k); pid != "" && rest == "" {
			return nil, fail(400, "docAccess 的 %s 要寫成 <專案 id>:<區塊 id 或頁籤網址>", k)
		}
		if old, ok := acc[k]; ok && old != v {
			return nil, fail(400, "docAccess 的 %s 重複出現,值不一致(%s、%s)", k, old, v)
		}
		acc[k] = v
	}
	routines := []model.AgentRoutine{}
	routineIDs := map[string]bool{}
	for _, r := range in.Routines {
		if strings.TrimSpace(r.Title) == "" {
			continue
		}
		if r.ID == "" {
			r.ID = uuid()
		}
		if routineIDs[r.ID] {
			return nil, fail(400, "routines 的 id 重複:%s", r.ID)
		}
		routineIDs[r.ID] = true
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
	// 專案要存在(在 agents.mu 內檢查,和 DeleteProject 互斥);專案已刪、而目前存的角色本來就有這個 key 的,
	// 當成殘留直接拿掉(編輯器會把整份 docAccess 送回來,不然角色永遠存不了);新加的 key 指向不存在的專案就拒絕
	docAccess, had := map[string]string{}, curDocAccess(cur)
	for k, v := range acc {
		if v == "none" {
			continue
		}
		if pid, _ := model.SplitDocAccessKey(k); pid != "" {
			if _, err := s.Store.GetProject(org, pid); errors.Is(err, store.ErrNotFound) {
				if _, ok := had[k]; ok {
					continue
				}
				return nil, fail(400, "docAccess 的 %s:找不到專案 %s", k, pid)
			} else if err != nil {
				return nil, err
			}
		}
		docAccess[k] = v
	}
	in.DocAccess = docAccess
	if cur != nil {
		in.CreatedAt = cur.CreatedAt
	} else {
		in.CreatedAt = s.mt()
	}
	// 新建時 owner = 建立者的使用者 actor(沒有人類建立者 = store 的預設);已存在的角色 store 不改 owner
	if err := s.Store.SaveAgentOwned(org, in, ownerActorID(p)); err != nil {
		return nil, err
	}
	action := "agent.update"
	if cur == nil {
		action = "agent.create"
	}
	s.Audit(p, action, org, map[string]any{"agent": id, "name": in.Name})
	return &in, nil
}

// curDocAccess 目前存的角色的 docAccess(key 正規化;沒有角色回空)
func curDocAccess(g *model.Agent) map[string]string {
	out := map[string]string{}
	if g != nil {
		for k, v := range g.DocAccess {
			out[model.NormalizeDocAccessKey(k)] = v
		}
	}
	return out
}
