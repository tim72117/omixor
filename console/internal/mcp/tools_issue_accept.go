package mcp

import "omixor-core/model"

// issue_accept(接受、release=true 放回)由核心提供(omixor-core/mcp 的 tools_issues.go,資料存在核心)。
// 這裡是 appBackend 的對應部分:App.AcceptIssue 轉給核心(本機內建核心或遠端 omixor server),並更新 Issue 快取、通知畫面。
// 呼叫時拿著 Mu(核心的 issue_accept 工具先鎖 Env)。

func (b appBackend) AcceptIssue(_, id string) (*model.Issue, error) { return b.acceptIssue(id, false) }

func (b appBackend) ReleaseIssue(_, id string) (*model.Issue, error) { return b.acceptIssue(id, true) }

func (b appBackend) acceptIssue(id string, release bool) (*model.Issue, error) {
	i, err := b.a.AcceptIssue(id, release)
	if err != nil {
		return nil, err
	}
	c := copyIssue(*i)
	return &c, nil
}
