package model

import "strings"

// 專案:組織底下的一層,目前只有文件櫃(結構與文件)分專案;Issue、頻道、角色、成員、整合、功能開關都還是屬於組織。
//   - 專案可不填:Project 是空字串 = 「未分專案」,就是原本的組織文件櫃(行為不變)
//   - 權限沿用組織成員:組織成員看得到所有專案,沒有另外的專案成員
//   - 每個專案各自一份文件櫃結構;文件仍存在組織底下(DOC-<n> 在組織內唯一),用 CabinetDoc.Project 區分,可以在專案間移動
//   - 角色的 docAccess:沒有前綴的 key 只適用「未分專案」;"<專案 id>:<區塊 id 或頁籤網址>" 適用那個專案(見 DocAccessKey)

// ProjectPrefix 專案 id 的前綴:PRJ-<n>(一個組織內流水號)
const ProjectPrefix = "PRJ-"

// Project 組織底下的專案
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Key 專案代號(例如 APP),專案裡 Issue 的 key 是 <代號>-<n>;空字串 = 還沒設定(不能建 Issue)。規則見 CleanProjectKey
	Key         string `json:"key"`
	Description string `json:"description"`
	CreatedAt   Time   `json:"createdAt"`
	UpdatedAt   Time   `json:"updatedAt"`
}

// Number PRJ-3 → 3(格式不對回 0)
func (p Project) Number() int { return ProjectNumber(p.ID) }

// ProjectNumber PRJ-3 → 3(不分大小寫;格式不對回 0)
func ProjectNumber(id string) int {
	id = strings.TrimSpace(id)
	if len(id) > len(ProjectPrefix)+9 { // 數字最多 9 位
		return 0
	}
	return idNumber(id, ProjectPrefix)
}

// IsProjectID 是不是 PRJ-<n> 的格式(不分大小寫)
func IsProjectID(id string) bool { return ProjectNumber(id) > 0 }

// NormalizeProjectID 專案 id 的標準寫法:去掉前後空白;PRJ-<n> 格式的換成大寫(其他原樣回傳,由呼叫端回 404)
func NormalizeProjectID(id string) string {
	id = strings.TrimSpace(id)
	if IsProjectID(id) {
		return strings.ToUpper(id)
	}
	return id
}

// DocAccessKey 文件櫃位置(區塊 id 或頁籤網址)在角色 docAccess 裡的 key:未分專案就是位置本身,否則 "<專案 id>:<位置>"
func DocAccessKey(project, key string) string {
	if project == "" {
		return key
	}
	return project + ":" + key
}

// SplitDocAccessKey 拆開 docAccess 的 key:"PRJ-1:/a" → ("PRJ-1", "/a");沒有專案前綴回 ("", k)。
// 只有冒號前面是 PRJ-<n> 格式才算前綴(專案 id 換成大寫)
func SplitDocAccessKey(k string) (project, key string) {
	if p, rest, ok := strings.Cut(k, ":"); ok && IsProjectID(p) {
		return strings.ToUpper(strings.TrimSpace(p)), strings.TrimSpace(rest)
	}
	return "", k
}

// NormalizeDocAccessKey docAccess 的 key 的標準寫法(專案前綴換成大寫,其他不動)
func NormalizeDocAccessKey(k string) string {
	p, rest := SplitDocAccessKey(k)
	return DocAccessKey(p, rest)
}
