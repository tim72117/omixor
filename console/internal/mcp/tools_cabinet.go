package mcp

import (
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 文件櫃工具(cabinet、docs、doc_save)與 agent_context 的文件櫃欄位都在核心(omixor-core/mcp/tools_cabinet.go),
// 管理端點 /mcp 與角色端點 /mcp/agent 共用;規格取自核心(coremcp.CoreToolDefs,見 handler.go 的 webSpec)。
// 這裡只提供 appBackend 的文件櫃與專案部分:交給 App.CabinetOps(本機內建核心或遠端 omixor server),寫入後更新快取、寫動態、通知畫面。

func cabOrgName(a *core.App) string {
	idx := a.Orgs()
	for _, o := range idx.Orgs {
		if o.ID == idx.Current {
			return o.Name
		}
	}
	return idx.Current
}

func (b appBackend) GetCabinet(org string) (*model.Cabinet, error) {
	return b.a.CabinetOpsFor(b.who).GetCabinet(org)
}
func (b appBackend) SaveCabinetLayout(org string, l []model.CabinetSection, expected *int64) ([]model.CabinetSection, error) {
	return b.a.CabinetOpsFor(b.who).SaveLayout(org, l, expected)
}
func (b appBackend) ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	return b.a.CabinetOpsFor(b.who).Import(org, in)
}
func (b appBackend) GetDoc(org, id string) (*model.CabinetDoc, error) {
	return b.a.CabinetOpsFor(b.who).GetDoc(org, id)
}
func (b appBackend) CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error) {
	return b.a.CabinetOpsFor(b.who).CreateDoc(org, in)
}
func (b appBackend) UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error) {
	return b.a.CabinetOpsFor(b.who).UpdateDoc(org, id, p)
}
func (b appBackend) DeleteDoc(org, id string) error {
	return b.a.CabinetOpsFor(b.who).DeleteDoc(org, id)
}
func (b appBackend) ReadDoc(org, id string, in service.DocRead) (*service.DocView, error) {
	return b.a.CabinetOpsFor(b.who).ReadDoc(org, id, in)
}
func (b appBackend) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	return b.a.CabinetOpsFor(b.who).EditDoc(org, id, in)
}
func (b appBackend) GetProjectCabinet(org, project string) (*model.Cabinet, error) {
	return b.a.CabinetOpsFor(b.who).GetProjectCabinet(org, project)
}
func (b appBackend) SaveProjectCabinetLayout(org, project string, l []model.CabinetSection, expected *int64) ([]model.CabinetSection, error) {
	return b.a.CabinetOpsFor(b.who).SaveProjectLayout(org, project, l, expected)
}

// 專案(MCP 工具 projects、project_save 在核心;交給 App.CabinetOps)
func (b appBackend) ListProjects(org string) ([]model.Project, error) {
	return b.a.CabinetOpsFor(b.who).ListProjects(org)
}
func (b appBackend) CreateProject(org string, in service.ProjectInput) (*model.Project, error) {
	return b.a.CabinetOpsFor(b.who).CreateProject(org, in)
}
func (b appBackend) UpdateProject(org, id string, p service.ProjectPatch) (*model.Project, error) {
	return b.a.CabinetOpsFor(b.who).UpdateProject(org, id, p)
}
func (b appBackend) LinkProjectRepo(org, project, repo string) (*model.Project, error) {
	return b.a.CabinetOpsFor(b.who).LinkProjectRepo(org, project, repo)
}
func (b appBackend) UnlinkProjectRepo(org, project, repo string) (*model.Project, error) {
	return b.a.CabinetOpsFor(b.who).UnlinkProjectRepo(org, project, repo)
}
func (b appBackend) ListProjectMembers(org, project string) ([]model.ProjectMember, error) {
	return b.a.CabinetOpsFor(b.who).ListProjectMembers(org, project)
}
func (b appBackend) SetProjectMember(org, project string, in service.ProjectMemberInput) (*model.ProjectMember, error) {
	return b.a.CabinetOpsFor(b.who).SetProjectMember(org, project, in)
}
func (b appBackend) RemoveProjectMember(org, project, actor string) error {
	return b.a.CabinetOpsFor(b.who).RemoveProjectMember(org, project, actor)
}
func (b appBackend) DeleteProject(org, id string) error {
	return b.a.CabinetOpsFor(b.who).DeleteProject(org, id)
}
