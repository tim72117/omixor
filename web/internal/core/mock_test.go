package core

import (
	"testing"
)

func TestDigitsIntAndNextNumber(t *testing.T) {
	if n, ok := DigitsInt("ALT-5121"); !ok || n != 5121 {
		t.Fatalf("DigitsInt = %d %v", n, ok)
	}
	if _, ok := DigitsInt("abc"); ok {
		t.Fatal("沒有數字應該失敗")
	}
	a := New()
	if n := a.NextNumber("alerts", 5000); n != 5125 {
		t.Fatalf("下一個告警編號 = %d", n)
	}
	if n := a.NextNumber("incidents", 2000); n != 2042 {
		t.Fatalf("下一個事件編號 = %d", n)
	}
	a.C["alerts"] = nil
	if n := a.NextNumber("alerts", 5000); n != 5001 {
		t.Fatalf("空集合 = %d", n)
	}
}

func TestMatchesAndValueAt(t *testing.T) {
	row := map[string]any{
		"id": "X-1", "sev": 2.0, "on": true, "tags": []any{"a", "b"},
		"actor": map[string]any{"kind": "person", "id": "tim"}, "nested": map[string]any{"risk": "high"},
	}
	cases := []struct {
		path string
		want any
		ok   bool
	}{
		{"sev", int64(2), true}, {"sev", "2", true}, {"sev", 2.0, true}, {"on", true, true}, {"on", "true", true},
		{"on", int64(1), false}, {"tags", "b", true}, {"tags", "c", false}, {"actor", "tim", true},
		{"nested.risk", "high", true}, {"nested..risk", "high", true}, {"", "X-1", true}, {"nope", "x", false},
		{"id", map[string]any{}, false}, {"id", nil, false}, {"nested", "high", false}, {"id.x", "X-1", false},
	}
	for _, c := range cases {
		f, present := ValueAt(c.path, row)
		if got := Matches(f, present, c.want); got != c.ok {
			t.Errorf("%q = %v:得到 %v", c.path, c.want, got)
		}
	}
}

func TestCompact(t *testing.T) {
	series := []any{}
	for i := 0; i < 5; i++ {
		series = append(series, map[string]any{"date": "d", "value": float64(i)})
	}
	obj := map[string]any{
		"cpuSeries": series,
		"log":       []any{"1", "2", "3", "4"},
		"short":     []any{"1", "2", "3", "4"},
		"nested":    []any{map[string]any{"log": []any{"a", "b", "c", "d", "e"}}},
	}
	out := Compact(obj).(map[string]any)
	if len(out["cpuSeries"].([]any)) != 3 || out["cpuSeriesCount"] != 5 {
		t.Fatalf("時間序列沒裁:%v", out)
	}
	if len(out["log"].([]any)) != 3 || out["logLines"] != 4 || out["log"].([]any)[0] != "2" {
		t.Fatalf("日誌沒裁:%v", out)
	}
	if len(out["short"].([]any)) != 4 {
		t.Fatal("一般陣列不該裁")
	}
	n := out["nested"].([]any)[0].(map[string]any)
	if n["logLines"] != 5 {
		t.Fatal("巢狀的日誌也要裁")
	}
	if len(obj["log"].([]any)) != 4 {
		t.Fatal("不能改到原本的物件")
	}
}

func TestAlertActionsAndLog(t *testing.T) {
	a := New()
	if !a.AcknowledgeAlert("ALT-5124", MCPActor) {
		t.Fatal("找不到 ALT-5124")
	}
	_, al := a.Find("alerts", "ALT-5124")
	if al["state"] != "acknowledged" || ActorFrom(al["acknowledgedBy"]) != MCPActor {
		t.Fatalf("告警 = %v", al)
	}
	if got := a.C["activity"][0]["text"]; got != "確認告警「"+Str(al, "title")+"」" {
		t.Fatalf("動態 = %v", got)
	}
	a.ResolveAlert("ALT-5124", MCPActor)
	a.SilenceAlert("ALT-5123", Person("tim"))
	if Str(a.C["activity"][0], "icon") != "bell.slash" || Str(a.C["activity"][1], "icon") != "checkmark.circle" {
		t.Fatal("icon 不對")
	}
	if a.AcknowledgeAlert("ALT-1", MCPActor) {
		t.Fatal("不存在的告警應該回傳 false")
	}
}

