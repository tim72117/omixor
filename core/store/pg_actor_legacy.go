// pg_actor_legacy.go 舊「誰」字串 → actor 的寬鬆解析器(ISS-36 階段 C-1;規格見雲端 omixor DOC-13 §1.5、§1.6),以及 omixor_actor_aliases。
//
// 資料裡「誰」原本是自由字串(email、user:<email>、agent:<id>、mcp、routine、本機身分名稱、冒名的任意字串…)。
// ResolveLegacy 依 DOC-13 §1.5 的表格把一個舊值對到 actor;匯入(service 的 actor_import)、本機模式共用它。
// 寬鬆:對不上的舊值不報錯,一律落到 sys_legacy_<sha256 前 8 碼>(名稱 = 原值),同一個舊值永遠同一個 actor(冪等),並寫 omixor_actor_aliases 與 log。
// 嚴格解析器(一般 API 輸入,找不到回 400)不在這裡(service 層的事)。
//
// 純邏輯與資料來源分開:ResolveLegacy 只問 LegacyLookup 介面(查表),不寫資料庫,所以可以用假的查表單元測試;
// 結果 LegacyResult 描述「對到哪個 actor」或「要建哪個 actor」,由 PG.applyLegacy 寫進資料庫。
//
// 與 DOC-13 表格的差異(都是在表格沒寫到的地方補一個合理的做法):
//   - 不含 @ 的純名稱(例如 tim、bob):表格只列「本機別名」與「其他」。這裡多一步:若有同 handle 的使用者(omixor-local 的本機使用者 handle 是 tim)就對到那位使用者;
//     沒有才走「其他」→ sys_legacy。
//   - user:<x> 的 x 不含 @:本機別名 → 本機使用者(沒設本機使用者 → sys_legacy);找得到 omixor_users.id = x 的使用者 → 它;找得到 handle = x 的使用者 → 它;都沒有 → 建「未註冊使用者」actor(同含 @ 的 email)。
//   - 內含 < > , ; " 的值(例如組織鏈測試用的串接字串 user:a@x>agent:cto>目標)不是合法的 email / 名稱,走「其他」。

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// omixor_actor_aliases(pgSchema):舊值(規範化節點,例如 local:冒名)→ actor。只存 sys_legacy_* 與本機別名(不存使用者、角色:那些每次查表就對得到),
// 讓重跑、匯入穩定,也讓人查得到「這個 sys_legacy 是哪個舊值」。

func warnf(format string, args ...any) { log.Printf("[警告] "+format, args...) }

// ---- 本機身分設定 ----

// builtinLocalAliases 不論有沒有設定,都算「本機身分別名」的舊署名(console 本機模式的 omixor-console、更早的 ops-web)
var builtinLocalAliases = []string{"omixor-console", "ops-web"}

var legacyLocalCfg struct {
	mu      sync.RWMutex
	handle  string
	aliases map[string]bool
}

// SetMigrationLocalUser 在建 store(NewPG)之前告訴遷移「本機使用者」:handle 是本機使用者的 handle(omixor-local 的 OMIXOR_LOCAL_USER、console 的本機身分,例如 tim),
// aliases 是額外的本機別名(內建的 omixor-console、ops-web 一定算)。設了之後,舊資料裡的 mcp 與這些別名都對到這位使用者;
// 沒設(雲端)時別名一律對到 sys_legacy_*、mcp 對到 sys_legacy_mcp。handle 空字串 = 取消設定
func SetMigrationLocalUser(handle string, aliases ...string) {
	legacyLocalCfg.mu.Lock()
	defer legacyLocalCfg.mu.Unlock()
	h, err := model.CleanUserHandle(handle)
	if err != nil {
		legacyLocalCfg.handle, legacyLocalCfg.aliases = "", nil
		return
	}
	legacyLocalCfg.handle = h
	legacyLocalCfg.aliases = map[string]bool{}
	for _, a := range aliases {
		if a = strings.TrimSpace(a); a != "" {
			legacyLocalCfg.aliases[a] = true
		}
	}
}

// LegacyLocal 本機使用者設定(解析器用):Handle 空 = 沒有本機使用者
type LegacyLocal struct {
	Handle  string
	Aliases map[string]bool // 額外的別名(不含內建的)
}

