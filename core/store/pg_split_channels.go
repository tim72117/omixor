package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 頻道成員、訊息提及、組織鏈的邊(PG):本質是關聯,拆成有外鍵的表 ----
//
//	omixor_channel_members(org, channel_id, member, pos)             主鍵 (org, channel_id, member);外鍵 (org, channel_id) → omixor_channels(org, id) cascade
//	omixor_channel_mentions(org, channel_id, message_id, member, pos) 主鍵含 member;外鍵 (org, channel_id, message_id) → omixor_channel_messages cascade
//	omixor_org_chain_edges(org, pos, from_node, to_node, label)       主鍵 (org, pos);外鍵 org → omixor_org_chains(org) cascade
//
// 新表是權威來源:讀取一律從新表組回原本的結構(順序用 pos);寫入(含整份取代)在同一個交易裡更新新表。
// 成員、提及重複的只留第一個(主鍵不允許重複;服務層本來就不會產生重複;遷移舊資料時也是去重後展開)。
//
// 舊欄位 omixor_channels.members、omixor_channel_messages.mentions、omixor_org_chains.edges(jsonb)已經不再寫
// (新增的列用欄位預設值 '[]'),只有遷移與「未展開列的退回讀取」會讀它們。
// 【不支援回滾】新版不雙寫舊欄位:新建的列舊欄位是預設值,已展開的列舊欄位停在升級那一刻;回到舊版會看不到新版期間的資料,
// 所以要回滾只能從備份還原。這個版本「不 drop」舊欄位與 split_at 只是為了讓遷移能分批、可重跑、可退回讀取,不是為了回滾。
// 確定全部列都展開(UnmigratedRows() == 0)、而且線上所有執行個體都是這個版本之後,下一個版本再執行
// (升級路徑不得跳過「遷移還在」的這個版本):
//
//	alter table omixor_channels drop column members, drop column split_at;
//	alter table omixor_channel_messages drop column mentions, drop column split_at;
//	alter table omixor_org_chains drop column edges, drop column split_at;
//
// 並一併移除遷移與退回讀取的程式。細節與救援 SQL 見 MIGRATIONS.md。
// split_at 標記「這一列已展開到新表」:遷移只處理 split_at 為 NULL 的列;新版寫入的列一律在寫入時設成 now(),所以新表不會被舊 jsonb 覆蓋。

// roTx 唯讀、可重複讀的交易:同一次讀取的幾個查詢看到同一個時間點
func (p *PG) roTx(c context.Context) (pgx.Tx, error) {
	return p.pool.BeginTx(c, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
}

func posList(n int) []int32 {
	l := make([]int32, n)
	for i := range l {
		l[i] = int32(i)
	}
	return l
}

// replaceChannelMembers 在交易裡把頻道的成員整份換成 members(actor id,依主鍵 (org, channel_id, member) 去重、順序即 pos)
func replaceChannelMembers(c context.Context, tx pgx.Tx, org, channelID string, members []string) error {
	members = model.DedupExact(members)
	if _, err := tx.Exec(c, `delete from omixor_channel_members where org = $1 and channel_id = $2`, org, channelID); err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	_, err := tx.Exec(c, `insert into omixor_channel_members (org, channel_id, member, pos)
		select $1, $2, member, pos from unnest($3::text[], $4::int[]) as t(member, pos)`, org, channelID, members, posList(len(members)))
	return fkErr(err)
}

// insertMessageMentions 訊息的提及(actor id,依主鍵去重;訊息寫入後不再改,只在新增時寫)
func insertMessageMentions(c context.Context, tx pgx.Tx, org, channelID, messageID string, mentions []string) error {
	mentions = model.DedupExact(mentions)
	if len(mentions) == 0 {
		return nil
	}
	_, err := tx.Exec(c, `insert into omixor_channel_mentions (org, channel_id, message_id, member, pos)
		select $1, $2, $3, member, pos from unnest($4::text[], $5::int[]) as t(member, pos)`,
		org, channelID, messageID, mentions, posList(len(mentions)))
	return fkErr(err)
}

// replaceChainEdges 在交易裡把組織鏈的邊整份換成 edges(端點是 actor id;順序即 pos)
func replaceChainEdges(c context.Context, tx pgx.Tx, org string, edges []model.ChainEdge) error {
	if _, err := tx.Exec(c, `delete from omixor_org_chain_edges where org = $1`, org); err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	from := make([]string, len(edges))
	to := make([]string, len(edges))
	label := make([]string, len(edges))
	for i, e := range edges {
		from[i], to[i], label[i] = e.From, e.To, e.Label
	}
	// 舊結構(欄位還叫 from_node / to_node,遷移還沒改名;拆表遷移在 actor 引擎之前展開舊 jsonb 時)寫舊欄位名
	fc, tc := "from_actor", "to_actor"
	var legacy bool
	if err := tx.QueryRow(c, `select exists (select 1 from information_schema.columns where table_schema = current_schema()
		and table_name = 'omixor_org_chain_edges' and column_name = 'from_node')`).Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		fc, tc = "from_node", "to_node"
	}
	_, err := tx.Exec(c, `insert into omixor_org_chain_edges (org, pos, `+fc+`, `+tc+`, label)
		select $1, pos, f, t, l from unnest($2::int[], $3::text[], $4::text[], $5::text[]) as x(pos, f, t, l)`,
		org, posList(len(edges)), from, to, label)
	return fkErr(err)
}

