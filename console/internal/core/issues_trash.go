package core

import (
	"strings"

	"omixor-core/backend"
	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 垃圾桶(只有組織管理員;軟刪除是 issue_delete,任何成員都可以):
//
//	POST /api/action/issue_trash    {}     → Issue[](已刪除的,新的在前;含 key、title、deletedAt、deletedBy)
//	POST /api/action/issue_restore  {id}   → Issue(拿回來;被取消的任務不會自動重排)
//	POST /api/action/issue_purge    {id}   → null(永久刪除,不能復原;垃圾桶外的也可以)
//
// 權限在核心(service):不是組織管理員回 403。web 端另外把這三個列進 adminActions(web 共用同一個核心身分,
// 擋不住 web 的一般使用者);核心回 403 時 action 回 StatusError 403(HTTP 403,body 有 error)。
// 還原與永久刪除後本機清單重讀並通知 "issues"。

// trashErr 核心的錯誤 → 畫面用的錯誤:403(沒權限)、404(沒有這張 Issue)、409 保留狀態碼(StatusError),其餘同 agentUserErr(訊息經 httperr)
func trashErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(UserError); ok {
		return err
	}
	in := httperr.Report("issue_trash", err)
	if in.Status == 403 || in.Status == 404 || in.Status == 409 {
		return StatusError{Status: in.Status, Message: in.Msg, Data: in.Data}
	}
	return UserError{in.Msg}
}

// issueTrashTarget 目前組織與核心(拿 Mu 讀,網路請求在鎖外)
func (a *App) issueTrashTarget() (string, backend.Backend, error) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	b, err := a.issueBackend()
	return a.issueSt().Org, b, err
}

// findIssueAnywhere 依編號(內部 id、目前或舊的 key)找 Issue:先問核心(舊 key 走別名表),
// 找不到再看垃圾桶(只有組織管理員看得到垃圾桶;不是管理員就當作找不到)。都沒有回 404
func findIssueAnywhere(b backend.Backend, org, ref string) (*model.Issue, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, &service.Error{Code: 404, Msg: "要給 Issue 編號"}
	}
	i, err := b.GetIssue(org, ref)
	if err == nil && i != nil {
		return i, nil
	}
	if err != nil && httperr.Classify(err).Status != 404 {
		return nil, err
	}
	if trash, terr := b.QueryIssues(org, model.IssueQuery{Deleted: true}); terr == nil {
		for k := range trash {
			if issueIs(trash[k], ref) {
				return &trash[k], nil
			}
		}
	}
	return nil, &service.Error{Code: 404, Msg: "找不到 Issue " + ref}
}

// IssueRef issue_resolve 的回應:編號(內部 id、目前或舊的 key 都能查)對到的 Issue
type IssueRef struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"` // 在垃圾桶裡
}

func init() {
	// issue_resolve {ref} → {id, key, deleted}:把舊的 key、垃圾桶裡的 Issue 的編號解析成目前的(畫面清單只有未刪除、目前 key 的)。
	// 找不到 404。任何成員都可以(不洩漏內容,只回編號)
	RegisterAction("issue_resolve", func(a *App, args map[string]any) (any, error) {
		ref, _ := args["ref"].(string)
		org, b, err := a.issueTrashTarget()
		if err != nil {
			return nil, trashErr(err)
		}
		i, err := findIssueAnywhere(b, org, ref)
		if err != nil {
			return nil, trashErr(err)
		}
		return IssueRef{ID: i.ID, Key: i.Display(), Deleted: i.DeletedAt != nil}, nil
	})
	RegisterAction("issue_trash", func(a *App, args map[string]any) (any, error) {
		org, b, err := a.issueTrashTarget()
		if err != nil {
			return nil, trashErr(err)
		}
		list, err := b.QueryIssues(org, model.IssueQuery{Deleted: true})
		if err != nil {
			return nil, trashErr(err)
		}
		return normIssues(list), nil
	})
	RegisterAction("issue_restore", func(a *App, args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		org, b, err := a.issueTrashTarget()
		if err != nil {
			return nil, trashErr(err)
		}
		i, err := b.RestoreIssue(org, id)
		if err != nil {
			return nil, trashErr(err)
		}
		out := normIssue(*i)
		a.Mu.Lock()
		a.issueRemoteLoad(a.issueSt())
		a.logIssue(UserActor(a, args), "arrow.uturn.backward.circle", "從垃圾桶還原 "+out.Display()+"「"+out.Title+"」")
		a.changedIssues()
		a.Mu.Unlock()
		return out, nil
	})
	RegisterAction("issue_purge", func(a *App, args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		org, b, err := a.issueTrashTarget()
		if err != nil {
			return nil, trashErr(err)
		}
		label := id // 動態牆顯示編號(key),不顯示內部 UUID
		if i, err := findIssueAnywhere(b, org, id); err == nil {
			label = i.Display() + "「" + i.Title + "」"
		}
		if err := b.PurgeIssue(org, id); err != nil {
			return nil, trashErr(err)
		}
		a.Mu.Lock()
		a.issueRemoteLoad(a.issueSt())
		a.logIssue(UserActor(a, args), "trash", "永久刪除 "+label)
		a.changedIssues()
		a.Mu.Unlock()
		return nil, nil
	})
}