func currentLegacyLocal() LegacyLocal {
	legacyLocalCfg.mu.RLock()
	defer legacyLocalCfg.mu.RUnlock()
	out := LegacyLocal{Handle: legacyLocalCfg.handle, Aliases: map[string]bool{}}
	for k := range legacyLocalCfg.aliases {
		out.Aliases[k] = true
	}
	return out
}

func (l LegacyLocal) isAlias(x string) bool {
	for _, b := range builtinLocalAliases {
		if x == b {
			return true
		}
	}
	return l.Aliases[x] || (l.Handle != "" && strings.EqualFold(x, l.Handle))
}

// ---- 解析器 ----

// LegacyLookup 解析器的資料來源(只查不寫)
type LegacyLookup interface {
	// ActorExists actor id 是否存在(欄位已經是 actor id 的列,對到自己)
	ActorExists(id string) (bool, error)
	// UserActor 以 handle / email(小寫)找使用者 actor id:先找 omixor_users 的 email,再找 omixor_actors(未註冊使用者);沒有回 ""
	UserActor(handle string) (string, error)
	// UserActorByID 以 omixor_users.id(u_…)找使用者 actor id;沒有回 ""
	UserActorByID(userID string) (string, error)
	// AgentActor 組織 org 裡角色 id 的 actor:current = 現有角色(含暫停的)的 actor,retired = 已刪角色最新的退役 actor;都沒有回 ""
	AgentActor(org, id string) (current, retired string, err error)
	// AliasActor omixor_actor_aliases 裡這個規範化節點對到的 actor id;沒有回 ""
	AliasActor(alias string) (string, error)
	// Local 本機使用者設定
	Local() LegacyLocal
}

// LegacyOpts 一個欄位的解析設定
type LegacyOpts struct {
	Form    IdentityForm // IdentityAgentID:純角色 id;IdentityUserID:omixor_users.id 或 agent:<id>;其餘一般
	NotNull bool         // 欄位轉換後仍是 NOT NULL:空字串對到 sys_legacy_unknown,否則對到 NULL
}

// LegacyResult 一個舊值的對照結果
type LegacyResult struct {
	// ActorID 已經存在的 actor id("" 且 Null = 對到 NULL;"" 且 Create != nil = 要先建)
	ActorID string
	// Null 空值 → NULL(欄位可為 NULL)
	Null bool
	// Create 要先建立的 actor:系統 actor(sys_legacy_*,ID 固定)或未註冊使用者 / 本機使用者(ID 空,建立時產生 usr_<uuidv7>)
	Create *model.ActorRecord
	// Alias 要記進 omixor_actor_aliases 的規範化節點("" = 不記;只記 sys_legacy 與本機別名)
	Alias string
	// Why 要寫 log 的原因("" = 一般對照,不用寫)
	Why string
}

// legacyHash 規範化節點的 SHA-256 前 8 個十六進位字元
func legacyHash(node string) string { return legacyHashN(node, 8) }

// legacyHashN 規範化節點的 SHA-256 前 n 個十六進位字元(n ≤ 64;碰撞時用較長的)
func legacyHashN(node string, n int) string {
	h := sha256.Sum256([]byte(node))
	return hex.EncodeToString(h[:])[:n]
}

// legacyAliasMaxLen alias 主鍵的原值長度上限(位元組):btree 索引列約 2.7 KB 上限,超過會寫入失敗、整欄永遠轉不完。
// 超過的節點以 sha256 全雜湊存(aliasKey)
const legacyAliasMaxLen = 512

// aliasKey omixor_actor_aliases.alias 實際存的鍵:短的原樣;過長的存 "sha256:" + 全雜湊(穩定、冪等;節點一律以 user:/agent:/local:/org/ 開頭,不會與原樣的鍵撞)
func aliasKey(node string) string {
	if len(node) <= legacyAliasMaxLen {
		return node
	}
	h := sha256.Sum256([]byte(node))
	return "sha256:" + hex.EncodeToString(h[:])
}

// clipLegacy log 與錯誤訊息裡的舊值:太長只留前 80 個字元,附總長度(原值可能是任意大的垃圾)
func clipLegacy(s string) string {
	const max = 80
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return fmt.Sprintf("%s…(共 %d 字元)", string(r[:max]), len(r))
}

