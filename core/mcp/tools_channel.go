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
			"列出我(或 agent 角色)看得到的訊息頻道:id、名稱、主題、綁的 Issue、成員(node 與顯示名稱)、未讀數與最後一則訊息的時間。頻道把人與虛擬角色拉進同一個對話;@ 角色只通知、不排任務。給 issue 只找綁該 Issue 的頻道。", true,
			obj(map[string]any{
				"agent": pStr("以角色身分查看時填角色 id 或名稱(只列它是成員的頻道,未讀數是它的)"),
				"issue": pStr("只找綁這個 Issue 的頻道,例如 ISS-3"),
			})),
		coreDef("channel_create",
			"建立訊息頻道(角色身分不能建)。建立者自動是成員;members 可以給 agent:<角色 id 或名稱>、user:<email>、純 email、純角色 id 或名稱。給 issue 會綁該 Issue:同一個 Issue 只能有一個頻道,已有而且看得到時直接回傳那個(created = false)。", false,
			obj(map[string]any{
				"name":    pStr("頻道名稱(最多 80 字)"),
				"topic":   pStr("主題(最多 500 字)"),
				"issue":   pStr("綁的 Issue id(選填)"),
				"members": pStrs("成員,例如 [\"agent:marketing\", \"pm@example.com\"]"),
			}, "name")),
		coreDef("channel_messages",
			"讀頻道訊息(依時間由舊到新)。給 since(訊息 id M-n 或時間)回傳之後最早的 limit 筆;沒給回傳最新的 limit 筆。hasMore 表示還有更多。讀了算已讀(標到回傳的最後一則)。帶 agent 時以該角色身分讀(要是頻道成員)。", true,
			obj(map[string]any{
				"channel": pStr("頻道 id(CH-n)或名稱"),
				"since":   pStr("訊息 id(M-n)或 ISO 8601 時間"),
				"limit":   pInt("最多幾筆,預設 50、最多 200"),
				"agent":   pStr("以角色身分讀時填角色 id 或名稱"),
			}, "channel")),
		coreDef("channel_post",
			"在頻道發一則訊息(Markdown,最多 8000 字;要是頻道成員)。用 @角色名稱、@角色 id 或 @email 提到頻道成員:只通知對方(角色下次執行時在 agent_context 的 mentions 看得到),不會排任務。帶 agent 時以該角色署名(角色要是頻道成員)。", false,
			obj(map[string]any{
				"channel": pStr("頻道 id(CH-n)或名稱"),
				"text":    pStr("訊息內容(Markdown)"),
				"agent":   pStr("以角色身分發時填角色 id 或名稱"),
			}, "channel", "text")),
	}
}

// channelAs 以誰的身分:角色端點一律是憑證的角色;管理端點有給 agent 就是該角色(service 只認伺服器管理員給的),沒給是呼叫者
func channelAs(e AgentEnv, c *Call) (string, error) {
	agent := c.Args.Str("agent")
	if e.Role() == nil && strings.TrimSpace(agent) == "" {
		return "", nil
	}
	return e.Author(agent)
}

func membersJSON(e AgentEnv, nodes []string) []any {
	out := []any{}
	for _, n := range nodes {
		out = append(out, map[string]any{"node": n, "name": nodeName(e, n)})
	}
	return out
}

func channelJSON(e AgentEnv, c model.ChannelSummary) map[string]any {
	out := map[string]any{"id": c.ID, "name": c.Name, "topic": c.Topic, "issueId": c.IssueID,
		"members": membersJSON(e, c.Members), "unread": c.Unread, "createdBy": nodeName(e, c.CreatedBy)}
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
	return map[string]any{"id": m.ID, "author": m.Author, "authorName": nodeName(e, m.Author), "text": m.Text,
		"mentions": mentions, "at": iso(m.At.Time)}
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
		msgs = append(msgs, map[string]any{"channelId": m.ChannelID, "channelName": m.ChannelName, "id": m.ID,
			"author": m.Author, "authorName": nodeName(e, m.Author), "text": m.Text, "at": iso(m.At.Time)})
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
			"channel":  map[string]any{"id": ch.ID, "name": ch.Name, "topic": ch.Topic, "issueId": ch.IssueID, "members": membersJSON(e, ch.Members)},
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
