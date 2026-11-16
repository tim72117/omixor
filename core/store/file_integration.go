package store

// ---- 組織設定文件的寫入(File):先改記憶體,成功就存檔(讀取直接用 Mem 的) ----

func (f *File) SaveOrgDoc(org, kind string, d OrgDoc) error {
	return f.write(func() error { return f.Mem.SaveOrgDoc(org, kind, d) })
}
