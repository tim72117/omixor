// actor_import.go 匯入檔裡的「誰」字串 → actor id(Issue、角色任務、文件櫃、事件的匯入共用)
//
// 匯入檔是舊格式(email、user:<email>、agent:<id>、mcp、routine…),但匯入的呼叫者有兩種:
//   - 伺服器管理員(搬遷歷史資料;本機 omixor-local、omixor-web import 的呼叫者也是本機管理員):寬鬆解析器 Store.ResolveLegacy,
//     缺的 actor 與別名會建(歷史作者可能已經離職、不在名單上)。唯一的限制:別的組織的角色 actor id 不能拿來署名(落到 sys_legacy)
//   - 組織管理員(非伺服器管理員):署名只能是「本組織成員、本組織角色、系統 actor、呼叫者本人」(D9:不能代別的使用者署名,
//     也不能讓匯入檔憑空建立使用者)。其他任何字串(別組織的使用者、別組織的 actor id、未知字串)一律落到 sys_legacy_*,
//     顯示原值(不是真人);不會建立「未註冊使用者」actor,也不會寫別名
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// importGhostMaxName 匯入落到 sys_legacy_* 的 actor 顯示名稱(原值)最多保留的字元數
const importGhostMaxName = 200

// importWho 一次匯入用的解析器(帶快取,同一個字串只查一次)
type importWho struct {
	s       *Service
	p       *model.Principal
	org     string
	lenient bool // 伺服器管理員:寬鬆解析器
	cache   map[string]string
}

func (s *Service) newImportWho(p *model.Principal, org string) *importWho {
	return &importWho{s: s, p: p, org: org, lenient: p != nil && p.Agent == nil && p.ServerAdmin(), cache: map[string]string{}}
}

// id 匯入的署名字串 → actor id。空字串用 emptyAs(再空回 "",代表 NULL)
func (w *importWho) id(raw, emptyAs string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = emptyAs
	}
	if raw == "" {
		return "", nil
	}
	if v, ok := w.cache[raw]; ok {
		return v, nil
	}
	var id string
	var err error
	if w.lenient {
		id, err = w.lenientID(raw)
	} else {
		id, err = w.restrictedID(raw)
	}
	if err != nil {
		return "", err
	}
	w.cache[raw] = id
	return id, nil
}

// agentID 匯入的角色 id(純 handle、@handle、agent:<id>、actor id)→ actor id;空字串回 ""
func (w *importWho) agentID(raw string) (string, error) {
	a := strings.TrimSpace(raw)
	if a == "" {
		return "", nil
	}
	low := strings.ToLower(a)
	if !strings.HasPrefix(low, model.AgentPrefix) && !model.ValidActorID(low) {
		a = model.AgentPrefix + strings.TrimPrefix(a, "@")
	}
	return w.id(a, "")
}

// lenientID 伺服器管理員:寬鬆解析器;別的組織的角色 actor id 不能署名
func (w *importWho) lenientID(raw string) (string, error) {
	if low := strings.ToLower(raw); model.ValidActorID(low) {
		rec, err := w.s.Store.ActorByID(low)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
		if err == nil && rec.Kind == model.ActorKindAgent && rec.Org != w.org {
			return w.s.importGhost(raw)
		}
	}
	return w.s.Store.ResolveLegacy(w.org, raw)
}

// restrictedID 組織管理員:只認本組織成員、本組織角色、系統 actor、呼叫者本人;其餘落到 sys_legacy_*
func (w *importWho) restrictedID(raw string) (string, error) {
	low := strings.ToLower(raw)
	switch low { // 內建署名的舊字串
	case model.RoutineAuthor:
		return model.SystemActorRoutine, nil
	case model.ChainAuthor:
		return model.SystemActorChain, nil
	case model.SystemAuthor, "system":
		return model.SystemActorOmixor, nil
	case model.MCPActorID:
		return w.s.importLegacyMCP()
	}
	if model.ValidActorID(low) {
		ok, err := w.actorIDAllowed(low)
		if err != nil {
			return "", err
		}
		if ok {
			return low, nil
		}
		return w.s.importGhost(raw)
	}
	ref, err := w.s.ResolveActor(w.p, w.org, raw)
	if err == nil {
		return ref.ID, nil
	}
	if IsActorInputError(err) {
		return w.s.importGhost(raw)
	}
	return "", err
}

// actorIDAllowed 組織管理員匯入時,一個 actor id 能不能拿來署名:系統 actor 可以;角色要屬於本組織;
// 使用者要有使用者列,而且是呼叫者本人或本組織成員
func (w *importWho) actorIDAllowed(id string) (bool, error) {
	rec, err := w.s.Store.ActorByID(id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch rec.Kind {
	case model.ActorKindSystem:
		return true, nil
	case model.ActorKindAgent:
		return rec.Org == w.org, nil
	case model.ActorKindUser:
		u, err := w.s.Store.UserByActorID(rec.ID)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if w.p != nil && (w.p.Actor.ID == rec.ID || w.p.User.ActorID == rec.ID || (w.p.User.ID != "" && w.p.User.ID == u.ID)) {
			return true, nil
		}
		if _, err := w.s.Store.MemberRole(w.org, u.ID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// importGhost 匯入的署名對不上(或不准用)的字串:sys_legacy_i<雜湊> 系統 actor,名稱 = 原值(不是真人;讀回仍顯示原值)。
// 同一個字串永遠同一個 actor;不建立使用者、不寫別名
func (s *Service) importGhost(raw string) (string, error) {
	sum := sha256.Sum256([]byte(raw))
	h := hex.EncodeToString(sum[:])[:16]
	id := model.ActorIDPrefixSystem + "legacy_i" + h
	name := raw
	if r := []rune(name); len(r) > importGhostMaxName {
		name = string(r[:importGhostMaxName])
	}
	return s.ensureSystemActor(id, name, "legacy_i"+h)
}

// importLegacyMCP 舊的 mcp 署名:sys_legacy_mcp(不對到本機使用者;和遷移建的同一個 actor)
func (s *Service) importLegacyMCP() (string, error) {
	return s.ensureSystemActor("sys_legacy_mcp", "舊 MCP 署名", "legacy_mcp")
}

// ensureSystemActor 取得或建立一個 sys_ actor(已存在就用它)
func (s *Service) ensureSystemActor(id, name, handle string) (string, error) {
	if _, err := s.Store.ActorByID(id); err == nil {
		return id, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	try := name
	for i := 0; i < 2; i++ {
		_, err := s.Store.CreateActor(model.ActorRecord{ID: id, Kind: model.ActorKindSystem, Name: try, Handle: handle})
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, store.ErrDuplicateKey) {
			return "", err
		}
		if _, gerr := s.Store.ActorByID(id); gerr == nil { // 同時有人建了同一個
			return id, nil
		}
		try = name + " (" + handle + ")" // 名稱被別的 actor 佔用
	}
	return "", errors.New("建立匯入署名 actor 失敗:" + id)
}
