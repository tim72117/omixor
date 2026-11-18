package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 專案的操作:Unsupported(不支援)、Local(核心 service)、Remote(core/client)三種實作。
// Backend 介面的宣告在 backend.go;權限同 service/project.go。

// ---- Unsupported ----

func (Unsupported) ListProjects(string) ([]model.Project, error) { return nil, errUnsupported }
func (Unsupported) CreateProject(string, service.ProjectInput) (*model.Project, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateProject(string, string, service.ProjectPatch) (*model.Project, error) {
	return nil, errUnsupported
}
func (Unsupported) DeleteProject(string, string) error { return errUnsupported }

// ---- Local ----

func (l *Local) ListProjects(org string) ([]model.Project, error) {
	l.ensureOrg(org)
	return l.Svc.ListProjects(l.P, org)
}

func (l *Local) CreateProject(org string, in service.ProjectInput) (*model.Project, error) {
	l.ensureOrg(org)
	return l.Svc.CreateProject(l.P, org, in)
}

func (l *Local) UpdateProject(org, id string, p service.ProjectPatch) (*model.Project, error) {
	l.ensureOrg(org)
	return l.Svc.UpdateProject(l.P, org, id, p)
}

func (l *Local) DeleteProject(org, id string) error {
	l.ensureOrg(org)
	return l.Svc.DeleteProject(l.P, org, id)
}

// ---- Remote ----

func (r *Remote) ListProjects(org string) ([]model.Project, error) {
	l, err := r.Client().ListProjects(r.org(org))
	if err != nil {
		return nil, wrap(err)
	}
	return l, nil
}

func (r *Remote) CreateProject(org string, in service.ProjectInput) (*model.Project, error) {
	x, err := r.Client().CreateProject(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UpdateProject(org, id string, p service.ProjectPatch) (*model.Project, error) {
	x, err := r.Client().UpdateProject(r.org(org), id, p)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) DeleteProject(org, id string) error {
	return wrap(r.Client().DeleteProject(r.org(org), id))
}
