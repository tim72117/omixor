package core

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKeychain 假的鑰匙圈(service+account → data),不碰真的 /usr/bin/security
type fakeKeychain struct {
	mu   sync.Mutex
	m    map[string][]byte
	sets atomic.Int32
}

func newFakeKeychain() *fakeKeychain { return &fakeKeychain{m: map[string][]byte{}} }

func (f *fakeKeychain) key(service, account string) string { return service + "\x00" + account }

func (f *fakeKeychain) find(service, account string) ([]byte, bool) {
	time.Sleep(time.Millisecond) // 拉長競爭的時間窗
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.m[f.key(service, account)]
	return append([]byte(nil), d...), ok
}

func (f *fakeKeychain) put(service, account string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[f.key(service, account)] = append([]byte(nil), data...)
}

func (f *fakeKeychain) vault() *KeychainVault {
	return &KeychainVault{
		Service: "new.svc",
		Legacy:  "old.svc",
		findFn:  f.find,
		setFn: func(service, account string, data []byte) error {
			f.sets.Add(1)
			f.put(service, account, data)
			return nil
		},
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return &buf
}

func TestKeychainVaultMigrateConcurrentOnce(t *testing.T) {
	f := newFakeKeychain()
	f.put("old.svc", "a:token", []byte("secret-a"))
	f.put("old.svc", "b:token", []byte("secret-b"))
	v := f.vault()
	logs := captureLog(t)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		acct := "a:token"
		want := "secret-a"
		if i%2 == 1 {
			acct, want = "b:token", "secret-b"
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, ok := v.Get(acct)
			if !ok || string(d) != want {
				t.Errorf("%s 讀到 %q %v", acct, d, ok)
			}
		}()
	}
	wg.Wait()

	if n := f.sets.Load(); n != 2 {
		t.Fatalf("每個帳號只該搬一次,實際寫了 %d 次", n)
	}
	for _, acct := range []string{"a:token", "b:token"} {
		if _, ok := f.find("new.svc", acct); !ok {
			t.Fatalf("%s 沒搬到新名稱", acct)
		}
	}
	if logs.Len() != 0 {
		t.Fatalf("不該有錯誤 log:%s", logs)
	}
}

func TestKeychainVaultMigrateDuplicateIsSuccess(t *testing.T) {
	f := newFakeKeychain()
	f.put("old.svc", "a:token", []byte("secret-a"))
	v := f.vault()
	// 模擬別的程序同時搬好了:寫入時項目已存在,回報 45
	v.setFn = func(service, account string, data []byte) error {
		f.sets.Add(1)
		f.put(service, account, data)
		return errKeychainDuplicate
	}
	logs := captureLog(t)
	d, ok := v.Get("a:token")
	if !ok || string(d) != "secret-a" {
		t.Fatalf("讀到 %q %v", d, ok)
	}
	if logs.Len() != 0 {
		t.Fatalf("45 而且新名稱讀得到要視為成功,不記 log:%s", logs)
	}
}

func TestKeychainVaultMigrateFailureLogged(t *testing.T) {
	f := newFakeKeychain()
	f.put("old.svc", "a:token", []byte("secret-a"))
	v := f.vault()
	// 回報 45 但新名稱還是讀不到:真的失敗,要記 log,但照樣回傳舊的資料
	v.setFn = func(service, account string, data []byte) error { return errKeychainDuplicate }
	logs := captureLog(t)
	d, ok := v.Get("a:token")
	if !ok || string(d) != "secret-a" {
		t.Fatalf("讀到 %q %v", d, ok)
	}
	if !strings.Contains(logs.String(), "搬到 new.svc 失敗") {
		t.Fatalf("要記錯誤 log:%q", logs)
	}
}

func TestKeychainVaultNoLegacy(t *testing.T) {
	f := newFakeKeychain()
	v := f.vault()
	if _, ok := v.Get("x"); ok {
		t.Fatal("新舊都沒有要回 false")
	}
	if f.sets.Load() != 0 {
		t.Fatal("沒有東西不該寫")
	}
}