func TestHumanActions(t *testing.T) {
	a := New()
	a.DecideApproval("APV-311", true)
	_, ap := a.Find("approvals", "APV-311")
	if ap["state"] != "approved" || ap["decidedBy"] != "tim" {
		t.Fatalf("核准 = %v", ap)
	}
	if Str(a.C["activity"][0], "text") != "核准 AI 請求「"+Str(ap, "title")+"」" {
		t.Fatal(a.C["activity"][0])
	}
	a.DecideApproval("APV-312", false)
	if Str(a.C["activity"][0], "text")[:6] != "拒絕" {
		t.Fatal(a.C["activity"][0])
	}

	a.SetAutonomy("suggest", "ai-autoscale")
	_, au := a.Find("automations", "ai-autoscale")
	if au["autonomy"] != "suggest" || Str(a.C["activity"][0], "text") != "把「"+Str(au, "name")+"」改為只建議" {
		t.Fatal(a.C["activity"][0])
	}
	was := au["enabled"].(bool)
	a.ToggleAutomation("ai-autoscale")
	if au["enabled"].(bool) == was {
		t.Fatal("沒切換")
	}

	a.ApproveDeployment("dep-3070")
	_, d := a.Find("deployments", "dep-3070")
	if d["status"] != "running" {
		t.Fatal(d)
	}
	want := "核准部署 " + Str(d, "service") + " " + Str(d, "version") + " 到 " + DeployEnvLabel[Str(d, "env")]
	if Str(a.C["activity"][0], "text") != want {
		t.Fatal(a.C["activity"][0])
	}

	nd := a.Rollback("dep-3069")
	_, old := a.Find("deployments", "dep-3069")
	if a.C["deployments"][0]["id"] != nd["id"] || nd["version"] != old["previousVersion"] || nd["previousVersion"] != old["version"] ||
		nd["strategy"] != "Rolling" || nd["progress"] != 0.05 || nd["healthGate"] != "pending" {
		t.Fatalf("回滾 = %v", nd)
	}
	if _, ok := nd["canaryPercent"]; ok {
		t.Fatal("回滾不該有 canaryPercent")
	}

	a.SetIntegration("github", false)
	_, in := a.Find("integrations", "github")
	if in["connected"] != false {
		t.Fatal(in)
	}

	if a.AddIncidentNote("INC-2041", "", Person("tim")) {
		t.Fatal("空註記不該加")
	}
	_, inc := a.Find("incidents", "INC-2041")
	n := len(inc["timeline"].([]any))
	a.AddIncidentNote("INC-2041", "看一下", Person("tim"))
	tl := inc["timeline"].([]any)
	if len(tl) != n+1 || tl[n].(map[string]any)["kind"] != "note" {
		t.Fatal("註記沒加")
	}
}

func TestUpdateIncidentAndMove(t *testing.T) {
	a := New()
	a.UpdateIncident("INC-2041", "resolved", nil, MCPActor)
	_, inc := a.Find("incidents", "INC-2041")
	tl := inc["timeline"].([]any)
	last := tl[len(tl)-1].(map[string]any)
	if inc["resolvedAt"] == nil || last["text"] != "狀態改為「已解決」" || last["kind"] != "status" {
		t.Fatalf("事件 = %v", last)
	}
	if Str(a.C["activity"][0], "text") != "INC-2041 狀態改為已解決" {
		t.Fatal(a.C["activity"][0])
	}
	n := len(a.C["activity"])
	a.MoveWorkItem("OPS-151", "done", nil)
	if len(a.C["activity"]) != n {
		t.Fatal("沒有執行者時不寫動態")
	}
	who := MCPActor
	a.MoveWorkItem("OPS-151", "review", &who)
	if Str(a.C["activity"][0], "text") != "把 OPS-151 移到「審查中」" {
		t.Fatal(a.C["activity"][0])
	}
}

func TestRetryAndCancel(t *testing.T) {
	a := New()
	nr := a.RetryRun(1286, MCPActor)
	if nr == nil || nr["id"] != 1291.0 || nr["trigger"] != "手動重試" || nr["status"] != "running" {
		t.Fatalf("重試 = %v", nr)
	}
	stages := nr["stages"].([]any)
	for i, s := range stages {
		st := s.(map[string]any)
		want := "queued"
		if i == 0 {
			want = "running"
		}
		if st["status"] != want {
			t.Fatalf("stage %d = %v", i, st["status"])
		}
		for _, j := range st["jobs"].([]any) {
			if _, ok := j.(map[string]any)["duration"]; ok {
				t.Fatal("job 不該留 duration")
			}
		}
	}
	_, old := a.Find("pipeline_runs", "1286")
	if old["status"] != "failed" {
		t.Fatal("原本的 run 不能被改")
	}
	if !a.CancelRun(1291, MCPActor) || Str(a.C["activity"][0], "text") != "取消 "+Str(old, "pipeline")+" #1291" {
		t.Fatal(a.C["activity"][0])
	}
}

