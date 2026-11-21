package server

// 內建 PostgreSQL 的保護邏輯測試:資料目錄檢查、pid 驗證、埠重試字串、可放棄的啟動、舊 store.json 警告;
// 最後一個用真的 PostgreSQL 走「改 PG_VERSION 拒絕」「資料庫被刪後補建」(-short 或 OMIXOR_TEST_NO_EMBEDDED_PG=1 略過)

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPGDataDir(t *testing.T) {
	v := embeddedpostgres.V17
	root := t.TempDir()
	// 不存在、空的:放行
	if err := checkPGDataDir(filepath.Join(root, "none"), v); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty")
	_ = os.MkdirAll(empty, 0o755)
	if err := checkPGDataDir(empty, v); err != nil {
		t.Fatal(err)
	}
	// 版本相同:放行
	ok := filepath.Join(root, "ok")
	writeFile(t, filepath.Join(ok, "PG_VERSION"), "17\n")
	writeFile(t, filepath.Join(ok, "base", "1", "x"), "x")
	if err := checkPGDataDir(ok, v); err != nil {
		t.Fatal(err)
	}
	// 主版本不符:拒絕,資料還在
	old := filepath.Join(root, "old")
	writeFile(t, filepath.Join(old, "PG_VERSION"), "16\n")
	writeFile(t, filepath.Join(old, "global", "pg_control"), "data")
	err := checkPGDataDir(old, v)
	if err == nil || !strings.Contains(err.Error(), "拒絕啟動") || !strings.Contains(err.Error(), "備份") {
		t.Fatal("版本不符應該拒絕並說明怎麼處理:", err)
	}
	if _, err := os.Stat(filepath.Join(old, "global", "pg_control")); err != nil {
		t.Fatal("拒絕時資料不能被動到")
	}
	// 沒有 PG_VERSION 但有資料:拒絕
	lost := filepath.Join(root, "lost")
	writeFile(t, filepath.Join(lost, "global", "pg_control"), "data")
	writeFile(t, filepath.Join(lost, "base", "1", "a"), "a")
	writeFile(t, filepath.Join(lost, "base", "16384", "b"), "b")
	if err := checkPGDataDir(lost, v); err == nil {
		t.Fatal("PG_VERSION 不見但有資料應該拒絕")
	}
	if _, err := os.Stat(filepath.Join(lost, "base", "16384", "b")); err != nil {
		t.Fatal("資料不能被動到")
	}
	// PG_VERSION 是目錄(讀不到):拒絕
	unread := filepath.Join(root, "unread")
	_ = os.MkdirAll(filepath.Join(unread, "PG_VERSION"), 0o755)
	writeFile(t, filepath.Join(unread, "global", "pg_control"), "data")
	if err := checkPGDataDir(unread, v); err == nil {
		t.Fatal("PG_VERSION 讀不到應該拒絕")
	}
	// 沒初始化完成(沒有 pg_control、base 只有 template1、沒有 PG_VERSION):改名保留(不刪)
	half := filepath.Join(root, "half")
	writeFile(t, filepath.Join(half, "postgresql.conf"), "x")
	writeFile(t, filepath.Join(half, "base", "1", "a"), "a")
	if err := checkPGDataDir(half, v); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(half); err == nil {
		t.Fatal("沒初始化完成的資料目錄應該被改名")
	}
	m, _ := filepath.Glob(half + ".incomplete-*")
	if len(m) != 1 {
		t.Fatal("應該留下改名的資料夾", m)
	}
}

func TestPortBusy(t *testing.T) {
	for _, s := range []string{
		"process already listening on port 1",
		"pg_ctl: LOG: could not bind IPv4 address \"127.0.0.1\": Address already in use",
		"could not bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.",
	} {
		if !portBusy(s) {
			t.Error("應該算埠被佔走:", s)
		}
	}
	if portBusy("password authentication failed") {
		t.Error("不是埠的問題")
	}
}

