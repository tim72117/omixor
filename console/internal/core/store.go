package core

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 這台機器的設定與狀態(連線、模式、語音工作…的 JSON 檔)的存放處,不是組織 / Issue / 成員 / 角色 / 文件櫃的資料來源
// (那些一律在核心 service + PostgreSQL,沒有資料庫就回 errNoDB,不退回存檔案)。
// 預設是 DataRoot() 資料夾裡的 JSON 檔(遠端模式沒有資料庫時就是這樣);設了 OMIXOR_DATABASE_URL 就改存 PostgreSQL 的
// omixor_documents(store_pg.go)。各功能一律透過 ReadJSON / WriteJSON / ReadData / RenameData / DataExists… 讀寫,
// 路徑用 DataRoot() 底下的檔案路徑,由存放處決定實際存哪。
//
// 不在 DataRoot() 底下的檔案(例如 shuttle 專案裡的旁白檔)不經過這裡,照常用 os 讀寫。

// Backend 存放處
type Backend interface {
	// Read key 是相對 DataRoot() 的路徑(用 / 分隔);不存在回傳 fs.ErrNotExist
	Read(key string) ([]byte, error)
	Write(key string, data []byte) error
	Exists(key string) bool
	// Rename 改名一個檔案
	Rename(oldKey, newKey string) error
	// RemoveDir 刪掉 prefix 底下的全部(不能復原)
	RemoveDir(prefix string) error
	// DirExists prefix 底下有沒有任何東西
	DirExists(prefix string) bool
}

// Store 目前的存放處
var Store Backend = fileBackend{}

// keyOf 檔案路徑 → key;不在 DataRoot() 底下回傳 ok=false
func keyOf(path string) (string, bool) {
	rel, err := filepath.Rel(DataRoot(), path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// ReadData 讀資料夾裡的檔案(經過存放處);不在資料夾裡的直接讀檔
func ReadData(path string) ([]byte, error) {
	if k, ok := keyOf(path); ok {
		return Store.Read(k)
	}
	return os.ReadFile(path)
}

// WriteData 寫資料夾裡的檔案(原子寫入)
func WriteData(path string, data []byte) error {
	if k, ok := keyOf(path); ok {
		return Store.Write(k, data)
	}
	return writeFileAtomic(path, data)
}

// DataExists 檔案在不在
func DataExists(path string) bool {
	if k, ok := keyOf(path); ok {
		return Store.Exists(k)
	}
	_, err := os.Stat(path)
	return err == nil
}

// RenameData 改名(讀不出來的檔案改名保留時用)
func RenameData(oldPath, newPath string) error {
	ok1, ok2 := false, false
	var k1, k2 string
	k1, ok1 = keyOf(oldPath)
	k2, ok2 = keyOf(newPath)
	if ok1 && ok2 {
		return Store.Rename(k1, k2)
	}
	return os.Rename(oldPath, newPath)
}

// RemoveDataDir 直接刪掉資料夾裡的整個子資料夾(刪除組織時用;不備份、不能復原)
func RemoveDataDir(dir string) error {
	if k, ok := keyOf(dir); ok {
		return Store.RemoveDir(k)
	}
	return os.RemoveAll(dir)
}

// DataDirExists 子資料夾有沒有東西
func DataDirExists(dir string) bool {
	if k, ok := keyOf(dir); ok {
		return Store.DirExists(k)
	}
	_, err := os.Stat(dir)
	return err == nil
}

// MARK: 檔案

type fileBackend struct{}

func (fileBackend) path(key string) string { return filepath.Join(DataRoot(), filepath.FromSlash(key)) }

func (b fileBackend) Read(key string) ([]byte, error) { return os.ReadFile(b.path(key)) }

func (b fileBackend) Write(key string, data []byte) error { return writeFileAtomic(b.path(key), data) }

func (b fileBackend) Exists(key string) bool {
	_, err := os.Stat(b.path(key))
	return err == nil
}

func (b fileBackend) Rename(oldKey, newKey string) error {
	_ = os.MkdirAll(filepath.Dir(b.path(newKey)), 0o755)
	return os.Rename(b.path(oldKey), b.path(newKey))
}

func (b fileBackend) RemoveDir(prefix string) error { return os.RemoveAll(b.path(prefix)) }

func (b fileBackend) DirExists(prefix string) bool {
	_, err := os.Stat(b.path(prefix))
	return err == nil
}

func writeFileAtomic(path string, data []byte) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// IsNotExist 讀不到是因為不存在
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
