package client

import (
	"net/url"
	"strconv"

	"omixor-core/model"
	"omixor-core/service"
)

// 訊息頻道(見 core/api/channel.go;用角色憑證當 token 也可以)

func (c *Client) channels(org string) string { return "/api/orgs/" + esc(org) + "/channels" }

// ListChannels 我看得到的頻道
func (c *Client) ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error) {
	q := url.Values{}
	if f.IssueID != "" {
		q.Set("issue", f.IssueID)
	}
	if f.As != "" {
		q.Set("as", f.As)
	}
	path := c.channels(org)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	out := []model.ChannelSummary{}
	return out, c.Do("GET", path, nil, &out)
}

func (c *Client) GetChannel(org, id, as string) (*model.ChannelSummary, error) {
	path := c.channels(org) + "/" + esc(id)
	if as != "" {
		path += "?as=" + url.QueryEscape(as)
	}
	var x model.ChannelSummary
	return &x, c.Do("GET", path, nil, &x)
}

func (c *Client) CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error) {
	var x model.ChannelSummary
	return &x, c.Do("POST", c.channels(org), in, &x)
}

func (c *Client) UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error) {
	var x model.ChannelSummary
	return &x, c.Do("POST", c.channels(org)+"/"+esc(id), in, &x)
}

func (c *Client) ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error) {
	var x model.ChannelSummary
	return &x, c.Do("POST", c.channels(org)+"/"+esc(id)+"/members", in, &x)
}

func (c *Client) ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error) {
	q := url.Values{}
	if in.Since != "" {
		q.Set("since", in.Since)
	}
	if in.Before != "" {
		q.Set("before", in.Before)
	}
	if in.Limit > 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	if in.As != "" {
		q.Set("as", in.As)
	}
	path := c.channels(org) + "/" + esc(id) + "/messages"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var x service.MessagePage
	return &x, c.Do("GET", path, nil, &x)
}

func (c *Client) PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error) {
	var x model.ChannelMessage
	return &x, c.Do("POST", c.channels(org)+"/"+esc(id)+"/messages", in, &x)
}

func (c *Client) MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error) {
	var x model.ChannelRead
	return &x, c.Do("POST", c.channels(org)+"/"+esc(id)+"/read", in, &x)
}

// ChannelInbox 提到 who(me、agent:<id>、user:<email>)的未讀訊息與所在頻道的未讀數
func (c *Client) ChannelInbox(org, who string) (*service.ChannelInbox, error) {
	path := c.channels(org) + "/inbox"
	if who != "" {
		path += "?for=" + url.QueryEscape(who)
	}
	var x service.ChannelInbox
	return &x, c.Do("GET", path, nil, &x)
}
