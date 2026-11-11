package auth

import "opsweb/internal/core"

// VaultSecrets 把服務帳號密碼存在鑰匙圈(和連線同一個服務名稱,帳號 auth:ldap-bind)
type VaultSecrets struct{}

const bindAccount = "auth:ldap-bind"

func (VaultSecrets) Get() (string, bool) {
	b, ok := core.Vault.Get(bindAccount)
	if !ok || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

func (VaultSecrets) Set(pw string) error {
	if pw == "" {
		return core.Vault.Delete(bindAccount)
	}
	return core.Vault.Set(bindAccount, []byte(pw))
}

// MemorySecrets 測試用
type MemorySecrets struct{ PW string }

func (m *MemorySecrets) Get() (string, bool) { return m.PW, m.PW != "" }
func (m *MemorySecrets) Set(pw string) error { m.PW = pw; return nil }
