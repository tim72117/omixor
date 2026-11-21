package coretest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepOld(t *testing.T) {
	tmp := t.TempDir()
	mk := func(name string, age time.Duration) string {
		d := filepath.Join(tmp, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		_ = os.Chtimes(d, ts, ts)
		return d
	}
	old := mk("omixor-console-core-test-1", 3*time.Hour)
	fresh := mk("omixor-console-core-test-2", 5*time.Minute)
	other := mk("something-else-3", 3*time.Hour)
	file := filepath.Join(tmp, "omixor-console-core-test-file")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	_ = os.Chtimes(file, time.Now().Add(-5*time.Hour), time.Now().Add(-5*time.Hour))
	if n := sweepOld(tmp, "omixor-console-core-test-", time.Hour, time.Now()); n != 1 {
		t.Fatalf("該清 1 個:%d", n)
	}
	for _, p := range []string{fresh, other, file} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 不該被清:%v", p, err)
		}
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("舊資料夾沒被清掉")
	}
}
