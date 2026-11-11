package core

import (
	"math"
	"os"
	"sort"
	"time"
)

// 模擬即時資料,對應 Swift AppState 的 startLiveUpdates / tick:
// 每 3 秒讓伺服器指標、部署進度與執行中的流程往前走一點。OPS_NO_TIMERS=1 時不啟動。

const liveInterval = 3 * time.Second

func init() {
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			t := time.NewTicker(liveInterval)
			defer t.Stop()
			for range t.C {
				a.Mu.Lock()
				topics := a.LiveTick()
				a.Mu.Unlock()
				a.Changed(topics...)
			}
		}()
	})
}

type liveState struct{ tickCount int }

func (a *App) live() *liveState {
	if s, ok := a.Ext["mock.live"].(*liveState); ok {
		return s
	}
	s := &liveState{}
	a.Ext["mock.live"] = s
	return s
}

// LiveTick 往前走一步(拿著 Mu 呼叫),回傳有變動的主題
func (a *App) LiveTick() []string {
	st := a.live()
	st.tickCount++
	now := time.Now()
	nowS := ISO(now)
	for i, s := range a.C["servers"] {
		wobble := math.Sin(float64(st.tickCount+i*7)/3) * 4
		cpu := math.Min(99, math.Max(2, Num(s, "cpu")+wobble*0.5))
		mem := math.Min(98, math.Max(5, Num(s, "memory")+wobble*0.15))
		s["cpu"], s["memory"] = cpu, mem
		s["cpuSeries"] = appendPoint(s["cpuSeries"], nowS, cpu)
		s["memorySeries"] = appendPoint(s["memorySeries"], nowS, mem)
	}
	for _, d := range a.C["deployments"] {
		if Str(d, "status") != "running" {
			continue
		}
		p := math.Min(1, Num(d, "progress")+0.04)
		d["progress"] = p
		if p >= 1 {
			d["status"] = "succeeded"
			d["finishedAt"] = nowS
			d["healthGate"] = "passed"
		}
	}
	for _, r := range a.C["pipeline_runs"] {
		if Str(r, "status") == "running" {
			advanceRun(r, now)
		}
	}
	topics := []string{"servers", "deployments", "pipeline_runs"}
	if a.syncTrackedDeployments() {
		topics = append(topics, "services")
	}
	return topics
}

func appendPoint(series any, date string, v float64) []any {
	arr, _ := series.([]any)
	arr = append(arr, map[string]any{"date": date, "value": v})
	if len(arr) > 120 {
		arr = arr[1:]
	}
	return arr
}

func isActive(m map[string]any) bool {
	s := Str(m, "status")
	return s == "running" || s == "queued"
}

// advanceRun 把執行中的 run 往前推一個 job
func advanceRun(run map[string]any, now time.Time) {
	stages, _ := run["stages"].([]any)
	var stage map[string]any
	for _, st := range stages {
		if m, ok := st.(map[string]any); ok && isActive(m) {
			stage = m
			break
		}
	}
	if stage == nil {
		run["status"] = "succeeded"
		if start, err := ParseTime(Str(run, "startedAt")); err == nil {
			run["duration"] = now.Sub(start).Seconds()
		}
		return
	}
	stage["status"] = "running"
	jobs, _ := stage["jobs"].([]any)
	for j, jb := range jobs {
		job, ok := jb.(map[string]any)
		if !ok || !isActive(job) {
			continue
		}
		if Str(job, "status") == "queued" {
			job["status"] = "running"
		} else {
			job["status"] = "succeeded"
			job["duration"] = float64(20 + (j*13)%70)
		}
		break
	}
	all := true
	for _, jb := range jobs {
		if job, ok := jb.(map[string]any); !ok || Str(job, "status") != "succeeded" {
			all = false
		}
	}
	if all {
		stage["status"] = "succeeded"
	}
}

func sortStable(items []map[string]any, less func(x, y map[string]any) bool) {
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
}

// MARK: 側邊欄徽章

func init() {
	OnBadge(func(a *App) map[string]int {
		return map[string]int{
			"monitoring": len(a.FiringAlerts()),
			"incidents":  len(a.OpenIncidents()),
			"automation": len(a.PendingApprovals()),
			"pipelines":  len(a.RunningPipelines()),
		}
	})
}
