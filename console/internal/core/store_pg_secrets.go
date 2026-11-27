package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"time"
)

// 資料庫裡的密鑰(加密)與 console 自己的登入 session

// MARK: 密鑰(AES-256-GCM)

type pgVault struct{}

func (pgVault) Get(account string) ([]byte, bool) {
	ctx, cancel := pgCtx()
	defer cancel()
	var nonce, ct []byte
	if err := pgQueryRow(ctx, `select nonce, ciphertext from omixor_secrets where account = $1`, account).Scan(&nonce, &ct); err != nil {
		return nil, false
	}
	pt, err := secretOpen(account, nonce, ct)
	if err != nil {
		log.Printf("密鑰 %s 解不開(OMIXOR_SECRET_KEY 換過了嗎?):%v", account, err)
		return nil, false
	}
	return pt, true
}

func (pgVault) Set(account string, data []byte) error {
	nonce, ct, err := secretSeal(account, data)
	if err != nil {
		return err
	}
	ctx, cancel := pgCtx()
	defer cancel()
	_, err = pgExec(ctx, `insert into omixor_secrets (account, nonce, ciphertext, updated_at) values ($1, $2, $3, now())
		on conflict (account) do update set nonce = excluded.nonce, ciphertext = excluded.ciphertext, updated_at = now()`, account, nonce, ct)
	return err
}

func (pgVault) Delete(account string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgExec(ctx, `delete from omixor_secrets where account = $1`, account)
	return err
}

// 帳號名稱當作附加資料(AAD):密文搬到別的帳號底下會解不開
func secretSeal(account string, pt []byte) (nonce, ct []byte, err error) {
	gcm, err := secretGCM()
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, pt, []byte(account)), nil
}

func secretOpen(account string, nonce, ct []byte) ([]byte, error) {
	gcm, err := secretGCM()
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, []byte(account))
}

func secretGCM() (cipher.AEAD, error) {
	if len(pgKey) != 32 {
		return nil, errors.New("沒有密鑰加密金鑰")
	}
	b, err := aes.NewCipher(pgKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// MARK: session

func tokenHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// SessionPut / SessionGet / SessionDelete / SessionClear:auth 用(沒有資料庫時不會呼叫)
func SessionPut(tok string, user any, expires time.Time) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgExec(ctx, `insert into omixor_sessions (token_hash, user_json, expires_at) values ($1, $2, $3)
		on conflict (token_hash) do update set user_json = excluded.user_json, expires_at = excluded.expires_at`,
		tokenHash(tok), string(Marshal(user)), expires)
	return err
}

func SessionGet(tok string, user any) (time.Time, bool) {
	ctx, cancel := pgCtx()
	defer cancel()
	var b []byte
	var exp time.Time
	if err := pgQueryRow(ctx, `select user_json::text, expires_at from omixor_sessions where token_hash = $1`, tokenHash(tok)).Scan(&b, &exp); err != nil {
		return time.Time{}, false
	}
	if json.Unmarshal(b, user) != nil {
		return time.Time{}, false
	}
	return exp, true
}

func SessionDelete(tok string) {
	ctx, cancel := pgCtx()
	defer cancel()
	_, _ = pgExec(ctx, `delete from omixor_sessions where token_hash = $1`, tokenHash(tok))
}

// SessionClear 刪掉全部 session(認證設定改了,所有人要重新登入);順便清過期的
func SessionClear() {
	ctx, cancel := pgCtx()
	defer cancel()
	_, _ = pgExec(ctx, `delete from omixor_sessions`)
}

func sessionSweep() {
	pool := pgPool()
	if pool == nil {
		return
	}
	ctx, cancel := pgCtx()
	defer cancel()
	_, _ = pool.Exec(ctx, `delete from omixor_sessions where expires_at < now()`)
}
