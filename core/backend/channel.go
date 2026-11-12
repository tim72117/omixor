package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 訊息頻道的操作:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) ListChannels(string, service.ChannelFilter) ([]model.ChannelSummary, error) {
	return nil, errUnsupported
}
func (Unsupported) GetChannel(string, string, string) (*model.ChannelSummary, error) {
	return nil, errUnsupported
}
func (Unsupported) CreateChannel(string, service.ChannelInput) (*model.ChannelSummary, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateChannel(string, string, service.ChannelPatch) (*model.ChannelSummary, error) {
	return nil, errUnsupported
}
func (Unsupported) ChangeChannelMembers(string, string, service.ChannelMembersInput) (*model.ChannelSummary, error) {
	return nil, errUnsupported
}
func (Unsupported) ListChannelMessages(string, string, service.MessageListInput) (*service.MessagePage, error) {
	return nil, errUnsupported
}
func (Unsupported) PostChannelMessage(string, string, service.PostInput) (*model.ChannelMessage, error) {
	return nil, errUnsupported
}
func (Unsupported) MarkChannelRead(string, string, service.ReadInput) (*model.ChannelRead, error) {
	return nil, errUnsupported
}
func (Unsupported) ChannelInbox(string, string) (*service.ChannelInbox, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error) {
	l.ensureOrg(org)
	return l.Svc.ListChannels(l.P, org, f)
}

func (l *Local) GetChannel(org, id, as string) (*model.ChannelSummary, error) {
	l.ensureOrg(org)
	return l.Svc.GetChannel(l.P, org, id, as)
}

func (l *Local) CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error) {
	l.ensureOrg(org)
	return l.Svc.CreateChannel(l.P, org, in)
}

func (l *Local) UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error) {
	l.ensureOrg(org)
	return l.Svc.UpdateChannel(l.P, org, id, in)
}

func (l *Local) ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error) {
	l.ensureOrg(org)
	return l.Svc.ChangeChannelMembers(l.P, org, id, in)
}

func (l *Local) ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error) {
	l.ensureOrg(org)
	return l.Svc.ListChannelMessages(l.P, org, id, in)
}

func (l *Local) PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error) {
	l.ensureOrg(org)
	return l.Svc.PostChannelMessage(l.P, org, id, in)
}

func (l *Local) MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error) {
	l.ensureOrg(org)
	return l.Svc.MarkChannelRead(l.P, org, id, in)
}

func (l *Local) ChannelInbox(org, who string) (*service.ChannelInbox, error) {
	l.ensureOrg(org)
	return l.Svc.ChannelInbox(l.P, org, who)
}

// ---- Remote ----

func (r *Remote) ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error) {
	x, err := r.Client().ListChannels(r.org(org), f)
	return x, wrap(err)
}

func (r *Remote) GetChannel(org, id, as string) (*model.ChannelSummary, error) {
	x, err := r.Client().GetChannel(r.org(org), id, as)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error) {
	x, err := r.Client().CreateChannel(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error) {
	x, err := r.Client().UpdateChannel(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error) {
	x, err := r.Client().ChangeChannelMembers(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error) {
	x, err := r.Client().ListChannelMessages(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error) {
	x, err := r.Client().PostChannelMessage(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error) {
	x, err := r.Client().MarkChannelRead(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ChannelInbox(org, who string) (*service.ChannelInbox, error) {
	x, err := r.Client().ChannelInbox(r.org(org), who)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}
