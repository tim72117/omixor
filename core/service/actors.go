// actors.go actor(使用者、角色、系統的共同身分,ISS-36 B-3;規格見雲端 omixor DOC-13 §3.1、§3.2)
//   - Principal 填 Actor 的小工具(使用者取得或補建 actor、角色取 actor)
//   - ResolveActor 嚴格解析器:API、MCP、CLI 輸入的「人或角色」字串 → ActorRef;找不到不建立
//
// 這一批解析器還沒接進 chain、channel、issues(C-2 接),只提供函式與測試。

package service

import (
	"errors"
	"log"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// ResolveActor 與 Authenticate 的錯誤:固定句子(不夾帶使用者輸入、資料庫細節),呼叫端可用 errors.Is 判斷
var (
	ErrActorEmpty     = &Error{Code: 400, Msg: "要指定人或角色"}
	ErrActorNotFound  = &Error{Code: 400, Msg: "找不到這個人或角色"}
	ErrActorAmbiguous = &Error{Code: 400, Msg: "有多個符合(角色與使用者同名),請加上 agent: 或 user: 前綴"}
	// ErrActorUnavailable 使用者的 actor 讀不到也補建不起來(極端的 handle 衝突、資料庫錯誤):細節只進 log
	ErrActorUnavailable = &Error{Code: 500, Msg: "這個帳號的身分資料暫時無法使用,請聯絡管理員"}
	// ErrAuthInternal 角色憑證驗證(AuthenticateAgent、claimedAgent)時內部讀取失敗:細節只進 log
	ErrAuthInternal = &Error{Code: 500, Msg: "驗證時發生錯誤"}
)

// userActorRef 取得使用者的 actor(精簡版)。使用者還沒有 actor_id(滾動部署時舊版寫入的)就補建;
// 讀不到 actor 記錄(理論上不會)也走補建。補建失敗回 ErrActorUnavailable(固定句子,原因寫 log)
func (s *Service) userActorRef(u *model.User) (model.ActorRef, error) {
	var rec *model.ActorRecord
	var err error
	if u.ActorID != "" {
		rec, err = s.Store.ActorByID(u.ActorID)
		if errors.Is(err, store.ErrNotFound) {
			rec, err = nil, nil
		}
	}
	if err == nil && rec == nil {
		rec, err = s.Store.EnsureUserActor(u.ID)
	}
	if err != nil {
		log.Printf("使用者 %s 的 actor 讀取或補建失敗:%v", u.ID, err)
		return model.ActorRef{}, ErrActorUnavailable
	}
	if rec.Kind != model.ActorKindUser {
		log.Printf("使用者 %s 的 actor %s 種類是 %s,不是 user", u.ID, rec.ID, rec.Kind)
		return model.ActorRef{}, ErrActorUnavailable
	}
	return rec.Ref(), nil
}

// agentActorRef 角色的 actor(精簡版);角色沒有 actor(id 不符 handle 規則的舊資料)回零值、不是錯誤;
// 讀取失敗回 ErrAuthInternal(固定句子,原因寫 log)
func (s *Service) agentActorRef(g *model.Agent) (model.ActorRef, error) {
	if g == nil || g.ActorID == "" {
		return model.ActorRef{}, nil
	}
	rec, err := s.Store.ActorByID(g.ActorID)
	if errors.Is(err, store.ErrNotFound) {
		return model.ActorRef{}, nil
	}
	if err != nil {
		log.Printf("角色 %s 的 actor %s 讀取失敗:%v", g.ID, g.ActorID, err)
		return model.ActorRef{}, ErrAuthInternal
	}
	return rec.Ref(), nil
}

// saveUser 存使用者(SaveUser 在同一個交易建立或接回 actor)後重讀,把 actor_id 帶回傳入的結構。
// email 已經是「未註冊使用者」actor 的,store 會接回同一個 actor。
// 資料已經存進去之後重讀失敗:不回錯誤(呼叫端會以為沒存、重試或回 500),改走 EnsureUserActor 補 ActorID;
// 連那條也失敗就留空(寫 log;下一次 Authenticate 會補建),結果仍然是「已存」
func (s *Service) saveUser(u *model.User) error {
	if err := validUserEmail(u.Email); err != nil { // 所有對外入口(evoleap、LDAP、新增使用者、加成員)共用:沒有 @ 或保留的本機身分不能存
		return err
	}
	if err := s.Store.SaveUser(*u); err != nil {
		return err
	}
	cur, err := s.Store.UserByID(u.ID)
	if err == nil {
		u.ActorID = cur.ActorID
		return nil
	}
	log.Printf("使用者 %s 已存,重讀失敗:%v;改用 EnsureUserActor 取 actor", u.ID, err)
	rec, err2 := s.Store.EnsureUserActor(u.ID)
	if err2 != nil {
		log.Printf("使用者 %s 已存,取 actor 也失敗(ActorID 先留空,下次驗證時補建):%v", u.ID, err2)
		return nil
	}
	u.ActorID = rec.ID
	return nil
}

// ownerActorID 建立角色時的 owner:呼叫者是使用者就是它的使用者 actor;沒有(角色憑證、系統流程、手工 Principal)回空字串 = store 的預設
func ownerActorID(p *model.Principal) string {
	if p == nil || p.Agent != nil {
		return ""
	}
	if p.Actor.Kind == model.ActorKindUser && p.Actor.ID != "" {
		return p.Actor.ID
	}
	return p.User.ActorID
}

// ResolveActor 嚴格解析器(DOC-13 §3.2):把輸入的字串解析成現存的 actor,找不到不建立。接受:
//   - actor id(usr_、agt_、sys_ 開頭且格式正確)
//   - agent:<handle>:org 內的角色;user:<handle 或 email>:使用者
//   - @handle、handle、email:含 @(去掉前導 @ 之後)當使用者 email;否則同時找「org 內角色」與「使用者」,
//     兩邊都有 → ErrActorAmbiguous(請加前綴),都沒有 → ErrActorNotFound
//
// 不分大小寫。授權範圍(看不到的一律回和「不存在」完全相同的 ErrActorNotFound,不洩漏存在與否):
//   - 使用者:要有 omixor_users 列(「未註冊使用者」actor 不算,不能被指派成成員或作者),而且
//     呼叫者本人、或 org 的成員(呼叫者也要看得到這個 org:成員、該組織的角色憑證)、或呼叫者是伺服器管理員;
//     org 是空字串時只有本人與伺服器管理員。停用的使用者仍解析得到(它還佔著名字、舊紀錄的署名要能對回去)
//   - 角色:只限 org 內、角色表還有的角色,呼叫者要看得到這個 org(成員、伺服器管理員、該組織的角色憑證);
//     暫停(Active=false)的角色仍解析得到、ActorRef 照舊;退役(角色已刪)的解析不到,
//     同 id 重建後舊 actor id 也解析不到(新的 actor 才算)
//   - 系統 actor:只能用 id(固定清單與遷移建立的 sys_),不需要授權
//   - p 是 nil:沒有呼叫者,只解析得到系統 actor
func (s *Service) ResolveActor(p *model.Principal, org, raw string) (model.ActorRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return model.ActorRef{}, ErrActorEmpty
	}
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(low, "agent:"):
		return s.resolveAgentHandle(p, org, raw[len("agent:"):])
	case strings.HasPrefix(low, "user:"):
		return s.resolveUserHandle(p, org, raw[len("user:"):])
	case model.ValidActorID(low):
		return s.resolveActorID(p, org, low)
	}
	name := strings.TrimSpace(strings.TrimPrefix(raw, "@"))
	if strings.Contains(name, "@") {
		return s.resolveUserHandle(p, org, name)
	}
	ag, aerr := s.resolveAgentHandle(p, org, name)
	if aerr != nil && !errors.Is(aerr, ErrActorNotFound) {
		return model.ActorRef{}, aerr
	}
	us, uerr := s.resolveUserHandle(p, org, name)
	if uerr != nil && !errors.Is(uerr, ErrActorNotFound) {
		return model.ActorRef{}, uerr
	}
	switch {
	case aerr == nil && uerr == nil:
		return model.ActorRef{}, ErrActorAmbiguous
	case aerr == nil:
		return ag, nil
	case uerr == nil:
		return us, nil
	}
	return model.ActorRef{}, ErrActorNotFound
}

