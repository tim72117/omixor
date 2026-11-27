package core

import (
	"errors"
	"log"

	"omixor-core/backend"
	"omixor-core/model"
)

// 受限專案誰看得到:只有核心的一條規則(omixor-core 的 service/project_access.go:專案成員、組織管理員、伺服器管理員),
// console 不另外定。console 的共用快取是系統身分讀的(什麼都有),所以這裡只問核心「這位呼叫者看得到哪些專案」,
// 其餘的受限專案就是要從快取與衍生資料裡拿掉的(ScrubRestricted、Hidden)。每次請求都重問,移出專案立即生效。

// projectLister 能依身分列出專案的核心(*backend.Local 以它的 Principal 篩)
type projectLister interface {
	ListProjects(org string) ([]model.Project, error)
}

// HiddenProjects 網頁呼叫者 who 在 org 看不到的受限專案 id;unknown = 查不出來(呼叫端要當作所有專案都看不到)。
// who 是 nil(分不出是誰)時受限專案全部算看不到;
// 系統身分本身不需要篩的情況(沒啟用登入、API token、web 管理員)由呼叫端先排除
func (a *App) HiddenProjects(org string, who *Caller) (set map[string]bool, unknown bool) {
	if who == nil {
		set, err := a.RestrictedProjectIDsOf(org)
		if err != nil {
			return nil, true
		}
		return set, false
	}
	return hiddenFrom(org, func() (projectLister, error) { return localCoreFor(who, org) })
}

// HiddenProjectsFor 同 HiddenProjects,viewer 是已經帶著身分的核心(OAuth MCP 入口的授權者)
func (a *App) HiddenProjectsFor(org string, viewer *backend.Local) (set map[string]bool, unknown bool) {
	return hiddenFrom(org, func() (projectLister, error) { return viewer, nil })
}

// hiddenFrom 組織裡的受限專案(系統身分、不用快取)扣掉 viewer 看得到的。沒有受限專案時不必知道呼叫者是誰
func hiddenFrom(org string, viewer func() (projectLister, error)) (map[string]bool, bool) {
	be, err := agentBackend()
	if err != nil {
		log.Printf("受限專案:讀不到核心:%v", err)
		return nil, true
	}
	all, err := be.ListProjects(org)
	if errors.Is(err, backend.ErrUnsupported) { // 沒有專案功能
		return map[string]bool{}, false
	}
	if err != nil {
		log.Printf("受限專案:讀不到專案清單:%v", err)
		return nil, true
	}
	set := map[string]bool{}
	for _, x := range all {
		if x.Restricted {
			set[x.ID] = true
		}
	}
	if len(set) == 0 {
		return set, false
	}
	v, err := viewer()
	if err == nil {
		var seen []model.Project
		if seen, err = v.ListProjects(org); err == nil {
			for _, x := range seen {
				delete(set, x.ID)
			}
			return set, false
		}
	}
	log.Printf("受限專案:查不出呼叫者在 %s 看得到的專案(當作都看不到):%v", org, err)
	return nil, true
}
