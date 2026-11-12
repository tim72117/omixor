package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// 資料夾:OPS_DATA_DIR,沒設就是 ~/Library/Application Support/OpsWeb。
// 裡面的檔案格式和 Swift 版一樣(orgs.json、orgs/<組織>/issues.json…),所以可以把 OPS_DATA_DIR 指到 Swift 版的
// ~/Library/Application Support/Ops 共用資料;但兩個 app 不要同時開著改同一份資料,後寫的會蓋掉先寫的。

// DataRoot 回傳資料夾(不存在就建立)
func DataRoot() string {
	dir := os.Getenv("OPS_DATA_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Library", "Application Support", "OpsWeb")
	}
	_ = os.MkdirAll(dir, 0o755)
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
