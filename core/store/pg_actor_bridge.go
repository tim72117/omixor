// pg_actor_bridge.go 「誰」欄位的過渡橋(ISS-36 C-2 共用地基;契約見 ACTOR_CONTRACT.md)
//
// 22 個「誰」的欄位在資料庫裡已經是 actor id(最終結構,外鍵指向 omixor_actors)。領域程式碼(store 方法、服務層、API)還在用舊字串語意
// (email、user:<email>、agent:<id>、mcp…),在 C-2a~e 各自改完之前,這一層讓兩邊接得起來,而且「行為與改之前一樣」:
//
//	寫入  whoConv:舊字串 → actor id(寬鬆解析器 ResolveLegacy;缺的 actor 與別名自動建立,同一個字串永遠同一個 actor)。
//	      空字串 → NULL(可為 NULL 的欄位)或 sys_legacy_unknown(NOT NULL 的欄位)。傳進來的本來就是 actor id 也可以(解析器對到自己),
//	      所以工作包把服務層改成傳 actor id 之後,store 這一端不用同時改。
//
// 一般路徑已不經過這一層(model 欄位存 actor id、服務層 present 補名字;讀取的 whoSQL 等 SQL 版已移除)。
// 現在只剩舊 jsonb 展開遷移(pg_split_channels.go、pg_split_incidents_tasks.go、pg_chain.go)與 incident/channel 匯入(pg_incident_legacy.go)寫入時用。
//
// 轉換前的資料庫(舊結構,啟動遷移還沒做完)寫入時欄位還是舊字串:whoConv.col 先看欄位有沒有完成標記,沒有就原樣寫(拆表遷移在 actor 引擎之前展開舊 jsonb 會走這裡)。

package store

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// actorColByKey "表.欄位" → ActorColumn(Copy 欄位用 Target 的 key:omixor_incident_events.actor、omixor_audit.actor)
var actorColByKey = func() map[string]ActorColumn {
	m := map[string]ActorColumn{}
	for _, c := range ActorColumns {
		m[c.Key()] = c
		m[c.Table+"."+c.Target] = c
	}
	return m
}()

// convCache 欄位是否已轉換完成(完成標記,見 actorColumnConverted)。一旦轉換完成就不會回頭,所以只快取「是」。放在 migState 裡,同一個連線池的 PG(含批次裡的交易)共用
type convCache struct {
	mu   sync.Mutex
	done map[string]bool
}

func (cc *convCache) get(k string) bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.done[k]
}

func (cc *convCache) set(k string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.done == nil {
		cc.done = map[string]bool{}
	}
	cc.done[k] = true
}

// whoConv 一次 store 呼叫內的「舊字串 → actor id」轉換器(帶查表快取;不要跨呼叫、跨 goroutine 共用)
type whoConv struct {
	p  *PG
	c  context.Context
	lk *pgLegacyLookup
}

// newWho 建立轉換器;c 是這次呼叫的 context
func (p *PG) newWho(c context.Context) *whoConv {
	return &whoConv{p: p, c: c, lk: p.newLegacyLookup(c)}
}

func (w *whoConv) converted(ac ActorColumn) bool {
	k := ac.Table + "." + ac.Target
	if w.p.mig != nil && w.p.mig.conv.get(k) {
		return true
	}
	if !w.p.actorColumnConverted(w.c, ac.Table, ac.Target) {
		return false
	}
	if w.p.mig != nil {
		w.p.mig.conv.set(k)
	}
	return true
}

// whoTx 綁在交易 tx 上的轉換器:查表與建立 actor 都在同一個交易裡(不另外佔一條連線 —— 連線池限制連線數時,交易還握著連線又去拿第二條會互等死鎖;
// 回滾時一起回滾)。已經開了交易就用這個,沒有交易的單一語句用 newWho
func (p *PG) whoTx(c context.Context, tx pgx.Tx) *whoConv {
	return (&PG{pool: txConn{tx}, mig: p.mig}).newWho(c)
}

