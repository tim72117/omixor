package mcp

import (
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項工具(issue_item_add、issue_items、issue_item_update、issue_item_claim、issue_item_report、issue_item_promote)由核心提供
// (omixor-core/mcp 的 tools_issue_items.go),資料存在核心。這裡是 appBackend 的對應部分:只轉發給核心(內建核心);
// 署名、授權與可見性(繼承所屬 Issue)都由核心依登入身分決定(沒有 agent、author 參數)。畫面還沒有子項的視圖;提升出的 Issue 會在下次重讀 Issue 時出現在畫面。
// 呼叫時沒有拿著 Mu(這幾個工具不鎖 Env)。decisionBackend 見 tools_decision.go。

func (b appBackend) ListIssueItems(org, issue string, q service.ItemListQuery) ([]model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.ListIssueItems(org, issue, q)
}

func (b appBackend) GetIssueItem(org, issue, item string) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.GetIssueItem(org, issue, item)
}

func (b appBackend) AddIssueItem(org, issue string, in service.ItemInput) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.AddIssueItem(org, issue, in)
}

func (b appBackend) UpdateIssueItem(org, issue, item string, in service.ItemPatch) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.UpdateIssueItem(org, issue, item, in)
}

func (b appBackend) DeleteIssueItem(org, issue, item string) error {
	be, err := b.coreFor(org)
	if err != nil {
		return err
	}
	return be.DeleteIssueItem(org, issue, item)
}

func (b appBackend) ClaimIssueItem(org, issue, item string, in service.ItemClaimInput) (*service.ItemClaimResult, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.ClaimIssueItem(org, issue, item, in)
}

func (b appBackend) ReleaseIssueItem(org, issue, item string) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.ReleaseIssueItem(org, issue, item)
}

func (b appBackend) ReportIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.ReportIssueItem(org, issue, item, in)
}

func (b appBackend) BlockIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.BlockIssueItem(org, issue, item, in)
}

func (b appBackend) UnblockIssueItem(org, issue, item string) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.UnblockIssueItem(org, issue, item)
}

func (b appBackend) CancelIssueItem(org, issue, item string) (*model.IssueItem, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.CancelIssueItem(org, issue, item)
}

func (b appBackend) PromoteIssueItem(org, issue, item string, in service.ItemPromoteInput) (*service.ItemPromoteResult, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.PromoteIssueItem(org, issue, item, in)
}
