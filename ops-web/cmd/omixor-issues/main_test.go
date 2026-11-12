package main

import (
	"net/http/httptest"
	"testing"

	"omixor-core/server"
	"omixor-core/service"
	"omixor-core/store"
)

// 這個指令就是核心的 omixor-server:健康檢查與 API 都在
func TestServesCoreHandler(t *testing.T) {
	h := server.Handler(&service.Service{Store: store.NewMem()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/orgs", nil))
	if rec.Code != 401 {
		t.Fatal("沒登入要 401", rec.Code)
	}
}
