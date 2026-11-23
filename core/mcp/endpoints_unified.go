package mcp

import (
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"omixor-core/backend"
	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
)

// 統一入口(雲端 /mcp):一個連線操作帳號下所有組織。沒有預設組織:除了 OrgFreeTools,每次工具呼叫都要帶 org 參數(必填),
// 組織取自 args["org"],每次呼叫各自驗證「呼叫者是該組織成員」,並產生各自的 Call.Ctx(Hooks.CallCtx),
// 所以同一批 JSON-RPC 請求裡不同呼叫可以帶不同組織,不會互相影響。

// OrgFreeTools 統一入口上不需要 org 的工具(帳號層級):orgs 列可用組織、schema_check 檢核資料庫結構(伺服器管理員)
var OrgFreeTools = map[string]bool{"orgs": true, "schema_check": true}

// UnifiedOAuthMessage 統一入口收到 OAuth access token(omo_)時的說明
const UnifiedOAuthMessage = "統一入口 /mcp 暫不支援 OAuth 授權(第一階段只收 omixor 使用者 token omx_…);OAuth 請改用 /mcp/org/{org}"

// UnifiedEndpointConfig 統一入口的設定
type UnifiedEndpointConfig struct {
	// Resolve 認證這個請求,回傳這次連線的 Backend(沒有組織:組織每次呼叫自己帶)。錯誤處理同 AdminEndpointConfig.Resolve
	Resolve func(r *http.Request) (backend.Backend, error)
	// AgentEndpoint 角色端點的網址(領取任務時告訴呼叫端);空字串不附
	AgentEndpoint string
	// Tools 額外的工具(一律視為需要 org)
	Tools        []Tool
	Instructions string
}

var unifiedInstructions = "這是 omixor 的 MCP 統一入口:一個連線可以操作你帳號下所有組織,沒有預設組織。" +
	"除了 orgs 之外,每一次工具呼叫都必須帶 org 參數(組織 id),讀取與寫入都一樣;先用 orgs 看你能用的組織。" +
	"The result carries the organization actually used in its _meta field, under the key omixor/org." +
	strings.Replace(adminInstructions, "操作目前組織的", "操作 org 參數指定組織的", 1)

// orgDenied 缺 org、org 不合法、不存在、不是成員都用同一句(不洩漏組織存不存在),並列出呼叫者可用的組織
func orgDenied(why string, avail []model.Org) error {
	parts := make([]string, 0, len(avail))
	for _, o := range avail {
		if o.Name != "" && o.Name != o.ID {
			parts = append(parts, o.ID+"("+o.Name+")")
		} else {
			parts = append(parts, o.ID)
		}
	}
	list := "目前沒有可用的組織"
	if len(parts) > 0 {
		list = "可用的組織:" + strings.Join(parts, "、")
	}
	return ToolError{Message: why + "。" + list + "。", Status: 403}
}

// UnifiedOrg 統一入口解析這次呼叫的組織:args["org"] 必填,而且是呼叫者能用的組織(成員;伺服器管理員視同全部)。
// 缺、空白、不是字串回 400 並列出可用組織;格式不對、不存在、非成員一律同一句 403(不洩漏存在與否)。
// 成員資格以 Backend.ListOrgs 為準(service 層本來就會再檢查一次;這裡是第一道、也不依賴每個方法都有檢查)
func UnifiedOrg(b backend.Backend, args map[string]any) (string, error) {
	avail, err := b.ListOrgs()
	if err != nil {
		return "", err
	}
	return unifiedOrgIn(avail, args)
}

// unifiedOrgIn 同 UnifiedOrg,但用已查好的可用組織清單
func unifiedOrgIn(avail []model.Org, args map[string]any) (string, error) {
	v, has := args["org"]
	s, isStr := v.(string)
	s = strings.TrimSpace(s)
	if !has || !isStr || s == "" {
		e := orgDenied("統一入口每次呼叫都要帶 org 參數(組織 id)", avail).(ToolError)
		e.Status = 400
		return "", e
	}
	if model.ValidOrgID(s) {
		for _, o := range avail {
			if o.ID == s {
				return o.ID, nil
			}
		}
	}
	return "", orgDenied("你沒有組織 "+s+" 的權限(或沒有這個組織)", avail)
}

