package core

import (
	"testing"
	"time"
)

func lastLog(a *App) (Actor, string, string) {
	it := a.C["activity"][0]
	return ActorFrom(it["actor"]), Str(it, "icon"), Str(it, "text")
}

func mustAction(t *testing.T, a *App, name string, args map[string]any) map[string]any {
	t.Helper()
	out, err := Action(a, name, args)
	if err != nil {
		t.Fatalf("%s:%v", name, err)
	}
	m, _ := out.(map[string]any)
	return m
}

func TestPullRequestActions(t *testing.T) {
	a := New()
	_, pr := a.Find("pull_requests", "1426")
	title := Str(pr, "title")
	cases := []struct{ status, note, icon, text string }{
		{"approved", "  看過了 ", "checkmark.seal", "核准 PR #1426「" + title + "」:看過了"},
		{"changesRequested", "", "exclamationmark.bubble", "要求修改 PR #1426「" + title + "」"},
		{"merged", "", "arrow.triangle.merge", "合併 PR #1426「" + title + "」"},
		{"closed", "", "xmark.circle", "關閉 PR #1426「" + title + "」"},
		{"open", "", "arrow.uturn.backward.circle", "重新開啟 PR #1426「" + title + "」"},
	}
	for _, c := range cases {
		out := mustAction(t, a, "pull_request_set", map[string]any{"id": 1426.0, "status": c.status, "note": c.note, UserKey: "mei"})
		who, icon, text := lastLog(a)
		if out["status"] != c.status || who != Person("mei") || icon != c.icon || text != c.text {
			t.Errorf("%s:%v %s %s", c.status, who, icon, text)
		}
	}
	if _, err := Action(a, "pull_request_set", map[string]any{"id": 1426.0, "status": "draft"}); err == nil {
		t.Fatal("draft 應該不支援")
	}
	if _, err := Action(a, "pull_request_set", map[string]any{"id": 1.0, "status": "merged"}); err == nil {
		t.Fatal("找不到 PR 應該錯誤")
	}

	n := len(a.C["activity"])
	out := mustAction(t, a, "pull_request_join_review", map[string]any{"id": 1426.0, "person": "kai"})
	out2 := mustAction(t, a, "pull_request_join_review", map[string]any{"id": 1426.0, "person": "kai"})
	if len(out["reviewers"].([]any)) != len(out2["reviewers"].([]any)) || len(a.C["activity"]) != n {
		t.Fatal("重複加入不該再加,也不寫動態")
	}
}

func TestWorkItemAssign(t *testing.T) {
	a := New()
	out := mustAction(t, a, "work_item_assign", map[string]any{"id": "OPS-153", "assignee": "kai", UserKey: "hao"})
	if ActorFrom(out["assignee"]) != Person("kai") {
		t.Fatal(out)
	}
	who, icon, text := lastLog(a)
	if who != Person("hao") || icon != "person.crop.circle.badge.checkmark" || text != "把 OPS-153 指派給 "+a.Name(Person("kai")) {
		t.Fatal(who, icon, text)
	}
	out = mustAction(t, a, "work_item_assign", map[string]any{"id": "OPS-153", "assignee": "ai-ci-triage"})
	if ActorFrom(out["assignee"]) != AI("ai-ci-triage") {
		t.Fatal(out)
	}
	out = mustAction(t, a, "work_item_assign", map[string]any{"id": "OPS-153", "assignee": ""})
	if _, ok := out["assignee"]; ok {
		t.Fatal("取消指派要拿掉欄位")
	}
	if _, _, text := lastLog(a); text != "把 OPS-153 指派給 未指派" {
		t.Fatal(text)
	}
	if _, err := Action(a, "work_item_assign", map[string]any{"id": "OPS-153", "assignee": "nobody"}); err == nil {
		t.Fatal("不存在的人應該錯誤")
	}
}

