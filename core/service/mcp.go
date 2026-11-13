package service

import (
	"errors"
	"strings"
	"sync"

	"omixor-core/model"
	"omixor-core/store"
)

// 外部 MCP 閘道的登記(資料見 model/mcp.go;實際連線在用戶端,也就是 macOS App)。
//
//	讀:組織成員(角色憑證也可以,只能讀自己組織的,而且只看到自己的權限)
//	改(伺服器、工具規則、角色權限):組織管理員
//
// 要開「外部 MCP 閘道」功能開關(開發版、實驗、預設關):沒開時讀回空清單(enabled = false 並附說明),
// 寫入回 403 說明要先開功能;判斷工具能不能用(resolve)一律不放行。
// 任何密鑰都不收:輸入裡有 env、headers、secrets 這類帶值的欄位一律 400(只登記名稱 envNames、headerNames)。

// mcpMu 「讀了再改」在這把鎖裡(單一程式內)
var mcpMu sync.Mutex

const mcpOffNote = "「外部 MCP 閘道」功能沒開:請組織管理員到「系統」打開(開發版的實驗功能)"

// MCPView GET /api/orgs/{org}/mcp-servers 的回應
type MCPView struct {
	// Enabled 功能開關開著(false 時 servers、grants 一律是空的)
	Enabled bool                        `json:"enabled"`
	Note    string                      `json:"note,omitempty"`
	Servers []model.MCPServerReg        `json:"servers"`
	Grants  map[string][]model.MCPGrant `json:"grants"`
	// CanEdit 目前的使用者可以改(組織管理員)
	CanEdit   bool        `json:"canEdit"`
	UpdatedAt *model.Time `json:"updatedAt,omitempty"`
	UpdatedBy string      `json:"updatedBy,omitempty"`
}

// MCPServerInput 新增或修改一台伺服器(id 已存在就是修改;沒給的指標欄位不動)
type MCPServerInput struct {
	ID          string    `json:"id"`
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Transport   *string   `json:"transport,omitempty"`
	Command     *string   `json:"command,omitempty"`
	Args        *[]string `json:"args,omitempty"`
	Cwd         *string   `json:"cwd,omitempty"`
	EnvNames    *[]string `json:"envNames,omitempty"`
	URL         *string   `json:"url,omitempty"`
	HeaderNames *[]string `json:"headerNames,omitempty"`
	Enabled     *bool     `json:"enabled,omitempty"`
	ToolDefault *bool     `json:"toolDefault,omitempty"`

	// 下面這些是「帶值的密鑰」欄位:core 不收,有給就回 400(值請存在各裝置)
	Env     any `json:"env,omitempty"`
	Headers any `json:"headers,omitempty"`
	Secrets any `json:"secrets,omitempty"`
	Token   any `json:"token,omitempty"`
}

// MCPToolInfo 同步工具清單用:App 連上伺服器讀到的工具
type MCPToolInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// MCPToolRuleInput 修改一個工具的規則(沒給的不動)
type MCPToolRuleInput struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
	Confirm *bool  `json:"confirm,omitempty"`
}

// MCPToolsInput POST /mcp-servers/{id}/tools:Sync 是讀到的工具(新的加進規則,Enabled 照 ToolDefault、Confirm 依名稱猜;
// 已有的只更新標題與說明);Prune = true 時規則裡不在 Sync 的工具拿掉;Rules 改個別工具
type MCPToolsInput struct {
	Sync  []MCPToolInfo      `json:"sync,omitempty"`
	Prune bool               `json:"prune,omitempty"`
	Rules []MCPToolRuleInput `json:"rules,omitempty"`
}

// MCPGrantsInput 整份取代一個角色的權限
type MCPGrantsInput struct {
	AgentID string           `json:"agentId"`
	Grants  []model.MCPGrant `json:"grants"`
}

// MCPResolveInput 判斷工具能不能用:AgentID 空 = 一般成員(使用者自己的 Claude Code)
type MCPResolveInput struct {
	AgentID string           `json:"agentId,omitempty"`
	Tools   []MCPResolveTool `json:"tools"`
}

type MCPResolveTool struct {
	Server string `json:"server"`
	Name   string `json:"name"`
}

// MCPResolved 一個工具的判斷結果
type MCPResolved struct {
	Server    string `json:"server"`
	Name      string `json:"name"`
	Qualified string `json:"qualified"` // <伺服器 id>__<工具名>
	Allowed   bool   `json:"allowed"`
	Confirm   bool   `json:"confirm"`
	Reason    string `json:"reason,omitempty"` // 不能用的原因
}

