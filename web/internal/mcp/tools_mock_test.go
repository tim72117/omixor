package mcp

import (
	"strings"
	"testing"

	"omixor-web/internal/core"
)

func mockRun(t *testing.T, a *core.App, name string, args map[string]any) map[string]any {
	t.Helper()
	out, err := Run(a, name, args, core.MCPActor)
	if err != nil {
		t.Fatalf("%s:%v", name, err)
	}
	m, _ := core.Object(out).(map[string]any)
	return m
}

func mockRunErr(t *testing.T, a *core.App, name string, args map[string]any) string {
	t.Helper()
	_, err := Run(a, name, args, core.MCPActor)
	if err == nil {
		t.Fatalf("%s %v 應該失敗", name, args)
	}
	if _, ok := err.(ToolError); !ok {
		t.Fatalf("%s 應該是 ToolError:%T %v", name, err, err)
	}
	return err.Error()
}

func TestMockToolErrors(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir()) // 事件寫進核心(測試用記憶體核心,每個資料夾一份)
	a := core.New()
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list", map[string]any{}, "缺少參數 collection"},
		{"list", map[string]any{"collection": "x"}, "沒有這個集合:x。可用:people、services"},
		{"get", map[string]any{"collection": "alerts", "id": "ALT-1"}, "alerts 裡找不到 id「ALT-1」"},
		{"navigate", map[string]any{"env": "qa"}, "env 必須是 production、staging、development 之一,收到「qa」"},
		{"alert_update", map[string]any{"alert_id": "ALT-1"}, "找不到告警 ALT-1"},
		{"alert_update", map[string]any{"alert_id": "ALT-5121"}, "action 與 ai_note 至少要給一項"},
		{"alert_update", map[string]any{"alert_id": "ALT-5121", "action": "x"}, "action 必須是 acknowledge、resolve 或 silence,收到「x」"},
		{"alert_create", map[string]any{"service": "nope"}, "找不到服務 nope。可用:course-web、api-server"},
		{"incident_declare", map[string]any{"title": "x", "sev": 9.0}, "sev 必須是 1–4"},
		{"incident_declare", map[string]any{"title": "x"}, "缺少或無效的整數參數 sev"},
		{"incident_declare", map[string]any{"title": "x", "sev": 1.0}, "缺少參數 impact"},
		{"incident_declare", map[string]any{"title": "x", "sev": 1.0, "impact": "i", "commander": "nobody"}, "找不到 person nobody"},
		{"incident_declare", map[string]any{"title": "x", "sev": 1.0, "impact": "i", "alert_ids": []any{"ALT-1"}}, "找不到告警 ALT-1"},
		{"incident_update", map[string]any{"incident_id": "INC-1", "note": "x"}, "找不到事件 INC-1"},
		{"incident_update", map[string]any{"incident_id": "INC-2041"}, "status、note、finding、ai_summary、impact 至少要給一項"},
		{"work_item_move", map[string]any{"id": "OPS-1", "status": "done"}, "找不到工作項目 OPS-1"},
		{"pipeline_update", map[string]any{"run_id": 1.0}, "找不到 pipeline run #1"},
		{"pipeline_update", map[string]any{"run_id": 1286.0, "action": "cancel"}, "#1286 目前是「失敗」,不能取消"},
		{"request_approval", map[string]any{"automation": "x"}, "找不到自動化 x"},
		{"log_append", map[string]any{"level": "x"}, "level 必須是 debug、info、warn、error 之一,收到「x」"},
		{"activity_post", map[string]any{}, "缺少參數 text"},
	}
	for _, c := range cases {
		if got := mockRunErr(t, a, c.tool, c.args); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s %v:\n  得到 %s\n  應該 %s", c.tool, c.args, got, c.want)
		}
	}
}

