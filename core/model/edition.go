package model

// 版本(edition):編譯時決定這次的 server、本機守護服務、CLI 有哪些功能(功能清單見 features.go 的 Feature.Edition)。
//
//	stable(正式版,預設):只有已實作的真功能
//	dev(開發版):再加上只有範例資料的實驗功能、還在框架階段的功能(例如家庭助理)
//
// 用 build tag 選:沒給是正式版;`go build -tags dev`(go test -tags dev)是開發版。
// BuildEdition 定義在 edition_stable.go / edition_dev.go(依 build tag 二選一)。
//
// 兩層開關:這次編譯有的功能(Feature.Available)裡,組織管理員再決定要開哪些(OrgSystemSettings,存在 core)。

const (
	// EditionStable 正式版
	EditionStable = "stable"
	// EditionDev 開發版
	EditionDev = "dev"
)

// editionRank 版本由小到大;大的版本包含小的版本的所有功能
var editionRank = map[string]int{EditionStable: 0, EditionDev: 1}

// EditionIncludes build 這個版本有沒有包含屬於 e 的功能(不認得的版本一律不包含)
func EditionIncludes(build, e string) bool {
	b, ok1 := editionRank[build]
	x, ok2 := editionRank[e]
	return ok1 && ok2 && x <= b
}

// EditionLabel 版本的中文名稱(錯誤訊息、畫面用)
func EditionLabel(e string) string {
	switch e {
	case EditionStable:
		return "正式版"
	case EditionDev:
		return "開發版"
	}
	return e
}
