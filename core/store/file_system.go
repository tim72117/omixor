package store

import "omixor-core/model"

// ---- 組織的系統設定的寫入(File):先改記憶體,成功就存檔(讀取直接用 Mem 的) ----

func (f *File) SaveSystemSettings(org string, s model.OrgSystemSettings) error {
	return f.write(func() error { return f.Mem.SaveSystemSettings(org, s) })
}
