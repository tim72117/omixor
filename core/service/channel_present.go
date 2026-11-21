package service

import (
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 頻道的輸出轉換與提及、清理(ISS-36 C-2c):內部的「誰」是 actor id,對外輸出相容的節點寫法 agent:<角色 id> / user:<email>(一個方法一次批次查名稱)

// outSummaries 頻道摘要的成員與建立者換成節點寫法
func (s *Service) outSummaries(list []model.ChannelSummary) ([]model.ChannelSummary, error) {
	ids := []string{}
	for _, c := range list {
		ids = append(ids, c.Members...)
		ids = append(ids, c.CreatedBy)
	}
	names, err := s.LoadActors(ids...)
	if err != nil {
		return nil, err
	}
	out := make([]model.ChannelSummary, len(list))
	for i, c := range list {
		out[i] = c
		out[i].Channel = presentChannel(names, c.Channel)
	}
	return out, nil
}

func presentChannel(names *Names, c model.Channel) model.Channel {
	members := make([]string, 0, len(c.Members))
	for _, m := range c.Members {
		if x := names.Legacy(m, model.LegacyNode); x != "" {
			members = append(members, x)
		}
	}
	c.MemberActors = names.Views(c.Members) // 換成字串前用原本的 actor id 查
	c.CreatedByActor = names.View(c.CreatedBy)
	c.Members = members
	c.CreatedBy = names.Legacy(c.CreatedBy, model.LegacyNode)
	return c
}

// outMessages 訊息的作者與提及換成節點寫法
func (s *Service) outMessages(list []model.ChannelMessage) ([]model.ChannelMessage, error) {
	ids := []string{}
	for _, m := range list {
		ids = append(ids, m.Author)
		ids = append(ids, m.Mentions...)
	}
	names, err := s.LoadActors(ids...)
	if err != nil {
		return nil, err
	}
	out := make([]model.ChannelMessage, len(list))
	for i, m := range list {
		out[i] = m
		out[i].AuthorActor, out[i].MentionActors = names.View(m.Author), names.Views(m.Mentions)
		out[i].Author = names.Legacy(m.Author, model.LegacyNode)
		out[i].Mentions = make([]string, 0, len(m.Mentions))
		for _, x := range m.Mentions {
			if v := names.Legacy(x, model.LegacyNode); v != "" {
				out[i].Mentions = append(out[i].Mentions, v)
			}
		}
	}
	return out, nil
}

// legacyNodes actor id → 節點寫法(稽核用;查不到的略過,失敗回原 id)
func (s *Service) legacyNodes(ids []string) []string {
	names, err := s.LoadActors(ids...)
	if err != nil {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if x := names.Legacy(id, model.LegacyNode); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// mentionCandidates 頻道成員可以被 @ 的寫法:handle(角色 id、email)、顯示名稱(角色名稱、使用者名稱),另外保留舊的 agent:<handle> / user:<email> 寫法
func mentionCandidates(c model.Channel, names *Names) []model.MentionCandidate {
	out := []model.MentionCandidate{}
	for _, id := range c.Members {
		r, ok := names.Record(id)
		if !ok {
			continue
		}
		prefix := model.UserPrefix
		if r.Kind == model.ActorKindAgent {
			prefix = model.AgentPrefix
		}
		if r.Handle != "" {
			out = append(out, model.MentionCandidate{Text: r.Handle, Node: id, Word: true},
				model.MentionCandidate{Text: prefix + r.Handle, Node: id, Word: true})
		}
		if nm := strings.TrimSpace(r.Name); nm != "" && !strings.EqualFold(nm, r.Handle) {
			out = append(out, model.MentionCandidate{Text: nm, Node: id})
		}
	}
	return out
}

// memberMatching 在現有成員(actor id)裡找輸入對到誰(id、handle、名稱、節點寫法,不分大小寫);找不到回空字串
func memberMatching(members []string, names *Names, raw string) string {
	raw = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@")))
	for _, id := range members {
		r, ok := names.Record(id)
		if !ok {
			continue
		}
		prefix := model.UserPrefix
		if r.Kind == model.ActorKindAgent {
			prefix = model.AgentPrefix
		}
		for _, x := range []string{id, r.Handle, r.Name, prefix + r.Handle, names.Legacy(id, model.LegacyNode)} {
			if x != "" && strings.ToLower(x) == raw {
				return id
			}
		}
	}
	return ""
}

// pruneChannelNode 把 actor id 從所有頻道成員拿掉(刪角色、移除成員時;失敗不影響原本的操作)
func (s *Service) pruneChannelNode(org, actorID string) {
	_ = s.pruneChannelNodeOn(s.Store, org, actorID)
}

// pruneChannelNodeOn 同 pruneChannelNode,但在指定的 store(例如 Atomic 的 tx)上做,遇到錯誤就回傳(第一個失敗就停)
func (s *Service) pruneChannelNodeOn(st store.Store, org, actorID string) error {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	all, err := st.ListChannels(org)
	if err != nil {
		return err
	}
	now := s.mt()
	for _, c := range all {
		if !c.HasMember(actorID) {
			continue
		}
		kept := []string{}
		for _, m := range c.Members {
			if m != actorID {
				kept = append(kept, m)
			}
		}
		c.Members, c.UpdatedAt = kept, now
		if err := st.UpdateChannel(org, c); err != nil {
			return err
		}
	}
	return nil
}
