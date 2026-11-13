package server

// 獨立模式:沒有 macOS App 也能直接啟動 omixor-local(Windows 雙擊、命令列、開機自動啟動)。
//
//   - 資料夾:-data > OMIXOR_DATA_DIR > <os.UserConfigDir()>/omixor/core
//     (macOS:~/Library/Application Support/omixor/core;Windows:%AppData%\omixor\core;Linux:~/.config/omixor/core)。
//     刻意和 macOS App 的資料夾(~/Library/Application Support/Ops/core)分開;就算指到同一個,資料夾鎖也會擋住第二個
//   - 位址:-addr > OMIXOR_ADDR > 127.0.0.1:8770(固定埠,用戶端才知道去哪連);只接受 loopback
//   - token:第一次啟動產生一把,存在 <資料夾>/credentials.json,之後啟動沿用;-rotate-token 換一把新的、舊的作廢。
//     credentials.json 的欄位和 omixor CLI 的登入資料相同(server、token、tokenId、email),
//     CLI 可以直接用:OMIXOR_SERVER=<網址> OMIXOR_CREDENTIALS=<資料夾>/credentials.json omixor orgs
//   - 權限:Unix 上 credentials.json 是 0600(只有自己能讀);Windows 沒有這種權限位元,
//     靠 %AppData% 本身的 ACL(預設只有這個使用者、SYSTEM 與系統管理員能讀)
//   - token 不印在日誌;啟動時只印網址、資料夾與 credentials.json 的位置

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"omixor-core/service"
)

// StandaloneAddr 獨立模式預設的監聽位址
const StandaloneAddr = "127.0.0.1:8770"

// StandaloneCredFile 獨立模式存 token 的檔名(在資料夾裡)
const StandaloneCredFile = "credentials.json"

// userConfigDir 使用者設定資料夾(測試可以換掉)
var userConfigDir = os.UserConfigDir

// DefaultStandaloneDir 獨立模式預設的資料夾:<使用者設定資料夾>/omixor/core
func DefaultStandaloneDir() (string, error) {
	d, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("找不到使用者設定資料夾(請用 -data 或 OMIXOR_DATA_DIR 指定資料夾):%w", err)
	}
	return filepath.Join(d, "omixor", "core"), nil
}

// StandaloneCred <資料夾>/credentials.json:和 omixor CLI 的登入資料同格式(core/cli 的 credentials)
type StandaloneCred struct {
	Server  string `json:"server"`
	Token   string `json:"token"`
	TokenID string `json:"tokenId"`
	Email   string `json:"email"`
}

// StandaloneCredPath 資料夾裡 credentials.json 的路徑
func StandaloneCredPath(dir string) string { return filepath.Join(dir, StandaloneCredFile) }

// ReadStandaloneCred 讀 credentials.json(沒有或讀不出來回傳空的)
func ReadStandaloneCred(dir string) StandaloneCred {
	var c StandaloneCred
	if b, err := os.ReadFile(StandaloneCredPath(dir)); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func validLocalToken(t string) bool {
	return strings.HasPrefix(t, service.TokenPrefix) && len(t) >= len(service.TokenPrefix)+20
}

// writeStandaloneCred 寫 credentials.json(暫存檔 + 改名;Unix 0600,Windows 靠使用者資料夾的 ACL)
func writeStandaloneCred(dir string, c StandaloneCred) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	p := StandaloneCredPath(dir)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	// Windows 上 os.Rename 會取代既有的 credentials.json
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp) // 只清掉自己剛建的暫存檔
		return err
	}
	return nil
}

// OpenStandalone 獨立模式打開資料夾:沿用 credentials.json 的 token(沒有、格式不對或 rotate 時產生新的),
// 裝置名稱用 StandaloneDevice(只作廢獨立模式自己的舊 token,不動 App 模式的)。
// credentials.json 的 token 在 store 裡沒有有效紀錄(被撤銷、換了本機使用者、store.json 不見)時也換新的,
// 不會用舊的雜湊重建紀錄(見 ensureUser);rotate 時這台守護服務發過的所有獨立模式 token 都作廢。
// 打開後馬上把 token 寫回 credentials.json(網址先沿用檔案裡的,啟動好再用 SaveStandaloneCred 補上這次的)
func OpenStandalone(cfg LocalConfig, rotate bool) (*Local, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("要給資料夾")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	old := ReadStandaloneCred(cfg.DataDir)
	cfg.Token, cfg.Device = old.Token, StandaloneDevice
	if rotate || !validLocalToken(cfg.Token) {
		cfg.Token = service.RandID(service.TokenPrefix, 32)
	}
	l, err := OpenLocal(cfg)
	if err != nil {
		return nil, err
	}
	if l.regenerated && !rotate && validLocalToken(old.Token) {
		log.Printf("%s 裡的 token 已作廢或找不到紀錄,換了一把新的", StandaloneCredPath(cfg.DataDir))
	}
	// 先把 token 存起來(網址之後啟動好再補),免得 token 換了檔案卻沒更新
	if err := l.SaveStandaloneCred(old.Server); err != nil {
		return nil, fmt.Errorf("寫 %s 失敗:%w", StandaloneCredPath(cfg.DataDir), err)
	}
	return l, nil
}

// SaveStandaloneCred 把這次的 token 與網址寫進 credentials.json
func (l *Local) SaveStandaloneCred(server string) error {
	return writeStandaloneCred(l.Cfg.DataDir, StandaloneCred{Server: server, Token: l.Cfg.Token, TokenID: l.tokenID, Email: l.user.Email})
}