// WithOrgParam 統一入口的規格:需要組織的工具加上必填的 org 屬性(複製,不改共用的規格);
// orgs 的說明改成統一入口的版本(沒有目前組織)
func WithOrgParam(defs []any) []any {
	out := make([]any, 0, len(defs))
	for _, d := range defs {
		m, ok := d.(map[string]any)
		name, _ := m["name"].(string)
		if !ok {
			out = append(out, d)
			continue
		}
		cp := maps.Clone(m)
		if name == "orgs" {
			cp["description"] = "列出你能用的組織(id、名稱)。統一入口沒有目前組織:其他工具每次都要帶 org(組織 id)。"
			out = append(out, cp)
			continue
		}
		if OrgFreeTools[name] {
			out = append(out, d)
			continue
		}
		if desc, ok := m["description"].(string); ok {
			cp["description"] = desc + "(統一入口:必須帶 org 指定組織,見 orgs。)"
		}
		sch, _ := m["inputSchema"].(map[string]any)
		s2 := maps.Clone(sch)
		if s2 == nil {
			s2 = map[string]any{"type": "object", "additionalProperties": false}
		}
		props := maps.Clone(func() map[string]any { p, _ := s2["properties"].(map[string]any); return p }())
		if props == nil {
			props = map[string]any{}
		}
		props["org"] = pStr("組織 id(必填;用 orgs 查你能用的組織)")
		s2["properties"] = props
		req := []string{}
		switch r := s2["required"].(type) {
		case []string:
			req = slices.Clone(r)
		case []any:
			for _, x := range r {
				if str, ok := x.(string); ok {
					req = append(req, str)
				}
			}
		}
		if !slices.Contains(req, "org") {
			req = append([]string{"org"}, req...)
		}
		s2["required"] = req
		cp["inputSchema"] = s2
		out = append(out, cp)
	}
	return out
}

// UnifiedEndpoint 統一入口的 http.Handler(雲端掛在 /mcp,用 omixor 使用者 token 認證;OAuth 的 omo_ 一律 403)
func UnifiedEndpoint(cfg UnifiedEndpointConfig) http.Handler {
	ins := cfg.Instructions
	if ins == "" {
		ins = unifiedInstructions
	}
	srv := NewCoreServer(ins)
	allow := map[string]bool{}
	for k := range AdminToolAllow {
		allow[k] = true
	}
	extra := map[string]bool{}
	for _, t := range cfg.Tools {
		srv.Add(t)
		n := t.Def["name"].(string)
		allow[n] = true
		extra[n] = true
	}
	uiResources := UIResources()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok, _ := BearerToken(r); strings.HasPrefix(tok, model.OAuthTokenPrefix) {
			text(w, 403, UnifiedOAuthMessage)
			return
		}
		bk, err := cfg.Resolve(r)
		if err != nil {
			if c := service.Code(err); c == 403 || c == 404 || c == 429 {
				http.Error(w, httperr.Report("mcp 統一入口解析身分", err).Msg, c)
				return
			}
			unauthorized(w, "omixor")
			return
		}
		// OAuth read-only grants are handled in phase 2; share a helper with AdminEndpoint then.
		// ListOrgs is looked up at most once per HTTP request (this bk) and dropped with it; never shared across requests or users.
		oc := &orgCache{b: bk}
		hooks := Hooks{
			// 預設的上下文沒有組織(空字串):就算漏了 CallCtx,工具也會因組織 id 不合法而失敗,不會落到某個組織
			Ctx: &Session{B: bk, Endpoint: cfg.AgentEndpoint},
			Allow: func(name string) bool {
				return allow[name] && !HumanHiddenTools[name]
			},
			Defs: func(defs []any) []any { return WithOrgParam(WithUIMeta(WithoutIdentityAgent(defs))) },
			// 每次呼叫各自的上下文:組織取自 args["org"],驗證成員資格後建立專屬的 Session(不共用、不改寫)
			CallCtx: func(name string, args map[string]any) (any, map[string]any, error) {
				sess := &Session{B: bk, Endpoint: cfg.AgentEndpoint}
				if OrgFreeTools[name] && !extra[name] {
					delete(args, "org")
					return sess, nil, nil
				}
				avail, err := oc.list()
				if err != nil {
					return nil, nil, err
				}
				org, err := unifiedOrgIn(avail, args)
				if err != nil {
					return nil, nil, err
				}
				delete(args, "org") // 工具看到的參數和舊端點一樣
				sess.OrgID = org
				sess.OrgNameFn = func() string { return oc.name(org) }
				return sess, map[string]any{"omixor/org": org}, nil
			},
			Guard:     RejectIdentityAgent,
			Resources: uiResources}
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}

// orgCache caches Backend.ListOrgs for the lifetime of one HTTP request (batch calls share it).
type orgCache struct {
	b    backend.Backend
	mu   sync.Mutex
	done bool
	l    []model.Org
	err  error
}

func (c *orgCache) list() ([]model.Org, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.done {
		c.l, c.err = c.b.ListOrgs()
		c.done = true
	}
	return c.l, c.err
}

// name returns the organization's name, or its id when unknown.
func (c *orgCache) name(id string) string {
	if l, err := c.list(); err == nil {
		for _, o := range l {
			if o.ID == id {
				return o.Name
			}
		}
	}
	return id
}
