package service

import (
	"sync"

	"omixor-core/model"
)

// 給選用模組(core/module、core/modules/…)用的公開工具:模組是另一個套件,用這些檢查權限、回錯誤、署名,
// 規則和核心自己的功能一致。

// RequireViewer 組織成員(角色憑證也可以,只能讀自己組織的);不是成員 403、組織不存在 404
func (s *Service) RequireViewer(p *model.Principal, org string) error { return s.viewer(p, org) }

// RequireOrgAdmin 組織管理員(伺服器管理員視同管理員);角色憑證不行
func (s *Service) RequireOrgAdmin(p *model.Principal, org string) error {
	return s.needOrgAdmin(p, org)
}

// NowTime 現在時間(到秒;測試可以換 Service 的時鐘)
func (s *Service) NowTime() model.Time { return s.mt() }

// Fail 帶 HTTP 狀態碼的錯誤(*Error)
func Fail(code int, format string, a ...any) error { return fail(code, format, a...) }

// WhoName 署名:呼叫者的 actor id(存進 updatedBy;輸出時用 WhoLegacy 轉成 email 或名稱)
func WhoName(p *model.Principal) string { return whoName(p) }

// WhoActor 署名用的 actor id(Principal.Actor;沒填由使用者列、角色補;補不到回空)。模組存 updatedBy 用,讀回輸出用 WhoLegacy 轉成 email 或名稱
func (s *Service) WhoActor(p *model.Principal) string { return s.whoActor(p) }

// ModuleLock 模組自己的「讀了再改」鎖(一個 Service 實例、一個模組 id 一把;只保護同一個程式、同一個實例,同 locks.go)。
// 是最內層的鎖:拿著時不要再呼叫會拿 Service 其他鎖的方法
func (s *Service) ModuleLock(id string) *sync.Mutex {
	s.moduleMu.Lock()
	defer s.moduleMu.Unlock()
	if s.moduleLocks == nil {
		s.moduleLocks = map[string]*sync.Mutex{}
	}
	m := s.moduleLocks[id]
	if m == nil {
		m = &sync.Mutex{}
		s.moduleLocks[id] = m
	}
	return m
}
