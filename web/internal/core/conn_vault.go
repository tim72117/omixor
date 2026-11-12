package core

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// 鑰匙圈:token 與 client secret,同 Swift 的 TokenVault。
// 服務名稱 com.tim72117.ops-web.connection(和 Swift 版分開),帳號名稱是 <組織前綴><連線 id>:token / :secret,
// 組織前綴:預設組織是空字串,其他組織是 "<組織 id>/"。
// 用 /usr/bin/security;寫入時密碼經 security -i 從 stdin 送(十六進位),不會出現在指令參數裡。
// 會跑外部程式,所以呼叫時不能拿著 App.Mu。跑 go test 時預設是記憶體版本,不會碰到真的鑰匙圈。

// SecretStore 鑰匙圈的介面(測試時換成 NewMemoryVault())
type SecretStore interface {
	Get(account string) ([]byte, bool)
	Set(account string, data []byte) error
	Delete(account string) error
}

const VaultService = "com.tim72117.ops-web.connection"

// Vault 目前使用的鑰匙圈
var Vault SecretStore = &KeychainVault{Service: VaultService}

func init() {
	if testing.Testing() {
		Vault = NewMemoryVault()
	}
}

// KeychainVault macOS 鑰匙圈(/usr/bin/security);Keychain 空白表示預設的鑰匙圈
type KeychainVault struct {
	Service  string
	Keychain string
}

func (k *KeychainVault) args(base ...string) []string {
	if k.Keychain != "" {
		base = append(base, k.Keychain)
	}
	return base
}

func (k *KeychainVault) Get(account string) ([]byte, bool) {
	out, err := exec.Command("/usr/bin/security", k.args("find-generic-password", "-s", k.Service, "-a", account, "-w")...).Output()
	if err != nil {
		return nil, false
	}
	return bytes.TrimSuffix(out, []byte("\n")), true
}

func (k *KeychainVault) Set(account string, data []byte) error {
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -X %s", secQuote(k.Service), secQuote(account), hex.EncodeToString(data))
	if k.Keychain != "" {
		line += " " + secQuote(k.Keychain)
	}
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(line + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return err
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		return errors.New("鑰匙圈寫入失敗:" + s)
	}
	return nil
}

func (k *KeychainVault) Delete(account string) error {
	err := exec.Command("/usr/bin/security", k.args("delete-generic-password", "-s", k.Service, "-a", account)...).Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 44 { // 本來就沒有
		return nil
	}
	return err
}

// secQuote security -i 的參數:雙引號包起來,跳脫 \ 與 "
func secQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// MemoryVault 記憶體版本(測試用)
type MemoryVault struct {
	mu sync.Mutex
	m  map[string][]byte
}

func NewMemoryVault() *MemoryVault { return &MemoryVault{m: map[string][]byte{}} }

func (v *MemoryVault) Get(account string) ([]byte, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d, ok := v.m[account]
	return append([]byte(nil), d...), ok
}

func (v *MemoryVault) Set(account string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.m[account] = append([]byte(nil), data...)
	return nil
}

func (v *MemoryVault) Delete(account string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.m, account)
	return nil
}

// Accounts 目前有哪些帳號(測試用)
func (v *MemoryVault) Accounts() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := []string{}
	for k := range v.m {
		out = append(out, k)
	}
	return out
}

// MARK: 快取:畫面(拿著鎖)只看快取,不跑外部程式

type vaultEntry struct {
	data []byte
	ok   bool
}

var (
	vaultMu    sync.Mutex
	vaultCache = map[string]vaultEntry{}
	vaultOwner SecretStore
)

// vaultCurrent 目前的 Vault;測試換了 Vault 時快取作廢(呼叫時拿著 vaultMu)
func vaultCurrent() SecretStore {
	if vaultOwner != Vault {
		vaultCache = map[string]vaultEntry{}
		vaultOwner = Vault
	}
	return vaultOwner
}

func vaultGet(account string) ([]byte, bool) {
	vaultMu.Lock()
	v := vaultCurrent()
	if e, ok := vaultCache[account]; ok {
		vaultMu.Unlock()
		return e.data, e.ok
	}
	vaultMu.Unlock()
	d, ok := v.Get(account)
	vaultMu.Lock()
	if vaultOwner == v {
		vaultCache[account] = vaultEntry{d, ok}
	}
	vaultMu.Unlock()
	return d, ok
}

// vaultPeek 只看快取(known=false 表示還沒讀過)
func vaultPeek(account string) (has, known bool) {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	vaultCurrent()
	e, ok := vaultCache[account]
	return e.ok, ok
}

// vaultSet data 是 nil 表示刪除
func vaultSet(account string, data []byte) {
	vaultMu.Lock()
	v := vaultCurrent()
	vaultMu.Unlock()
	if data == nil {
		_ = v.Delete(account)
	} else if err := v.Set(account, data); err != nil {
		data = nil
	}
	vaultMu.Lock()
	if vaultOwner == v {
		vaultCache[account] = vaultEntry{data, data != nil}
	}
	vaultMu.Unlock()
}

// MARK: token 與 secret(scope 是組織前綴)

func tokenAccount(scope, id string) string  { return scope + id + ":token" }
func secretAccount(scope, id string) string { return scope + id + ":secret" }

func vaultTokens(scope, id string) *TokenSet {
	d, ok := vaultGet(tokenAccount(scope, id))
	if !ok {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(d, &raw) != nil {
		return nil
	}
	if _, ok := raw["accessToken"].(string); !ok {
		return nil
	}
	var t TokenSet
	if json.Unmarshal(d, &t) != nil {
		return nil
	}
	return &t
}

func vaultSetTokens(scope, id string, t *TokenSet) {
	if t == nil {
		vaultSet(tokenAccount(scope, id), nil)
		return
	}
	vaultSet(tokenAccount(scope, id), Marshal(t))
}

func vaultSecret(scope, id string) (string, bool) {
	d, ok := vaultGet(secretAccount(scope, id))
	return string(d), ok
}

func vaultSetSecret(scope, id string, s *string) {
	if s == nil {
		vaultSet(secretAccount(scope, id), nil)
		return
	}
	vaultSet(secretAccount(scope, id), []byte(*s))
}

func vaultRemoveAll(scope, id string) {
	vaultSetTokens(scope, id, nil)
	vaultSetSecret(scope, id, nil)
}

// orgScope 組織的鑰匙圈前綴
func orgScope(orgID string) string {
	if orgID == DefaultOrgID || orgID == "" {
		return ""
	}
	return orgID + "/"
}