// normalizeLegacyPrefix 前綴(user:、agent:、local:)大小寫不分:Agent:dev、USER:a@x 先正規化成小寫前綴,其餘不動
func normalizeLegacyPrefix(t string) string {
	for _, pre := range []string{model.AgentPrefix, model.UserPrefix, model.LocalPrefix} {
		if len(t) >= len(pre) && strings.EqualFold(t[:len(pre)], pre) {
			return pre + t[len(pre):]
		}
	}
	return t
}

// legacyAgentNode 對不上角色的 agent:<id> 的 sys_legacy 節點:帶組織(不同組織同名的已刪角色是不同的人,不能併成同一個 actor)
func legacyAgentNode(org, node string) string { return "org/" + org + "/" + node }

// LegacyAgentActorID 組織 org 裡對不上角色的 agent:<id> 對到的 sys_legacy actor id(沒有碰撞時)
func LegacyAgentActorID(org, node string) string { return LegacyActorID(legacyAgentNode(org, node)) }

// LegacyActorID 一個規範化節點對到的 sys_legacy actor id(sys_legacy_<雜湊>)
func LegacyActorID(node string) string {
	return model.ActorIDPrefixSystem + "legacy_" + legacyHash(node)
}

func legacySystem(id, name, node, why string) LegacyResult {
	return LegacyResult{
		Create: &model.ActorRecord{ID: id, Kind: model.ActorKindSystem, Name: name, Handle: strings.TrimPrefix(id, model.ActorIDPrefixSystem)},
		Alias:  node, Why: why,
	}
}

// sysLegacyFor 對不上的舊值:sys_legacy_<雜湊>,名稱 = 原值
func sysLegacyFor(node, raw, why string) LegacyResult {
	return legacySystem(LegacyActorID(node), raw, node, why)
}

func existing(id string) LegacyResult { return LegacyResult{ActorID: id} }

// legacyUserHandleOK 這個字串能不能當使用者 handle(email 或本機人員名稱):規範化得了,而且不含 < > , ; "(組織鏈測試用的串接字串、含引號的怪值不是人)
func legacyUserHandleOK(x string) bool {
	if _, err := model.CleanUserHandle(x); err != nil {
		return false
	}
	return !strings.ContainsAny(x, `<>,;"`)
}

// ResolveLegacy 寬鬆解析器:把欄位裡的舊值 raw(org 是該列所屬組織,無組織的列給 "")對到 actor。規則依 DOC-13 §1.5,依序:
//
//  1. 空字串 → NULL(opt.NotNull 時 → sys_legacy_unknown)
//  2. 本來就是存在的 actor id → 它自己(欄位部分已轉換的資料庫再跑)
//  3. agent:<id>(或 IdentityAgentID 欄位的純角色 id)→ org 的角色 actor;角色已刪 → 同 handle 最新的退役 actor;都沒有 → sys_legacy
//     (角色依組織解析,不查別名表)
//  4. omixor_actor_aliases 已經有這個規範化節點 → 它(重跑、匯入穩定)
//  5. user:<x> 或含 @ → 找使用者(x 不含 @ 時:先找 omixor_users.id = x,再找 handle = x);沒有 → 未註冊使用者 actor(x 不是合法 handle → sys_legacy)
//  6. 內建署名:routine → sys_routine、chain → sys_chain、omixor / system → sys_omixor
//  7. mcp、本機別名(omixor-console、ops-web、設定的別名、local:<x>)→ 本機使用者;沒設本機使用者 → mcp 是 sys_legacy_mcp、別名是 sys_legacy_<雜湊>
//  8. 其他 → sys_legacy_<sha256(規範化節點) 前 8 碼>,名稱 = 原值,記 log
//
// 只讀不寫:要建的 actor 與要記的別名在回傳的結果裡,由呼叫端(PG.applyLegacy)寫入
func ResolveLegacy(lk LegacyLookup, org, raw string, opt LegacyOpts) (LegacyResult, error) {
	t := normalizeLegacyPrefix(strings.TrimSpace(raw))
	if t == "" {
		return emptyLegacy(opt), nil
	}
	if model.ValidActorID(t) {
		ok, err := lk.ActorExists(t)
		if err != nil {
			return LegacyResult{}, err
		}
		if ok {
			return existing(t), nil
		}
	}
	if opt.Form == IdentityUserID && strings.HasPrefix(t, "u_") {
		id, err := lk.UserActorByID(t)
		if err != nil {
			return LegacyResult{}, err
		}
		if id != "" {
			return existing(id), nil
		}
		return sysLegacyFor(model.LocalPrefix+t, t, "找不到這位使用者(omixor_users.id)"), nil
	}
	var node string
	if opt.Form == IdentityAgentID {
		node = model.AssigneeNode(t)
	} else {
		node = model.PrincipalNode(t)
	}
	kind := model.PrincipalKind(node)
	if kind == model.PrincipalKindAgent { // 角色依組織解析(同一個 agent:<id> 在不同組織是不同的 actor),不查別名(別名沒有組織)
		return resolveLegacyAgent(lk, org, node, t)
	}
	if alias, err := lk.AliasActor(node); err != nil {
		return LegacyResult{}, err
	} else if alias != "" {
		return existing(alias), nil
	}
	switch kind {
	case model.PrincipalKindUser:
		return resolveLegacyUser(lk, node, t)
	}
	return resolveLegacyLocal(lk, node, t)
}

