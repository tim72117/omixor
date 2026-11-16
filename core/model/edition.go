package model

// 版本(edition):以前編譯時用 build tag 決定 server、本機守護服務、CLI 有哪些功能;現在所有功能都編進去,
// 改由兩層開關決定(都存在 core 的資料庫,見 features.go):
//
//	系統層(ServerFeatures):伺服器管理員在「系統」頁決定這台伺服器開放哪些功能
//	組織層(OrgSystemSettings):組織管理員在「設定 › 功能」決定自己組織要用哪些
//
// Feature.Edition 保留當作資料:決定系統層的預設值(stable 預設開放、dev 預設不開放)。
// -tags dev 已經沒有作用(編出來和沒給一樣)。

const (
	// EditionStable 原本的正式版功能(系統層預設開放)
	EditionStable = "stable"
	// EditionDev 原本只有開發版的功能(系統層預設不開放)
	EditionDev = "dev"
)

// EditionLabel 版本的中文名稱(相容欄位與畫面用)
func EditionLabel(e string) string {
	switch e {
	case EditionStable:
		return "正式版"
	case EditionDev:
		return "開發版"
	}
	return e
}