func TestPipelineGateAndAIFix(t *testing.T) {
	a := New()
	out := mustAction(t, a, "pipeline_approve_gate", map[string]any{"id": 1287.0, UserKey: "jason"})
	if out["status"] != "running" {
		t.Fatal(out["status"])
	}
	for _, st := range out["stages"].([]any) {
		s := st.(map[string]any)
		if s["status"] == "waitingApproval" {
			t.Fatal("關卡沒打開")
		}
		for _, jb := range s["jobs"].([]any) {
			if jb.(map[string]any)["status"] == "waitingApproval" {
				t.Fatal("job 沒改成排隊")
			}
		}
	}
	_, r := a.Find("pipeline_runs", "1287")
	who, _, text := lastLog(a)
	if who != Person("jason") || text != "核准 "+Str(r, "pipeline")+" #1287 部署到 "+DeployEnvLabel[Str(r, "targetEnv")] {
		t.Fatal(who, text)
	}

	out = mustAction(t, a, "pipeline_ai_fix", map[string]any{"id": 1286.0, "openPR": true, "rerun": true})
	if out["runId"] != 1291.0 {
		t.Fatal(out)
	}
	nr := a.C["pipeline_runs"][0]
	if nr["branch"] != "ai/fix-run-1286" || nr["trigger"] != "AI 修復" || nr["commitMessage"] != "fix: 修正 #1286 的失敗(AI)" ||
		ActorFrom(nr["triggeredBy"]) != AI("ai-ci-triage") {
		t.Fatal(nr)
	}
	st0 := nr["stages"].([]any)[0].(map[string]any)
	j0 := st0["jobs"].([]any)[0].(map[string]any)
	if j0["status"] != "running" || len(j0["log"].([]any)) != 0 {
		t.Fatal(j0)
	}
	if _, _, text := lastLog(a); text != "為 #1286 的修復開 PR,等待審查" {
		t.Fatal(text)
	}
	if Str(a.C["activity"][1], "text") != "開始修復 course-web / PR #1418 #1286,建立分支 ai/fix-run-1286" {
		t.Fatal(a.C["activity"][1])
	}
	out = mustAction(t, a, "pipeline_ai_fix", map[string]any{"id": 1286.0, "openPR": false, "rerun": false})
	if out["runId"] != nil || len(a.C["pipeline_runs"]) != 16 {
		t.Fatal("不重跑就不該開新 run")
	}
}

func TestPromoteAndTrack(t *testing.T) {
	a := New()
	_, s := a.Find("services", "course-web")
	v := s["versions"].(map[string]any)
	stg, prod := v["staging"], v["production"]

	d := mustAction(t, a, "deployment_promote", map[string]any{"service": "course-web", "to": "production"})
	if d["status"] != "waitingApproval" || d["strategy"] != "Canary" || d["canaryPercent"] != 10.0 || d["progress"] != 0.0 ||
		d["version"] != stg || d["previousVersion"] != prod {
		t.Fatal(d)
	}
	if _, _, text := lastLog(a); text != "推進 course-web "+stg.(string)+" 到 Production" {
		t.Fatal(text)
	}
	d2 := mustAction(t, a, "deployment_promote", map[string]any{"service": "course-web", "to": "staging"})
	if d2["status"] != "running" || d2["strategy"] != "Rolling" || d2["progress"] != 0.05 {
		t.Fatal(d2)
	}
	if _, ok := d2["canaryPercent"]; ok {
		t.Fatal("staging 不該有 canaryPercent")
	}
	if _, err := Action(a, "deployment_promote", map[string]any{"service": "course-web", "to": "development"}); err == nil {
		t.Fatal("不能推進到 development")
	}
	// 跑完後回寫服務版本
	a.Mu.Lock()
	for i := 0; i < 30; i++ {
		a.LiveTick()
	}
	a.Mu.Unlock()
	if v["staging"] != d2["version"] {
		t.Fatalf("staging 版本沒回寫:%v", v["staging"])
	}
	if v["production"] != prod {
		t.Fatal("等待核准的部署不該回寫")
	}
}

func TestServerRebootAndRules(t *testing.T) {
	old := RebootDelay
	RebootDelay = 20 * time.Millisecond
	defer func() { RebootDelay = old }()
	a := New()
	id := Str(a.C["servers"][0], "id")
	out := mustAction(t, a, "server_reboot", map[string]any{"id": id})
	if out["health"] != "maintenance" || out["uptimeDays"] != 0.0 {
		t.Fatal(out)
	}
	if _, icon, text := lastLog(a); icon != "restart" || text != "重新開機 "+Str(out, "name") {
		t.Fatal(text)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.Mu.Lock()
		h := Str(a.C["servers"][0], "health")
		who, _, text := lastLog(a)
		a.Mu.Unlock()
		if h == "healthy" {
			if who != SystemActor || text != Str(out, "name")+" 重新開機完成" {
				t.Fatal(who, text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("沒有恢復")
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, r := a.Find("alert_rules", "rule-webhook-latency")
	out = mustAction(t, a, "alert_rule_toggle", map[string]any{"id": "rule-webhook-latency"})
	if out["enabled"] != false {
		t.Fatal(out)
	}
	if _, icon, text := lastLog(a); icon != "bell.slash" || text != "停用告警規則「"+Str(r, "name")+"」" {
		t.Fatal(text)
	}
	n := len(a.C["activity"])
	mustAction(t, a, "alert_rule_toggle", map[string]any{"id": "rule-webhook-latency", "enabled": false})
	if len(a.C["activity"]) != n {
		t.Fatal("狀態一樣時不寫動態")
	}
	out = mustAction(t, a, "alert_rule_toggle", map[string]any{"id": "rule-webhook-latency", "enabled": true})
	if _, icon, _ := lastLog(a); out["enabled"] != true || icon != "bell" {
		t.Fatal(out)
	}
	if _, err := Action(a, "alert_rule_toggle", map[string]any{"id": "nope"}); err == nil {
		t.Fatal("找不到應該錯誤")
	}
}
