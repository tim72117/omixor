// actor_input.go 寫入端用:把 API / MCP / CLI 輸入的「誰」解析成 actor id 的共用輔助(ISS-36 C-2 共用地基;規格見雲端 DOC-13 §3.2、D9,
// 契約見 store/ACTOR_CONTRACT.md 第 6 節)
//
// 各工作包把輸入欄位(author、as、assignee、commander、reportTo、頻道成員、組織鏈節點、範本建立者…)接進來時,都走這裡,不要自己拼字串:
//
//	SelfActor(p)                       呼叫者本人的 actor(使用者登入 = 使用者 actor;角色憑證 = 角色 actor)
//	ResolveWho(p, org, raw, want)      輸入字串 → 現存的 actor(嚴格:找不到回 400,不建立);want 限制種類(使用者、角色、系統的組合)
//	WhoOrSelf(p, org, raw, want)       同上,raw 是空字串就是呼叫者本人(要種類相符)
//	ResolveAuthor(p, org, given, mode) 署名:「呼叫者本人預設」+「管理員代署名只能代角色與系統(D9)」+「角色憑證只能是自己」
//
// 解析器本身是 ResolveActor(actors.go),接受 actor id、@handle、handle、email、agent:<handle>、user:<handle|email>(舊寫法也行,
// 所以「讀出再寫回」的舊字串仍可運作)。回傳的 ActorRef.ID 就是要寫進欄位的值。

package service

import (
	"errors"

	"omixor-core/model"
)

// ActorWant 允許的 actor 種類(位元組合)
type ActorWant uint8

const (
	WantUser ActorWant = 1 << iota
	WantAgent
	WantSystem
	// WantPeople 人與角色(頻道成員、組織鏈節點、指揮官…)
	WantPeople = WantUser | WantAgent
	// WantAny 三種都行
	WantAny = WantUser | WantAgent | WantSystem
)

func (w ActorWant) has(k model.ActorKind) bool {
	switch k {
	case model.ActorKindUser:
		return w&WantUser != 0
	case model.ActorKindAgent:
		return w&WantAgent != 0
	case model.ActorKindSystem:
		return w&WantSystem != 0
	}
	return false
}

// ErrActorKind 解析到的 actor 種類不在允許的範圍(例如 assignee 只能是角色)
var ErrActorKind = &Error{Code: 400, Msg: "這個欄位不接受這種種類的對象(人、角色或系統)"}

// ErrActorImpersonate 想用別人(使用者)的名義署名(D9:管理員只能代角色與系統;一般成員、角色憑證只能是自己)
var ErrActorImpersonate = &Error{Code: 403, Msg: "只能用自己的名義署名;伺服器管理員可以代角色或系統署名,不能代別的使用者"}

// SelfActor 呼叫者本人的 actor。Principal.Actor 有值就用它(Authenticate、AuthenticateAgent 已填);沒有(手工組的 Principal、舊路徑)時由使用者列或角色補。
// 都補不出來(角色沒有 actor、使用者列不存在)回 ErrActorUnavailable / ErrActorEmpty,不回零值,免得「誰」被寫成 NULL
func (s *Service) SelfActor(p *model.Principal) (model.ActorRef, error) {
	if p == nil {
		return model.ActorRef{}, ErrActorEmpty
	}
	if p.Actor.ID != "" {
		return p.Actor, nil
	}
	if p.Agent != nil {
		ref, err := s.agentActorFromIdentity(p.Agent)
		if err != nil {
			return model.ActorRef{}, err
		}
		if ref.ID == "" {
			return model.ActorRef{}, ErrActorUnavailable
		}
		return ref, nil
	}
	if p.User.ID == "" && p.User.ActorID == "" {
		return model.ActorRef{}, ErrActorEmpty
	}
	return s.userActorRef(&p.User)
}

