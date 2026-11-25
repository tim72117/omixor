package mcp

import (
	"net/http"

	"omixor-core/backend"
	"omixor-core/httperr"
	"omixor-core/service"
)

// ================= 執行者端點 =================

// WorkerToolAllow 執行者端點開放的工具(其餘一律「沒有這個工具」)
var WorkerToolAllow = map[string]bool{
	"dispatch_queue":    true,
	"dispatch_claim":    true,
	"dispatch_status":   true,
	"dispatch_release":  true,
	"dispatch_report":   true,
	"dispatch_comment":  true,
	"dispatch_delegate": true,
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
	// Begin、Tool 同 Hooks(宿主統計、處理完重新整理畫面用);nil = 不呼叫
	Begin func() (end func())
	Tool  func(name string) (done func())
}

const workerInstructions = "This is omixor's worker (dispatch) endpoint, for LLMs that claim tasks: " +
	"dispatch_queue shows which tasks can be claimed, dispatch_claim claims one (returning the agent instructions and next steps), dispatch_status shows the tasks I am working on, " +
	"dispatch_report reports as the agent (inProgress renews the lease, blocked asks a question, done closes the task), dispatch_comment comments on an issue as the agent, dispatch_delegate delegates to a downstream agent along the organization chain, and dispatch_release abandons a claim. " +
	"Standard flow: queue → claim → work following the agent instructions and task description → periodically dispatch_report (inProgress) to renew the lease → dispatch_comment on the related issue to explain the result → dispatch_report done or blocked. " +
	"All these tools act as your signed-in omixor identity; the server confirms you are the claimer and signs as the agent, so you do not need to, and must not, use the secret of the agent credential." + protocolInstructions

// WorkerEndpoint 執行者專用 MCP 的 http.Handler(雲端掛在 /mcp/worker/{org},本機 web 掛在 /mcp/worker):
// 只開放 dispatch_ 工具,權限同呼叫者的 omixor 身分(組織成員可領取;回報、留言限領取者本人;放棄限領取者本人或組織管理員)。
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
				http.Error(w, httperr.Report("mcp 端點解析身分", err).Msg, c)
				return
			}
			unauthorized(w, "omixor")
			return
		}
		ep := cfg.AgentEndpoint
		if cfg.AgentEndpointFn != nil {
			ep = cfg.AgentEndpointFn()
		}
		sess := &Session{B: bk, OrgID: org, Endpoint: ep, Diag: newConnDiag("worker", nil, nil, r)}
		hooks := Hooks{Ctx: sess, Begin: cfg.Begin, Tool: cfg.Tool, Allow: func(name string) bool { return WorkerToolAllow[name] }}
		serve(w, r, func(body []byte) []byte { return srv.Handle(body, hooks) })
	})
}
