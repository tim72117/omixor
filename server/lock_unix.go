//go:build !windows

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDir 同一個資料夾只能有一個守護服務(flock;程式結束時作業系統自動放開)。
// 0o700 / 0o600 在 Unix 上就是「只有自己能讀寫」
func lockDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("另一個 omixor 本機守護服務正在使用 %s", dir)
	}
	return f, nil
}
