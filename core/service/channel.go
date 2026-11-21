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
//	成員:內部是 actor id;輸入走共用解析器(actor id、@handle、agent:<角色 id>、user:<email>、純 email),也接受角色名稱。角色要存在、人要是組織成員(建立者自己例外);
//	輸出仍是相容的 agent:<角色 id> / user:<email>(channel_present.go)
//	綁 Issue:IssueID 要存在;同一個 Issue 只能有一個頻道 —— 已有而且呼叫者看得到就回傳那個頻道(不新建),看不到 409
//	訊息:去頭尾空白後不能空白(400),最多 model.MaxChannelMessage 字;@ 只算頻道成員(不是成員的 @ 忽略,不報錯)、作者自己不算
//	已讀:每個成員每個頻道一個「讀到第幾則」與時間,只會往前;發訊息時作者的已讀跟著標到那一則
//	@ 角色只通知、不排任務:角色下次執行時 agent_context 的 mentions 看得到(ChannelInbox)
//	以別人身分(As):只有伺服器管理員給的算數(同 Issue 的 author;例如 omixor-web 內建核心、本機 MCP 帶 agent);
//	以角色身分時套用角色的規則(只能碰它是成員的頻道,不能建、不能改成員)
//	刪角色、從組織移除成員:從所有頻道成員拿掉(pruneChannelNode,參數是 actor id)

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
	ChannelID   string `json:"channelId"`
	ChannelName string `json:"channelName"`
	ID          string `json:"id"`
	Author      string `json:"author"`
	// AuthorActor 作者的 actor 物件(ISS-36 C-3)
	AuthorActor *model.ActorView `json:"authorActor,omitempty"`
	Text        string           `json:"text"`
	At          model.Time       `json:"at"`
}

// InboxChannel 節點所在頻道的未讀數
type InboxChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Unread int    `json:"unread"`
}

// ChannelInbox 提到這個節點的未讀訊息(新的在前,最多 InboxLimit 則)與它所在頻道的未讀數
type ChannelInbox struct {
	Node string `json:"node"`
	// NodeActor node 對應的 actor 物件(ISS-36 C-3)
	NodeActor *model.ActorView `json:"nodeActor,omitempty"`
	Mentions  []InboxMention   `json:"mentions"`
	Channels  []InboxChannel   `json:"channels"`
}

// chanActor 這次操作是以哪個 actor 的身分、套用哪種規則
type chanActor struct {
	id    string // actor id
	agent bool   // 角色規則:只能碰自己是成員的頻道
	admin bool   // 組織管理員(看得到全部頻道、可以管成員)
}

func (a chanActor) canSee(c model.Channel) bool {
	return c.HasMember(a.id) || (a.admin && !a.agent)
}

func (a chanActor) canManage(c model.Channel) bool {
	return !a.agent && (c.HasMember(a.id) || a.admin)
}

// channelActor 呼叫者的身分:角色憑證 → 角色自己;伺服器管理員給 as → 那個角色(只能代角色,不能代別的使用者:D9,給了使用者靜默當本人);其他 → 呼叫者本人(Principal.Actor)
func (s *Service) channelActor(p *model.Principal, org, as string) (chanActor, *chainNodes, error) {
	if p.Agent != nil {
		if err := s.viewer(p, org); err != nil {
			return chanActor{}, nil, err
		}
		n, err := s.loadChainNodes(org)
		if err != nil {
			return chanActor{}, nil, err
		}
		self, err := s.SelfActor(p)
		if err != nil {
			return chanActor{}, nil, err
		}
		return chanActor{id: self.ID, agent: true}, n, nil
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return chanActor{}, nil, err
	}
	n, err := s.loadChainNodes(org)
	if err != nil {
		return chanActor{}, nil, err
	}
	self, selfErr := s.selfActorID(org, p)
	a := chanActor{id: self, admin: role == "admin"}
	if as = strings.TrimSpace(as); as != "" && p.ServerAdmin() {
		// D9:管理員的 as 只能是角色;解析不到(400)或解析到別的使用者時,舊式相容模式靜默當本人(不會變成別人的署名或視角)
		id, err := s.resolveNode(p, org, as, n, self, modeChannel)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || e.Code != 400 {
				return chanActor{}, nil, err
			}
		} else if n.isAgent(id) {
			a.id, selfErr = id, nil
			a.agent = true
		}
	}
	if selfErr != nil {
		return chanActor{}, nil, selfErr
	}
	return a, n, nil
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
		if c.HasMember(a.id) {
			mine = append(mine, c.ID)
		}
	}
	unread, err := s.Store.UnreadCounts(org, a.id, mine)
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

// summary 一個頻道的摘要(輸出用:成員與建立者換成相容的節點寫法)
func (s *Service) summary(org string, a chanActor, c model.Channel) (*model.ChannelSummary, error) {
	l, err := s.summaries(org, a, []model.Channel{c})
	if err != nil {
		return nil, err
	}
	if l, err = s.outSummaries(l); err != nil {
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
	if issue != "" { // Issue 編號(UUID、目前或舊的 key)→ 內部 id;找不到就照原字串比(結果是空的)
		if id, err := s.Store.ResolveIssueID(org, issue); err == nil {
			issue = id
		}
	}
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
	return s.outSummaries(out)
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
	members := []string{a.id}
	for _, raw := range in.Members {
		id, err := s.resolveNode(p, org, raw, n, a.id, modeChannel)
		if err != nil {
			return nil, err
		}
		if !hasNode(members, id) {
			members = append(members, id)
		}
	}
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	issueID := strings.TrimSpace(in.IssueID)
	if issueID != "" {
		i, err := s.Store.GetIssue(org, issueID) // 接受 UUID、目前或舊的 key;垃圾桶裡的當作不存在
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
		CreatedBy: a.id, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	s.Audit(p, "channel.create", org, map[string]any{"channel": c.ID, "name": name, "issue": issueID, "members": len(members)})
	return s.summary(org, a, c)
}

func hasNode(list []string, id string) bool {
	for _, x := range list {
		if x == id {
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
		id, err := s.resolveNode(p, org, raw, n, a.id, modeChannel)
		if err != nil {
			return nil, err
		}
		if !c.HasMember(id) {
			c.Members = append(c.Members, id)
			added = append(added, id)
		}
	}
	var memberNames *Names // 拿掉:已經離開的人、已刪的角色解析不到,改用「現有成員」的 handle、名稱、節點寫法比對
	for _, raw := range in.Remove {
		id, err := s.resolveNode(p, org, raw, n, a.id, modeChannel)
		if err != nil {
			if memberNames == nil {
				if memberNames, err = s.LoadActors(c.Members...); err != nil {
					return nil, err
				}
			}
			id = memberMatching(c.Members, memberNames, raw)
		}
		kept := []string{}
		for _, m := range c.Members {
			if m != id {
				kept = append(kept, m)
			}
		}
		if len(kept) != len(c.Members) {
			removed = append(removed, id)
		}
		c.Members = kept
	}
	if len(added)+len(removed) > 0 {
		c.UpdatedAt = s.mt()
		if err := s.Store.UpdateChannel(org, *c); err != nil {
			return nil, err
		}
		s.Audit(p, "channel.members", org, map[string]any{"channel": c.ID, "add": s.legacyNodes(added), "remove": s.legacyNodes(removed)})
	}
	return s.summary(org, a, *c)
}