// callerSeesOrg 呼叫者看得到這個組織的人與角色:伺服器管理員、組織成員、該組織的角色憑證。p 為 nil 或沒有組織都是 false
func (s *Service) callerSeesOrg(p *model.Principal, org string) (bool, error) {
	if p == nil || org == "" {
		return false, nil
	}
	if p.Agent != nil {
		return p.Agent.Org == org, nil
	}
	if p.ServerAdmin() {
		return true, nil
	}
	_, err := s.Store.MemberRole(org, p.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) resolveActorID(p *model.Principal, org, id string) (model.ActorRef, error) {
	rec, err := s.Store.ActorByID(id)
	if errors.Is(err, store.ErrNotFound) {
		return model.ActorRef{}, ErrActorNotFound
	}
	if err != nil {
		return model.ActorRef{}, err
	}
	switch rec.Kind {
	case model.ActorKindUser:
		return s.visibleUser(p, org, rec)
	case model.ActorKindAgent:
		return s.visibleAgent(p, org, rec)
	}
	return rec.Ref(), nil
}

func (s *Service) resolveUserHandle(p *model.Principal, org, handle string) (model.ActorRef, error) {
	if p == nil { // 不查資料庫:沒有呼叫者就沒有範圍
		return model.ActorRef{}, ErrActorNotFound
	}
	rec, err := s.Store.ActorByHandle(model.ActorKindUser, "", handle)
	if errors.Is(err, store.ErrNotFound) {
		return model.ActorRef{}, ErrActorNotFound
	}
	if err != nil {
		return model.ActorRef{}, err
	}
	return s.visibleUser(p, org, rec)
}

func (s *Service) resolveAgentHandle(p *model.Principal, org, handle string) (model.ActorRef, error) {
	if ok, err := s.callerSeesOrg(p, org); err != nil || !ok {
		return model.ActorRef{}, notFoundOr(err)
	}
	h, err := model.CleanActorHandle(model.ActorKindAgent, handle)
	if err != nil {
		return model.ActorRef{}, ErrActorNotFound
	}
	g, err := s.Store.GetAgent(org, h) // 角色表有 = 現役或暫停;已刪(退役)沒有
	if errors.Is(err, store.ErrNotFound) {
		return model.ActorRef{}, ErrActorNotFound
	}
	if err != nil {
		return model.ActorRef{}, err
	}
	if g.ActorID == "" { // id 不符 handle 規則的舊資料,沒有 actor
		return model.ActorRef{}, ErrActorNotFound
	}
	rec, err := s.Store.ActorByID(g.ActorID)
	if errors.Is(err, store.ErrNotFound) {
		return model.ActorRef{}, ErrActorNotFound
	}
	if err != nil {
		return model.ActorRef{}, err
	}
	return rec.Ref(), nil
}

// visibleAgent 用 actor id 找到的角色 actor:要在 org、呼叫者看得到這個 org、角色表還有這個角色而且指著同一個 actor
func (s *Service) visibleAgent(p *model.Principal, org string, rec *model.ActorRecord) (model.ActorRef, error) {
	if rec.Org != org {
		return model.ActorRef{}, ErrActorNotFound
	}
	if ok, err := s.callerSeesOrg(p, org); err != nil || !ok {
		return model.ActorRef{}, notFoundOr(err)
	}
	g, err := s.Store.GetAgent(org, rec.Handle)
	if errors.Is(err, store.ErrNotFound) || (err == nil && g.ActorID != rec.ID) { // 已退役(角色已刪,或同 id 重建後的舊 actor)
		return model.ActorRef{}, ErrActorNotFound
	}
	if err != nil {
		return model.ActorRef{}, err
	}
	return rec.Ref(), nil
}

// visibleUser 使用者 actor 的授權:要有 omixor_users 列;呼叫者本人、伺服器管理員、或(呼叫者看得到 org 而且對方是 org 成員)
func (s *Service) visibleUser(p *model.Principal, org string, rec *model.ActorRecord) (model.ActorRef, error) {
	if p == nil {
		return model.ActorRef{}, ErrActorNotFound
	}
	u, err := s.Store.UserByActorID(rec.ID) // 未註冊使用者(沒有使用者列)= 找不到
	if errors.Is(err, store.ErrNotFound) {
		return model.ActorRef{}, ErrActorNotFound
	}
	if err != nil {
		return model.ActorRef{}, err
	}
	if p.Agent == nil && (p.Actor.ID == rec.ID || p.User.ActorID == rec.ID || (p.User.ID != "" && p.User.ID == u.ID) || p.ServerAdmin()) {
		return rec.Ref(), nil
	}
	if ok, err := s.callerSeesOrg(p, org); err != nil || !ok {
		return model.ActorRef{}, notFoundOr(err)
	}
	if _, err := s.Store.MemberRole(org, u.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.ActorRef{}, ErrActorNotFound
		}
		return model.ActorRef{}, err
	}
	return rec.Ref(), nil
}

// notFoundOr err 是 nil(只是沒有權限)回 ErrActorNotFound,否則照原錯誤
func notFoundOr(err error) error {
	if err != nil {
		return err
	}
	return ErrActorNotFound
}
