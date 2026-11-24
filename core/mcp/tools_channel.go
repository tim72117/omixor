package mcp

import (
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

// 訊息頻道工具:channels(列我的頻道,含未讀數)、channel_create(建立;角色端點沒有)、
// channel_messages(讀,讀了算已讀)、channel_post(發)。規則都在 service/channel.go(透過 Backend);
// 名稱、參數同 Swift 版 MCPHandler。@ 角色只通知、不排任務:角色在 agent_context 的 mentions 看得到。

// ChannelToolDefs 訊息頻道工具的規格(接在 CoreToolDefs 後面)
func ChannelToolDefs() []any {
	return []any{
		coreDef("channels",
			"Lists the message channels I can see: id, name, topic, bound issue, members (node and display name), unread count and the time of the last message. A channel puts people and agents into one conversation; mentioning an agent with @ only notifies it and does not queue a task. With issue, finds only the channel bound to that issue.", true,
			obj(map[string]any{
				"issue": pStr("Find only the channel bound to this issue, e.g. ISS-3"),
			})),
		coreDef("channel_create",
			"Creates a message channel (agent identities cannot create one). The creator is a member automatically; members may be agent:<agent id or name>, user:<email>, a bare email, or a bare agent id or name. With issue the channel is bound to that issue: an issue can have only one channel, and if one already exists and is visible it is returned directly (created = false).", false,
			obj(map[string]any{
				"name":    pStr("Channel name (up to 80 characters)"),
				"topic":   pStr("Topic (up to 500 characters)"),
				"issue":   pStr("Id of the issue to bind (optional)"),
				"members": pStrs("Members, e.g. [\"agent:marketing\", \"pm@example.com\"]"),
			}, "name")),
		coreDef("channel_messages",
			"Reads channel messages (oldest to newest). With since (a message id M-n or a time), returns the earliest limit messages after it; without it, returns the latest limit messages. hasMore indicates there are more. Reading marks as read (up to the last returned message). You must be a channel member.", true,
			obj(map[string]any{
				"channel": pStr("Channel id (CH-n) or name"),
				"since":   pStr("Message id (M-n) or an ISO 8601 time"),
				"limit":   pInt("Maximum number of results, default 50, at most 200"),
			}, "channel")),
		coreDef("channel_post",
			"Posts a message to a channel (Markdown, up to 8000 characters; you must be a channel member). Mention channel members with @agent-name, @agent-id or @email: this only notifies them (an agent sees it in mentions of agent_context on its next run) and does not queue a task. The signature is the caller (on the agent endpoint, the credential's agent).", false,
			obj(map[string]any{
				"channel": pStr("Channel id (CH-n) or name"),
				"text":    pStr("Message content (Markdown)"),
			}, "channel", "text")),
	}
}

// channelAs 以誰的身分:角色端點一律是憑證的角色;人類端點是呼叫者(空字串;帶 agent 回 400)
func channelAs(e AgentEnv, c *Call) (string, error) {
	agent, err := identityAgent(c)
	if err != nil {
		return "", err
	}
	if e.Role() == nil {
		return "", nil
	}
	return e.Author(agent)
}

// membersJSON 成員:node(相容字串)、name,和 actor 物件(actors 與 nodes 同順序,服務層補的;數量對不上就不放)
func membersJSON(e AgentEnv, nodes []string, actors []model.ActorView) []any {
	out := []any{}
	for k, n := range nodes {
		m := map[string]any{"node": n, "name": nodeName(e, n)}
		if len(actors) == len(nodes) {
			m["actor"] = actors[k]
		}
		out = append(out, m)
	}
	return out
}

func channelJSON(e AgentEnv, c model.ChannelSummary) map[string]any {
	out := map[string]any{"id": c.ID, "name": c.Name, "topic": c.Topic, "issueId": c.IssueID,
		"members": membersJSON(e, c.Members, c.MemberActors), "unread": c.Unread, "createdBy": nodeName(e, c.CreatedBy)}
	putActor(out, "createdByActor", c.CreatedByActor)
	if c.LastMessageAt != nil {
		out["lastMessageAt"] = iso(c.LastMessageAt.Time)
	} else {
		out["lastMessageAt"] = nil
	}
	return out
}

func messageJSON(e AgentEnv, m model.ChannelMessage) map[string]any {
	mentions := m.Mentions
	if mentions == nil {
		mentions = []string{}
	}
	out := map[string]any{"id": m.ID, "author": m.Author, "authorName": nodeName(e, m.Author), "text": m.Text,
		"mentions": mentions, "at": iso(m.At.Time)}
	putActor(out, "authorActor", m.AuthorActor)
	putActors(out, "mentionActors", m.MentionActors)
	return out
}

// findChannelArg 頻道參數:id(CH-<n>)直接用;否則在看得到的頻道裡依名稱找(不分大小寫)
func findChannelArg(e AgentEnv, raw, as string) (string, error) {
	raw = strings.TrimSpace(raw)
	if (model.Channel{ID: raw}).Number() > 0 {
		return raw, nil
	}
	l, err := e.Backend().ListChannels(e.Org(), service.ChannelFilter{As: as})
	if err != nil {
		return "", terr(err)
	}
	for _, c := range l {
		if strings.EqualFold(c.Name, raw) {
			return c.ID, nil
		}
	}
	return "", Errorf("找不到頻道 %s(或你不是成員);用 channels 看有哪些頻道", raw)
}

// channelMentions agent_context 的 mentions:提到這個角色的未讀訊息(最多 20 則)與它所在頻道的未讀數;取不到回 nil
func channelMentions(e AgentEnv, agentID string) map[string]any {
	x, err := e.Backend().ChannelInbox(e.Org(), model.AgentNode(agentID))
	if err != nil {
		return nil
	}
	msgs := []any{}
	for _, m := range x.Mentions {
		row := map[string]any{"channelId": m.ChannelID, "channelName": m.ChannelName, "id": m.ID,
			"author": m.Author, "authorName": nodeName(e, m.Author), "text": m.Text, "at": iso(m.At.Time)}
		putActor(row, "authorActor", m.AuthorActor)
		msgs = append(msgs, row)
	}
	chs := []any{}
	for _, c := range x.Channels {
		chs = append(chs, map[string]any{"id": c.ID, "name": c.Name, "unread": c.Unread})
	}
	return map[string]any{"messages": msgs, "channels": chs,
		"hint": "提到你的訊息只是通知,不是任務;要回覆用 channel_post,讀完整對話用 channel_messages(讀了算已讀)"}
}

// RegisterChannelTools 登記訊息頻道工具。執行環境(Call.Ctx)要實作 AgentEnv
func RegisterChannelTools(s *Server) {
	s.Register("channels", func(c *Call) (any, error) {
		e := agentEnv(c)
		as, err := channelAs(e, c)
		if err != nil {
			return nil, err
		}
		l, err := e.Backend().ListChannels(e.Org(), service.ChannelFilter{IssueID: c.Args.Str("issue"), As: as})
		if err != nil {
			return nil, terr(err)
		}
		out := []any{}
		for _, x := range l {
			out = append(out, channelJSON(e, x))
		}
		return map[string]any{"channels": out}, nil
	})

	s.Register("channel_create", func(c *Call) (any, error) {
		e := agentEnv(c)
		if e.Role() != nil {
			return nil, Errorf("角色身分不能建立頻道")
		}
		name, err := c.Args.Require("name")
		if err != nil {
			return nil, err
		}
		members, _ := c.Args.Strings("members")
		bk, org := e.Backend(), e.Org()
		existed := map[string]bool{}
		if issue := c.Args.Str("issue"); issue != "" { // created = false:這個 Issue 原本就有(看得到的)頻道
			if l, err := bk.ListChannels(org, service.ChannelFilter{IssueID: issue}); err == nil {
				for _, x := range l {
					existed[x.ID] = true
				}
			}
		}
		x, err := bk.CreateChannel(org, service.ChannelInput{Name: name, Topic: c.Args.Str("topic"),
			IssueID: c.Args.Str("issue"), Members: members})
		if err != nil {
			return nil, terr(err)
		}
		out := channelJSON(e, *x)
		out["created"] = !existed[x.ID]
		return out, nil
	})

	s.Register("channel_messages", func(c *Call) (any, error) {
		e := agentEnv(c)
		raw, err := c.Args.Require("channel")
		if err != nil {
			return nil, err
		}
		as, err := channelAs(e, c)
		if err != nil {
			return nil, err
		}
		bk, org := e.Backend(), e.Org()
		id, err := findChannelArg(e, raw, as)
		if err != nil {
			return nil, err
		}
		ch, err := bk.GetChannel(org, id, as)
		if err != nil {
			return nil, terr(err)
		}
		limit, ok := c.Args.Int("limit")
		if !ok || limit <= 0 {
			limit = 50
		}
		page, err := bk.ListChannelMessages(org, ch.ID, service.MessageListInput{Since: c.Args.Str("since"), Limit: limit, As: as})
		if err != nil {
			return nil, terr(err)
		}
		msgs := []any{}
		for _, m := range page.Messages {
			msgs = append(msgs, messageJSON(e, m))
		}
		if n := len(page.Messages); n > 0 { // 讀了算已讀(不是成員的組織管理員沒有已讀,失敗忽略)
			_, _ = bk.MarkChannelRead(org, ch.ID, service.ReadInput{UpTo: page.Messages[n-1].ID, As: as})
		}
		return map[string]any{
			"channel":  map[string]any{"id": ch.ID, "name": ch.Name, "topic": ch.Topic, "issueId": ch.IssueID, "members": membersJSON(e, ch.Members, ch.MemberActors)},
			"messages": msgs, "hasMore": page.HasMore,
		}, nil
	})

	s.Register("channel_post", func(c *Call) (any, error) {
		e := agentEnv(c)
		raw, err := c.Args.Require("channel")
		if err != nil {
			return nil, err
		}
		text, ok := c.Args.RawString("text")
		if !ok || strings.TrimSpace(text) == "" {
			return nil, Errorf("訊息不能空白")
		}
		as, err := channelAs(e, c)
		if err != nil {
			return nil, err
		}
		id, err := findChannelArg(e, raw, as)
		if err != nil {
			return nil, err
		}
		m, err := e.Backend().PostChannelMessage(e.Org(), id, service.PostInput{Text: text, As: as})
		if err != nil {
			return nil, terr(err)
		}
		return messageJSON(e, *m), nil
	})
}