func TestPidIsPostgres(t *testing.T) {
	// 這個測試程式自己:不是 postgres
	isPG, known := pidIsPostgres(os.Getpid())
	if !known || isPG {
		t.Fatal("測試程式自己不是 postgres:", isPG, known)
	}
	// 一個已經結束的行程:不是 postgres(known 或 unknown 都不能是 true)
	cmd := exec.Command(os.Args[0], "-test.run=NONE")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if isPG, _ := pidIsPostgres(cmd.Process.Pid); isPG {
		t.Fatal("結束的行程不是 postgres")
	}
}

// pid 檔指向無關行程時,stopStale 不能對它動作(這裡的「無關行程」就是測試程式自己:被送訊號會死掉)
func TestStopStaleSkipsNonPostgres(t *testing.T) {
	dir := t.TempDir()
	data, rt := filepath.Join(dir, "pgdata"), filepath.Join(dir, "pgruntime")
	// 假的 pg_ctl:被呼叫就留下記號
	mark := filepath.Join(dir, "called")
	name := "pg_ctl"
	body := "#!/bin/sh\ntouch " + filepath.ToSlash(mark) + "\n"
	if os.PathSeparator == '\\' {
		name, body = "pg_ctl.exe", "not an exe"
	}
	writeFile(t, filepath.Join(rt, "bin", name), body)
	_ = os.Chmod(filepath.Join(rt, "bin", name), 0o755)
	writeFile(t, filepath.Join(data, "postmaster.pid"), itoa(os.Getpid())+"\n"+data+"\n")
	if pid, ok := readPostmasterPID(data); !ok || pid != os.Getpid() {
		t.Fatal("讀 pid 失敗", pid, ok)
	}
	if stopStale(rt, data) {
		t.Fatal("pid 不是 postgres,不該嘗試關閉")
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("不該呼叫 pg_ctl")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func dropDB(t *testing.T, port uint32, pw string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/postgres?sslmode=disable", embeddedPGUser, pw, port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `drop database "`+embeddedPGDB+`"`); err != nil {
		t.Fatal(err)
	}
}

func TestRunAbandonable(t *testing.T) {
	// 正常完成
	if err := runAbandonable(context.Background(), func() error { return nil }, func() { t.Error("不該清理") }, time.Second); err != nil {
		t.Fatal(err)
	}
	// 取消後在 grace 內完成且成功:要清理(把起來的關掉)
	ctx, cancel := context.WithCancel(context.Background())
	var cleaned atomic.Int32
	err := runAbandonable(ctx, func() error { cancel(); time.Sleep(50 * time.Millisecond); return nil }, func() { cleaned.Add(1) }, 2*time.Second)
	if !errors.Is(err, context.Canceled) || cleaned.Load() != 1 {
		t.Fatal("取消後成功的啟動要被清理", err, cleaned.Load())
	}
	// grace 過了還沒完成:先清理一次,之後成功了再清理一次
	ctx2, cancel2 := context.WithCancel(context.Background())
	release := make(chan struct{})
	var cleaned2 atomic.Int32
	go func() { time.Sleep(20 * time.Millisecond); cancel2() }()
	err = runAbandonable(ctx2, func() error { <-release; return nil }, func() { cleaned2.Add(1) }, 50*time.Millisecond)
	if !errors.Is(err, context.Canceled) || cleaned2.Load() != 1 {
		t.Fatal("grace 過了要先清理", err, cleaned2.Load())
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for cleaned2.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cleaned2.Load() != 2 {
		t.Fatal("之後才成功的啟動要被清理", cleaned2.Load())
	}
	// 啟動失敗:回傳啟動的錯誤,不清理
	boom := errors.New("boom")
	if err := runAbandonable(context.Background(), func() error { return boom }, func() { t.Error("不該清理") }, time.Second); err != boom {
		t.Fatal(err)
	}
}

func TestWarnLegacyStore(t *testing.T) {
	dir := t.TempDir()
	if warnLegacyStore(dir) {
		t.Fatal("沒有 store.json 不該警告")
	}
	writeFile(t, filepath.Join(dir, "store.json"), "{}")
	if !warnLegacyStore(dir) {
		t.Fatal("有 store.json 而且沒有 pgdata 要警告")
	}
	if _, err := os.Stat(filepath.Join(dir, "store.json")); err != nil {
		t.Fatal("不能刪 store.json")
	}
	_ = os.MkdirAll(filepath.Join(dir, "pgdata"), 0o755)
	if warnLegacyStore(dir) {
		t.Fatal("已經有 pgdata 不再警告")
	}
}

// 用真的 PostgreSQL:資料還在時改 PG_VERSION → 拒絕啟動、資料不被清;omixor 資料庫被刪掉(模擬初始化被中斷)→ 補建
func TestEmbeddedPGGuards(t *testing.T) {
	if testing.Short() || os.Getenv("OMIXOR_TEST_NO_EMBEDDED_PG") == "1" {
		t.Skip("不起內建 PostgreSQL")
	}
	old := libHealthTimeout
	libHealthTimeout = 5 * time.Second
	defer func() { libHealthTimeout = old }()
	dir := t.TempDir()
	pg, err := StartEmbeddedPG(EmbeddedPGOptions{Dir: dir, Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.Stop(); err != nil {
		t.Fatal(err)
	}
	pgdata := filepath.Join(dir, "pgdata")

	// 改 PG_VERSION 成 16:要拒絕,而且資料(pg_control、base)還在
	vf := filepath.Join(pgdata, "PG_VERSION")
	orig, _ := os.ReadFile(vf)
	writeFile(t, vf, "16\n")
	if _, err := StartEmbeddedPG(EmbeddedPGOptions{Dir: dir, Fast: true}); err == nil || !strings.Contains(err.Error(), "拒絕啟動") {
		t.Fatal("PG_VERSION 改成 16 應該拒絕啟動:", err)
	}
	for _, p := range []string{"global/pg_control", "base"} {
		if _, err := os.Stat(filepath.Join(pgdata, p)); err != nil {
			t.Fatal("拒絕啟動後資料不見:", p)
		}
	}
	// PG_VERSION 不見:也拒絕
	_ = os.Remove(vf)
	if _, err := StartEmbeddedPG(EmbeddedPGOptions{Dir: dir, Fast: true}); err == nil || !strings.Contains(err.Error(), "拒絕啟動") {
		t.Fatal("PG_VERSION 不見應該拒絕啟動:", err)
	}
	if _, err := os.Stat(filepath.Join(pgdata, "global", "pg_control")); err != nil {
		t.Fatal("資料不見")
	}
	writeFile(t, vf, string(orig))

	// 取消的 context:不會啟動、不留 postgres
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := StartEmbeddedPG(EmbeddedPGOptions{Dir: dir, Fast: true, Context: ctx}); err == nil {
		t.Fatal("已取消的 context 應該中止啟動")
	}
	if _, err := os.Stat(filepath.Join(pgdata, "postmaster.pid")); err == nil {
		t.Fatal("中止後不該留下在跑的 postgres")
	}

	// 模擬「initdb 完成、建 omixor 資料庫之前被中斷」:自己起來把 omixor 資料庫刪掉
	pw, _ := embeddedPGPassword(dir)
	rt := filepath.Join(dir, "pgruntime")
	port, _ := pickPort()
	exe := pgCtl(rt)
	if exe == "" {
		t.Fatal("沒有 pg_ctl")
	}
	// 輸出要導到檔案(postgres 繼承標準輸出,用管道會卡住)
	lf, _ := os.Create(filepath.Join(dir, "pgctl.log"))
	cmd := exec.Command(exe, "start", "-w", "-D", pgdata, "-o", "-p "+itoa(int(port))+" -c listen_addresses=127.0.0.1")
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	lf.Close()
	dropDB(t, port, pw)
	if !stopPostgresFast(rt, pgdata, 30*time.Second) {
		t.Fatal("關不掉")
	}
	pg2, err := StartEmbeddedPG(EmbeddedPGOptions{Dir: dir, Fast: true})
	if err != nil {
		t.Fatal("資料庫被刪掉後應該自動補建:", err)
	}
	if err := pg2.Stop(); err != nil {
		t.Fatal(err)
	}
}