// MCPResolveResult POST /mcp-tools/resolve 的回應
type MCPResolveResult struct {
	Enabled bool          `json:"enabled"`
	Note    string        `json:"note,omitempty"`
	Tools   []MCPResolved `json:"tools"`
}

func (s *Service) mcpEnabled(org string) (bool, error) {
	x, err := s.loadSystem(org)
	if err != nil {
		return false, err
	}
	return x.Enabled(model.FeatureMCPGateway), nil
}

func (s *Service) loadMCP(org string) (model.OrgMCPConfig, error) {
	x, err := s.Store.GetMCPConfig(org)
	if errors.Is(err, store.ErrNotFound) {
		return model.OrgMCPConfig{Servers: []model.MCPServerReg{}, Grants: map[string][]model.MCPGrant{}}, nil
	}
	if err != nil {
		return model.OrgMCPConfig{}, err
	}
	return x.Clone(), nil
}

// needMCPAdmin 寫入前:組織管理員 + 功能開著
func (s *Service) needMCPAdmin(p *model.Principal, org string) error {
	if err := s.needOrgAdmin(p, org); err != nil {
		return err
	}
	on, err := s.mcpEnabled(org)
	if err != nil {
		return err
	}
	if !on {
		return fail(403, "%s", mcpOffNote)
	}
	return nil
}

// GetMCP 組織的外部 MCP 登記(成員;角色身分只看到自己的權限)
func (s *Service) GetMCP(p *model.Principal, org string) (*MCPView, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	v := &MCPView{Servers: []model.MCPServerReg{}, Grants: map[string][]model.MCPGrant{}, CanEdit: s.canEditOrg(p, org)}
	on, err := s.mcpEnabled(org)
	if err != nil {
		return nil, err
	}
	if !on {
		v.Note = mcpOffNote
		return v, nil
	}
	c, err := s.loadMCP(org)
	if err != nil {
		return nil, err
	}
	v.Enabled, v.Servers, v.UpdatedAt, v.UpdatedBy = true, c.Servers, c.UpdatedAt, c.UpdatedBy
	for k, g := range c.Grants {
		if p.Agent != nil && !strings.EqualFold(k, p.Agent.AgentID) {
			continue
		}
		v.Grants[k] = g
	}
	return v, nil
}

// cleanNames 名稱清單:去空白、去重複、檢查格式
func cleanNames(in []string, valid func(string) bool, what string, max int) ([]string, error) {
	out := []string{}
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !valid(n) {
			return nil, fail(400, "%s格式不對:%s", what, n)
		}
		if !containsFold(out, n) {
			out = append(out, n)
		}
	}
	if len(out) > max {
		return nil, fail(400, "%s最多 %d 個", what, max)
	}
	return out, nil
}

