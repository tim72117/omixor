package mcpauth

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// RestrictToOwner 把含權杖的檔案收緊成只有本人可讀寫:
//   - 非 Windows:chmod 0600
//   - Windows:chmod 在 NTFS 上沒有作用(只影響唯讀旗標),改用 icacls 拿掉繼承來的權限,只留本人完全控制:
//     icacls <檔案> /inheritance:r /grant:r "<網域\使用者>:F"
//
// 失敗要回傳錯誤,呼叫端必須警告使用者,不能默默略過(權杖檔仍可能被同機器的其他使用者讀到)。
func RestrictToOwner(path string) error {
	if runtime.GOOS != "windows" {
		return os.Chmod(path, 0o600)
	}
	who, err := windowsAccount()
	if err != nil {
		return err
	}
	if out, err := icacls(path, "/inheritance:r", "/grant:r", who+":F"); err != nil {
		return fmt.Errorf("icacls 收緊 %s 的權限失敗:%v %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// icacls 與 windowsAccount 是測試用的縫(單元測試換成假的,不真的動 ACL)
var (
	icacls = func(path string, args ...string) ([]byte, error) {
		return exec.Command("icacls", append([]string{path}, args...)...).CombinedOutput()
	}
	windowsAccount = func() (string, error) {
		if u, err := user.Current(); err == nil && u.Username != "" {
			return u.Username, nil // 網域\使用者 或 電腦名稱\使用者
		}
		return "", fmt.Errorf("找不到目前的 Windows 使用者名稱,無法收緊權限")
	}
)

// Warnf 權限收緊失敗時的警告輸出(預設寫到 stderr;呼叫端可以換成 log)
var Warnf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, "警告:"+format+"\n", a...) }

// RestrictOrWarn 收緊權限,失敗時用 Warnf 警告(不中斷);回傳有沒有成功
func RestrictOrWarn(path string) bool {
	if err := RestrictToOwner(path); err != nil {
		Warnf("%s 的權限沒能收緊成只有本人可讀寫(%v);請自己限制這個檔案的存取,檔案內含權杖", path, err)
		return false
	}
	return true
}
