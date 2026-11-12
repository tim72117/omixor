package service

import (
	"errors"
	"sort"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// 訊息頻道(資料模型見 model/channel.go):
//
//	建立:組織成員(建立者自動是成員);角色身分不能建
//	看:頻道成員;組織管理員看得到全部(可讀訊息、改名稱主題、加減成員,但發訊息要是成員)
//	頻道成員:讀、發、加減成員、改名稱主題;角色身分(角色憑證)只能看與發自己是成員的頻道,不能改成員、名稱、主題
//	成員:agent:<角色 id> 或 user:<email 小寫>;輸入也接受角色名稱、純角色 id、純 email。角色要存在、人要是組織成員(建立者自己例外)
//	綁 Issue:IssueID 要存在;同一個 Issue 只能有一個頻道 —— 已有而且呼叫者看得到就回傳那個頻道(不新建),看不到 409
//	訊息:去頭尾空白後不能空白(400),最多 model.MaxChannelMessage 字;@ 只算頻道成員(不是成員的 @ 忽略,不報錯)、作者自己不算
//	已讀:每個成員每個頻道一個「讀到第幾則」與時間,只會往前;發訊息時作者的已讀跟著標到那一則
//	@ 角色只通知、不排任務:角色下次執行時 ops_agent_context 的 mentions 看得到(ChannelInbox)
//	以別人身分(As):只有伺服器管理員給的算數(同 Issue 的 author;例如 omixor-web 內建核心、本機 MCP 帶 agent);
//	以角色身分時套用角色的規則(只能碰它是成員的頻道,不能建、不能改成員)
//	刪角色、從組織移除成員:從所有頻道成員拿掉(pruneChannelNode)

const (
	defaultMessageLimit = 50
	maxMessageLimit     = 200
	// InboxLimit ChannelInbox 最多回幾則提到的訊息
	InboxLimit = 20
)

// ChannelInput 建立頻道
type ChannelInput struct {
	Name    string   `json:"name"`
	Topic   string   `json:"topic"`
	IssueID string   `json:"issueId"`
	Members []string `json:"members"`
	As      string   `json:"as,omitempty"`
}

// ChannelPatch 改名稱、主題(nil = 不改)
type ChannelPatch struct {
	Name  *string `json:"name,omitempty"`
	Topic *string `json:"topic,omitempty"`
	As    string  `json:"as,omitempty"`
}

// ChannelMembersInput 加減成員
type ChannelMembersInput struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
	As     string   `json:"as,omitempty"`
}

// ChannelFilter 列頻道的條件
type ChannelFilter struct {
	IssueID string
	As      string
}

// MessageListInput 列訊息:Since 是訊息 id(M-n,回傳之後的)或時間(RFC 3339,回傳之後的);Before 是訊息 id(往前翻頁)。
// 有 Since → 之後最早的 Limit 筆;沒有 → 最新的 Limit 筆。一律由舊到新。Limit 預設 50、最多 200
type MessageListInput struct {
	Since  string
	Before string
	Limit  int
	As     string
}

// MessagePage 一頁訊息
type MessagePage struct {
	Messages []model.ChannelMessage `json:"messages"`
	HasMore  bool                   `json:"hasMore"`
}

// PostInput 發訊息(Author 同 As,給和 Issue 一樣用 author 欄位的用戶端)
type PostInput struct {
	Text   string `json:"text"`
	As     string `json:"as,omitempty"`
	Author string `json:"author,omitempty"`
}

// ReadInput 標已讀:UpTo 是訊息 id(沒給 = 最新的);Member 同 As
type ReadInput struct {
	UpTo   string `json:"upTo"`
	As     string `json:"as,omitempty"`
	Member string `json:"member,omitempty"`
}

// InboxMention 提到某個節點的未讀訊息
type InboxMention struct {
	ChannelID   string     `json:"channelId"`
	ChannelName string     `json:"channelName"`
	ID          string     `json:"id"`
	Author      string     `json:"author"`
	Text        string     `json:"text"`
	At          model.Time `json:"at"`
}

// InboxChannel 節點所在頻道的未讀數
type InboxChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Unread int    `json:"unread"`
}

