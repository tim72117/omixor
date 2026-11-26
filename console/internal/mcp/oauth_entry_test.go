package mcp

import (
	"testing"

	"omixor-core/config"
)

// 畫面連接埠的 MCP 入口只在有設 OMIXOR_PUBLIC_URL 且條件合時啟用;條件不合只回原因(不擋啟動)
func TestWebEntryConditions(t *testing.T) {
	load := func(env map[string]string) *config.Config {
		return config.LoadFrom(func(k string) string { return env[k] }, config.Web)
	}
	ok := load(map[string]string{"OMIXOR_PUBLIC_URL": "https://console.test"})
	if err := ok.Validate(config.Web); err != nil {
		t.Fatal(err)
	}
	if p := webEntryProblem(ok, true, false); p != "" {
		t.Fatal(p)
	}
	if webEntryProblem(ok, false, false) == "" || webEntryProblem(ok, true, true) == "" {
		t.Fatal("沒有資料庫、遠端模式不啟用")
	}
	bad := load(map[string]string{"OMIXOR_PUBLIC_URL": "http://console.test"})
	if err := bad.Validate(config.Web); err != nil {
		t.Fatalf("只設 PUBLIC_URL 不該因此拒絕啟動:%v", err)
	}
	if webEntryProblem(bad, true, false) == "" {
		t.Fatal("公開網址用 http 不啟用")
	}
	if WebEntry(nil, load(nil), nil) != nil {
		t.Fatal("沒設 PUBLIC_URL 沒有入口")
	}
}
