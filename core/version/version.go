// Package version 是 omixor 各程式(server、web、CLI)共用的版本資訊。
// 建置時用 -ldflags 注入:
//
//	-X omixor-core/version.Version=<版本> -X omixor-core/version.Commit=<git commit> -X omixor-core/version.BuildTime=<RFC3339 時間>
//
// 沒注入時是 "dev";Commit、BuildTime 會盡量從 runtime/debug.ReadBuildInfo 的 vcs.revision、vcs.time 補。
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// 這三個變數由 -ldflags -X 注入
var (
	Version   = "dev"
	Commit    = "dev"
	BuildTime = "dev"
)

// Data 版本資訊(也是 GET /api/version 的回應)
type Data struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
}

// Info 目前這支程式的版本;沒注入的欄位盡量用建置資訊補
func Info() Data {
	d := Data{Version: Version, Commit: Commit, BuildTime: BuildTime}
	if d.Commit != "dev" && d.BuildTime != "dev" {
		return d
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if d.Commit == "dev" && s.Value != "" {
					d.Commit = s.Value
				}
			case "vcs.time":
				if d.BuildTime == "dev" && s.Value != "" {
					d.BuildTime = s.Value
				}
			}
		}
	}
	return d
}

// String 一行文字,例如 "1.2.0 (abc1234, 2026-11-12T01:02:03Z)"
func (d Data) String() string {
	c := d.Commit
	if len(c) > 12 {
		c = c[:12]
	}
	return fmt.Sprintf("%s (%s, %s)", d.Version, c, d.BuildTime)
}

// Fetch 查遠端 omixor server 的 GET /api/version(免登入);timeout 內沒回來就當作查不到
func Fetch(base string, timeout time.Duration) (Data, error) {
	var d Data
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/api/version", nil)
	if err != nil {
		return d, err
	}
	res, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return d, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return d, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 1<<16)).Decode(&d); err != nil {
		return d, err
	}
	return d, nil
}
