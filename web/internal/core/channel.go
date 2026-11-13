package core

import (
	"fmt"
	"strings"
	"sync/atomic"

	"omixor-core/model"
	"omixor-core/service"
)

// 訊息頻道:資料與規則都在核心(omixor-core 的 service/channel.go):
//
//	本機模式:內建核心 + 本機 PostgreSQL(omixor_channels 等);遠端模式:omixor server 的 /api/orgs/{org}/channels
//
// 這裡是轉接層:AgentOps 的頻道部分(MCP 的 channels、channel_*、agent_context 的 mentions 用),
// 以及畫面的視圖 channels、動作 channel_*。寫入成功後通知畫面(主題 channels)、寫動態牆(建立頻道、@ 角色)。
//
// 身分:本機模式是內建核心的 ops-web(伺服器管理員,節點 user:ops-web);遠端模式是登入雲端的帳號(user:<email>)。

// ChannelsView 對話視圖:我的頻道(含未讀數)、我的節點、組織成員與角色(選成員、顯示頭像用)
type ChannelsView struct {
	Org      string                 `json:"org"`
	Remote   bool                   `json:"remote"`
	Me       string                 `json:"me"`
	Channels []model.ChannelSummary `json:"channels"`
	Members  []model.Member         `json:"members"`
	Agents   []model.Agent          `json:"agents"`
	Error    string                 `json:"error,omitempty"`
}

// channelUnread 最近一次讀到的未讀總數(側邊欄徽章用,不在拿著 Mu 時打網路)
var channelUnread atomic.Int64

// channelsView 視圖(在 Mu 下執行;和組織鏈視圖一樣,遠端模式會在這裡問伺服器)
func (a *App) channelsView() ChannelsView {
	m := a.membersView()
	v := ChannelsView{Org: m.Org, Remote: m.Remote, Members: m.Members, Error: m.Error,
		Channels: []model.ChannelSummary{}, Agents: []model.Agent{}}
	if v.Org == "" {
		return v
	}
	b, err := agentBackend()
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.Me = model.UserNode(b.Whoami())
	l, err := b.ListChannels(v.Org, service.ChannelFilter{})
	if err != nil {
		v.Error = err.Error()
		return v
	}
	if l != nil {
		v.Channels = l
	}
	if g, err := b.ListAgents(v.Org); err == nil && g != nil {
		v.Agents = g
	}
	total := 0
	for _, c := range v.Channels {
		total += c.Unread
	}
	if channelUnread.Swap(int64(total)) != int64(total) {
		go a.Changed("badges")
	}
	return v
}

// channelsChanged 寫入成功後通知畫面
func (o AgentOps) channelsChanged() { o.a.Changed("channels", "badges") }

func (o AgentOps) ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.ListChannels(org, f)
}

func (o AgentOps) GetChannel(org, id, as string) (*model.ChannelSummary, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.GetChannel(org, id, as)
}

// CreateChannel 建立頻道;新建的(不是綁 Issue 已有的那個)寫動態
func (o AgentOps) CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	existed := map[string]bool{}
	if strings.TrimSpace(in.IssueID) != "" {
		if l, err := b.ListChannels(org, service.ChannelFilter{IssueID: in.IssueID}); err == nil {
			for _, c := range l {
				existed[c.ID] = true
			}
		}
	}
	c, err := b.CreateChannel(org, in)
	if err != nil {
		return nil, err
	}
	if !existed[c.ID] {
		o.channelsChanged()
		o.a.Mu.Lock()
		me := o.a.Me
		o.a.Mu.Unlock()
		text := fmt.Sprintf("建立對話頻道「%s」(%d 位成員)", c.Name, len(c.Members))
		if c.IssueID != "" {
			text = fmt.Sprintf("為 %s 建立對話頻道「%s」", c.IssueID, c.Name)
		}
		o.logChannel(Person(me), text)
	}
	return c, nil
}

func (o AgentOps) logChannel(actor Actor, text string) {
	o.a.Mu.Lock()
	o.a.Log(actor, "bubble.left.and.bubble.right", text, "channels")
	o.a.Mu.Unlock()
}

