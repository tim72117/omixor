package mcp

import (
	"omixor-core/backend"
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
