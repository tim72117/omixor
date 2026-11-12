package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃的操作:Unsupported(不支援)、Local(核心 service)、Remote(core/client)三種實作。
// Backend 介面的宣告在 backend.go;權限同 service/cabinet.go。

// ---- Unsupported ----

func (Unsupported) GetCabinet(string) (*model.Cabinet, error) { return nil, errUnsupported }
func (Unsupported) SaveCabinetLayout(string, []model.CabinetSection) ([]model.CabinetSection, error) {
	return nil, errUnsupported
}
func (Unsupported) ImportCabinet(string, service.CabinetImport) (*service.CabinetImportResult, error) {
	return nil, errUnsupported
}
func (Unsupported) GetDoc(string, string) (*model.CabinetDoc, error) { return nil, errUnsupported }
func (Unsupported) CreateDoc(string, service.DocInput) (*model.CabinetDoc, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateDoc(string, string, service.DocPatch) (*model.CabinetDoc, error) {
	return nil, errUnsupported
}
func (Unsupported) DeleteDoc(string, string) error { return errUnsupported }

// ---- Local ----

func (l *Local) GetCabinet(org string) (*model.Cabinet, error) {
	l.ensureOrg(org)
	return l.Svc.GetCabinet(l.P, org)
}

func (l *Local) SaveCabinetLayout(org string, layout []model.CabinetSection) ([]model.CabinetSection, error) {
	l.ensureOrg(org)
	return l.Svc.SaveCabinetLayout(l.P, org, layout)
}

func (l *Local) ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	l.ensureOrg(org)
	return l.Svc.ImportCabinet(l.P, org, in)
}

func (l *Local) GetDoc(org, id string) (*model.CabinetDoc, error) {
	l.ensureOrg(org)
	return l.Svc.GetDoc(l.P, org, id)
}

func (l *Local) CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error) {
	l.ensureOrg(org)
	return l.Svc.CreateDoc(l.P, org, in)
}

func (l *Local) UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error) {
	l.ensureOrg(org)
	return l.Svc.UpdateDoc(l.P, org, id, p)
}

func (l *Local) DeleteDoc(org, id string) error {
	l.ensureOrg(org)
	return l.Svc.DeleteDoc(l.P, org, id)
}

// ---- Remote ----

func (r *Remote) GetCabinet(org string) (*model.Cabinet, error) {
	c, err := r.Client().GetCabinet(r.org(org))
	if err != nil {
		return nil, wrap(err)
	}
	return c, nil
}

func (r *Remote) SaveCabinetLayout(org string, layout []model.CabinetSection) ([]model.CabinetSection, error) {
	l, err := r.Client().SaveCabinetLayout(r.org(org), layout)
	return l, wrap(err)
}

func (r *Remote) ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	res, err := r.Client().ImportCabinet(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return res, nil
}

func (r *Remote) GetDoc(org, id string) (*model.CabinetDoc, error) {
	d, err := r.Client().GetDoc(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return d, nil
}

func (r *Remote) CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error) {
	d, err := r.Client().CreateDoc(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return d, nil
}

func (r *Remote) UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error) {
	d, err := r.Client().UpdateDoc(r.org(org), id, p)
	if err != nil {
		return nil, wrap(err)
	}
	return d, nil
}

func (r *Remote) DeleteDoc(org, id string) error {
	return wrap(r.Client().DeleteDoc(r.org(org), id))
}

// ---- 分段讀寫 ----

func (Unsupported) ReadDoc(string, string, service.DocRead) (*service.DocView, error) {
	return nil, errUnsupported
}
func (Unsupported) EditDoc(string, string, service.DocEdit) (*service.DocEditResult, error) {
	return nil, errUnsupported
}

func (l *Local) ReadDoc(org, id string, in service.DocRead) (*service.DocView, error) {
	l.ensureOrg(org)
	return l.Svc.ReadDoc(l.P, org, id, in)
}

func (l *Local) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	l.ensureOrg(org)
	return l.Svc.EditDoc(l.P, org, id, in)
}

func (r *Remote) ReadDoc(org, id string, in service.DocRead) (*service.DocView, error) {
	v, err := r.Client().ReadDoc(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return v, nil
}

func (r *Remote) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	v, err := r.Client().EditDoc(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return v, nil
}
