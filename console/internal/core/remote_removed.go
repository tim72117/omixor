package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"omixor-core/config"
)

// console 以前有「遠端模式」(自己沒有資料,代理到另一台 omixor server),已經移除。這裡只剩升級防呆:
// 還設定成遠端模式的安裝不能不聲不響地開出另一份(可能是空的)看板,所以拒絕啟動,直到人明確執行 leave-remote。
// 拒絕的條件:mode.json 寫 remote;或沒有 mode.json 而設了 OMIXOR_SERVER。mode.json 寫 local 的照舊啟動。
// 比移除前嚴格:舊版在 mode.json 寫 remote、但那台伺服器已不在啟動設定(OMIXOR_SERVERS、servers.json、OMIXOR_SERVER)時
// 會退回本機模式(mode.json 不改),這種安裝的資料其實在本機。這裡不靠那些已不用的設定判斷(docker-compose 也不再傳),
// 一律拒絕,由錯誤訊息說明兩種情況、讓人自己確認。
// 舊的資料(mode.json、servers.json、orgs-remote*.json、存著的登入)一律不刪。

const (
	legacyModeLocal  = "local"
	legacyModeRemote = "remote"
)

type legacyModeFile struct {
	Mode   string `json:"mode"`
	Server string `json:"server,omitempty"`
}

func legacyModePath() string { return filepath.Join(DataRoot(), "mode.json") }

// readLegacyMode 讀 mode.json:先看目前的存放處;用資料庫而那裡沒有時再看資料夾裡的檔案
// (遠端模式不需要資料庫,升級時才加上資料庫的話,紀錄只在檔案裡)。沒有、或內容不認得,回空的
func readLegacyMode() (legacyModeFile, error) {
	var m legacyModeFile
	b, err := ReadData(legacyModePath())
	if errors.Is(err, fs.ErrNotExist) {
		b, err = os.ReadFile(legacyModePath())
	}
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("讀不到 mode.json,無法確認這台 console 以前是不是遠端模式:%w", err)
	}
	if json.Unmarshal(b, &m) != nil || (m.Mode != legacyModeLocal && m.Mode != legacyModeRemote) {
		return legacyModeFile{}, nil
	}
	return m, nil
}

func legacyEnvServer() string {
	return strings.TrimRight(strings.TrimSpace(config.Current().Str(config.ServerURL)), "/")
}

// withoutUserInfo 網址拿掉帳號密碼(要印出來)
func withoutUserInfo(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "(網址格式不對)"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// CheckRemovedRemoteMode 啟動伺服器前呼叫(資料庫開好之後):還設定成遠端模式就回錯誤
func CheckRemovedRemoteMode() error {
	m, err := readLegacyMode()
	if err != nil {
		return err
	}
	var server, source string
	switch {
	case m.Mode == legacyModeRemote:
		server, source = m.Server, "mode.json"
		if server == "" {
			server = legacyEnvServer()
		}
	case m.Mode == "" && legacyEnvServer() != "":
		server, source = legacyEnvServer(), "環境變數 OMIXOR_SERVER"
	default:
		return nil
	}
	return fmt.Errorf("沒有啟動:這台 console 的紀錄還是「遠端模式」(%s,連 %s),這個模式已經移除。請先確認你以前用的資料在哪裡:"+
		"(1) 以前畫面上是那台 omixor server 的資料:組織、Issue 等在那台 server,不在這裡,請直接使用它。"+
		"(2) 那台 server 早已從啟動設定(OMIXOR_SERVERS、servers.json、OMIXOR_SERVER)拿掉:舊版那時已退回本機模式,你一直用的是這台自己的資料,執行 leave-remote 就對了。"+
		"leave-remote 的用法:用同樣的環境變數執行一次 omixor-web leave-remote 再啟動;它只把模式記成本機,不刪任何資料或存著的登入。"+
		"屬於 (1) 而仍要改用這台自己的資料庫也是執行它(那是另一份資料,可能是空的)",
		source, withoutUserInfo(server))
}

// LeaveRemoteMode 人明確確認改用自己的資料庫:mode.json 記成 local(保留原本的伺服器網址供查閱)
func LeaveRemoteMode() error {
	m, err := readLegacyMode()
	if err != nil {
		return err
	}
	if m.Server == "" {
		m.Server = legacyEnvServer()
	}
	m.Mode = legacyModeLocal
	return WriteJSON(legacyModePath(), m)
}