// queryer 交易或連線池
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// loadChannelMembers 組織內所有頻道(ids 為 nil)或指定頻道的成員,頻道 id → 依 pos 排序的成員;
// 還沒展開的頻道(只在遷移有略過的列時才有)退回讀舊的 members jsonb
func (p *PG) loadChannelMembers(c context.Context, q queryer, org string, ids []string) (map[string][]string, error) {
	var rows pgx.Rows
	var err error
	if ids == nil {
		rows, err = q.Query(c, `select channel_id, member from omixor_channel_members where org = $1 order by channel_id, pos`, org)
	} else {
		rows, err = q.Query(c, `select channel_id, member from omixor_channel_members where org = $1 and channel_id = any($2) order by channel_id, pos`, org, ids)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, m string
		if err := rows.Scan(&id, &m); err != nil {
			return nil, err
		}
		out[id] = append(out[id], m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if !p.hasUnsplit() {
		return out, nil
	}
	if ids == nil {
		rows, err = q.Query(c, `select id, members from omixor_channels where org = $1 and split_at is null`, org)
	} else {
		rows, err = q.Query(c, `select id, members from omixor_channels where org = $1 and split_at is null and id = any($2)`, org, ids)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		out[id] = p.legacyActorIDs(c, "omixor_channel_members.member", org, lenientStrings(b))
	}
	return out, rows.Err()
}

// fillMentions 把訊息的提及補進 msgs(就地改);沒有提及的是空陣列;還沒展開的訊息退回讀舊的 mentions jsonb
func (p *PG) fillMentions(c context.Context, q queryer, org string, msgs []model.ChannelMessage) error {
	for i := range msgs {
		msgs[i].Mentions = []string{}
	}
	if len(msgs) == 0 {
		return nil
	}
	chans := make([]string, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	idx := make(map[[2]string]int, len(msgs))
	for i, m := range msgs {
		chans = append(chans, m.ChannelID)
		ids = append(ids, m.ID)
		idx[[2]string{m.ChannelID, m.ID}] = i
	}
	rows, err := q.Query(c, `select channel_id, message_id, member from omixor_channel_mentions
		where org = $1 and channel_id = any($2) and message_id = any($3) order by channel_id, message_id, pos`, org, chans, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ch, id, m string
		if err := rows.Scan(&ch, &id, &m); err != nil {
			return err
		}
		if i, ok := idx[[2]string{ch, id}]; ok {
			msgs[i].Mentions = append(msgs[i].Mentions, m)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if !p.hasUnsplit() {
		return nil
	}
	rows, err = q.Query(c, `select channel_id, id, mentions from omixor_channel_messages
		where org = $1 and split_at is null and channel_id = any($2) and id = any($3)`, org, chans, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ch, id string
		var b []byte
		if err := rows.Scan(&ch, &id, &b); err != nil {
			return err
		}
		if i, ok := idx[[2]string{ch, id}]; ok {
			msgs[i].Mentions = p.legacyActorIDs(c, "omixor_channel_mentions.member", org, lenientStrings(b))
		}
	}
	return rows.Err()
}

// ---- 啟動遷移(共用的批次驅動、保護、退回讀取見 pg_split_migrate.go) ----

// migrateChannelSplit 啟動時把還沒展開(split_at 為 NULL)的頻道、訊息、組織鏈列展開進新表(分批、冪等,見 pg_split_migrate.go)。
// 成員、提及重複的直接去重(留第一個)再展開;JSON 壞掉、元素不是字串的列略過(split_at 保持 NULL,讀取退回舊 jsonb)。
func (p *PG) migrateChannelSplit() error {
	return p.splitRun(nil, splitChannels, splitMessages, splitChains)
}

var splitChannels = &splitSpec{
	name: "頻道成員", table: "omixor_channels", keys: []string{"org", "id"},
	empty:  `case when jsonb_typeof(members) = 'array' then jsonb_array_length(members) = 0 else false end`,
	expand: expandChannelMembers,
}

var splitMessages = &splitSpec{
	name: "訊息提及", table: "omixor_channel_messages", keys: []string{"org", "channel_id", "id"},
	empty:  `case when jsonb_typeof(mentions) = 'array' then jsonb_array_length(mentions) = 0 else false end`,
	expand: expandMessageMentions,
}

var splitChains = &splitSpec{
	name: "組織鏈的邊", table: "omixor_org_chains", keys: []string{"org"},
	empty:  `case when jsonb_typeof(edges) = 'array' then jsonb_array_length(edges) = 0 else false end`,
	expand: expandChainEdges,
}

// strictStrings 把 jsonb 解成不重複的字串陣列(null 當空陣列;重複的去掉、留第一個);不是陣列、或元素不是字串就回錯誤
func strictStrings(b []byte) ([]string, error) {
	var raw []any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("元素不是字串:%v", v)
		}
		out = append(out, s)
	}
	return model.DedupExact(out), nil
}

// lenientStrings 退回讀取舊 jsonb 用:不是陣列回空;非字串元素丟掉;重複去掉
func lenientStrings(b []byte) []string {
	var raw []any
	if err := json.Unmarshal(b, &raw); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return model.DedupExact(out)
}

func expandChannelMembers(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	var b []byte
	err := tx.QueryRow(c, `select members from omixor_channels where org = $1 and id = $2 and split_at is null for update`, key[0], key[1]).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	members, err := strictStrings(b)
	if err != nil {
		return skipErr(err)
	}
	members, err = p.whoTx(c, tx).cols("omixor_channel_members.member", key[0], members) // 舊 jsonb 的字串 → actor id(過渡橋;只有遷移用)
	if err != nil {
		return err
	}
	if err := replaceChannelMembers(c, tx, key[0], key[1], members); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_channels set split_at = now() where org = $1 and id = $2`, key[0], key[1])
	return err
}

func expandMessageMentions(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	var b []byte
	err := tx.QueryRow(c, `select mentions from omixor_channel_messages where org = $1 and channel_id = $2 and id = $3 and split_at is null for update`,
		key[0], key[1], key[2]).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	mentions, err := strictStrings(b)
	if err != nil {
		return skipErr(err)
	}
	mentions, err = p.whoTx(c, tx).cols("omixor_channel_mentions.member", key[0], mentions)
	if err != nil {
		return err
	}
	if err := insertMessageMentions(c, tx, key[0], key[1], key[2], mentions); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_channel_messages set split_at = now() where org = $1 and channel_id = $2 and id = $3`, key[0], key[1], key[2])
	return err
}

func expandChainEdges(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	var b []byte
	err := tx.QueryRow(c, `select edges from omixor_org_chains where org = $1 and split_at is null for update`, key[0]).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var edges []model.ChainEdge
	if err := json.Unmarshal(b, &edges); err != nil {
		return skipErr(fmt.Errorf("JSON 不合格:%v", err))
	}
	w := p.whoTx(c, tx)
	from := make([]string, len(edges))
	to := make([]string, len(edges))
	for i, e := range edges {
		from[i], to[i] = e.From, e.To
	}
	if from, err = w.colsAll("omixor_org_chain_edges.from_actor", key[0], from); err != nil {
		return err
	}
	if to, err = w.colsAll("omixor_org_chain_edges.to_actor", key[0], to); err != nil {
		return err
	}
	for i := range edges {
		edges[i].From, edges[i].To = from[i], to[i]
	}
	if err := replaceChainEdges(c, tx, key[0], edges); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_org_chains set split_at = now() where org = $1`, key[0])
	return err
}

// legacyActorIDs 退回讀取舊 jsonb 時把舊字串換成 actor id(寬鬆解析器:同一個舊字串永遠是同一個 actor,缺的會建;和之後欄位轉換引擎對到的一樣)。
// 只有遷移略過的未展開列才會走到
func (p *PG) legacyActorIDs(c context.Context, key, org string, raws []string) []string {
	w := p.newWho(c)
	out := make([]string, 0, len(raws))
	for _, r := range raws {
		if v, err := w.col(key, org, r); err == nil {
			if id, ok := v.(string); ok && id != "" {
				out = append(out, id)
			}
		}
	}
	return model.DedupExact(out)
}

// unsplitChainEdges 退回讀取:這個組織的組織鏈還沒展開時,舊 jsonb 的邊(解不出來回空)
func unsplitChainEdges(c context.Context, q dbq, org string) ([]model.ChainEdge, bool, error) {
	var b []byte
	err := q.QueryRow(c, `select edges from omixor_org_chains where org = $1 and split_at is null`, org).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	edges := []model.ChainEdge{}
	if err := json.Unmarshal(b, &edges); err != nil || edges == nil {
		edges = []model.ChainEdge{}
	}
	return edges, true, nil
}
