package store

// PG 測試用的連線位址(testPGURL):
//   - 有設 OPS_TEST_DATABASE_URL(CI 用 postgres service)→ 直接用它,不起 embedded。
//   - 沒設 → 這個測試 binary 第一次要 PG 時,用 embedded-postgres 起一個臨時的 PostgreSQL 17
//     (隨機 port、資料放暫存資料夾;二進位檔用 library 預設快取 ~/.embedded-postgres-go,第一次會下載),
//     TestMain 在全部測試結束後 Stop 並刪掉暫存資料夾。
//   - go test -short 或 OPS_TEST_NO_EMBEDDED_PG=1 → 不起 embedded,PG 測試照舊略過。
// 每個測試的隔離照舊:testPG 在這個 URL 上建自己的暫時 schema,測完刪掉。

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

const noEmbeddedPGEnv = "OPS_TEST_NO_EMBEDDED_PG"

var embeddedPG struct {
	once sync.Once
	db   *embeddedpostgres.EmbeddedPostgres
	dir  string
	url  string
	err  error
	log  bytes.Buffer
}

func TestMain(m *testing.M) {
	code := m.Run()
	stopEmbeddedPG()
	os.Exit(code)
}

// testPGURL 回傳 PG 測試要連的位址;沒有可用的 PG 時 t.Skip
func testPGURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv(pgEnv); url != "" {
		return url
	}
	if testing.Short() {
		t.Skip("沒設 " + pgEnv + ",-short 不起 embedded PostgreSQL")
	}
	if os.Getenv(noEmbeddedPGEnv) == "1" {
		t.Skip("沒設 " + pgEnv + ",且 " + noEmbeddedPGEnv + "=1")
	}
	embeddedPG.once.Do(startEmbeddedPG)
	if embeddedPG.err != nil {
		t.Fatalf("起 embedded PostgreSQL 失敗:%v\n%s", embeddedPG.err, embeddedPG.log.String())
	}
	return embeddedPG.url
}

// freePort 找一個本機可用的 port,避開 5432(預設)與 5440(使用者本機的 omixor-local-pg)
func freePort() (uint32, error) {
	for range 20 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if p != 5432 && p != 5440 {
			return uint32(p), nil
		}
	}
	return 0, fmt.Errorf("找不到可用的 port")
}

func startEmbeddedPG() {
	e := &embeddedPG
	port, err := freePort()
	if err != nil {
		e.err = err
		return
	}
	dir, err := os.MkdirTemp("", "core-store-pg-")
	if err != nil {
		e.err = err
		return
	}
	e.dir = dir
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).
		Port(port).
		Username("postgres").
		Password("test").
		Database("ops_test").
		RuntimePath(dir + "/runtime").
		DataPath(dir + "/data").
		StartTimeout(2 * time.Minute). // 第一次要下載、解壓
		Logger(&e.log).
		StartParameters(map[string]string{
			"fsync":              "off",
			"synchronous_commit": "off",
			"full_page_writes":   "off",
		})
	db := embeddedpostgres.NewDatabase(cfg)
	if err := db.Start(); err != nil {
		e.err = err
		os.RemoveAll(dir)
		return
	}
	e.db = db
	e.url = fmt.Sprintf("postgres://postgres:test@127.0.0.1:%d/ops_test?sslmode=disable", port)
}

func stopEmbeddedPG() {
	e := &embeddedPG
	if e.db != nil {
		if err := e.db.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "停 embedded PostgreSQL 失敗:", err)
		}
		e.db = nil
	}
	if e.dir != "" {
		os.RemoveAll(e.dir)
		e.dir = ""
	}
}
