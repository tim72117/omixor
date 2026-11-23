package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 頻道成員、訊息提及、組織鏈的邊(PG):本質是關聯,拆成有外鍵的表 ----
//
//	omixor_channel_members(org, channel_id, member, pos)             主鍵 (org, channel_id, member);外鍵 (org, channel_id) → omixor_channels(org, id) cascade
//	omixor_channel_mentions(org, channel_id, message_id, member, pos) 主鍵含 member;外鍵 (org, channel_id, message_id) → omixor_channel_messages cascade
//	omixor_org_chain_edges(org, pos, from_node, to_node, label)       主鍵 (org, pos);外鍵 org → omixor_org_chains(org) cascade
//
// 讀取一律從這些表組回原本的結構(順序用 pos);寫入(含整份取代)在同一個交易裡更新。成員、提及重複的只留第一個(主鍵不允許重複)。
//
// 舊欄位 omixor_channels.members、omixor_channel_messages.mentions、omixor_org_chains.edges(jsonb)與各表的 split_at 已經不再讀、不再寫
// (新增的列用欄位預設值 '[]'),下一版 drop:
//
//	alter table omixor_channels drop column members, drop column split_at;
//	alter table omixor_channel_messages drop column mentions, drop column split_at;
//	alter table omixor_org_chains drop column edges, drop column split_at;
//
// split_at 現在只在寫入時設成 now()(保留這個欄位的語意,下一版一併 drop)。

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
	_, err := tx.Exec(c, `insert into omixor_org_chain_edges (org, pos, from_actor, to_actor, label)
		select $1, pos, f, t, l from unnest($2::int[], $3::text[], $4::text[], $5::text[]) as x(pos, f, t, l)`,
		org, posList(len(edges)), from, to, label)
	return fkErr(err)
}

// queryer 交易或連線池
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// loadChannelMembers 組織內所有頻道(ids 為 nil)或指定頻道的成員,頻道 id → 依 pos 排序的成員
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
	return out, nil
}

// fillMentions 把訊息的提及補進 msgs(就地改);沒有提及的是空陣列
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
	return nil
}