func emptyLegacy(opt LegacyOpts) LegacyResult {
	if opt.NotNull {
		return legacySystem("sys_legacy_unknown", "舊資料(空值)", "", "")
	}
	return LegacyResult{Null: true}
}

func resolveLegacyAgent(lk LegacyLookup, org, node, raw string) (LegacyResult, error) {
	id := strings.TrimPrefix(node, model.AgentPrefix)
	if org == "" {
		return sysLegacyFor(node, raw, "沒有組織,對不上角色"), nil
	}
	ghost := func(why string) LegacyResult { // 帶組織:不同組織同名的已刪角色各自一個 sys_legacy actor
		return legacySystem(LegacyAgentActorID(org, node), raw, legacyAgentNode(org, node), why)
	}
	cur, ret, err := lk.AgentActor(org, id)
	if err != nil {
		return LegacyResult{}, err
	}
	switch {
	case cur != "":
		return existing(cur), nil
	case ret != "":
		r := existing(ret)
		r.Why = fmt.Sprintf("組織 %s 的角色 %s 已刪除,對到退役的 actor", org, id)
		return r, nil
	}
	return ghost(fmt.Sprintf("組織 %s 找不到角色 %s", org, id)), nil
}

// localUserResult 對到本機使用者(沒有就要建)
func localUserResult(lk LegacyLookup, node, why string) (LegacyResult, error) {
	h := lk.Local().Handle
	id, err := lk.UserActor(h)
	if err != nil {
		return LegacyResult{}, err
	}
	r := LegacyResult{ActorID: id, Alias: node, Why: why}
	if id == "" {
		r.Create = &model.ActorRecord{Kind: model.ActorKindUser, Name: h, Handle: h}
	}
	return r, nil
}

func resolveLegacyUser(lk LegacyLookup, node, raw string) (LegacyResult, error) {
	x := strings.TrimPrefix(node, model.UserPrefix)
	if !strings.Contains(x, "@") && lk.Local().isAlias(x) {
		if lk.Local().Handle != "" {
			return localUserResult(lk, node, "本機身分別名對到本機使用者")
		}
		if id, err := lk.UserActor(x); err != nil {
			return LegacyResult{}, err
		} else if id != "" {
			return existing(id), nil
		}
		return sysLegacyFor(node, raw, "本機身分別名,但這個資料庫沒有設定本機使用者"), nil
	}
	if !strings.Contains(x, "@") {
		// 舊值 user:<使用者 id>(例如 user:u_a):先當 omixor_users.id 找(id 大小寫要保留,不用已轉小寫的 x),找不到再當 handle
		uid := strings.TrimSpace(strings.TrimPrefix(normalizeLegacyPrefix(strings.TrimSpace(raw)), model.UserPrefix))
		if uid != "" {
			if id, err := lk.UserActorByID(uid); err != nil {
				return LegacyResult{}, err
			} else if id != "" {
				return existing(id), nil
			}
		}
	}
	if !legacyUserHandleOK(x) {
		return sysLegacyFor(node, raw, "不是合法的使用者 handle 或 email"), nil
	}
	id, err := lk.UserActor(x)
	if err != nil {
		return LegacyResult{}, err
	}
	if id != "" {
		return existing(id), nil
	}
	return LegacyResult{Create: &model.ActorRecord{Kind: model.ActorKindUser, Name: x, Handle: x},
		Why: "找不到使用者,建立「未註冊使用者」actor"}, nil
}

