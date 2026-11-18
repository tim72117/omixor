package store

import "omixor-core/model"

// ---- 專案的寫入(File):先改記憶體,成功就存檔(讀取直接用 Mem 的) ----

func (f *File) CreateProject(org string, p model.Project) (model.Project, error) {
	return w1(f, func() (model.Project, error) { return f.Mem.CreateProject(org, p) })
}

func (f *File) UpdateProject(org string, p model.Project) error {
	return f.write(func() error { return f.Mem.UpdateProject(org, p) })
}

func (f *File) DeleteProject(org, id string) error {
	return f.write(func() error { return f.Mem.DeleteProject(org, id) })
}
