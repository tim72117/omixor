//go:build windows

package server

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// lockDir 同一個資料夾只能有一個守護服務(Windows:LockFileEx 排他鎖、拿不到立刻失敗;
// 檔案關閉或程式結束時作業系統自動放開)。
// Windows 上 0o700 / 0o600 的權限位元沒有作用(Go 只用它決定唯讀屬性),
// 資料夾的保護靠使用者資料夾(%AppData%)本身的 ACL:預設只有這個使用者與系統管理員能讀
func lockDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, fmt.Errorf("另一個 omixor 本機守護服務正在使用 %s", dir)
	}
	return f, nil
}