func resolveLegacyLocal(lk LegacyLookup, node, raw string) (LegacyResult, error) {
	x := strings.TrimPrefix(node, model.LocalPrefix)
	explicit := strings.HasPrefix(normalizeLegacyPrefix(raw), model.LocalPrefix)
	switch x {
	case model.RoutineAuthor:
		return existing(model.SystemActorRoutine), nil
	case model.ChainAuthor:
		return existing(model.SystemActorChain), nil
	case model.SystemAuthor, "system":
		return existing(model.SystemActorOmixor), nil
	case model.MCPActorID:
		if lk.Local().Handle != "" {
			return localUserResult(lk, node, "舊的 mcp 署名對到本機使用者")
		}
		return legacySystem("sys_legacy_mcp", "舊 MCP 署名", node, "舊的 mcp 署名(非本機模式)對到 sys_legacy_mcp"), nil
	}
	if explicit || lk.Local().isAlias(x) {
		if lk.Local().Handle != "" {
			return localUserResult(lk, node, "本機身分別名對到本機使用者")
		}
		return sysLegacyFor(node, raw, "本機身分別名,但這個資料庫沒有設定本機使用者"), nil
	}
	// 純名稱(本機人員 id):有同 handle 的使用者就是他
	if !strings.ContainsAny(x, ">< ,;\"") {
		id, err := lk.UserActor(strings.ToLower(x))
		if err != nil {
			return LegacyResult{}, err
		}
		if id != "" {
			return existing(id), nil
		}
	}
	return sysLegacyFor(node, raw, "對不上任何使用者、角色或內建署名"), nil
}

// ResolveLegacyEvent 事件時間軸的執行者(actor_kind + actor_id)對到 actor:
// system → sys_omixor;ai → 角色(id 是 mcp 時同 mcp 的規則;不是角色的自動化 id → sys_legacy,名稱 = 原 id);person → 同一般的值;兩個都空 → NULL
func ResolveLegacyEvent(lk LegacyLookup, org, kind, id string, opt LegacyOpts) (LegacyResult, error) {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	switch kind {
	case "system":
		return existing(model.SystemActorOmixor), nil
	case "ai":
		if id == "" {
			return emptyLegacy(opt), nil
		}
		if id == model.MCPActorID {
			return ResolveLegacy(lk, org, id, opt)
		}
		opt.Form = IdentityAgentID
		return ResolveLegacy(lk, org, id, opt)
	}
	return ResolveLegacy(lk, org, id, opt)
}

// ---- 資料庫版的查表 ----

// pgLegacyLookup 查 omixor_users、omixor_agents、omixor_actors、omixor_actor_aliases;結果快取在記憶體(一輪遷移內用,建立新 actor 後要 remember)
type pgLegacyLookup struct {
	p     *PG
	c     context.Context
	local LegacyLocal

	users   map[string]string // handle → actor id("" 也快取:查過沒有)
	userIDs map[string]string
	aliases map[string]string
	exist   map[string]bool
	agents  map[string][2]string // org + "\x00" + id → [current, retired]
}

func (p *PG) newLegacyLookup(c context.Context) *pgLegacyLookup {
	return &pgLegacyLookup{p: p, c: c, local: currentLegacyLocal(),
		users: map[string]string{}, userIDs: map[string]string{}, aliases: map[string]string{}, exist: map[string]bool{}, agents: map[string][2]string{}}
}

func (l *pgLegacyLookup) Local() LegacyLocal { return l.local }

func (l *pgLegacyLookup) ActorExists(id string) (bool, error) {
	if v, ok := l.exist[id]; ok {
		return v, nil
	}
	var ok bool
	if err := l.p.pool.QueryRow(l.c, `select exists (select 1 from omixor_actors where id = $1)`, id).Scan(&ok); err != nil {
		return false, err
	}
	l.exist[id] = ok
	return ok, nil
}

