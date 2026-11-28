package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 開始與回呼分開計;灌「開始」的少數來源填不滿保留給沒失敗過的來源的名額,也不影響回呼與 CLI 的 evoleap 登入
func TestWebLoginRateReserve(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("id-%d", n)})
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	s := &Service{Evoleap: srv.URL, LoginPerIP: 10, LoginTotal: 60, Now: func() time.Time { return now }}
	start := func(ip string) error {
		_, _, err := s.StartWebEvoleapLogin("https://x.test/cb", ip)
		return err
	}
	got := 0
	for i := 0; i < 200; i++ {
		if start(fmt.Sprintf("9.9.9.%d", i%8)) == nil {
			got++
		}
	}
	if got != 40 {
		t.Fatalf("少數來源開始了 %d 次,要 40", got)
	}
	if err := start("8.8.8.8"); err != nil {
		t.Fatal("沒失敗過的來源要能開始:", err)
	}
	// 對不上的回呼不佔名額
	for i := 0; i < 200; i++ {
		if _, _, err := s.CompleteWebEvoleapLogin("id-1", "c", "wrong", "7.7.7.7", time.Hour); Code(err) != 400 {
			t.Fatal(err)
		}
	}
	if w, ok := s.logins.webCallback.take(now, "7.7.7.7", 10, 60); !ok {
		t.Fatal("回呼名額應該還在")
	} else {
		s.logins.webCallback.refund(w, "7.7.7.7")
	}
	if !s.allowLogin(&s.logins.evoleap, "9.9.9.1") {
		t.Fatal("CLI 登入的限額分開")
	}
	// 完成登入後收回失敗記號
	s.logins.webStart.forgive(limitKey("8.8.8.8"))
	if c, _ := s.logins.webStart.failed.get(limitKey("8.8.8.8"), now, loginFailMemory); c != 0 {
		t.Fatalf("失敗記號:%d", c)
	}
}

func TestWebLoginTxExpiry(t *testing.T) {
	var w webLoginTxs
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	w.put(now, "id", webLoginTx{bindHash: oauthBindHash("k"), expires: now.Add(webLoginTxTTL)})
	if _, ok := w.match(now, "id", strings.ToUpper("k"), true); ok {
		t.Fatal("密鑰不對")
	}
	if _, ok := w.match(now.Add(webLoginTxTTL), "id", "k", true); ok {
		t.Fatal("逾時")
	}
	if _, ok := w.match(now, "id", "k", true); !ok {
		t.Fatal("要能用")
	}
	if _, ok := w.match(now, "id", "k", true); ok {
		t.Fatal("只能用一次")
	}
}
