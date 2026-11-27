package service

import (
	"testing"
	"time"
)

// refund 只退同一個視窗裡佔的名額:退了可以再用,視窗換掉後不會多給
func TestRateWindowRefund(t *testing.T) {
	var r rateWindow
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	w, ok := r.take(now, "a", 1, 1)
	if !ok || r.allow(now, "a", 1, 1) {
		t.Fatal("上限 1")
	}
	r.refund(w, "a")
	if !r.allow(now, "a", 1, 1) {
		t.Fatal("退回後要可以再用")
	}
	later := now.Add(time.Minute)
	if !r.allow(later, "a", 1, 1) {
		t.Fatal("新視窗")
	}
	r.refund(w, "a") // 舊視窗的
	if r.allow(later, "a", 1, 1) {
		t.Fatal("舊視窗的退回不能影響新視窗")
	}
}
