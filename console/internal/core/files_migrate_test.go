package core

import (
	"os"
	"path/filepath"
	"testing"
)

// 舊的預設資料夾(OpsWeb)搬到新的(omixor-console)
func TestMigrateDataDir(t *testing.T) {
	base := t.TempDir()
	legacy, dir := filepath.Join(base, "OpsWeb"), filepath.Join(base, "omixor-console")

	// 都不在:用新的(不建立,由 DataRoot 建)
	if got := migrateDataDir(legacy, dir); got != dir {
		t.Fatal("都不在時要用新的", got)
	}

	// 只有舊的:改名成新的,檔案跟著過去
	_ = os.MkdirAll(legacy, 0o755)
	_ = os.WriteFile(filepath.Join(legacy, "orgs.json"), []byte("[]"), 0o644)
	if got := migrateDataDir(legacy, dir); got != dir {
		t.Fatal("要搬到新的", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "orgs.json")); err != nil {
		t.Fatal("檔案要在新資料夾", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("舊資料夾應該已經改名", err)
	}

	// 兩個都在:用新的,不動舊的
	_ = os.MkdirAll(legacy, 0o755)
	if got := migrateDataDir(legacy, dir); got != dir {
		t.Fatal("新的在就用新的", got)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("新的在時不要動舊的", err)
	}

	// 改名失敗:用舊的(新資料夾的上層不存在 → Rename 失敗)
	bad := filepath.Join(base, "no-such-parent", "omixor-console")
	if got := migrateDataDir(legacy, bad); got != legacy {
		t.Fatal("改名失敗要繼續用舊的", got)
	}
}
