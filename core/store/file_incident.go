package store

import "omixor-core/model"

// ---- 事件的寫入(File):先改記憶體,成功就存檔(讀取直接用 Mem 的) ----

func (f *File) CreateIncident(org string, i model.Incident) (model.Incident, error) {
	return w1(f, func() (model.Incident, error) { return f.Mem.CreateIncident(org, i) })
}

func (f *File) UpdateIncident(org string, i model.Incident) error {
	return f.write(func() error { return f.Mem.UpdateIncident(org, i) })
}

func (f *File) ImportIncidents(org string, list []model.Incident) (int, error) {
	return w1(f, func() (int, error) { return f.Mem.ImportIncidents(org, list) })
}
