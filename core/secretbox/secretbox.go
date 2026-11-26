// Package secretbox 加密存進資料庫的機密(例如 webhook 簽章密鑰),只用標準函式庫。
//
// 密文格式(一個字串,可直接存文字欄位):
//
//	v1:<keyid>:<base64url(nonce||ciphertext)>   AES-256-GCM,每次 Seal 隨機 12 bytes nonce
//	v0::<base64url(明文)>                        沒設金鑰時的明文格式(明確標記,之後可重新加密)
//
// 金鑰環:一把 active 金鑰(Seal 用)加零或多把舊金鑰(只 Open,依 keyid 選),
// 換金鑰時舊的移到舊金鑰清單,再用 Reseal 慢慢把資料換成新金鑰。
// keyid 由金鑰算出來(SHA-256 前 8 個 hex),不含也推不回金鑰。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Box 加解密一個機密字串
type Box interface {
	// Seal 加密(或在沒有金鑰時以 v0 編碼)明文,回傳可存的字串
	Seal(plain []byte) (string, error)
	// Open 還原明文;v0 一律能開,v1 依 keyid 找金鑰
	Open(sealed string) ([]byte, error)
}

const (
	versionPlain = "v0"
	versionAES   = "v1"
	keyLen       = 32
)

var b64 = base64.RawURLEncoding

// 錯誤訊息可能出現在 MCP 回應,維持英文、簡短、不含值
var (
	errFormat     = errors.New("secretbox: malformed sealed value")
	errKeyLen     = errors.New("secretbox: key must be 32 bytes")
	errAuth       = errors.New("secretbox: cannot decrypt (wrong key or tampered)")
	errNoSealedV1 = errors.New("secretbox: no key configured for v1 value")
)

// KeyID 金鑰的短代號(SHA-256 前 8 個 hex);不含金鑰
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}

// sealed 拆開後的字串
type parts struct{ version, keyID, body string }

func parse(sealed string) (parts, error) {
	f := strings.SplitN(sealed, ":", 3)
	if len(f) != 3 || (f[0] != versionPlain && f[0] != versionAES) {
		return parts{}, errFormat
	}
	return parts{f[0], f[1], f[2]}, nil
}

func join(version, keyID string, body []byte) string {
	return version + ":" + keyID + ":" + b64.EncodeToString(body)
}

// ---- 明文(沒設金鑰) ----

type plainBox struct{}

// NewPlain 沒設金鑰時用:Seal 只做 v0 編碼(沒有加密),Open 只認 v0
func NewPlain() Box { return plainBox{} }

func (plainBox) Seal(plain []byte) (string, error) { return join(versionPlain, "", plain), nil }

func (plainBox) Open(sealed string) ([]byte, error) {
	p, err := parse(sealed)
	if err != nil {
		return nil, err
	}
	if p.version == versionAES {
		return nil, errNoSealedV1
	}
	return openPlain(p)
}

func openPlain(p parts) ([]byte, error) {
	b, err := b64.DecodeString(p.body)
	if err != nil {
		return nil, errFormat
	}
	return b, nil
}

// ---- AES-256-GCM ----

type aesBox struct {
	activeID string
	aeads    map[string]cipher.AEAD // keyid -> AEAD(含 active)
}

// NewAES 建立 AES-256-GCM 的箱子:active 用來 Seal,old 只用來 Open;每把都要是 32 bytes
func NewAES(active []byte, old ...[]byte) (Box, error) {
	b := &aesBox{activeID: KeyID(active), aeads: map[string]cipher.AEAD{}}
	for _, k := range append([][]byte{active}, old...) {
		if err := b.add(k); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *aesBox) add(key []byte) error {
	if len(key) != keyLen {
		return errKeyLen
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return errKeyLen
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return err
	}
	b.aeads[KeyID(key)] = g
	return nil
}

// aad 把版本與 keyid 綁進認證資料,改標頭會解不開
func aad(keyID string) []byte { return []byte(versionAES + ":" + keyID) }

func (b *aesBox) Seal(plain []byte) (string, error) {
	g := b.aeads[b.activeID]
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: random: %w", err)
	}
	return join(versionAES, b.activeID, g.Seal(nonce, nonce, plain, aad(b.activeID))), nil
}

func (b *aesBox) Open(sealed string) ([]byte, error) {
	p, err := parse(sealed)
	if err != nil {
		return nil, err
	}
	if p.version == versionPlain { // 設金鑰前寫的資料一律讀得到
		return openPlain(p)
	}
	return b.openAES(p)
}

func (b *aesBox) openAES(p parts) ([]byte, error) {
	g, ok := b.aeads[p.keyID]
	if !ok {
		return nil, fmt.Errorf("secretbox: unknown key id %q", p.keyID)
	}
	raw, err := b64.DecodeString(p.body)
	if err != nil || len(raw) < g.NonceSize() {
		return nil, errFormat
	}
	n := g.NonceSize()
	plain, err := g.Open(nil, raw[:n], raw[n:], aad(p.keyID))
	if err != nil {
		return nil, errAuth
	}
	return plain, nil
}

// ---- 重新加密 ----

// activeIDer 箱子目前用哪把金鑰 Seal;明文箱子沒有(回空字串)
type activeIDer interface{ activeKeyID() string }

func (b *aesBox) activeKeyID() string { return b.activeID }
func (plainBox) activeKeyID() string  { return "" }

// Reseal 值是 v0、或用了非 active 的金鑰時,用 active 金鑰重新加密;changed 表示有沒有換。
// 已經是 active 金鑰的 v1、或箱子沒有金鑰(明文箱子)時原樣回傳、changed=false。給背景搬移用:逐筆呼叫,changed 時寫回
func Reseal(b Box, sealed string) (out string, changed bool, err error) {
	p, err := parse(sealed)
	if err != nil {
		return "", false, err
	}
	active := ""
	if a, ok := b.(activeIDer); ok {
		active = a.activeKeyID()
	}
	if active == "" || (p.version == versionAES && p.keyID == active) {
		return sealed, false, nil
	}
	plain, err := b.Open(sealed)
	if err != nil {
		return "", false, err
	}
	out, err = b.Seal(plain)
	return out, err == nil, err
}
