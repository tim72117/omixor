// pg_actor_bridge.go 「誰」欄位的過渡橋(ISS-36 C-2 共用地基;契約見 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》)
//
// 22 個「誰」的欄位在資料庫裡是 actor id(外鍵指向 omixor_actors)。匯入檔等舊格式入口帶的是舊字串(email、user:<email>、agent:<id>、mcp…),
// 這一層把它轉成 actor id:
//
//	寫入  whoConv:舊字串 → actor id(寬鬆解析器 ResolveLegacy;缺的 actor 與別名自動建立,同一個字串永遠同一個 actor)。
//	      空字串 → NULL(可為 NULL 的欄位)或 sys_legacy_unknown(NOT NULL 的欄位)。傳進來的本來就是 actor id 也可以(解析器對到自己),
//	      所以工作包把服務層改成傳 actor id 之後,store 這一端不用同時改。
//
// 一般路徑已不經過這一層(model 欄位存 actor id、服務層 present 補名字)。現在只剩 incident/channel 匯入(pg_incident_legacy.go)與其他舊字串入口寫入時用。

package store

import (
	"context"
	"fmt"
	"strings"

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

// whoTx 綁在交易 tx 上的轉換器:查表與建立 actor 都在同一個交易裡(不另外佔一條連線 —— 連線池限制連線數時,交易還握著連線又去拿第二條會互等死鎖;
// 回滾時一起回滾)。已經開了交易就用這個,沒有交易的單一語句用 newWho
func (p *PG) whoTx(c context.Context, tx pgx.Tx) *whoConv {
	return (&PG{pool: txConn{tx}}).newWho(c)
}

// col 把欄位 key("表.欄位",見 ActorColumns)的舊字串 raw 轉成要寫進欄位的值:actor id,或 nil(NULL)。org 是這一列所屬的組織(無組織的表給 "")。
// raw 本來就是 actor id 就是它自己;對不上任何 actor 的字串一律落到 sys_legacy_<雜湊>(名稱 = 原值),不會失敗
func (w *whoConv) col(key, org, raw string) (any, error) {
	ac, ok := actorColByKey[key]
	if !ok {
		return nil, fmt.Errorf("不認得的「誰」欄位 %s", key)
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

// event 事件時間軸的執行者(舊的 kind = ai / person / system + id)→ omixor_incident_events.actor 的值(actor id 或 nil)
func (w *whoConv) event(org, kind, id string) (any, error) {
	ac := actorColByKey["omixor_incident_events.actor"]
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
