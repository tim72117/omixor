// Package mcpauth omixor-mcp 的 OAuth 登入:授權碼 + PKCE(本機回呼)、動態註冊、token 保存與更新。
// 對象是 omixor 的 MCP 入口(console 或 omixor server,同一套),每個 resource(統一入口 /mcp、執行者端點 /mcp/worker/{org})各自一份登入。
// token 只送給它的 resource 所在的同一個伺服器,不寫進 log、不印出來。
package mcpauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cred 一個 resource 的登入資料
type Cred struct {
	Resource      string `json:"resource"`
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	// ClientID、RedirectURI 動態註冊的用戶端:重新登入時沿用(伺服器的用戶端總數有上限);登出只清 token
	ClientID    string `json:"clientId,omitempty"`
	RedirectURI string `json:"redirectUri,omitempty"`

	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitzero"`
}

// Store 登入資料夾:每個 resource 一個檔,只有本人可讀寫
type Store struct{ Dir string }

// Path 這個 resource 的檔案:主機名稱(給人辨認)加網址的雜湊(不同端點分開)
func (s Store) Path(resource string) string {
	host := "server"
	if u, err := url.Parse(resource); err == nil && u.Host != "" {
		host = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
				return r
			}
			return '_'
		}, u.Host)
	}
	sum := sha256.Sum256([]byte(resource))
	return filepath.Join(s.Dir, host+"_"+hex.EncodeToString(sum[:6])+".json")
}

// Load 讀登入資料;沒有檔案回空的 Cred(不是錯誤)。檔案裡的 resource 和要的不同當作沒有
func (s Store) Load(resource string) (Cred, error) {
	b, err := os.ReadFile(s.Path(resource))
	if errors.Is(err, fs.ErrNotExist) {
		return Cred{Resource: resource}, nil
	}
	if err != nil {
		return Cred{}, err
	}
	var c Cred
	if err := json.Unmarshal(b, &c); err != nil {
		return Cred{}, fmt.Errorf("%s 讀不出來(可以刪掉後重新登入)", s.Path(resource))
	}
	if c.Resource != resource {
		return Cred{Resource: resource}, nil
	}
	return c, nil
}

// Save 先寫暫存檔、收緊權限、再改名(讀的人不會看到寫一半的內容)
func (s Store) Save(c Cred) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, "cred-*.tmp") // CreateTemp 的權限是 0600
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name) // 改名成功後這個路徑已不存在
	RestrictOrWarn(name)  // Windows 要用 icacls;寫入內容前就收緊,改名後權限跟著檔案走
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.Path(c.Resource))
}

const (
	lockStale = time.Minute
	lockWait  = 45 * time.Second
)

// Lock 跨行程的鎖:多個 omixor-mcp 共用同一份登入時,refresh token 只能用一次(重複使用會讓整組授權被撤銷),
// 所以換 token 前一定要拿鎖、拿到後重讀。持有超過 lockStale 的鎖當作行程已經死掉
func (s Store) Lock(resource string) (unlock func(), err error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	path := s.Path(resource) + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > lockStale {
			// 改名成功的那一個才刪(兩個行程同時清舊鎖時,不會有人刪到別人剛建的新鎖)
			old := fmt.Sprintf("%s.%d", path, os.Getpid())
			if os.Rename(path, old) == nil {
				_ = os.Remove(old)
			}
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等不到登入資料的鎖(%s):%v", path, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