func (l *pgLegacyLookup) UserActor(handle string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(handle))
	if h == "" {
		return "", nil
	}
	if v, ok := l.users[h]; ok {
		return v, nil
	}
	// 同 email 只差大小寫的多個帳號:確定性地選最早建立的(created_at、id),並寫 log;不能 limit 1 任選
	rows, err := l.p.pool.Query(l.c, `select u.actor_id from omixor_users u join omixor_actors a on a.id = u.actor_id and a.kind = 'user'
where lower(u.email) = $1 order by u.created_at, u.id`, h)
	if err != nil {
		return "", err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	v := ""
	if len(ids) > 0 {
		v = ids[0]
		if len(ids) > 1 {
			warnf("%d 個使用者帳號的 email 只差大小寫(%s),舊值對到最早建立的 %s", len(ids), clipLegacy(h), v)
		}
	} else {
		var id *string
		if err := l.p.pool.QueryRow(l.c, `select a.id from omixor_actors a where a.kind = 'user' and lower(a.handle) = $1 order by a.created_at, a.id limit 1`, h).Scan(&id); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if id != nil {
			v = *id
		}
	}
	l.users[h] = v
	return v, nil
}

func (l *pgLegacyLookup) UserActorByID(userID string) (string, error) {
	if v, ok := l.userIDs[userID]; ok {
		return v, nil
	}
	var id *string
	err := l.p.pool.QueryRow(l.c, `select u.actor_id from omixor_users u join omixor_actors a on a.id = u.actor_id and a.kind = 'user' where u.id = $1`, userID).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	v := ""
	if id != nil {
		v = *id
	}
	l.userIDs[userID] = v
	return v, nil
}

func (l *pgLegacyLookup) AgentActor(org, id string) (string, string, error) {
	k := org + "\x00" + id
	if v, ok := l.agents[k]; ok {
		return v[0], v[1], nil
	}
	var cur, ret *string
	err := l.p.pool.QueryRow(l.c, `select
  coalesce(
    (select g.actor_id from omixor_agents g where g.org = $1 and g.id = $2 and g.actor_id is not null),
    (select a.id from omixor_actors a where a.kind = 'agent' and a.org = $1 and a.handle = $2 and a.disabled_at is null)),
  (select a.id from omixor_actors a where a.kind = 'agent' and a.org = $1 and a.handle = $2 and a.disabled_at is not null
     order by a.disabled_at desc, a.created_at desc, a.id desc limit 1)`, org, id).Scan(&cur, &ret)
	if err != nil {
		return "", "", err
	}
	var v [2]string
	if cur != nil {
		v[0] = *cur
	}
	if ret != nil {
		v[1] = *ret
	}
	l.agents[k] = v
	return v[0], v[1], nil
}

func (l *pgLegacyLookup) AliasActor(alias string) (string, error) {
	key := aliasKey(alias)
	if v, ok := l.aliases[key]; ok {
		return v, nil
	}
	var id string
	err := l.p.pool.QueryRow(l.c, `select actor_id from omixor_actor_aliases where alias = $1`, key).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	l.aliases[key] = id
	return id, nil
}

// applyLegacy 把解析結果寫進資料庫:建缺的 actor(系統 actor:on conflict do nothing;使用者:先找 handle、沒有才建)、記別名、寫 log。
// 回傳要寫進欄位的 actor id;Null 回 ""。where 是 log 用的位置說明(表.欄位 組織 舊值 筆數)
func (l *pgLegacyLookup) applyLegacy(res LegacyResult, where string) (string, error) {
	if res.Null {
		return "", nil
	}
	id := res.ActorID
	if id == "" && res.Create != nil {
		create := *res.Create
		if res.Alias != "" && create.Kind == model.ActorKindSystem && create.ID == LegacyActorID(res.Alias) {
			nid, err := l.uniqueLegacyID(res.Alias, create.ID)
			if err != nil {
				return "", err
			}
			create.ID, create.Handle = nid, strings.TrimPrefix(nid, model.ActorIDPrefixSystem)
		}
		var err error
		if id, err = l.createActor(create); err != nil {
			return "", err
		}
	}
	if id == "" {
		return "", fmt.Errorf("解析結果沒有 actor(%s)", where)
	}
	if res.Alias != "" {
		key := aliasKey(res.Alias)
		if _, err := l.p.pool.Exec(l.c, `insert into omixor_actor_aliases (alias, actor_id) values ($1, $2) on conflict (alias) do nothing`, key, id); err != nil {
			return "", fmt.Errorf("寫入別名 %q:%w", clipLegacy(res.Alias), err)
		}
		l.aliases[key] = id
	}
	if res.Why != "" {
		log.Printf("[遷移] 舊值對照:%s → %s(%s)", where, id, res.Why)
	}
	return id, nil
}

