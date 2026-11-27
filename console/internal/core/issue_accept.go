package core

import (
	"strings"

	"omixor-core/model"
)

// Issue 的接受與放回(核心 service/issue_accept.go;MCP 工具 issue_accept,畫面用 POST /api/tool/issue_accept)。
// 走核心的 service,成功後更新這裡的 Issue 快取並通知畫面
// (看板的「進行中」是由快取的 assignee / assigneeActor 算的,見 model.IssueAssigned)。呼叫時要拿著 Mu。

// AcceptIssue 接受 Issue(呼叫者成為負責人);release = true 是放回。id 是 Issue 編號(key 或內部 id)。失敗回核心的錯誤(409、403、404…)
func (a *App) AcceptIssue(id string, release bool) (*model.Issue, error) {
	s := a.issueSt()
	b, err := a.issueWriteBackend()
	var i *model.Issue
	if err == nil {
		if release {
			i, err = b.ReleaseIssue(s.Org, id)
		} else {
			i, err = b.AcceptIssue(s.Org, id)
		}
	}
	if err != nil {
		a.issueRemoteFailed(err)
		return nil, err
	}
	saved := normIssue(*i)
	a.issueRemoteOK(s)
	found := false
	for k := range s.Issues {
		if strings.EqualFold(s.Issues[k].ID, saved.ID) {
			s.Issues[k], found = saved, true
			break
		}
	}
	if !found {
		a.issueRemoteLoad(s)
	}
	a.changedIssues()
	if release {
		go a.AgentsChanged() // 放回會取消原本是角色、還沒領取的任務
	}
	return &saved, nil
}
