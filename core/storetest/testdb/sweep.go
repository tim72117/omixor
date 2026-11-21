package testdb

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// 內建 PG 的暫存資料夾(omixor-testpg-*)在測試程序被殺或 panic 時不會被 Main 清掉;
// 下次要起內建 PG 前,sweepOrphans 把超過一小時的清掉:資料夾裡的 postmaster.pid 指向的行程確實是 postgres、
// 且資料目錄就是這個資料夾的 data,才用 pg_ctl stop -m immediate 停掉再刪;pid 不是 postgres、不存在、或判斷不了就只刪資料夾,不送任何訊號。

const (
	embDirPrefix = "omixor-testpg-"
	orphanAfter  = time.Hour
)

// 可替換的行程操作(測試用)
var (
	pidIsPostgres = realPidIsPostgres
	stopPostgres  = realStopPostgres
)

// sweepOrphans 清掉 tmp 底下名稱以 prefix 開頭、修改時間早於 now-olderThan 的資料夾;回傳清掉的資料夾數
func sweepOrphans(tmp, prefix string, olderThan time.Duration, now time.Time) int {
	dirs, _ := filepath.Glob(filepath.Join(tmp, prefix+"*"))
	n := 0
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() || now.Sub(fi.ModTime()) < olderThan {
			continue
		}
		data := filepath.Join(d, "data")
		if pid, dataDir, ok := readPostmasterPID(data); ok && sameDir(dataDir, data) {
			if isPG, known := pidIsPostgres(pid); known && isPG {
				stopPostgres(filepath.Join(d, "runtime"), data)
			}
		}
		if os.RemoveAll(d) == nil {
			n++
		}
	}
	return n
}

// readPostmasterPID 讀 postmaster.pid:第一行行程 id、第二行資料目錄
func readPostmasterPID(dataDir string) (pid int, dir string, ok bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, "postmaster.pid"))
	if err != nil {
		return 0, "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return 0, "", false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return 0, "", false
	}
	return pid, strings.TrimSpace(lines[1]), true
}

func sameDir(a, b string) bool {
	norm := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		p = filepath.ToSlash(filepath.Clean(p))
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	return norm(a) == norm(b)
}

// realPidIsPostgres 這個 pid 的行程是不是 postgres;known=false 表示判斷不了(這時候不送訊號)
func realPidIsPostgres(pid int) (isPG, known bool) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return false, false
		}
		return strings.Contains(strings.ToLower(string(out)), "postgres"), true
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		return strings.Contains(strings.ToLower(strings.TrimSpace(string(b))), "postgres"), true
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok { // 行程不存在
			return false, true
		}
		return false, false
	}
	return strings.Contains(strings.ToLower(filepath.Base(strings.TrimSpace(string(out)))), "postgres"), true
}

func realStopPostgres(runtimeDir, dataDir string) {
	for _, n := range []string{"pg_ctl.exe", "pg_ctl"} {
		p := filepath.Join(runtimeDir, "bin", n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			_ = exec.Command(p, "stop", "-m", "immediate", "-w", "-t", "20", "-D", dataDir).Run()
			time.Sleep(time.Second) // Windows 要過一下才放開檔案
			return
		}
	}
}
