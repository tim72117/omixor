package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/service"
	"omixor-core/store"
)

// 健康檢查與 API 都在
func TestServesCoreHandler(t *testing.T) {
	h := Handler(&service.Service{Store: store.NewMem()})
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
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/orgs/x/cabinet", nil))
	if rec.Code != 401 {
		t.Fatal("文件櫃的 API 要在,沒登入要 401", rec.Code)
	}
}

// 雲端預設就有文件櫃工具(核心內建,管理端點與角色端點都開放)
func TestCabinetToolsAllowed(t *testing.T) {
	for _, n := range []string{"cabinet", "docs", "doc_save"} {
		if !mcp.AdminToolAllow[n] || !mcp.AgentToolAllow[n] {
			t.Fatal("沒開放", n)
		}
	}
	names := mcp.NewCoreServer("").ToolNames()
	if !strings.Contains(strings.Join(names, ","), "doc_save") || len(mcp.NewCoreServer("").Missing()) != 0 {
		t.Fatal(names)
	}
}
