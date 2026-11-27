package web

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// 層數 0 而有轉送標頭:只警告一次;沒有標頭或層數 > 0 不警告
func TestWarnUntrustedProxy(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	serve := func(h http.Handler, xff string) {
		r := httptest.NewRequest("GET", "/", nil)
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	serve(WarnUntrustedProxy(next, 1), "203.0.113.5")
	h := WarnUntrustedProxy(next, 0)
	serve(h, "")
	if buf.Len() != 0 {
		t.Fatal("不該警告:", buf.String())
	}
	serve(h, "203.0.113.5")
	serve(h, "203.0.113.6")
	if n := strings.Count(buf.String(), "OMIXOR_TRUSTED_PROXY_HOPS"); n != 1 {
		t.Fatalf("要只警告一次,得到 %d:%s", n, buf.String())
	}
}