// ChannelInbox 提到這個節點的未讀訊息(新的在前,最多 InboxLimit 則)與它所在頻道的未讀數
type ChannelInbox struct {
	Node     string         `json:"node"`
	Mentions []InboxMention `json:"mentions"`
	Channels []InboxChannel `json:"channels"`
}

// chanActor 這次操作是以哪個節點的身分、套用哪種規則
type chanActor struct {
	node  string
	agent bool // 角色規則:只能碰自己是成員的頻道
	admin bool // 組織管理員(看得到全部頻道、可以管成員)
}

func (a chanActor) canSee(c model.Channel) bool {
	return c.HasMember(a.node) || (a.admin && !a.agent)
}

func (a chanActor) canManage(c model.Channel) bool {
	return !a.agent && (c.HasMember(a.node) || a.admin)
}

// channelActor 呼叫者的身分:角色憑證 → agent:<自己>;伺服器管理員給 as → 那個節點;其他 → user:<自己的 email>
func (s *Service) channelActor(p *model.Principal, org, as string) (chanActor, *chainNodes, error) {
	if p.Agent != nil {
		if err := s.viewer(p, org); err != nil {
			return chanActor{}, nil, err
		}
		n, err := s.loadChainNodes(org)
		if err != nil {
			return chanActor{}, nil, err
		}
		return chanActor{node: model.AgentNode(p.Agent.AgentID), agent: true}, n, nil
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return chanActor{}, nil, err
	}
	n, err := s.loadChainNodes(org)
	if err != nil {
		return chanActor{}, nil, err
	}
	a := chanActor{node: model.UserNode(p.User.Email), admin: role == "admin"}
	if as := strings.TrimSpace(as); as != "" && p.ServerAdmin() && !strings.EqualFold(as, a.node) {
		node, err := n.resolveChannelNode(as, "")
		if err != nil {
			return chanActor{}, nil, err
		}
		a.node = node
		a.agent = strings.HasPrefix(node, model.AgentPrefix)
	}
	if strings.TrimSpace(p.User.Email) == "" && !a.agent && a.node == model.UserPrefix {
		return chanActor{}, nil, fail(400, "無法判斷你的 email,不能使用訊息頻道")
	}
	return a, n, nil
}

// resolveChannelNode 成員的輸入換成節點:agent:<id 或名稱>、user:<email>、純 email、純角色 id 或名稱。
// self 是呼叫者自己的節點(人不是組織成員也可以加自己,例如伺服器管理員)
func (n *chainNodes) resolveChannelNode(raw, self string) (string, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if raw == "" {
		return "", fail(400, "成員不能空白")
	}
	findAgent := func(x string) (string, bool) {
		x = strings.TrimSpace(x)
		for id := range n.agents {
			if strings.EqualFold(id, x) {
				return model.AgentNode(id), true
			}
		}
		for id, g := range n.agents {
			if strings.EqualFold(g.Name, x) {
				return model.AgentNode(id), true
			}
		}
		return "", false
	}
	user := func(e string) (string, error) {
		node := model.UserNode(e)
		if _, ok := n.members[strings.TrimPrefix(node, model.UserPrefix)]; ok || node == self {
			return node, nil
		}
		return "", fail(400, "%s 不是組織成員,不能加進頻道", strings.TrimPrefix(node, model.UserPrefix))
	}
	if rest, ok := cutPrefixFold(raw, model.AgentPrefix); ok {
		if node, ok := findAgent(rest); ok {
			return node, nil
		}
		return "", fail(400, "找不到角色 %s", strings.TrimSpace(rest))
	}
	if rest, ok := cutPrefixFold(raw, model.UserPrefix); ok {
		return user(rest)
	}
	if strings.Contains(raw, "@") {
		return user(raw)
	}
	if node, ok := findAgent(raw); ok {
		return node, nil
	}
	return "", fail(400, "找不到成員 %s(要寫 agent:<角色 id>、角色名稱、user:<email> 或 email)", raw)
}