func TestMockToolsFlow(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir()) // 事件寫進核心(測試用記憶體核心,每個資料夾一份)
	a := core.New()
	al := mockRun(t, a, "alert_create", map[string]any{"service": "payments", "title": "t", "severity": "critical", "rule": "r", "value": "v"})
	if al["id"] != "ALT-5125" || al["state"] != "firing" {
		t.Fatal(al)
	}
	if _, ok := al["aiNote"]; ok {
		t.Fatal("沒給 ai_note 就不該有這個欄位")
	}

	// 缺 impact:什麼都不改(事件改由核心處理後,不再沿用 Swift 版「先改告警再檢查 impact」的行為)
	mockRunErr(t, a, "incident_declare", map[string]any{"title": "x", "sev": 2.0, "alert_ids": []any{"ALT-5125"}})
	if g := mockRun(t, a, "get", map[string]any{"collection": "alerts", "id": "ALT-5125"}); g["incidentId"] != nil {
		t.Fatal(g)
	}
	inc := mockRun(t, a, "incident_declare", map[string]any{"title": "x", "sev": 2.0, "impact": "i", "alert_ids": []any{"ALT-5125"}})
	if inc["id"] != "INC-2042" || inc["commander"] != "tim" || len(inc["timeline"].([]any)) != 2 {
		t.Fatal(inc)
	}
	if txt := inc["timeline"].([]any)[0].(map[string]any)["text"]; txt != "宣告 SEV2 事件,指揮官:蔡庭宇" {
		t.Fatal(txt)
	}
	if g := mockRun(t, a, "get", map[string]any{"collection": "alerts", "id": "ALT-5125"}); g["incidentId"] != "INC-2042" {
		t.Fatal("宣告後關聯告警要標上事件", g)
	}
	if tl := inc["timeline"].([]any); tl[1].(map[string]any)["text"] != "關聯告警「t」" {
		t.Fatal(tl[1])
	}
	u := mockRun(t, a, "incident_update", map[string]any{"incident_id": "INC-2042", "status": "resolved", "finding": "找到了"})
	if u["status"] != "resolved" || u["resolvedAt"] == nil || len(u["timeline"].([]any)) != 4 {
		t.Fatal(u)
	}

	w := mockRun(t, a, "work_item_create", map[string]any{"title": "w", "type": "bug", "incident_id": "INC-2042"})
	if w["id"] != "OPS-156" || w["status"] != "todo" || w["points"] != 3.0 || w["createdByAI"] != true {
		t.Fatal(w)
	}
	g := mockRun(t, a, "get", map[string]any{"collection": "incidents", "id": "inc-2042"})
	if f := g["followUps"].([]any); len(f) != 1 || f[0] != "OPS-156" {
		t.Fatal(g)
	}

	r := mockRun(t, a, "pipeline_update", map[string]any{"run_id": 1286.0, "action": "retry"})
	if r["retried"] != 1286.0 || r["newRun"].(map[string]any)["id"] != 1291.0 {
		t.Fatal(r)
	}

	ap := mockRun(t, a, "request_approval", map[string]any{"title": "t", "risk": "low", "action": "a", "rationale": "r", "rollback_plan": "b"})
	if ap["id"] != "APV-314" || ap["automation"] != "mcp" || ap["state"] != "pending" {
		t.Fatal(ap)
	}

	l := mockRun(t, a, "list", map[string]any{"collection": "activity", "where": map[string]any{"actor": "mcp"}, "limit": 10.0})
	texts := []string{}
	for _, it := range l["items"].([]any) {
		texts = append(texts, it.(map[string]any)["text"].(string))
	}
	want := []string{"送出待核准請求「t」", "重新執行 course-web / PR #1418 #1291", "建立 OPS-156「w」", "INC-2042 狀態改為已解決", "宣告 SEV2 事件 INC-2042「x」", "新告警「t」(Critical)"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("動態:\n%v\n%v", texts, want)
	}

	p := mockRun(t, a, "activity_post", map[string]any{"text": " hi "})
	if p["text"] != "hi" || p["icon"] != "sparkles" || p["section"] != "overview" {
		t.Fatal(p)
	}
}

func TestMockListPaging(t *testing.T) {
	a := core.New()
	l := mockRun(t, a, "list", map[string]any{"collection": "logs", "limit": 0.0, "offset": -2.0})
	if l["count"] != 1.0 || l["offset"] != 0.0 || l["total"] != 150.0 {
		t.Fatal(l["count"], l["offset"], l["total"])
	}
	l = mockRun(t, a, "list", map[string]any{"collection": "logs", "limit": 999.0})
	if l["count"] != 150.0 {
		t.Fatal(l["count"])
	}
	l = mockRun(t, a, "list", map[string]any{"collection": "logs"})
	if l["count"] != 30.0 {
		t.Fatal(l["count"])
	}
	l = mockRun(t, a, "list", map[string]any{"collection": "alerts", "search": " PADDLE "})
	if l["total"].(float64) < 1 {
		t.Fatal(l)
	}
	o := mockRun(t, a, "overview", nil)
	if o["counts"].(map[string]any)["firingAlerts"] != 4.0 {
		t.Fatal(o["counts"])
	}
	first := o["firingAlerts"].([]any)[0].(map[string]any)
	if first["severity"] != "critical" {
		t.Fatal("觸發中告警要依嚴重度排序")
	}
}

// live tick 同時在改資料時,工具的回傳值(鎖外才轉 JSON)不能和它搶(用 -race 跑)
func TestMockToolsWithLiveTick(t *testing.T) {
	a := core.New()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			a.Mu.Lock()
			a.LiveTick()
			a.Mu.Unlock()
		}
	}()
	for i := 0; i < 50; i++ {
		for _, args := range []map[string]any{
			{"collection": "servers"}, {"collection": "pipeline_runs"}, {"collection": "deployments"},
		} {
			out, err := Run(a, "list", args, core.MCPActor)
			if err != nil {
				t.Fatal(err)
			}
			_ = core.Marshal(out)
		}
		out, _ := Run(a, "get", map[string]any{"collection": "servers", "id": "gke-prod-ae1-a1"}, core.MCPActor)
		_ = core.Marshal(out)
		out, _ = Run(a, "overview", nil, core.MCPActor)
		_ = core.Marshal(out)
	}
	<-done
}