// uniqueLegacyID sys_legacy_<雜湊> 的 id:短雜湊(8 碼)已被「另一個原值」的別名佔用(碰撞)時,改用較長的雜湊(16、32、64 碼)。
// 決定性:同一個原值每次得到同一個 id(別名表記得誰佔了哪個 id);不會靜默併成同一個 actor
func (l *pgLegacyLookup) uniqueLegacyID(node, shortID string) (string, error) {
	key := aliasKey(node)
	for _, n := range []int{8, 16, 32, 64} {
		id := model.ActorIDPrefixSystem + "legacy_" + legacyHashN(node, n)
		var taken bool
		if err := l.p.pool.QueryRow(l.c, `select exists (select 1 from omixor_actor_aliases where actor_id = $1 and alias <> $2)`, id, key).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			if n > 8 {
				warnf("sys_legacy 雜湊碰撞:%s 已被別的舊值佔用,舊值 %s 改用較長的 %s", shortID, clipLegacy(node), id)
			}
			return id, nil
		}
	}
	return "", fmt.Errorf("sys_legacy 雜湊連 64 碼都碰撞(不可能):%s", clipLegacy(node))
}

func (l *pgLegacyLookup) createActor(a model.ActorRecord) (string, error) {
	switch a.Kind {
	case model.ActorKindSystem:
		if _, err := l.p.pool.Exec(l.c, `insert into omixor_actors (id, kind, name, handle) values ($1, 'system', $2, $3) on conflict do nothing`,
			a.ID, a.Name, a.Handle); err != nil {
			return "", fmt.Errorf("建立系統 actor %s:%w", a.ID, err)
		}
		l.exist[a.ID] = true
		return a.ID, nil
	case model.ActorKindUser:
		if id, err := l.UserActor(a.Handle); err != nil || id != "" {
			return id, err
		}
		id, err := ensureUnregisteredUser(l.c, l.p.pool, a.Handle, a.Name)
		if err != nil {
			return "", fmt.Errorf("建立使用者 actor %q:%w", a.Handle, err)
		}
		l.users[strings.ToLower(a.Handle)] = id
		l.exist[id] = true
		return id, nil
	}
	return "", fmt.Errorf("不支援建立 %s 種類的 actor", a.Kind)
}

// ResolveLegacy 寬鬆解析器(資料庫版;匯入等共用):把一個舊值對到 actor id,缺的 actor 與別名會寫進資料庫。
// 空值回 ""(NULL)。冪等:同一個舊值永遠同一個 actor
func (p *PG) ResolveLegacy(org, raw string) (string, error) {
	c, cancel := ctx()
	defer cancel()
	lk := p.newLegacyLookup(c)
	res, err := ResolveLegacy(lk, org, raw, LegacyOpts{})
	if err != nil {
		return "", err
	}
	return lk.applyLegacy(res, fmt.Sprintf("組織 %q 舊值 %q", org, clipLegacy(raw)))
}

// ensureUnregisteredUser 取得或建立「未註冊使用者」actor(handle = email 小寫,沒有 omixor_users 列)。在連線池或交易上都能用:
// 衝突(同時有人建了同一個 handle)用 on conflict do nothing 處理,不會讓外層交易進入 aborted 狀態(EnsureUserActorByEmail 靠捕捉唯一衝突錯誤,在交易裡不行)
func ensureUnregisteredUser(c context.Context, q actorQ, email, name string) (string, error) {
	h, err := model.CleanUserHandle(email)
	if err != nil {
		return "", err
	}
	find := func() (string, error) {
		var id string
		err := q.QueryRow(c, `select id from omixor_actors where kind = 'user' and lower(handle) = $1 order by created_at, id limit 1`, h).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return id, err
	}
	if id, err := find(); err != nil || id != "" {
		return id, err
	}
	var id string
	err = q.QueryRow(c, `insert into omixor_actors (id, kind, name, handle) values ($1, 'user', $2, $3) on conflict do nothing returning id`,
		model.NewUserActorID(time.Now()), userActorName(name, h), h).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return find()
	}
	return id, err
}
