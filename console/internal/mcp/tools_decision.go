package mcp

import (
	"omixor-core/backend"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 待決定 / 需要協助工具(issue_decision_add、issue_decisions、issue_decision_answer、issue_decision_to_issue)由核心提供
// (omixor-core/mcp 的 tools_decision.go),資料存在核心。這裡是 appBackend 的對應部分:只轉發給核心(本機內建核心或遠端 omixor server);
// 署名與可見性都由核心依登入身分決定(這裡沒有任何 agent、author 參數)。畫面還沒有待決定的視圖;由決定開出的 Issue 會在下次重讀 Issue 時出現在畫面。
// 呼叫時沒有拿著 Mu(這幾個工具不鎖 Env)。

func decisionBackend() (backend.Backend, error) { return core.AgentBackend() }

func (b appBackend) ListDecisions(org string, q service.DecisionListQuery) ([]model.Decision, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.ListDecisions(org, q)
}

func (b appBackend) GetDecision(org, id string) (*model.Decision, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.GetDecision(org, id)
}

func (b appBackend) AddDecision(org string, in service.DecisionInput) (*model.Decision, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.AddDecision(org, in)
}

func (b appBackend) AnswerDecision(org, id string, in service.DecisionAnswer) (*model.Decision, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.AnswerDecision(org, id, in)
}

func (b appBackend) WithdrawDecision(org, id string) (*model.Decision, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.WithdrawDecision(org, id)
}

func (b appBackend) ReopenDecision(org, id string) (*model.Decision, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.ReopenDecision(org, id)
}

func (b appBackend) DecisionToIssue(org, id string, in service.DecisionToIssueInput) (*service.DecisionIssueResult, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.DecisionToIssue(org, id, in)
}

func (b appBackend) DecisionSummaries(org string) (map[string]model.DecisionSummary, error) {
	be, err := decisionBackend()
	if err != nil {
		return nil, err
	}
	return be.DecisionSummaries(org)
}

// 撤回、重新開啟:console 自己的工具(規格在 spec/tools_console.json,核心的 MCP 端點沒有),轉發給核心的
// POST …/decisions/{id}/withdraw、reopen。權限由核心判斷(撤回:建立者或組織管理員、只有 open;重新開啟:組織管理員或原處理人、只有 decided;
// 不符回 403 / 409,看不到的回找不到)。畫面用 account view 的 features 裡有沒有這兩個名稱決定按鈕能不能按,名稱見 DecisionFeatures。
func init() { core.ConsoleFeatures = DecisionFeatures }

func init() {
	Register("issue_decision_withdraw", func(c *Call) (any, error) {
		return decisionTransition(c, func(b appBackend, org, id string) (*model.Decision, error) { return b.WithdrawDecision(org, id) })
	})
	Register("issue_decision_reopen", func(c *Call) (any, error) {
		return decisionTransition(c, func(b appBackend, org, id string) (*model.Decision, error) { return b.ReopenDecision(org, id) })
	})
}

// DecisionFeatures 這個 console 登記了的決定工具;account view 把它們併進 features(工具有登記才宣告,畫面才會開放對應按鈕)
func DecisionFeatures() []string {
	out := []string{}
	for _, n := range []string{"issue_decision_withdraw", "issue_decision_reopen"} {
		if HasTool(n) {
			out = append(out, n)
		}
	}
	return out
}

func decisionTransition(c *Call, do func(appBackend, string, string) (*model.Decision, error)) (any, error) {
	id, err := c.Args.Require("id")
	if err != nil {
		return nil, err
	}
	x := &callCtx{App: c.App, Actor: c.Actor}
	d, err := do(appBackend{a: c.App}, x.Org(), id)
	if err != nil {
		if backend.NotFound(err) {
			return nil, Errorf("%s not found", id)
		}
		return nil, ToolErr(err)
	}
	return coremcp.DecisionJSON(x, *d), nil
}