func (o AgentOps) UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	c, err := b.UpdateChannel(org, id, in)
	if err == nil {
		o.channelsChanged()
	}
	return c, err
}

func (o AgentOps) ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	c, err := b.ChangeChannelMembers(org, id, in)
	if err == nil {
		o.channelsChanged()
	}
	return c, err
}

func (o AgentOps) ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.ListChannelMessages(org, id, in)
}

// PostChannelMessage 發訊息;提到角色時寫一筆動態(角色只收到通知,不排任務)
func (o AgentOps) PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	m, err := b.PostChannelMessage(org, id, in)
	if err != nil {
		return nil, err
	}
	o.channelsChanged()
	names := []string{}
	for _, n := range m.Mentions {
		if aid, ok := strings.CutPrefix(n, model.AgentPrefix); ok {
			names = append(names, o.agentName(org, aid))
		}
	}
	if len(names) > 0 {
		chName := m.ChannelID
		if c, err := b.GetChannel(org, m.ChannelID, in.As); err == nil {
			chName = c.Name
		}
		actor := Person(strings.TrimPrefix(m.Author, model.UserPrefix))
		if aid, ok := strings.CutPrefix(m.Author, model.AgentPrefix); ok {
			actor = AI(aid)
		}
		o.logChannel(actor, fmt.Sprintf("在頻道「%s」提到 %s", chName, strings.Join(names, "、")))
	}
	return m, nil
}

func (o AgentOps) MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	r, err := b.MarkChannelRead(org, id, in)
	if err == nil {
		go o.a.Changed("channels", "badges")
	}
	return r, err
}

func (o AgentOps) ChannelInbox(org, who string) (*service.ChannelInbox, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.ChannelInbox(org, who)
}

func init() {
	RegisterView("channels", func(a *App) any { return a.channelsView() })
	OnBadge(func(a *App) map[string]int { return map[string]int{"channels": int(channelUnread.Load())} })

	RegisterAction("channel_create", func(a *App, args map[string]any) (any, error) {
		var in service.ChannelInput
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		in.As = ""
		c, err := a.AgentOps().CreateChannel(a.currentOrgNow(), in)
		return c, agentUserErr(err)
	})
	RegisterAction("channel_update", func(a *App, args map[string]any) (any, error) {
		var in struct {
			ID string `json:"id"`
			service.ChannelPatch
		}
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		in.As = ""
		c, err := a.AgentOps().UpdateChannel(a.currentOrgNow(), in.ID, in.ChannelPatch)
		return c, agentUserErr(err)
	})
	RegisterAction("channel_members", func(a *App, args map[string]any) (any, error) {
		var in struct {
			ID string `json:"id"`
			service.ChannelMembersInput
		}
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		in.As = ""
		c, err := a.AgentOps().ChangeChannelMembers(a.currentOrgNow(), in.ID, in.ChannelMembersInput)
		return c, agentUserErr(err)
	})
	RegisterAction("channel_messages", func(a *App, args map[string]any) (any, error) {
		var in struct {
			ID     string `json:"id"`
			Since  string `json:"since"`
			Before string `json:"before"`
			Limit  int    `json:"limit"`
		}
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		p, err := a.AgentOps().ListChannelMessages(a.currentOrgNow(), in.ID, service.MessageListInput{Since: in.Since, Before: in.Before, Limit: in.Limit})
		return p, agentUserErr(err)
	})
	RegisterAction("channel_post", func(a *App, args map[string]any) (any, error) {
		var in struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		m, err := a.AgentOps().PostChannelMessage(a.currentOrgNow(), in.ID, service.PostInput{Text: in.Text})
		return m, agentUserErr(err)
	})
	RegisterAction("channel_read", func(a *App, args map[string]any) (any, error) {
		var in struct {
			ID   string `json:"id"`
			UpTo string `json:"upTo"`
		}
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		r, err := a.AgentOps().MarkChannelRead(a.currentOrgNow(), in.ID, service.ReadInput{UpTo: in.UpTo})
		return r, agentUserErr(err)
	})
}
