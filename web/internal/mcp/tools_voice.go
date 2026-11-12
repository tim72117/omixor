package mcp

import (
	"fmt"
	"strings"

	"omixor-web/internal/core"
)

// 旁白語音批次:ops_voice_submit、ops_voice_jobs(同 Swift MCPHandler 的 voiceSubmit / voiceJobs)。
// 發佈沒有 MCP 工具,只能由人在畫面上按。

// rawBool 同 Swift 的 `raw[k] as? Bool`:布林,或 NSNumber 的 0 / 1
func rawBool(raw map[string]any, k string) (bool, bool) {
	switch x := raw[k].(type) {
	case bool:
		return x, true
	case int64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case float64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	}
	return false, false
}

func init() {
	Register("ops_voice_submit", func(c *Call) (any, error) {
		voice := core.VoiceOf(c.App)
		if voice == nil {
			return nil, ToolError{"旁白功能還沒啟動"}
		}
		all, _ := rawBool(c.Args.Raw, "all")
		scripts, _ := c.Args.Strings("scripts")
		if scripts == nil {
			scripts = []string{}
		}
		var limit *int
		if n, ok := c.Args.Int("limit"); ok {
			limit = &n
		}
		if dry, ok := rawBool(c.Args.Raw, "dry_run"); ok && dry {
			out, err := voice.DryRun(scripts, all, limit)
			if err != nil {
				return nil, ToolError{err.Error()}
			}
			return map[string]any{"dryRun": out}, nil
		}
		c.App.Mu.Lock()
		defer c.App.Mu.Unlock()
		job, err := voice.Submit(scripts, all, limit, c.Actor)
		if err != nil {
			return nil, ToolError{err.Error()}
		}
		ahead := 0
		for _, j := range voice.Jobs {
			if (j.State == core.VoiceQueued && j.ID != job.ID) || j.State.HoldsBatch() {
				ahead++
			}
		}
		note := "馬上送出。完成後由人在 omixor › CI/CD › 旁白發佈 試聽並發佈;可用 ops_voice_jobs 查進度"
		if ahead > 0 {
			note = fmt.Sprintf("前面還有 %d 批,輪到時自動送出", ahead)
		}
		return map[string]any{"job": voiceJSON(job, false), "note": note}, nil
	})

	Register("ops_voice_jobs", func(c *Call) (any, error) {
		voice := core.VoiceOf(c.App)
		c.App.Mu.Lock()
		defer c.App.Mu.Unlock()
		var jobs []core.VoiceJob
		if voice != nil {
			jobs = voice.Jobs
		}
		if id, ok := c.Args.String("id"); ok {
			for _, j := range jobs {
				if strings.EqualFold(j.ID, id) {
					return voiceJSON(j, true), nil
				}
			}
			return nil, ToolError{"找不到旁白批次 " + id}
		}
		list := []any{}
		for i, j := range jobs {
			if i >= 20 {
				break
			}
			list = append(list, voiceJSON(j, false))
		}
		return map[string]any{"jobs": list}, nil
	})
}

// voiceJSON 批次給 MCP 看的樣子:不含比對用的 baseline;摘要模式不列段落文字,紀錄只留最後幾行
func voiceJSON(job core.VoiceJob, full bool) any {
	d, ok := core.Object(job).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	delete(d, "baseline")
	d["stateLabel"] = job.State.Label()
	if log, ok := d["log"].([]any); ok {
		n := 3
		if full {
			n = 30
		}
		if len(log) > n {
			log = log[len(log)-n:]
		}
		d["log"] = log
	}
	if !full {
		if changes, ok := d["changes"].([]any); ok {
			out := []any{}
			for _, c := range changes {
				m, ok := c.(map[string]any)
				if !ok {
					continue
				}
				added, _ := m["added"].([]any)
				m["addedCount"] = len(added)
				delete(m, "added")
				out = append(out, m)
			}
			d["changes"] = out
		}
	}
	return d
}
