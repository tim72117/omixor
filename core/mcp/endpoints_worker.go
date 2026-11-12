package mcp

import (
	"net/http"

	"omixor-core/backend"
	"omixor-core/service"
)

// ================= 執行者端點 =================

// WorkerToolAllow 執行者端點開放的工具(其餘一律「沒有這個工具」)
var WorkerToolAllow = map[string]bool{
	"dispatch_queue":   true,
	"dispatch_claim":   true,
	"dispatch_status":  true,
	"dispatch_release": true,
}

// WorkerEndpointConfig 執行者端點的設定
type WorkerEndpointConfig struct {
	// Resolve 同 AdminEndpointConfig:認證這個請求,回傳 Backend 與組織
	Resolve func(r *http.Request) (backend.Backend, string, error)
	// AgentEndpoint 角色端點的網址(領取任務時放進 credential.endpoint);空字串不附
	AgentEndpoint string
	// AgentEndpointFn 同上,但每次請求才決定(例如 web 依本機/遠端模式);有設就優先
	AgentEndpointFn func() string
	Instructions    string
}

const workerInstructions = "這是 omixor 的執行者(派發)端點,給領取任務的 LLM 用,只有四個工具:" +
	"dispatch_queue 看有哪些任務可領、dispatch_claim 領取(回傳角色設定、憑證與下一步說明)、dispatch_status 看我領取中的任務、dispatch_release 放棄領取。" +
	"標準流程:queue → claim → 用 claim 給的憑證(Authorization: Bearer <agentId>:<secret>)連 credential.endpoint 的角色端點以該角色身分工作 → 定期用 ops_agent_task_report(inProgress)續約 → done 或 blocked 回報。"

// WorkerEndpoint 執行者專用 MCP 的 http.Handler(雲端掛在 /mcp/worker/{org},本機 web 掛在 /mcp/worker):
// 只開放 dispatch_ 四個工具,權限同呼叫者的 omixor 身分(組織成員可領取;放棄限領取者本人或組織管理員)。
func WorkerEndpoint(cfg WorkerEndpointConfig) http.Handler {
	ins := cfg.Instructions
	if ins == "" {
		ins = workerInstructions
	}
	srv := NewWorkerServer(ins)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bk, org, err := cfg.Resolve(r)
		if err != nil {
			if c := service.Code(err); c == 403 || c == 404 || c == 429 {
				http.Error(w, err.Error(), c)
				return
			}
			unauthorized(w, "omixor")
			return
		}
		ep := cfg.AgentEndpoint
		if cfg.AgentEndpointFn != nil {
			ep = cfg.AgentEndpointFn()
		}
		sess := &Session{B: bk, OrgID: org, Endpoint: ep}
		hooks := Hooks{Ctx: sess, Allow: func(name string) bool { return WorkerToolAllow[name] }}
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}