// col 把欄位 key("表.欄位",見 ActorColumns)的舊字串 raw 轉成要寫進欄位的值:actor id,或 nil(NULL)。org 是這一列所屬的組織(無組織的表給 "")。
// raw 本來就是 actor id 就是它自己;對不上任何 actor 的字串一律落到 sys_legacy_<雜湊>(名稱 = 原值),不會失敗
func (w *whoConv) col(key, org, raw string) (any, error) {
	ac, ok := actorColByKey[key]
	if !ok {
		return nil, fmt.Errorf("不認得的「誰」欄位 %s", key)
	}
	if !w.converted(ac) { // 舊結構、還沒轉換:原樣寫(含空字串:舊欄位是 NOT NULL default ''),之後由 actor 欄位引擎轉換
		return raw, nil
	}
	res, err := ResolveLegacy(w.lk, org, raw, ac.opts())
	if err != nil {
		return nil, err
	}
	id, err := w.lk.applyLegacy(res, fmt.Sprintf("%s 組織 %q 舊值 %q", key, org, clipLegacy(raw)))
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil
	}
	return id, nil
}

// cols 一批舊字串(頻道成員、提及、組織鏈端點…)轉成 actor id 的字串切片:轉換後同一個人的兩種寫法會變成同一個 id,所以去重(留第一個,順序不變);
// 轉成 NULL 的略過。欄位還沒轉換完成時原樣回傳
func (w *whoConv) cols(key, org string, raws []string) ([]string, error) {
	out := make([]string, 0, len(raws))
	for _, r := range raws {
		v, err := w.col(key, org, r)
		if err != nil {
			return nil, err
		}
		if id, ok := v.(string); ok {
			out = append(out, id)
		}
	}
	return model.DedupExact(out), nil
}

// colsAll 一批舊字串轉成 actor id,一對一(不去重、不略過;組織鏈的端點用,from 與 to 兩個陣列要等長)。NULL 不可能(欄位都是 NOT NULL)
func (w *whoConv) colsAll(key, org string, raws []string) ([]string, error) {
	out := make([]string, len(raws))
	for i, r := range raws {
		v, err := w.col(key, org, r)
		if err != nil {
			return nil, err
		}
		id, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s 不可為 NULL(值 %q)", key, clipLegacy(r))
		}
		out[i] = id
	}
	return out, nil
}

// event 事件時間軸的執行者(舊的 kind = ai / person / system + id)→ omixor_incident_events.actor 的值(actor id 或 nil)
func (w *whoConv) event(org, kind, id string) (any, error) {
	ac := actorColByKey["omixor_incident_events.actor"]
	if !w.converted(ac) {
		return nil, fmt.Errorf("omixor_incident_events.actor 還沒轉換完成")
	}
	// 過渡橋與遷移不同:kind = person 的 id 一律當使用者(沒有同名使用者就建「未註冊使用者」),讀回來才還是 person/<id>;
	// 遷移是對舊資料盡量對到人,對不上的落到 sys_legacy
	if strings.TrimSpace(kind) == "person" && strings.TrimSpace(id) != "" && !strings.Contains(id, ":") {
		id = model.UserPrefix + strings.TrimSpace(id)
	}
	res, err := ResolveLegacyEvent(w.lk, org, kind, id, ac.opts())
	if err != nil {
		return nil, err
	}
	aid, err := w.lk.applyLegacy(res, fmt.Sprintf("omixor_incident_events.actor 組織 %q 舊值 %q", org, clipLegacy(kind+"/"+id)))
	if err != nil {
		return nil, err
	}
	if aid == "" {
		return nil, nil
	}
	return aid, nil
}

// auditUser 稽核的舊 user_id(u_<x> 使用者 id、agent:<id> 角色假使用者、空字串)→ omixor_audit.actor 的值
func (w *whoConv) auditUser(org, userID string) (any, error) {
	return w.col("omixor_audit.actor", org, userID)
}

// lookup 只查不建:舊字串 raw 對到的 actor id,對不上(或要建新的才對得上)回 ""。給「讀取時以某個人當條件」(頻道已讀、未讀)用,讀取不該為了查詢建立 actor
func (w *whoConv) lookup(key, org, raw string) (string, error) {
	ac, ok := actorColByKey[key]
	if !ok {
		return "", fmt.Errorf("不認得的「誰」欄位 %s", key)
	}
	if !w.converted(ac) {
		return raw, nil
	}
	res, err := ResolveLegacy(w.lk, org, raw, ac.opts())
	if err != nil {
		return "", err
	}
	if res.Null {
		return "", nil
	}
	if res.ActorID != "" {
		return res.ActorID, nil
	}
	if res.Create != nil && res.Create.Kind == model.ActorKindSystem {
		// sys_legacy_*:id 固定,存在就是它(不存在 = 沒有任何資料用到,查不到也對)
		ok, err := w.lk.ActorExists(res.Create.ID)
		if err != nil || !ok {
			return "", err
		}
		return res.Create.ID, nil
	}
	return "", nil
}
