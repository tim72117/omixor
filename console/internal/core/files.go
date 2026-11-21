package core

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"omixor-core/config"
)

// 資料夾:OMIXOR_CONSOLE_DATA_DIR(舊名稱 OPS_DATA_DIR 仍相容),沒設就是 ~/Library/Application Support/omixor-console。
// 裡面的檔案格式和 Swift 版一樣(orgs.json、orgs/<組織>/issues.json…),所以可以把 OMIXOR_CONSOLE_DATA_DIR 指到 Swift 版的
// ~/Library/Application Support/Ops 共用資料;但兩個 app 不要同時開著改同一份資料,後寫的會蓋掉先寫的。
//
// 預設資料夾以前叫 OpsWeb:沒設環境變數、新資料夾不存在而舊的存在時,第一次用到時把舊的改名成新的;
// 改名失敗(跨磁碟、權限、被其他程式開著)就繼續用舊資料夾並記 log。有設環境變數時照環境變數,不搬。

const (
	dataDirName       = "omixor-console"
	legacyDataDirName = "OpsWeb"
)

var (
	defaultRootOnce sync.Once
	defaultRoot     string
)

// DataRoot 回傳資料夾(不存在就建立)
func DataRoot() string {
	dir := config.Current().Str(config.ConsoleDataDir)
	if dir == "" {
		dir = defaultDataRoot()
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// defaultDataRoot 沒設環境變數時的資料夾(只決定一次;需要時把舊的 OpsWeb 搬過來)
func defaultDataRoot() string {
	defaultRootOnce.Do(func() {
		home, _ := os.UserHomeDir()
		base := filepath.Join(home, "Library", "Application Support")
		if testing.Testing() { // 測試沒設資料夾時不搬使用者的資料夾
			defaultRoot = filepath.Join(base, dataDirName)
			return
		}
		defaultRoot = migrateDataDir(filepath.Join(base, legacyDataDirName), filepath.Join(base, dataDirName))
	})
	return defaultRoot
}

// migrateDataDir 新資料夾不存在、舊資料夾存在時把舊的改名成新的,回傳要用的資料夾。
// 新的已經在(或舊的不在)就用新的;改名失敗就用舊的。
func migrateDataDir(legacy, dir string) string {
	if _, err := os.Lstat(dir); err == nil {
		return dir
	}
	fi, err := os.Stat(legacy)
	if err != nil || !fi.IsDir() {
		return dir
	}
	if err := os.Rename(legacy, dir); err != nil {
		log.Printf("資料夾 %s 搬到 %s 失敗,繼續用舊資料夾:%v", legacy, dir, err)
		return legacy
	}
	log.Printf("資料夾已從 %s 搬到 %s", legacy, dir)
	return dir
}

// WriteJSON 寫入(經過存放處:檔案是原子寫入,資料庫是 upsert),兩格縮排、key 排序
func WriteJSON(path string, v any) error { return WriteData(path, MarshalIndent(v)) }

// ReadJSON 讀取解析;不存在回傳 fs.ErrNotExist
func ReadJSON(path string, v any) error {
	b, err := ReadData(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Exists 檔案在不在(資料夾裡的經過存放處)
func Exists(path string) bool { return DataExists(path) }

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
