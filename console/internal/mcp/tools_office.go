package mcp

import (
	"context"
	"errors"
	"time"

	"omixor-core/httperr"
	"omixor-web/internal/office"
)

// 3D 虛擬辦公室(internal/office;HTTP 端是 internal/web/office.go,兩邊共用同一個 Hub)。
// 這兩個工具只有 Go 版有,定義在 spec/tools_console.json。
//
//	office_move  {text} 或 {action, agents, target},可帶 wait 秒數 → {ids, id, viewers, commands, waited?, finished?}
//	office_state {command?, limit?} → 同 GET /api/office/state,另加 places;給 command 只回那個命令
func init() {
	Register("office_move", func(c *Call) (any, error) {
		hub := office.For(c.App)
		args := map[string]any{}
		for _, k := range []string{"text", "action", "agents", "target"} {
			if v, ok := c.Args.Raw[k]; ok {
				args[k] = v
			}
		}
		wait := 0
		if _, ok := c.Args.Raw["wait"]; ok {
			n, ok := c.Args.Int("wait")
			if !ok || n < 0 || n > 120 {
				return nil, ToolError{Message: "wait 要是 0–120 的秒數"}
			}
			wait = n
		}
		req, err := office.RequestFromArgs(args)
		if err != nil {
			return nil, officeToolError(err)
		}
		cmds, err := hub.Submit(req)
		if err != nil {
			return nil, officeToolError(err)
		}
		ids := make([]string, 0, len(cmds))
		for _, x := range cmds {
			ids = append(ids, x.ID)
		}
		out := map[string]any{"ids": ids, "id": ids[0], "viewers": hub.Viewers()}
		if wait > 0 {
			var done bool
			cmds, done = hub.Wait(context.Background(), ids, time.Duration(wait)*time.Second)
			out["waited"] = true
			out["finished"] = done
		}
		out["commands"] = cmds
		return out, nil
	})

	Register("office_state", func(c *Call) (any, error) {
		hub := office.For(c.App)
		if id := c.Args.Str("command"); id != "" {
			cmd := hub.Command(id)
			if cmd == nil {
				return nil, Errorf("找不到命令 %s(只保留最近 200 個)", id)
			}
			return cmd, nil
		}
		limit, ok := c.Args.Int("limit")
		if !ok {
			limit = 20
		}
		if limit < 1 || limit > 200 {
			return nil, ToolError{Message: "limit 要是 1–200"}
		}
		st := hub.State(limit)
		if places, err := hub.Places(); err == nil {
			st["places"] = places
		}
		return st, nil
	})
}

func officeToolError(err error) error {
	var ve office.ValidationError
	if errors.As(err, &ve) {
		return ToolError{Message: ve.Msg}
	}
	return ToolError{Message: "虛擬辦公室:" + httperr.Report("mcp 虛擬辦公室", err).Msg} // 找不到資料夾等的路徑換成通用訊息、原文進 log
}