func TestTick(t *testing.T) {
	a := New()
	run := map[string]any{
		"id": 1.0, "status": "running", "startedAt": "2026-01-01T00:00:00Z",
		"stages": []any{
			map[string]any{"status": "running", "jobs": []any{map[string]any{"status": "queued"}, map[string]any{"status": "queued"}}},
			map[string]any{"status": "queued", "jobs": []any{}},
		},
	}
	a.C["pipeline_runs"] = []map[string]any{run}
	a.C["deployments"] = []map[string]any{{"status": "running", "progress": 0.97}, {"status": "waitingApproval", "progress": 0.0}}
	cpu0 := Num(a.C["servers"][0], "cpu")
	series0 := len(a.C["servers"][0]["cpuSeries"].([]any))

	job := func(s, j int) string {
		return Str(run["stages"].([]any)[s].(map[string]any)["jobs"].([]any)[j].(map[string]any), "status")
	}
	stage := func(s int) string { return Str(run["stages"].([]any)[s].(map[string]any), "status") }

	a.LiveTick()
	if job(0, 0) != "running" || job(0, 1) != "queued" {
		t.Fatalf("第 1 步:%s %s", job(0, 0), job(0, 1))
	}
	d := a.C["deployments"][0]
	if d["status"] != "succeeded" || d["progress"] != 1.0 || d["healthGate"] != "passed" || d["finishedAt"] == nil {
		t.Fatalf("部署 = %v", d)
	}
	if a.C["deployments"][1]["progress"] != 0.0 {
		t.Fatal("等待核准的部署不該動")
	}
	a.LiveTick()
	if job(0, 0) != "succeeded" || Num(run["stages"].([]any)[0].(map[string]any)["jobs"].([]any)[0].(map[string]any), "duration") != 20 {
		t.Fatal("第 2 步")
	}
	a.LiveTick() // job 1 → running
	a.LiveTick() // job 1 → succeeded(duration 20+13)
	if job(0, 1) != "succeeded" || stage(0) != "succeeded" {
		t.Fatalf("第 4 步:%s %s", job(0, 1), stage(0))
	}
	if Num(run["stages"].([]any)[0].(map[string]any)["jobs"].([]any)[1].(map[string]any), "duration") != 33 {
		t.Fatal("duration 規則")
	}
	a.LiveTick() // 第二個 stage 沒有 job:直接成功
	if stage(1) != "succeeded" || run["status"] != "running" {
		t.Fatalf("第 5 步:%s %s", stage(1), run["status"])
	}
	a.LiveTick()
	if run["status"] != "succeeded" || Num(run, "duration") <= 0 {
		t.Fatalf("第 6 步:%v", run)
	}

	if Num(a.C["servers"][0], "cpu") == cpu0 && len(a.C["servers"][0]["cpuSeries"].([]any)) == series0 {
		t.Fatal("伺服器指標沒動")
	}
	if l := len(a.C["servers"][0]["cpuSeries"].([]any)); l != min(120, series0+6) {
		t.Fatalf("cpuSeries 長度 %d", l)
	}
	for i := 0; i < 130; i++ {
		a.LiveTick()
	}
	for _, s := range a.C["servers"] {
		if l := len(s["cpuSeries"].([]any)); l > 120 {
			t.Fatalf("cpuSeries 超過 120:%d", l)
		}
		if c := Num(s, "cpu"); c < 2 || c > 99 {
			t.Fatalf("cpu 超出範圍:%v", c)
		}
	}
}

func TestBadges(t *testing.T) {
	a := New()
	v, _ := View(a, "badges")
	b := v.(map[string]any)
	// 範例資料:4 個觸發中告警、2 個未結束事件、5 個待核准、3 個執行中/排隊的流程
	want := map[string]float64{"monitoring": 4, "incidents": 2, "automation": 5, "pipelines": 3}
	for k, n := range want {
		if b[k] != n {
			t.Errorf("%s = %v,應該是 %v", k, b[k], n)
		}
	}
}

func TestUIActions(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir()) // incident_note 寫進核心(測試用記憶體核心,每個資料夾一份)
	a := New()
	if _, err := Action(a, "approval_decide", map[string]any{"id": "APV-311", "approve": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "approval_decide", map[string]any{"id": "APV-1"}); err == nil {
		t.Fatal("找不到應該錯誤")
	}
	if _, err := Action(a, "automation_autonomy", map[string]any{"id": "ai-cost", "autonomy": "never"}); err == nil {
		t.Fatal("不合法的 autonomy 應該錯誤")
	}
	out, err := Action(a, "automation_autonomy", map[string]any{"id": "ai-cost", "autonomy": "auto"})
	if err != nil || out.(map[string]any)["autonomy"] != "auto" {
		t.Fatal(out, err)
	}
	if _, err := Action(a, "automation_toggle", map[string]any{"id": "ai-cost"}); err != nil {
		t.Fatal(err)
	}
	out, err = Action(a, "deployment_rollback", map[string]any{"id": "dep-3069"})
	if err != nil || out.(map[string]any)["strategy"] != "Rolling" {
		t.Fatal(out, err)
	}
	if _, err := Action(a, "deployment_approve", map[string]any{"id": "dep-3070"}); err != nil {
		t.Fatal(err)
	}
	out, err = Action(a, "integration_set", map[string]any{"id": "github", "connected": false})
	if err != nil || out.(map[string]any)["connected"] != false {
		t.Fatal(out, err)
	}
	if _, err := Action(a, "incident_note", map[string]any{"id": "INC-2041", "text": " "}); err == nil {
		t.Fatal("空註記應該錯誤")
	}
	out, err = Action(a, "incident_note", map[string]any{"id": "INC-2041", "text": "人加的註記"})
	if err != nil {
		t.Fatal(err)
	}
	tl := out.(map[string]any)["timeline"].([]any)
	last := tl[len(tl)-1].(map[string]any)
	if last["text"] != "人加的註記" || ActorFrom(last["actor"]) != Person("tim") {
		t.Fatal(last)
	}
}
