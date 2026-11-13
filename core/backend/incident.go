package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 事件的操作:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) ListIncidents(string) ([]model.Incident, error)      { return nil, errUnsupported }
func (Unsupported) GetIncident(string, string) (*model.Incident, error) { return nil, errUnsupported }
func (Unsupported) DeclareIncident(string, service.IncidentInput) (*model.Incident, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateIncident(string, string, service.IncidentPatch) (*model.Incident, error) {
	return nil, errUnsupported
}
func (Unsupported) ImportIncidents(string, []model.Incident) (*model.ImportResult, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) ListIncidents(org string) ([]model.Incident, error) {
	l.ensureOrg(org)
	return l.Svc.ListIncidents(l.P, org)
}

func (l *Local) GetIncident(org, id string) (*model.Incident, error) {
	l.ensureOrg(org)
	return l.Svc.GetIncident(l.P, org, id)
}

func (l *Local) DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error) {
	l.ensureOrg(org)
	return l.Svc.DeclareIncident(l.P, org, in)
}

func (l *Local) UpdateIncident(org, id string, in service.IncidentPatch) (*model.Incident, error) {
	l.ensureOrg(org)
	return l.Svc.UpdateIncident(l.P, org, id, in)
}

func (l *Local) ImportIncidents(org string, list []model.Incident) (*model.ImportResult, error) {
	l.ensureOrg(org)
	return l.Svc.ImportIncidents(l.P, org, list)
}

// ---- Remote ----

func (r *Remote) ListIncidents(org string) ([]model.Incident, error) {
	x, err := r.Client().ListIncidents(r.org(org))
	return x, wrap(err)
}

func (r *Remote) GetIncident(org, id string) (*model.Incident, error) {
	x, err := r.Client().GetIncident(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error) {
	x, err := r.Client().DeclareIncident(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UpdateIncident(org, id string, in service.IncidentPatch) (*model.Incident, error) {
	x, err := r.Client().UpdateIncident(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ImportIncidents(org string, list []model.Incident) (*model.ImportResult, error) {
	x, err := r.Client().ImportIncidents(r.org(org), list)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}
