// actor_cols.go 「誰」欄位的 NULL 安全讀寫輔助(ISS-36 C-2 共用地基;契約見 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》 第 3 節)
//
// 可以為 NULL 的 actor 欄位(Issue 的 assignee、deleted_by、文件作者、任務的建立者與領取者…,欄位清單見 identity_fields.go:ActorColumns 的 Nullable)
// 沒有「誰」時是 NULL,不是空字串(外鍵不接受空字串)。領域 store 檔直接用 string 掃描會在 NULL 時失敗,所以:
//
//	讀  var by store.NullID; row.Scan(&by); x.CreatedBy = by.String()      (NULL → "")
//	    或 SQL 端 coalesce(col, ''),或掃進 *string(NULL → nil)
//	寫  args 放 store.IDOrNil(x.CreatedBy)                                 ("" → NULL;其餘原樣)
//	    或 store.NullID(x.CreatedBy) 直接當參數(Valuer)
//
// NOT NULL 的欄位(Issue 建立者、留言作者、頻道成員與訊息作者、組織鏈端點)不需要這些:它們永遠有值,用一般 string 即可。

package store

import (
	"database/sql/driver"
	"fmt"
)

// NullID 可為 NULL 的 actor id 欄位:Scan 時 NULL → 空字串;當查詢參數時空字串 → NULL
type NullID string

// Scan 實作 sql.Scanner(pgx 讀 text 欄位時呼叫)
func (n *NullID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*n = ""
	case string:
		*n = NullID(v)
	case []byte:
		*n = NullID(v)
	default:
		return fmt.Errorf("NullID 不能從 %T 掃描", src)
	}
	return nil
}

// Value 實作 driver.Valuer:空字串 → NULL
func (n NullID) Value() (driver.Value, error) {
	if n == "" {
		return nil, nil
	}
	return string(n), nil
}

// String NULL 是空字串
func (n NullID) String() string { return string(n) }

// IDOrNil 寫入可為 NULL 的 actor 欄位:空字串 → nil(NULL),其餘原樣
func IDOrNil(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// IDPtr 同 IDOrNil,但回 *string(給 unnest 的 text[] 陣列元素用:[]*string 的 nil 是 NULL)
func IDPtr(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}
