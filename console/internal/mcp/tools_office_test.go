package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omixor-web/internal/office"
)

const officeMCPPlaces = `{"agents":[{"id":"ceo","name":"CEO","aliases":["ceo"],"seat":"seat_ceo"},{"id":"engineer","name":"工程師","aliases":["engineer","工程師"],"seat":"seat_engineer"}],
"places":[{"id":"seat_ceo","kind":"seat","name":"CEO的座位","aliases":[]},{"id":"seat_engineer","kind":"seat","name":"工程師的座位","aliases":[]},{"id":"meeting","kind":"shared","name":"會議室","aliases":["會議室","meeting"]}]}`

func TestOfficeTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "places.json"), []byte(officeMCPPlaces), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMIXOR_OFFICE_DIR", dir)
	a := extTestApp(t) // 新的 App → 新的 Hub(第一次用時才找資料夾)

	// 驗證
	for _, bad := range []map[string]any{
		{"action": "goto", "agents": []any{"nobody"}, "target": "meeting"},
		{"action": "goto", "agents": []any{"ceo"}, "target": "mars"},
		{"text": "ceo 去 火星"},
		{"action": "stop", "agents": []any{"ceo"}, "wait": 999},
	} {
		if msg := extErr(t, a, "office_move", bad); msg == "" {
			t.Error(bad)
		}
	}

	// 沒有檢視器:no_viewer,wait 也馬上回
	start := time.Now()
	out := extOK(t, a, "office_move", map[string]any{"text": "工程師 去 會議室", "wait": 10})
	c := out["commands"].([]any)[0].(map[string]any)
	if c["status"] != office.StatusNoViewer || out["finished"] != true || time.Since(start) > 3*time.Second {
		t.Fatal(out)
	}

	// 有檢視器:wait 等到抵達
	hub := office.For(a)
	v, err := hub.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Unsubscribe(v)
	go func() {
		for m := range v.C {
			if m.Event != "command" {
				continue
			}
			hub.Delivered(v, m.CmdID)
			var w struct {
				ID     string        `json:"id"`
				Action office.Action `json:"action"`
			}
			_ = json.Unmarshal(m.Data, &w)
			res := []office.ReportResult{}
			for _, id := range w.Action.Agents {
				res = append(res, office.ReportResult{Agent: id, OK: true})
			}
			_, _ = hub.Report(office.Report{ViewerID: v.ID, Type: "ack", CommandID: w.ID, Results: res})
			time.Sleep(30 * time.Millisecond)
			for _, id := range w.Action.Agents {
				_, _ = hub.Report(office.Report{ViewerID: v.ID, Type: "arrived", CommandID: w.ID, Agent: id, Arrived: &office.ArrivedDetail{Where: "會議室"}})
			}
		}
	}()
	out = extOK(t, a, "office_move", map[string]any{"action": "goto", "agents": []any{"ceo", "engineer"}, "target": "meeting", "wait": 5})
	c = out["commands"].([]any)[0].(map[string]any)
	if c["status"] != office.StatusArrived || out["finished"] != true || out["viewers"].(float64) != 1 {
		t.Fatal(out)
	}
	id := out["id"].(string)

	// 不等:馬上回 pending / delivered
	out = extOK(t, a, "office_move", map[string]any{"action": "return", "agents": "ceo"})
	if s := out["commands"].([]any)[0].(map[string]any)["status"]; s != office.StatusPending && s != office.StatusDelivered && s != office.StatusRunning {
		t.Fatal(out)
	}

	// office_state:角色、命令、地點;單一命令
	st := extOK(t, a, "office_state", map[string]any{})
	if st["viewerCount"].(float64) != 1 || len(st["places"].([]any)) != 3 || len(st["agents"].([]any)) != 2 {
		t.Fatal(st)
	}
	one := extOK(t, a, "office_state", map[string]any{"command": id})
	if one["id"] != id || one["status"] != office.StatusArrived {
		t.Fatal(one)
	}
	if msg := extErr(t, a, "office_state", map[string]any{"command": "oc-x"}); !strings.Contains(msg, "Command oc-x not found") {
		t.Fatal(msg)
	}
	if msg := extErr(t, a, "office_state", map[string]any{"limit": 0}); msg == "" {
		t.Fatal("limit 0 要錯")
	}
}
