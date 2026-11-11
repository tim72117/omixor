//go:build windows

package core

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 沒有 macOS 的鑰匙圈(/usr/bin/security),改用 DPAPI(CryptProtectData)加密後存成檔案:
// <資料夾>/secrets/<帳號名稱的十六進位>.bin。只有同一個 Windows 使用者能解開。
// 設了 OPS_DATABASE_URL 時照舊由資料庫的 ops_secrets 取代(OpenDB 會換掉 Vault)。

func init() {
	if !testing.Testing() {
		Vault = &DPAPIVault{}
	}
}

// DPAPIVault Windows 版的鑰匙圈;Dir 空白表示 DataRoot()/secrets
type DPAPIVault struct {
	Dir string
}

func (v *DPAPIVault) path(account string) string {
	dir := v.Dir
	if dir == "" {
		dir = filepath.Join(DataRoot(), "secrets")
	}
	return filepath.Join(dir, hex.EncodeToString([]byte(account))+".bin")
}

func (v *DPAPIVault) Get(account string) ([]byte, bool) {
	enc, err := os.ReadFile(v.path(account))
	if err != nil {
		return nil, false
	}
	data, err := dpapi(enc, false, account)
	if err != nil {
		return nil, false
	}
	return data, true
}

func (v *DPAPIVault) Set(account string, data []byte) error {
	enc, err := dpapi(data, true, account)
	if err != nil {
		return errors.New("加密失敗:" + err.Error())
	}
	p := v.path(account)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (v *DPAPIVault) Delete(account string) error {
	err := os.Remove(v.path(account))
	if errors.Is(err, os.ErrNotExist) { // 本來就沒有
		return nil
	}
	return err
}

// dpapi 加密或解密;帳號名稱當額外的熵,檔案被改名搬到別的帳號也解不開
func dpapi(in []byte, encrypt bool, account string) ([]byte, error) {
	blob := func(b []byte) *windows.DataBlob {
		if len(b) == 0 { // 空值:DPAPI 不接受空指標,給一個長度 0 的緩衝區
			var z [1]byte
			return &windows.DataBlob{Size: 0, Data: &z[0]}
		}
		return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	}
	entropy := []byte("omixor:" + account)
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(blob(in), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(blob(in), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
