package service

import (
	"strings"
	"time"

	"omixor-core/model"
)

// 頻道訊息、已讀、提到我的訊息(頻道本身見 channel.go;輸出轉換見 channel_present.go)

// ListChannelMessages 讀訊息(看得到頻道才行;不會自動標已讀,要標用 MarkChannelRead)
func (s *Service) ListChannelMessages(p *model.Principal, org, id string, in MessageListInput) (*MessagePage, error) {
	a, _, err := s.channelActor(p, org, in.As)
	if err != nil {
		return nil, err
	}
	c, err := s.visibleChannel(a, org, id)
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultMessageLimit
	}
	if limit > maxMessageLimit {
		limit = maxMessageLimit
	}
	q := model.MessageQuery{Limit: limit, Latest: true}
	if b := strings.TrimSpace(in.Before); b != "" {
		if q.Before = model.MessageNumber(b); q.Before == 0 {
			return nil, fail(400, "before 要是訊息 id(例如 M-12)")
		}
	}
	var sinceTime *time.Time
	if since := strings.TrimSpace(in.Since); since != "" {
		if num := model.MessageNumber(since); num > 0 {
			q.After, q.Latest = num, false
		} else if t, err := time.Parse(time.RFC3339Nano, since); err == nil {
			sinceTime = &t
		} else {
			return nil, fail(400, "since 要是訊息 id(例如 M-12)或時間(RFC 3339)")
		}
	}
	if sinceTime == nil {
		l, more, err := s.Store.ListChannelMessages(org, c.ID, q)
		if err != nil {
			return nil, err
		}
		if l, err = s.outMessages(l); err != nil {
			return nil, err
		}
		return &MessagePage{Messages: l, HasMore: more}, nil
	}
	// 依時間:讀出範圍內全部再篩(頻道訊息量不大;要翻很多頁的請用訊息 id)
	all, _, err := s.Store.ListChannelMessages(org, c.ID, model.MessageQuery{Before: q.Before})
	if err != nil {
		return nil, err
	}
	out := []model.ChannelMessage{}
	more := false
	for _, m := range all {
		if m.At.After(*sinceTime) {
			if len(out) >= limit {
				more = true
				break
			}
			out = append(out, m)
		}
	}
	if out, err = s.outMessages(out); err != nil {
		return nil, err
	}
	return &MessagePage{Messages: out, HasMore: more}, nil
}

// PostChannelMessage 發訊息(頻道成員;角色身分也可以,署名是角色)。@ 只算頻道成員
func (s *Service) PostChannelMessage(p *model.Principal, org, id string, in PostInput) (*model.ChannelMessage, error) {
	as := in.As
	if strings.TrimSpace(as) == "" {
		as = in.Author
	}
	a, _, err := s.channelActor(p, org, as)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, fail(400, "訊息不能空白")
	}
	if len([]rune(text)) > model.MaxChannelMessage {
		return nil, fail(400, "訊息最多 %d 個字", model.MaxChannelMessage)
	}
	c, err := s.visibleChannel(a, org, id)
	if err != nil {
		return nil, err
	}
	if !c.HasMember(a.id) {
		return nil, fail(403, "你不是頻道 %s 的成員,要先把自己加進去才能發訊息", c.ID)
	}
	names, err := s.LoadActors(c.Members...)
	if err != nil {
		return nil, err
	}
	mentions := []string{}
	for _, m := range model.ParseMentions(text, mentionCandidates(*c, names)) {
		if m != a.id {
			mentions = append(mentions, m)
		}
	}
	msg, err := s.Store.CreateChannelMessage(org, model.ChannelMessage{ChannelID: c.ID, Author: a.id, Text: text, Mentions: mentions, At: s.mt()})
	if err != nil {
		return nil, err
	}
	s.channelMu.Lock()
	_, _ = s.advanceRead(org, c.ID, a.id, msg.Number())
	s.channelMu.Unlock()
	out, err := s.outMessages([]model.ChannelMessage{msg})
	if err != nil {
		return nil, err
	}
	if len(mentions) > 0 {
		s.Audit(p, "channel.mention", org, map[string]any{"channel": c.ID, "message": msg.ID, "author": out[0].Author, "mentions": out[0].Mentions})
	}
	return &out[0], nil
}