// SaveMCPServer 新增或修改一台伺服器(組織管理員)
func (s *Service) SaveMCPServer(p *model.Principal, org string, in MCPServerInput) (*model.MCPServerReg, error) {
	if err := s.needMCPAdmin(p, org); err != nil {
		return nil, err
	}
	if in.Env != nil || in.Headers != nil || in.Secrets != nil || in.Token != nil {
		return nil, fail(400, "core 不存任何密鑰:只登記需要的環境變數名稱(envNames)或標頭名稱(headerNames),值請在各裝置輸入(App 存在鑰匙圈)")
	}
	id := strings.ToLower(strings.TrimSpace(in.ID))
	if !model.ValidMCPServerID(id) {
		return nil, fail(400, "伺服器 id 要是 1–32 個小寫英文、數字或 -(不能有底線)")
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	c, err := s.loadMCP(org)
	if err != nil {
		return nil, err
	}
	cur, idx, exists := c.Server(id)
	if !exists {
		if len(c.Servers) >= model.MaxMCPServers {
			return nil, fail(400, "最多登記 %d 台伺服器", model.MaxMCPServers)
		}
		cur = model.MCPServerReg{ID: id, Args: []string{}, EnvNames: []string{}, HeaderNames: []string{}, Tools: []model.MCPToolRule{}, Enabled: true}
	}
	x := cur.Clone()
	if in.Name != nil {
		x.Name = model.ClipMCPText(*in.Name, true)
	}
	if in.Description != nil {
		x.Description = model.ClipMCPText(*in.Description, false)
	}
	if in.Transport != nil {
		x.Transport = strings.ToLower(strings.TrimSpace(*in.Transport))
	}
	if in.Command != nil {
		x.Command = strings.TrimSpace(*in.Command)
	}
	if in.Args != nil {
		if len(*in.Args) > model.MaxMCPArgs {
			return nil, fail(400, "參數最多 %d 個", model.MaxMCPArgs)
		}
		x.Args = append([]string{}, *in.Args...)
	}
	if in.Cwd != nil {
		x.Cwd = strings.TrimSpace(*in.Cwd)
	}
	if in.EnvNames != nil {
		if x.EnvNames, err = cleanNames(*in.EnvNames, model.ValidMCPEnvName, "環境變數名稱", model.MaxMCPSecrets); err != nil {
			return nil, err
		}
	}
	if in.URL != nil {
		x.URL = strings.TrimSpace(*in.URL)
	}
	if in.HeaderNames != nil {
		if x.HeaderNames, err = cleanNames(*in.HeaderNames, model.ValidMCPHeaderName, "標頭名稱", model.MaxMCPSecrets); err != nil {
			return nil, err
		}
	}
	if in.Enabled != nil {
		x.Enabled = *in.Enabled
	}
	if in.ToolDefault != nil {
		x.ToolDefault = *in.ToolDefault
	}
	if x.Name == "" {
		x.Name = id
	}
	switch x.Transport {
	case model.MCPTransportStdio:
		if x.Command == "" {
			return nil, fail(400, "stdio 伺服器要有啟動指令(command)")
		}
		x.URL, x.HeaderNames = "", []string{}
	case model.MCPTransportHTTP:
		u, why := model.NormalizeMCPURL(x.URL)
		if why != "" {
			return nil, fail(400, "%s", why)
		}
		x.URL = u
		x.Command, x.Args, x.Cwd, x.EnvNames = "", []string{}, "", []string{}
	default:
		return nil, fail(400, "傳輸方式要是 stdio 或 http")
	}
	now := s.mt()
	x.UpdatedAt, x.UpdatedBy = &now, whoName(p)
	if exists {
		c.Servers[idx] = x
	} else {
		c.Servers = append(c.Servers, x)
	}
	c.UpdatedAt, c.UpdatedBy = &now, whoName(p)
	if err := s.Store.SaveMCPConfig(org, c); err != nil {
		return nil, err
	}
	// 稽核只記非機密的摘要
	s.Audit(p, "mcp.server.save", org, map[string]any{"id": id, "transport": x.Transport, "enabled": x.Enabled, "new": !exists})
	return &x, nil
}

// DeleteMCPServer 刪除一台伺服器(組織管理員;角色權限裡的這台一併拿掉)
func (s *Service) DeleteMCPServer(p *model.Principal, org, id string) error {
	if err := s.needMCPAdmin(p, org); err != nil {
		return err
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	c, err := s.loadMCP(org)
	if err != nil {
		return err
	}
	srv, idx, ok := c.Server(strings.TrimSpace(id))
	if !ok {
		return fail(404, "找不到 MCP 伺服器 %s", id)
	}
	c.Servers = append(c.Servers[:idx], c.Servers[idx+1:]...)
	for k, gs := range c.Grants {
		kept := []model.MCPGrant{}
		for _, g := range gs {
			if !strings.EqualFold(g.Server, srv.ID) {
				kept = append(kept, g)
			}
		}
		if len(kept) == 0 {
			delete(c.Grants, k)
		} else {
			c.Grants[k] = kept
		}
	}
	now := s.mt()
	c.UpdatedAt, c.UpdatedBy = &now, whoName(p)
	if err := s.Store.SaveMCPConfig(org, c); err != nil {
		return err
	}
	s.Audit(p, "mcp.server.delete", org, map[string]any{"id": srv.ID})
	return nil
}

// SaveMCPTools 同步工具清單、改工具規則(組織管理員)
func (s *Service) SaveMCPTools(p *model.Principal, org, id string, in MCPToolsInput) (*model.MCPServerReg, error) {
	if err := s.needMCPAdmin(p, org); err != nil {
		return nil, err
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	c, err := s.loadMCP(org)
	if err != nil {
		return nil, err
	}
	cur, idx, ok := c.Server(strings.TrimSpace(id))
	if !ok {
		return nil, fail(404, "找不到 MCP 伺服器 %s", id)
	}
	x := cur.Clone()
	if in.Sync != nil {
		seen := map[string]bool{}
		for _, t := range in.Sync {
			name := strings.TrimSpace(t.Name)
			if !model.ValidMCPToolName(name) {
				return nil, fail(400, "工具名稱格式不對:%s", name)
			}
			seen[name] = true
			found := false
			for i := range x.Tools {
				if x.Tools[i].Name == name {
					x.Tools[i].Title, x.Tools[i].Description = model.ClipMCPText(t.Title, true), model.ClipMCPText(t.Description, false)
					found = true
				}
			}
			if !found {
				x.Tools = append(x.Tools, model.MCPToolRule{Name: name, Title: model.ClipMCPText(t.Title, true),
					Description: model.ClipMCPText(t.Description, false), Enabled: x.ToolDefault, Confirm: model.MCPRiskyTool(name)})
			}
		}
		if in.Prune {
			kept := []model.MCPToolRule{}
			for _, r := range x.Tools {
				if seen[r.Name] {
					kept = append(kept, r)
				}
			}
			x.Tools = kept
		}
	}
	for _, r := range in.Rules {
		i := -1
		for j := range x.Tools {
			if x.Tools[j].Name == strings.TrimSpace(r.Name) {
				i = j
			}
		}
		if i < 0 {
			name := strings.TrimSpace(r.Name)
			if !model.ValidMCPToolName(name) {
				return nil, fail(400, "工具名稱格式不對:%s", name)
			}
			x.Tools = append(x.Tools, model.MCPToolRule{Name: name, Enabled: x.ToolDefault, Confirm: model.MCPRiskyTool(name)})
			i = len(x.Tools) - 1
		}
		if r.Enabled != nil {
			x.Tools[i].Enabled = *r.Enabled
		}
		if r.Confirm != nil {
			x.Tools[i].Confirm = *r.Confirm
		}
	}
	if len(x.Tools) > model.MaxMCPTools {
		return nil, fail(400, "一台伺服器最多記 %d 個工具", model.MaxMCPTools)
	}
	now := s.mt()
	x.UpdatedAt, x.UpdatedBy = &now, whoName(p)
	c.Servers[idx] = x
	c.UpdatedAt, c.UpdatedBy = &now, whoName(p)
	if err := s.Store.SaveMCPConfig(org, c); err != nil {
		return nil, err
	}
	s.Audit(p, "mcp.tools.save", org, map[string]any{"id": x.ID, "tools": len(x.Tools), "rules": len(in.Rules)})
	return &x, nil
}

// SaveMCPGrants 整份取代一個角色的權限(組織管理員;伺服器要已登記、角色要存在)
func (s *Service) SaveMCPGrants(p *model.Principal, org string, in MCPGrantsInput) ([]model.MCPGrant, error) {
	if err := s.needMCPAdmin(p, org); err != nil {
		return nil, err
	}
	g, err := s.findAgent(org, in.AgentID)
	if err != nil {
		return nil, fail(400, "找不到角色 %s", in.AgentID)
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	c, err := s.loadMCP(org)
	if err != nil {
		return nil, err
	}
	out := []model.MCPGrant{}
	for _, gr := range in.Grants {
		srv, _, ok := c.Server(strings.TrimSpace(gr.Server))
		if !ok {
			return nil, fail(400, "沒有登記這台 MCP 伺服器:%s", gr.Server)
		}
		tools, err := cleanNames(gr.Tools, model.ValidMCPToolName, "工具名稱", model.MaxMCPTools)
		if err != nil {
			return nil, err
		}
		merged := false
		for i := range out {
			if out[i].Server == srv.ID {
				// 同一台給兩次:任何一次是「全部」就是全部,否則合併
				if len(out[i].Tools) == 0 || len(tools) == 0 {
					out[i].Tools = []string{}
				} else {
					for _, t := range tools {
						if !containsFold(out[i].Tools, t) {
							out[i].Tools = append(out[i].Tools, t)
						}
					}
				}
				merged = true
			}
		}
		if !merged {
			out = append(out, model.MCPGrant{Server: srv.ID, Tools: tools})
		}
	}
	// key 用角色的正式 id(大小寫照角色);同一個角色別的大小寫寫法的舊 key 一併拿掉
	for k := range c.Grants {
		if strings.EqualFold(k, g.ID) {
			delete(c.Grants, k)
		}
	}
	if len(out) > 0 {
		c.Grants[g.ID] = out
	}
	now := s.mt()
	c.UpdatedAt, c.UpdatedBy = &now, whoName(p)
	if err := s.Store.SaveMCPConfig(org, c); err != nil {
		return nil, err
	}
	s.Audit(p, "mcp.grants.save", org, map[string]any{"agent": g.ID, "servers": len(out)})
	return out, nil
}

// grantsOf 角色的權限(key 不分大小寫)
func grantsOf(c model.OrgMCPConfig, agentID string) []model.MCPGrant {
	for k, g := range c.Grants {
		if strings.EqualFold(k, agentID) {
			return g
		}
	}
	return nil
}

// mcpDecide 判斷一個工具:agentID 空 = 一般成員
func mcpDecide(c model.OrgMCPConfig, agentID, server, tool string) MCPResolved {
	r := MCPResolved{Server: server, Name: tool, Qualified: model.MCPQualifiedName(server, tool)}
	srv, _, ok := c.Server(server)
	if !ok {
		r.Reason = "沒有登記這台 MCP 伺服器"
		return r
	}
	r.Server, r.Qualified = srv.ID, model.MCPQualifiedName(srv.ID, tool)
	r.Confirm = srv.ToolConfirm(tool)
	if !srv.Enabled {
		r.Reason = "伺服器沒有啟用(到 設定 › 整合 › 外部 MCP 打開)"
		return r
	}
	if !srv.ToolOn(tool) {
		if _, has := srv.Rule(tool); has {
			r.Reason = "這個工具在閘道沒開(到 設定 › 整合 › 外部 MCP 打開)"
		} else {
			r.Reason = "還沒同步到工具規則,這台伺服器預設不開新工具(管理員在 設定 › 整合 › 外部 MCP 同步工具清單)"
		}
		return r
	}
	if agentID == "" {
		r.Allowed = true
		return r
	}
	for _, g := range grantsOf(c, agentID) {
		if !strings.EqualFold(g.Server, srv.ID) {
			continue
		}
		if len(g.Tools) == 0 {
			r.Allowed = true
			return r
		}
		for _, t := range g.Tools {
			if t == tool {
				r.Allowed = true
				return r
			}
		}
		r.Reason = "角色沒有勾選這個工具(在角色設定的「可用工具」勾選)"
		return r
	}
	r.Reason = "角色沒有這台伺服器的權限(在角色設定的「可用工具」勾選;預設不給)"
	return r
}

// ResolveMCPTools 判斷工具能不能用(App 的閘道列工具、呼叫前都問這裡;成員,角色身分只能問自己)
func (s *Service) ResolveMCPTools(p *model.Principal, org string, in MCPResolveInput) (*MCPResolveResult, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(in.AgentID)
	if p.Agent != nil {
		if agentID != "" && !strings.EqualFold(agentID, p.Agent.AgentID) {
			return nil, fail(403, "角色憑證只能查自己的工具")
		}
		agentID = p.Agent.AgentID
	}
	if agentID != "" {
		g, err := s.findAgent(org, agentID)
		if err != nil {
			return nil, fail(404, "找不到角色 %s", agentID)
		}
		agentID = g.ID
	}
	out := &MCPResolveResult{Tools: []MCPResolved{}}
	on, err := s.mcpEnabled(org)
	if err != nil {
		return nil, err
	}
	if len(in.Tools) > 2000 {
		return nil, fail(400, "一次最多查 2000 個工具")
	}
	if !on {
		out.Note = mcpOffNote
		for _, t := range in.Tools {
			out.Tools = append(out.Tools, MCPResolved{Server: t.Server, Name: t.Name, Qualified: model.MCPQualifiedName(t.Server, t.Name), Reason: mcpOffNote})
		}
		return out, nil
	}
	out.Enabled = true
	c, err := s.loadMCP(org)
	if err != nil {
		return nil, err
	}
	for _, t := range in.Tools {
		out.Tools = append(out.Tools, mcpDecide(c, agentID, strings.TrimSpace(t.Server), strings.TrimSpace(t.Name)))
	}
	return out, nil
}

// mcpAgentTools 角色設定頁「可用工具」裡的外部 MCP 工具(功能沒開時沒有;只列已同步規則的工具)
func (s *Service) mcpAgentTools(org string, g model.Agent) []AgentTool {
	on, err := s.mcpEnabled(org)
	if err != nil || !on {
		return nil
	}
	c, err := s.loadMCP(org)
	if err != nil {
		return nil
	}
	out := []AgentTool{}
	for _, srv := range c.Servers {
		for _, r := range srv.Tools {
			d := mcpDecide(c, g.ID, srv.ID, r.Name)
			label := r.Title
			if label == "" {
				label = r.Name
			}
			out = append(out, AgentTool{Name: d.Qualified, Label: label, Group: "外部 MCP · " + srv.Name, Available: d.Allowed,
				Reason: d.Reason, Source: "mcp", Server: srv.ID, Tool: r.Name, Confirm: d.Confirm})
		}
	}
	return out
}
