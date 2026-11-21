// Package mcptoken console 的 MCP 連接埠權杖:產生、檔案存取、遮罩顯示。
// 所有接進 console 的 MCP(/mcp、/mcp/worker)都要帶 Authorization: Bearer <權杖>;權杖由 console 第一次啟動時產生並保存,
// omixor-mcp(stdio 橋接、--channel、--init-worker)讀同一把。保存位置與優先序見 internal/core/mcp_token.go。
package mcptoken

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// Prefix 權杖前綴(和雲端使用者 token 的 omx_ 區分)
	Prefix = "omxm_"
	// FileName console 資料夾裡的權杖檔(權限 0600)
	FileName = "mcp_token"
	// MinLen 權杖至少要這麼長(太短猜得到);環境變數給的不夠長或含空白,console 與 omixor-mcp 直接拒絕啟動,不會悄悄改用別把
	MinLen = 32
)

// New 產生新權杖:omxm_ 加 32 位元組隨機(base64url,無填充)
func New() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Valid 長度夠、沒有空白與控制字元(會放進 HTTP 標頭與 JSON)
func Valid(tok string) bool {
	if len(tok) < MinLen || len(tok) > 512 {
		return false
	}
	for _, c := range tok {
		if c <= ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

// FromEnv 處理環境變數 OMIXOR_CONSOLE_MCP_TOKEN 的值:先去掉結尾的換行與空白(常見的複製貼上、echo 產生的檔案內容),
// 空字串 = 沒設(set=false);有值卻不合格式(太短、含空白或換行或控制字元)回傳錯誤(錯誤訊息不含權杖內容)
func FromEnv(raw string) (tok string, set bool, err error) {
	tok = strings.TrimRight(raw, " \t\r\n")
	if tok == "" {
		return "", false, nil
	}
	if !Valid(tok) {
		return "", true, fmt.Errorf("OMIXOR_CONSOLE_MCP_TOKEN 無效:要至少 %d 字元(最多 512),不能含空白、換行或控制字元(目前長度 %d)。"+
			"請改成夠長的隨機字串(例如 omxm_ 加 openssl rand -hex 24),或清掉這個環境變數讓 console 自己產生並保存", MinLen, len(tok))
	}
	return tok, true, nil
}

// Hint 遮罩顯示:前綴(有的話)加「…」加最後 4 碼;太短只顯示「…」
func Hint(tok string) string {
	if len(tok) < MinLen {
		return "…"
	}
	pre := ""
	if strings.HasPrefix(tok, Prefix) {
		pre = Prefix
	}
	return pre + "…" + tok[len(tok)-4:]
}

// Path 權杖檔的路徑
func Path(dir string) string { return filepath.Join(dir, FileName) }

// ReadFile 讀 dir 底下的權杖檔;不存在回傳 os.ErrNotExist,內容不合格式回傳錯誤(錯誤訊息不含內容)
func ReadFile(dir string) (string, error) {
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if !Valid(tok) {
		return "", fmt.Errorf("%s 的內容不是有效的權杖", Path(dir))
	}
	return tok, nil
}

// WriteFile 寫入 dir 底下的權杖檔(先寫暫存檔再改名,權限 0600;不存在的資料夾會建立,權限 0700)
func WriteFile(dir, tok string) error {
	if !Valid(tok) {
		return errors.New("權杖格式不對")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, FileName+".tmp*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name) // 改名成功後這個路徑已不存在,Remove 無害
	if !isWindows() {
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return err
		}
	} else {
		// Windows 的 chmod 沒有作用:先寫入內容前就用 icacls 收緊(改名後 ACL 跟著檔案走);失敗警告,不中斷
		RestrictOrWarn(name)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, Path(dir))
}

func isWindows() bool { return runtime.GOOS == "windows" }
