package core

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 連線的 TLS 設定:自架服務(例如公司的 GitLab)常用自簽憑證。
//   Connection.CACert   PEM 檔路徑:除了系統的憑證,也信任這個檔案裡的 CA
//   Connection.Insecure 不驗證憑證(只給測試環境)
// 兩個欄位都是 Go 版才有的可省略欄位;Swift 版讀同一份 connections.json 時會忽略。

// tlsAware 測試用的假網路(FakeNet)實作這個介面,讓連線的 TLS 設定也能套到假伺服器
type tlsAware interface {
	WithTLS(cfg *tls.Config) http.RoundTripper
}

var (
	tlsMu    sync.Mutex
	tlsCache = map[string]http.RoundTripper{}
)

// ExpandPath 支援 ~/ 開頭
func ExpandPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// LoadCAPool 讀 PEM 檔,回傳「系統的憑證 + 檔案裡的憑證」
func LoadCAPool(path string) (*x509.CertPool, []byte, error) {
	p := ExpandPath(path)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, connErr("讀不到 CA 憑證檔 " + path + ":" + err.Error())
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, nil, connErr("CA 憑證檔 " + path + " 裡沒有 PEM 格式的憑證")
	}
	return pool, b, nil
}

// connTLSConfig 連線自訂的 TLS 設定;沒有自訂時回傳 nil
func connTLSConfig(c *Connection) (*tls.Config, string, error) {
	if c == nil || (c.CACert == "" && !c.Insecure) {
		return nil, "", nil
	}
	if c.Insecure {
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, "insecure", nil
	}
	pool, pem, err := LoadCAPool(c.CACert)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(pem)
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, fmt.Sprintf("ca:%x", sum[:8]), nil
}

func connTransport(c *Connection) (http.RoundTripper, error) {
	base := ConnTransport
	if base == nil {
		base = http.DefaultTransport
	}
	cfg, key, err := connTLSConfig(c)
	if err != nil {
		return nil, err
	}
	if t, ok := base.(tlsAware); ok {
		return t.WithTLS(cfg), nil
	}
	if cfg == nil {
		return base, nil
	}
	ht, ok := base.(*http.Transport)
	if !ok {
		return base, nil
	}
	tlsMu.Lock()
	defer tlsMu.Unlock()
	k := fmt.Sprintf("%p|%s", ht, key)
	if t, ok := tlsCache[k]; ok {
		return t, nil
	}
	nt := ht.Clone()
	nt.TLSClientConfig = cfg
	tlsCache[k] = nt
	return nt, nil
}

// explainTLSError 憑證驗證失敗時講清楚怎麼處理
func explainTLSError(c *Connection, err error) error {
	var unknown x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	switch {
	case errors.As(err, &unknown) || errors.As(err, &verify) && errors.As(verify.Err, &unknown):
		hint := "在連線設定指定公司的 CA 憑證檔(PEM)"
		if c != nil && c.CACert != "" {
			hint = "CA 憑證檔 " + c.CACert + " 裡沒有簽這張憑證的 CA"
		}
		return connErr("憑證驗證失敗:伺服器的憑證不是受信任的 CA 簽的(自簽憑證?)。" + hint)
	case errors.As(err, &hostErr):
		return connErr("憑證驗證失敗:憑證上的主機名稱不符(" + hostErr.Error() + ")")
	case errors.As(err, &invalid):
		return connErr("憑證驗證失敗:" + invalid.Error())
	}
	return err
}
