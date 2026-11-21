// pg_channel.go 訊息頻道、頻道訊息、已讀與未讀的 PG 實作

package store

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 訊息頻道 ----

var channelCols = `id, name, topic, coalesce(issue_id, ''), created_by, created_at, updated_at`

func scanChannel(row pgx.Row) (*model.Channel, error) {
	var ch model.Channel
	var created, updated time.Time
	var by NullID
	if err := row.Scan(&ch.ID, &ch.Name, &ch.Topic, &ch.IssueID, &by, &created, &updated); err != nil {
		return nil, err
	}
	ch.CreatedBy = by.String() // actor id;NULL = 沒有
	ch.Members = []string{} // 成員在 omixor_channel_members,由呼叫端補上
	ch.CreatedAt, ch.UpdatedAt = model.Time{Time: secs(created)}, model.Time{Time: secs(updated)}
	return &ch, nil
}

func (p *PG) ListChannels(org string) ([]model.Channel, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.roTx(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select `+channelCols+` from omixor_channels where org = $1 order by number`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Channel{}
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	members, err := p.loadChannelMembers(c, tx, org, nil)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if m := members[out[i].ID]; m != nil {
			out[i].Members = m
		}
	}
	return out, nil
}

func (p *PG) GetChannel(org, id string) (*model.Channel, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.roTx(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	ch, err := scanChannel(tx.QueryRow(c, `select `+channelCols+` from omixor_channels where org = $1 and id = $2`, org, id))
	if err != nil {
		return nil, notFound(err)
	}
	members, err := p.loadChannelMembers(c, tx, org, []string{id})
	if err != nil {
		return nil, err
	}
	if m := members[id]; m != nil {
		ch.Members = m
	}
	return ch, nil
}

func (p *PG) CreateChannel(org string, ch model.Channel) (model.Channel, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return ch, err
	}
	defer tx.Rollback(c)
	// 號碼由序號表單句原子取得(刪掉的號碼不再使用)
	num, err := seqNext(c, tx, org, "channel", 0)
	if err != nil {
		return ch, err
	}
	ch.ID = fmt.Sprintf("%s%d", model.ChannelPrefix, num)
	// 不再寫舊的 members 欄位;split_at = now() 讓啟動遷移不碰(見 pg_split_channels.go)
	if _, err := tx.Exec(c, `insert into omixor_channels (org, id, number, name, topic, issue_id, created_by, created_at, updated_at, split_at)
		values ($1,$2,$3,$4,$5,nullif($6,''),$7,$8,$9,now())`, org, ch.ID, num, ch.Name, ch.Topic, ch.IssueID,
		IDOrNil(ch.CreatedBy), secs(ch.CreatedAt.Time), secs(ch.UpdatedAt.Time)); err != nil {
		return ch, fkErr(err)
	}
	if err := replaceChannelMembers(c, tx, org, ch.ID, ch.Members); err != nil {
		return ch, err
	}
	return ch, tx.Commit(c)
}

func (p *PG) UpdateChannel(org string, ch model.Channel) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := p.guardUnsplit(c, tx, splitChannels, org, ch.ID); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	tag, err := tx.Exec(c, `update omixor_channels set name = $3, topic = $4, issue_id = nullif($5, ''), updated_at = $6, split_at = now()
		where org = $1 and id = $2`, org, ch.ID, ch.Name, ch.Topic, ch.IssueID, secs(ch.UpdatedAt.Time))
	if err != nil {
		return fkErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := replaceChannelMembers(c, tx, org, ch.ID, ch.Members); err != nil {
		return err
	}
	return tx.Commit(c)
}

var messageCols = `channel_id, id, author, text, at`

func scanMessage(row pgx.Row) (*model.ChannelMessage, error) {
	var m model.ChannelMessage
	var at time.Time
	if err := row.Scan(&m.ChannelID, &m.ID, &m.Author, &m.Text, &at); err != nil {
		return nil, err
	}
	m.Mentions = []string{} // 提及在 omixor_channel_mentions,由呼叫端補上
	m.At = model.Time{Time: secs(at)}
	return &m, nil
}

func (p *PG) CreateChannelMessage(org string, m model.ChannelMessage) (model.ChannelMessage, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return m, err
	}
	defer tx.Rollback(c)
	// 每個頻道各自一組號碼
	num, err := seqNext(c, tx, org, msgScope(m.ChannelID), 0)
	if err != nil {
		return m, err
	}
	m.ID = fmt.Sprintf("%s%d", model.MessagePrefix, num)
	// 不再寫舊的 mentions 欄位;split_at = now() 讓啟動遷移不碰(見 pg_split_channels.go)
	if _, err := tx.Exec(c, `insert into omixor_channel_messages (org, channel_id, id, number, author, text, at, split_at)
		values ($1,$2,$3,$4,$5,$6,$7,now())`, org, m.ChannelID, m.ID, num, m.Author, m.Text, secs(m.At.Time)); err != nil {
		return m, fkErr(err)
	}
	if err := insertMessageMentions(c, tx, org, m.ChannelID, m.ID, m.Mentions); err != nil {
		return m, err
	}
	return m, tx.Commit(c)
}

func (p *PG) ListChannelMessages(org, channelID string, q model.MessageQuery) ([]model.ChannelMessage, bool, error) {
	c, cancel := ctx()
	defer cancel()
	order := "asc"
	if q.Latest {
		order = "desc"
	}
	sql := `select ` + messageCols + ` from omixor_channel_messages where org = $1 and channel_id = $2
		and ($3 <= 0 or number > $3) and ($4 <= 0 or number < $4) order by number ` + order
	args := []any{org, channelID, q.After, q.Before}
	if q.Limit > 0 {
		sql += ` limit $5`
		args = append(args, q.Limit+1)
	}
	tx, err := p.roTx(c)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []model.ChannelMessage{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	rows.Close()
	more := false
	if q.Limit > 0 && len(out) > q.Limit {
		more, out = true, out[:q.Limit]
	}
	if err := p.fillMentions(c, tx, org, out); err != nil {
		return nil, false, err
	}
	if q.Latest { // 換回由舊到新
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, more, nil
}

func (p *PG) LastChannelMessages(org string) (map[string]model.ChannelMessage, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.roTx(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select distinct on (channel_id) `+messageCols+` from omixor_channel_messages
		where org = $1 order by channel_id, number desc`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []model.ChannelMessage{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := p.fillMentions(c, tx, org, list); err != nil {
		return nil, err
	}
	out := make(map[string]model.ChannelMessage, len(list))
	for _, m := range list {
		out[m.ChannelID] = m
	}
	return out, nil
}

func (p *PG) ChannelReads(org, member string) (map[string]model.ChannelRead, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select channel_id, last_read, at from omixor_channel_reads where org = $1 and member = $2`, org, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ChannelRead{}
	for rows.Next() {
		r := model.ChannelRead{Member: member}
		var at time.Time
		if err := rows.Scan(&r.ChannelID, &r.LastRead, &at); err != nil {
			return nil, err
		}
		r.At = model.Time{Time: secs(at)}
		out[r.ChannelID] = r
	}
	return out, rows.Err()
}

func (p *PG) SaveChannelRead(org string, r model.ChannelRead) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `insert into omixor_channel_reads (org, channel_id, member, last_read, at) values ($1,$2,$3,$4,$5)
		on conflict (org, channel_id, member) do update set last_read = $4, at = $5`, org, r.ChannelID, r.Member, r.LastRead, secs(r.At.Time))
	return fkErr(err)
}

func (p *PG) UnreadCounts(org, member string, channelIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(channelIDs) == 0 {
		return out, nil
	}
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select m.channel_id, count(*) from omixor_channel_messages m
		left join omixor_channel_reads r on r.org = m.org and r.channel_id = m.channel_id and r.member = $2
		where m.org = $1 and m.channel_id = any($3) and m.author <> $2 and m.number > coalesce(r.last_read, 0)
		group by m.channel_id`, org, member, channelIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (p *PG) UnreadMentions(org, member string, channelIDs []string, limit int) ([]model.ChannelMessage, error) {
	out := []model.ChannelMessage{}
	if len(channelIDs) == 0 {
		return out, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	c, cancel := ctx()
	defer cancel()
	tx, err := p.roTx(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select m.channel_id, m.id, m.author, m.text, m.at from omixor_channel_messages m
		left join omixor_channel_reads r on r.org = m.org and r.channel_id = m.channel_id and r.member = $2
		where m.org = $1 and m.channel_id = any($3) and m.author <> $2 and m.number > coalesce(r.last_read, 0)
		and exists (select 1 from omixor_channel_mentions x where x.org = m.org and x.channel_id = m.channel_id and x.message_id = m.id and x.member = $2)
		order by m.at desc, m.number desc limit $4`, org, member, channelIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := p.fillMentions(c, tx, org, out); err != nil {
		return nil, err
	}
	return out, nil
}
