package core

import (
	"strings"

	"omixor-core/model"
	"omixor-core/version"
)

// Issue 的接受與放回(核心 service/issue_accept.go;MCP 工具 issue_accept,畫面用 POST /api/tool/issue_accept)。
// 走核心(本機 service 或遠端 POST …/issues/{id}/accept、release),成功後更新這裡的 Issue 快取並通知畫面
// (看板的「進行中」是由快取的 assignee / assigneeActor 算的,見 model.IssueAssigned)。呼叫時要拿著 Mu。

// AcceptIssue 接受 Issue(呼叫者成為負責人);release = true 是放回。id 是 Issue 編號(key 或內部 id)。失敗回核心的錯誤(409、403、404…)
func (a *App) AcceptIssue(id string, release bool) (*model.Issue, error) {
	s := a.issueSt()
	if u := IssueRemoteURL(); u != "" && remoteLacks(u, version.FeatureIssueAccept) { // 舊伺服器(/api/version 沒宣告)還沒有接受功能
		return nil, UserError{Message: "這台伺服器還不支援接受功能,請先更新伺服器版本"}
	}
	b, err := a.issueBackend()
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
	s.gen++
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
