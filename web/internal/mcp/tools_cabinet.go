package mcp

import (
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 文件櫃工具(cabinet、docs、doc_save)與 agent_context 的文件櫃欄位都在核心(omixor-core/mcp/tools_cabinet.go),
// 管理端點 /mcp 與角色端點 /mcp/agent 共用;規格仍是 spec/tools.json(和 Swift 版一樣)。
// 這裡只提供 appBackend 的文件櫃部分:交給 App.CabinetOps(本機內建核心或遠端 omixor server),寫入後更新快取、寫動態、通知畫面。

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
	return b.a.CabinetOps().GetCabinet(org)
}
func (b appBackend) SaveCabinetLayout(org string, l []model.CabinetSection) ([]model.CabinetSection, error) {
	return b.a.CabinetOps().SaveLayout(org, l)
}
func (b appBackend) ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	return b.a.CabinetOps().Import(org, in)
}
func (b appBackend) GetDoc(org, id string) (*model.CabinetDoc, error) {
	return b.a.CabinetOps().GetDoc(org, id)
}
func (b appBackend) CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error) {
	return b.a.CabinetOps().CreateDoc(org, in)
}
func (b appBackend) UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error) {
	return b.a.CabinetOps().UpdateDoc(org, id, p)
}
func (b appBackend) DeleteDoc(org, id string) error { return b.a.CabinetOps().DeleteDoc(org, id) }
func (b appBackend) ReadDoc(org, id string, in service.DocRead) (*service.DocView, error) {
	return b.a.CabinetOps().ReadDoc(org, id, in)
}
func (b appBackend) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	return b.a.CabinetOps().EditDoc(org, id, in)
}