// advanceRead 已讀往前推到 num(不倒退;呼叫端拿著 channelMu)
func (s *Service) advanceRead(org, channelID, member string, num int) (model.ChannelRead, error) {
	reads, err := s.Store.ChannelReads(org, member)
	if err != nil {
		return model.ChannelRead{}, err
	}
	r, ok := reads[channelID]
	if ok && r.LastRead >= num {
		return r, nil
	}
	r = model.ChannelRead{ChannelID: channelID, Member: member, LastRead: num, At: s.mt()}
	return r, s.Store.SaveChannelRead(org, r)
}

// MarkChannelRead 標已讀到 UpTo(沒給 = 最新的;只會往前)。要是頻道成員
func (s *Service) MarkChannelRead(p *model.Principal, org, id string, in ReadInput) (*model.ChannelRead, error) {
	as := in.As
	if strings.TrimSpace(as) == "" {
		as = in.Member
	}
	a, _, err := s.channelActor(p, org, as)
	if err != nil {
		return nil, err
	}
	c, err := s.visibleChannel(a, org, id)
	if err != nil {
		return nil, err
	}
	if !c.HasMember(a.id) {
		return nil, fail(403, "你不是頻道 %s 的成員,沒有已讀可以標", c.ID)
	}
	last, _, err := s.Store.ListChannelMessages(org, c.ID, model.MessageQuery{Limit: 1, Latest: true})
	if err != nil {
		return nil, err
	}
	top := 0
	if len(last) > 0 {
		top = last[0].Number()
	}
	num := top
	if u := strings.TrimSpace(in.UpTo); u != "" {
		if num = model.MessageNumber(u); num == 0 {
			return nil, fail(400, "upTo 要是訊息 id(例如 M-12)")
		}
		num = min(num, top)
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	r, err := s.advanceRead(org, c.ID, a.id, num)
	if err != nil {
		return nil, err
	}
	nm, err := s.LoadActors(r.Member)
	if err != nil {
		return nil, err
	}
	r.MemberActor = nm.View(r.Member)
	r.Member = nm.Legacy(r.Member, model.LegacyNode) // 輸出是相容的節點寫法
	return &r, nil
}

// ChannelInbox 提到 who 的未讀訊息(新的在前,最多 InboxLimit 則)與 who 所在頻道的未讀數。
// who:me(呼叫者)、agent:<id 或名稱>、user:<email>。角色身分只能查自己;人可以查組織裡的角色或成員,
// 但只看得到自己也看得到的頻道(組織管理員是全部)
func (s *Service) ChannelInbox(p *model.Principal, org, who string) (*ChannelInbox, error) {
	a, n, err := s.channelActor(p, org, "")
	if err != nil {
		return nil, err
	}
	target := a.id
	if w := strings.TrimSpace(who); w != "" && !strings.EqualFold(w, "me") {
		if target, err = s.resolveNode(p, org, w, n, a.id, modeChannel); err != nil {
			return nil, err
		}
	}
	if a.agent && target != a.id {
		return nil, fail(403, "角色身分只能查自己的訊息")
	}
	all, err := s.Store.ListChannels(org)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	names := map[string]string{}
	for _, c := range all {
		if c.HasMember(target) && a.canSee(c) {
			ids = append(ids, c.ID)
			names[c.ID] = c.Name
		}
	}
	unread, err := s.Store.UnreadCounts(org, target, ids)
	if err != nil {
		return nil, err
	}
	msgs, err := s.Store.UnreadMentions(org, target, ids, InboxLimit)
	if err != nil {
		return nil, err
	}
	authors := []string{target}
	for _, m := range msgs {
		authors = append(authors, m.Author)
	}
	nm, err := s.LoadActors(authors...)
	if err != nil {
		return nil, err
	}
	out := &ChannelInbox{Node: nm.Legacy(target, model.LegacyNode), NodeActor: nm.View(target), Mentions: []InboxMention{}, Channels: []InboxChannel{}}
	for _, id := range ids {
		out.Channels = append(out.Channels, InboxChannel{ID: id, Name: names[id], Unread: unread[id]})
	}
	for _, m := range msgs {
		out.Mentions = append(out.Mentions, InboxMention{ChannelID: m.ChannelID, ChannelName: names[m.ChannelID], ID: m.ID, Author: nm.Legacy(m.Author, model.LegacyNode), AuthorActor: nm.View(m.Author), Text: m.Text, At: m.At})
	}
	return out, nil
}
