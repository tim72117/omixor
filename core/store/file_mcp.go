package store

import "omixor-core/model"

// ---- 組織的外部 MCP 登記的寫入(File):先改記憶體,成功就存檔(讀取直接用 Mem 的) ----

func (f *File) SaveMCPConfig(org string, c model.OrgMCPConfig) error {
	return f.write(func() error { return f.Mem.SaveMCPConfig(org, c) })
}
