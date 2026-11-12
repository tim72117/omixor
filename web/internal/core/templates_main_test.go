package core

import (
	"os"
	"testing"
)

// 預設不載入範本;這個套件的測試要用預設角色、文件櫃結構與示範資料,所以開著(templates/ 在 repo 最外層)
func TestMain(m *testing.M) {
	_ = os.Setenv("OMIXOR_TEMPLATES", "1")
	os.Exit(m.Run())
}