// mentionCandidates 頻道成員可以被 @ 的寫法:角色名稱、角色 id、email、完整節點
func (n *chainNodes) mentionCandidates(c model.Channel) []model.MentionCandidate {
	out := []model.MentionCandidate{}
	for _, m := range c.Members {
		out = append(out, model.MentionCandidate{Text: m, Node: m, Word: true})
		if id, ok := strings.CutPrefix(m, model.AgentPrefix); ok {
			out = append(out, model.MentionCandidate{Text: id, Node: m, Word: true})
			if g, ok := n.agents[id]; ok && strings.TrimSpace(g.Name) != "" {
				out = append(out, model.MentionCandidate{Text: g.Name, Node: m})
			}
		} else if e, ok := strings.CutPrefix(m, model.UserPrefix); ok {
			out = append(out, model.MentionCandidate{Text: e, Node: m, Word: true})
		}
	}
	return out
}

// findChannel 依 id(不分大小寫)找頻道;不存在 404
func (s *Service) findChannel(org, id string) (*model.Channel, error) {
	id = strings.TrimSpace(id)
	c, err := s.Store.GetChannel(org, id)
	if errors.Is(err, store.ErrNotFound) && id != strings.ToUpper(id) {
		c, err = s.Store.GetChannel(org, strings.ToUpper(id))
	}
	if errors.Is(err, store.ErrNotFound) || (err == nil && c == nil) {
		return nil, fail(404, "找不到頻道 %s", id)
	}
	return c, err
}

// visibleChannel 找頻道並確認看得到(看不到也回 404,不洩漏存在)
func (s *Service) visibleChannel(a chanActor, org, id string) (*model.Channel, error) {
	c, err := s.findChannel(org, id)
	if err != nil {
		return nil, err
	}
	if !a.canSee(*c) {
		return nil, fail(404, "找不到頻道 %s(或你不是這個頻道的成員)", strings.TrimSpace(id))
	}
	return c, nil
}

// summaries 頻道加上 a 的未讀數與最後一則訊息時間
func (s *Service) summaries(org string, a chanActor, list []model.Channel) ([]model.ChannelSummary, error) {
	last, err := s.Store.LastChannelMessages(org)
	if err != nil {
		return nil, err
	}
	mine := []string{}
	for _, c := range list {
		if c.HasMember(a.node) {
			mine = append(mine, c.ID)
		}
	}
	unread, err := s.Store.UnreadCounts(org, a.node, mine)
	if err != nil {
		return nil, err
	}
	out := []model.ChannelSummary{}
	for _, c := range list {
		x := model.ChannelSummary{Channel: c, Unread: unread[c.ID]}
		if m, ok := last[c.ID]; ok {
			t := m.At
			x.LastMessageAt = &t
		}
		out = append(out, x)
	}
	return out, nil
}

func (s *Service) summary(org string, a chanActor, c model.Channel) (*model.ChannelSummary, error) {
	l, err := s.summaries(org, a, []model.Channel{c})
	if err != nil {
		return nil, err
	}
	return &l[0], nil
}

// ListChannels 我看得到的頻道(成員;組織管理員是全部),最近有動靜的在前;IssueID 不空時只列綁這個 Issue 的
func (s *Service) ListChannels(p *model.Principal, org string, f ChannelFilter) ([]model.ChannelSummary, error) {
	a, _, err := s.channelActor(p, org, f.As)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.ListChannels(org)
	if err != nil {
		return nil, err
	}
	issue := strings.TrimSpace(f.IssueID)
	list := []model.Channel{}
	for _, c := range all {
		if a.canSee(c) && (issue == "" || strings.EqualFold(c.IssueID, issue)) {
			list = append(list, c)
		}
	}
	out, err := s.summaries(org, a, list)
	if err != nil {
		return nil, err
	}
	active := func(x model.ChannelSummary) time.Time {
		if x.LastMessageAt != nil && x.LastMessageAt.After(x.UpdatedAt.Time) {
			return x.LastMessageAt.Time
		}
		return x.UpdatedAt.Time
	}
	sort.SliceStable(out, func(i, j int) bool { return active(out[i]).After(active(out[j])) })
	return out, nil
}

// GetChannel 一個頻道(看得到才行)
func (s *Service) GetChannel(p *model.Principal, org, id, as string) (*model.ChannelSummary, error) {
	a, _, err := s.channelActor(p, org, as)
	if err != nil {
		return nil, err
	}
	c, err := s.visibleChannel(a, org, id)
	if err != nil {
		return nil, err
	}
	return s.summary(org, a, *c)
}

func cleanChannelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fail(400, "要給頻道名稱")
	}
	if len([]rune(name)) > model.MaxChannelName {
		return "", fail(400, "頻道名稱最多 %d 個字", model.MaxChannelName)
	}
	return name, nil
}

func cleanChannelTopic(topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if len([]rune(topic)) > model.MaxChannelTopic {
		return "", fail(400, "主題最多 %d 個字", model.MaxChannelTopic)
	}
	return topic, nil
}

// CreateChannel 建立頻道(組織成員;角色身分不行)。綁的 Issue 已經有頻道:看得到就回傳那個,看不到 409
func (s *Service) CreateChannel(p *model.Principal, org string, in ChannelInput) (*model.ChannelSummary, error) {
	a, n, err := s.channelActor(p, org, in.As)
	if err != nil {
		return nil, err
	}
	if a.agent {
		return nil, fail(403, "角色身分不能建立頻道")
	}
	name, err := cleanChannelName(in.Name)
	if err != nil {
		return nil, err
	}
	topic, err := cleanChannelTopic(in.Topic)
	if err != nil {
		return nil, err
	}
	members := []string{a.node}
	for _, raw := range in.Members {
		node, err := n.resolveChannelNode(raw, a.node)
		if err != nil {
			return nil, err
		}
		if !hasNode(members, node) {
			members = append(members, node)
		}
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	issueID := strings.TrimSpace(in.IssueID)
	if issueID != "" {
		i, err := s.Store.GetIssue(org, issueID)
		if errors.Is(err, store.ErrNotFound) {
			i, err = s.Store.GetIssue(org, strings.ToUpper(issueID))
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, fail(404, "找不到 Issue %s", issueID)
		}
		if err != nil {
			return nil, err
		}
		issueID = i.ID
		all, err := s.Store.ListChannels(org)
		if err != nil {
			return nil, err
		}
		for _, c := range all {
			if c.IssueID == issueID {
				if a.canSee(c) {
					return s.summary(org, a, c)
				}
				return nil, fail(409, "%s 已經有對話頻道 %s,請頻道成員把你加進去", issueID, c.ID)
			}
		}
	}
	now := s.mt()
	c, err := s.Store.CreateChannel(org, model.Channel{Name: name, Topic: topic, IssueID: issueID, Members: members,
		CreatedBy: a.node, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	s.Audit(p, "channel.create", org, map[string]any{"channel": c.ID, "name": name, "issue": issueID, "members": len(members)})
	return s.summary(org, a, c)
}

func hasNode(list []string, node string) bool {
	for _, x := range list {
		if x == node {
			return true
		}
	}
	return false
}

// UpdateChannel 改名稱、主題(頻道成員或組織管理員;角色身分不行)
func (s *Service) UpdateChannel(p *model.Principal, org, id string, in ChannelPatch) (*model.ChannelSummary, error) {
	a, _, err := s.channelActor(p, org, in.As)
	if err != nil {
		return nil, err
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	c, err := s.visibleChannel(a, org, id)
	if err != nil {
		return nil, err
	}
	if !a.canManage(*c) {
		return nil, fail(403, "角色身分不能改頻道設定")
	}
	if in.Name != nil {
		if c.Name, err = cleanChannelName(*in.Name); err != nil {
			return nil, err
		}
	}
	if in.Topic != nil {
		if c.Topic, err = cleanChannelTopic(*in.Topic); err != nil {
			return nil, err
		}
	}
	c.UpdatedAt = s.mt()
	if err := s.Store.UpdateChannel(org, *c); err != nil {
		return nil, err
	}
	s.Audit(p, "channel.update", org, map[string]any{"channel": c.ID, "name": c.Name})
	return s.summary(org, a, *c)
}

// ChangeChannelMembers 加減成員(頻道成員或組織管理員;角色身分不行)。拿掉不存在的成員不算錯
func (s *Service) ChangeChannelMembers(p *model.Principal, org, id string, in ChannelMembersInput) (*model.ChannelSummary, error) {
	a, n, err := s.channelActor(p, org, in.As)
	if err != nil {
		return nil, err
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	c, err := s.visibleChannel(a, org, id)
	if err != nil {
		return nil, err
	}
	if !a.canManage(*c) {
		return nil, fail(403, "角色身分不能改頻道成員")
	}
	added, removed := []string{}, []string{}
	for _, raw := range in.Add {
		node, err := n.resolveChannelNode(raw, a.node)
		if err != nil {
			return nil, err
		}
		if !c.HasMember(node) {
			c.Members = append(c.Members, node)
			added = append(added, node)
		}
	}
	for _, raw := range in.Remove {
		node, err := n.resolveChannelNode(raw, a.node)
		if err != nil {
			// 已經不存在的角色或已離開組織的人:照原本的寫法拿掉
			node = strings.TrimSpace(raw)
		}
		kept := []string{}
		for _, m := range c.Members {
			if m != node {
				kept = append(kept, m)
			}
		}
		if len(kept) != len(c.Members) {
			removed = append(removed, node)
		}
		c.Members = kept
	}
	if len(added)+len(removed) > 0 {
		c.UpdatedAt = s.mt()
		if err := s.Store.UpdateChannel(org, *c); err != nil {
			return nil, err
		}
		s.Audit(p, "channel.members", org, map[string]any{"channel": c.ID, "add": added, "remove": removed})
	}
	return s.summary(org, a, *c)
}

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
	return &MessagePage{Messages: out, HasMore: more}, nil
}

// PostChannelMessage 發訊息(頻道成員;角色身分也可以,署名是角色)。@ 只算頻道成員
func (s *Service) PostChannelMessage(p *model.Principal, org, id string, in PostInput) (*model.ChannelMessage, error) {
	as := in.As
	if strings.TrimSpace(as) == "" {
		as = in.Author
	}
	a, n, err := s.channelActor(p, org, as)
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
	if !c.HasMember(a.node) {
		return nil, fail(403, "你不是頻道 %s 的成員,要先把自己加進去才能發訊息", c.ID)
	}
	mentions := []string{}
	for _, m := range model.ParseMentions(text, n.mentionCandidates(*c)) {
		if m != a.node {
			mentions = append(mentions, m)
		}
	}
	msg, err := s.Store.CreateChannelMessage(org, model.ChannelMessage{ChannelID: c.ID, Author: a.node, Text: text, Mentions: mentions, At: s.mt()})
	if err != nil {
		return nil, err
	}
	s.channelMu.Lock()
	_, _ = s.advanceRead(org, c.ID, a.node, msg.Number())
	s.channelMu.Unlock()
	if len(mentions) > 0 {
		s.Audit(p, "channel.mention", org, map[string]any{"channel": c.ID, "message": msg.ID, "author": a.node, "mentions": mentions})
	}
	return &msg, nil
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
	if !c.HasMember(a.node) {
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
	r, err := s.advanceRead(org, c.ID, a.node, num)
	if err != nil {
		return nil, err
	}
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
	target := a.node
	if w := strings.TrimSpace(who); w != "" && !strings.EqualFold(w, "me") {
		if target, err = n.resolveChannelNode(w, a.node); err != nil {
			return nil, err
		}
	}
	if a.agent && target != a.node {
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
	out := &ChannelInbox{Node: target, Mentions: []InboxMention{}, Channels: []InboxChannel{}}
	for _, id := range ids {
		out.Channels = append(out.Channels, InboxChannel{ID: id, Name: names[id], Unread: unread[id]})
	}
	for _, m := range msgs {
		out.Mentions = append(out.Mentions, InboxMention{ChannelID: m.ChannelID, ChannelName: names[m.ChannelID], ID: m.ID, Author: m.Author, Text: m.Text, At: m.At})
	}
	return out, nil
}

// pruneChannelNode 把節點從所有頻道成員拿掉(刪角色、移除成員時;失敗不影響原本的操作)
func (s *Service) pruneChannelNode(org, node string) {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	all, err := s.Store.ListChannels(org)
	if err != nil {
		return
	}
	now := s.mt()
	for _, c := range all {
		if !c.HasMember(node) {
			continue
		}
		kept := []string{}
		for _, m := range c.Members {
			if m != node {
				kept = append(kept, m)
			}
		}
		c.Members, c.UpdatedAt = kept, now
		_ = s.Store.UpdateChannel(org, c)
	}
}
