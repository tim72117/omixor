package model

// 資料庫結構檢核的報告(伺服器管理員用)。比對「全新資料庫跑完建表後的結構(golden)」與「這台資料庫目前實際的結構」。
// 型別放在 model,讓 api、client、backend 不必依賴 store。

// SchemaReport 是整台資料庫的檢核結果。
// OK = 所有表 ok、沒有缺表與遺留表,而且結構版本沒有落後(SchemaVersion >= ExpectedVersion)。
// 陣列一律不是 nil(輸出 [] 而不是 null),順序依名稱排序。
type SchemaReport struct {
	OK              bool               `json:"ok"`
	SchemaVersion   int                `json:"schemaVersion"`   // 資料庫目前記錄的最大結構版本(沒有版本表 = 0)
	ExpectedVersion int                `json:"expectedVersion"` // 這版程式已知的最大結構版本;SchemaVersion 比它小 = 還有結構異動沒套用(OK 為 false)
	Tables          []SchemaTableCheck `json:"tables"`
	MissingTables   []string           `json:"missingTables"` // golden 有、資料庫沒有
	OrphanTables    []string           `json:"orphanTables"`  // 資料庫有、golden 沒有(遺留表)
}

// SchemaTableCheck 是單一資料表的比對結果(只列 golden 有、資料庫也有的表)。
type SchemaTableCheck struct {
	Table              string                `json:"table"`
	MissingColumns     []string              `json:"missingColumns"`
	ExtraColumns       []string              `json:"extraColumns"`
	TypeMismatches     []SchemaTypeMismatch  `json:"typeMismatches"`
	PrimaryKeyMismatch *SchemaPrimaryKeyDiff `json:"primaryKeyMismatch,omitempty"`
	OK                 bool                  `json:"ok"`
}

// SchemaTypeMismatch 是型別或可否 NULL 不一致的欄位;Expected、Actual 例如 "text not null"、"integer null"。
type SchemaTypeMismatch struct {
	Column   string `json:"column"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// SchemaPrimaryKeyDiff 是主鍵欄位(依主鍵內的順序)不一致時,預期與實際各是什麼。
type SchemaPrimaryKeyDiff struct {
	Expected []string `json:"expected"`
	Actual   []string `json:"actual"`
}
