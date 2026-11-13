package store

import "omixor-core/model"

// ---- 組織的 GitHub、GitLab 設定的寫入(File):先改記憶體,成功就存檔(讀取直接用 Mem 的) ----

func (f *File) SaveGitHubSettings(org string, s model.GitHubSettings) error {
	return f.write(func() error { return f.Mem.SaveGitHubSettings(org, s) })
}

func (f *File) SaveGitLabSettings(org string, s model.GitLabSettings) error {
	return f.write(func() error { return f.Mem.SaveGitLabSettings(org, s) })
}