// agentActorFromIdentity 角色憑證的 Principal.Agent(AgentIdentity)→ actor:org 內的角色 handle = 角色 id
func (s *Service) agentActorFromIdentity(a *model.AgentIdentity) (model.ActorRef, error) {
	g, err := s.Store.GetAgent(a.Org, a.AgentID)
	if err != nil {
		return model.ActorRef{}, ErrActorUnavailable
	}
	return s.agentActorRef(g)
}

// ResolveWho 把輸入的字串解析成現存的 actor(嚴格解析器 ResolveActor 加種類檢查)。raw 空 → ErrActorEmpty;種類不符 → ErrActorKind
func (s *Service) ResolveWho(p *model.Principal, org, raw string, want ActorWant) (model.ActorRef, error) {
	ref, err := s.ResolveActor(p, org, raw)
	if err != nil {
		return model.ActorRef{}, err
	}
	if !want.has(ref.Kind) {
		return model.ActorRef{}, ErrActorKind
	}
	return ref, nil
}

// WhoOrSelf 同 ResolveWho,raw 是空字串(或只有空白)就是呼叫者本人(本人的種類也要符合 want)
func (s *Service) WhoOrSelf(p *model.Principal, org, raw string, want ActorWant) (model.ActorRef, error) {
	if isBlank(raw) {
		ref, err := s.SelfActor(p)
		if err != nil {
			return model.ActorRef{}, err
		}
		if !want.has(ref.Kind) {
			return model.ActorRef{}, ErrActorKind
		}
		return ref, nil
	}
	return s.ResolveWho(p, org, raw, want)
}

// AuthorMode ResolveAuthor 遇到「不該用別人名義署名」時的處理
type AuthorMode int

const (
	// AuthorIgnore 一般成員與角色憑證給的 author / as 靜默忽略,一律是本人(舊行為;現有端點與測試的語意)
	AuthorIgnore AuthorMode = iota
	// AuthorReject 給了不是自己的對象就回 ErrActorImpersonate(403)
	AuthorReject
)

// ResolveAuthor 署名(取代 issues.go 的 author()):
//   - given 空 → 呼叫者本人
//   - 角色憑證(p.Agent != nil)與一般成員 → 只能是自己:given 解析成自己就照給;其餘依 mode 忽略或 403
//   - 伺服器管理員 → 只能代角色與系統(D9),給自己照給。解析不到、或解析到別的使用者:
//     AuthorReject 模式回 400(找不到)/ErrActorImpersonate(403,明確的 API 參數);
//     AuthorIgnore 模式(舊式、相容)靜默當本人,與一般成員相同(D9 的相容修正:舊用戶端會把 app.me 之類的人員字串當 author 傳,
//     不能因此對管理員壞掉;只有解析到角色或系統才採用)。內部錯誤(資料庫)一律照原樣回
func (s *Service) ResolveAuthor(p *model.Principal, org, given string, mode AuthorMode) (model.ActorRef, error) {
	self, err := s.SelfActor(p)
	if err != nil {
		return model.ActorRef{}, err
	}
	if isBlank(given) {
		return self, nil
	}
	if p.Agent != nil || !p.ServerAdmin() {
		ref, rerr := s.ResolveActor(p, org, given)
		if rerr == nil && ref.ID == self.ID {
			return self, nil
		}
		if mode == AuthorReject {
			return model.ActorRef{}, ErrActorImpersonate
		}
		return self, nil
	}
	ref, err := s.ResolveActor(p, org, given)
	if err != nil {
		if mode == AuthorIgnore && IsActorInputError(err) {
			return self, nil
		}
		return model.ActorRef{}, err
	}
	if ref.ID != self.ID && ref.Kind == model.ActorKindUser {
		if mode == AuthorIgnore {
			return self, nil
		}
		return model.ActorRef{}, ErrActorImpersonate
	}
	return ref, nil
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// IsActorInputError 輸入端的 actor 解析錯誤(找不到、同名、種類不符、不能代署名、空值):API 層可以原樣當 4xx 回
func IsActorInputError(err error) bool {
	for _, e := range []*Error{ErrActorEmpty, ErrActorNotFound, ErrActorAmbiguous, ErrActorKind, ErrActorImpersonate} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
