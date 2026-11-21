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

// ---- 專案與 repo 的連結 ----

func (Unsupported) LinkProjectRepo(string, string, string) (*model.Project, error) {
	return nil, errUnsupported
}
func (Unsupported) UnlinkProjectRepo(string, string, string) (*model.Project, error) {
	return nil, errUnsupported
}
func (Unsupported) ListProjectRepos(string, string) ([]model.ProjectRepo, error) {
	return nil, errUnsupported
}

func (l *Local) LinkProjectRepo(org, project, repo string) (*model.Project, error) {
	l.ensureOrg(org)
	return l.Svc.LinkProjectRepo(l.P, org, project, repo)
}

func (l *Local) UnlinkProjectRepo(org, project, repo string) (*model.Project, error) {
	l.ensureOrg(org)
	return l.Svc.UnlinkProjectRepo(l.P, org, project, repo)
}

func (l *Local) ListProjectRepos(org, project string) ([]model.ProjectRepo, error) {
	l.ensureOrg(org)
	return l.Svc.ListProjectRepos(l.P, org, project)
}

func (r *Remote) LinkProjectRepo(org, project, repo string) (*model.Project, error) {
	x, err := r.Client().LinkProjectRepo(r.org(org), project, repo)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UnlinkProjectRepo(org, project, repo string) (*model.Project, error) {
	x, err := r.Client().UnlinkProjectRepo(r.org(org), project, repo)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ListProjectRepos(org, project string) ([]model.ProjectRepo, error) {
	l, err := r.Client().ListProjectRepos(r.org(org), project)
	if err != nil {
		return nil, wrap(err)
	}
	return l, nil
}

// ---- 專案成員 ----

func (Unsupported) ListProjectMembers(string, string) ([]model.ProjectMember, error) {
	return nil, errUnsupported
}
func (Unsupported) SetProjectMember(string, string, service.ProjectMemberInput) (*model.ProjectMember, error) {
	return nil, errUnsupported
}
func (Unsupported) RemoveProjectMember(string, string, string) error { return errUnsupported }

func (l *Local) ListProjectMembers(org, project string) ([]model.ProjectMember, error) {
	l.ensureOrg(org)
	return l.Svc.ListProjectMembers(l.P, org, project)
}

func (l *Local) SetProjectMember(org, project string, in service.ProjectMemberInput) (*model.ProjectMember, error) {
	l.ensureOrg(org)
	return l.Svc.SetProjectMember(l.P, org, project, in)
}

func (l *Local) RemoveProjectMember(org, project, actor string) error {
	l.ensureOrg(org)
	return l.Svc.RemoveProjectMember(l.P, org, project, actor)
}

func (r *Remote) ListProjectMembers(org, project string) ([]model.ProjectMember, error) {
	l, err := r.Client().ListProjectMembers(r.org(org), project)
	if err != nil {
		return nil, wrap(err)
	}
	return l, nil
}

func (r *Remote) SetProjectMember(org, project string, in service.ProjectMemberInput) (*model.ProjectMember, error) {
	x, err := r.Client().SetProjectMember(r.org(org), project, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) RemoveProjectMember(org, project, actor string) error {
	return wrap(r.Client().RemoveProjectMember(r.org(org), project, actor))
}
