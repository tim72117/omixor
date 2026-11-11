package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opsweb/internal/core"
)

// 照 Swift 的 VoicePipelineTests.testMCPVoiceTools 搬(假的 npm run voice,不會真的呼叫 Gemini)

type voiceDryRunner struct{ calls [][]string }

func (f *voiceDryRunner) Run(args []string, cwd string) core.VoiceShellResult {
	f.calls = append(f.calls, args)
	if slices.Contains(args, "--dry-run") {
		return core.VoiceShellResult{Out: "  launch/aaa.mp3  launch/1.1.json  新的一句\n共 2 段、10 字\n"}
	}
	return core.VoiceShellResult{Code: 1, Err: "測試不應該送出批次"}
}

func voiceTestApp(t *testing.T) (*core.App, *voiceDryRunner) {
	base := t.TempDir()
	root := filepath.Join(base, "shuttle")
	t.Setenv("OPS_SHUTTLE_PATH", root)
	t.Setenv("OPS_DATA_DIR", filepath.Join(base, "data"))
	for rel, text := range map[string]string{
		"web/src/content/live/launch/1.1.json":       `{"steps":[]}`,
		"web/src/content/live/launch/1.1.voice.json": `{"clips":{}}`,
	} {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(text), 0o644)
	}
	a := core.New()
	r := &voiceDryRunner{}
	core.StartVoice(a, false).Runner = r
	return a, r
}

// callTool 經過 JSON-RPC 呼叫工具,回傳文字內容與 isError
func voiceCallTool(t *testing.T, a *core.App, name string, args map[string]any) (string, bool) {
	t.Helper()
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	raw := (&Handler{App: a}).Handle(msg)
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("回應格式不對:%s", raw)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func voiceCallOK(t *testing.T, a *core.App, name string, args map[string]any) map[string]any {
	t.Helper()
	text, isErr := voiceCallTool(t, a, name, args)
	if isErr {
		t.Fatalf("%s 失敗:%s", name, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMCPVoiceTools(t *testing.T) {
	a, r := voiceTestApp(t)
	dry := voiceCallOK(t, a, "ops_voice_submit", map[string]any{"scripts": []any{"launch/1.1.json"}, "dry_run": true})
	if !strings.Contains(dry["dryRun"].(string), "共 2 段") {
		t.Fatal(dry)
	}
	if len(core.VoiceOf(a).Jobs) != 0 {
		t.Fatal("dry_run 不應該建立工作")
	}

	sub := voiceCallOK(t, a, "ops_voice_submit", map[string]any{"scripts": []any{"launch/1.1.json"}})
	id := sub["job"].(map[string]any)["id"].(string)
	if sub["note"] != "馬上送出。完成後由人在 Ops › CI/CD › 旁白發佈 試聽並發佈;可用 ops_voice_jobs 查進度" {
		t.Fatal(sub["note"])
	}
	one := voiceCallOK(t, a, "ops_voice_jobs", map[string]any{"id": strings.ToLower(id)})
	if one["state"] != "queued" || one["stateLabel"] != "排隊中" || one["by"] != "mcp" {
		t.Fatal(one)
	}
	if _, ok := one["baseline"]; ok {
		t.Fatal("不應該有 baseline")
	}
	list := voiceCallOK(t, a, "ops_voice_jobs", map[string]any{})
	if len(list["jobs"].([]any)) != 1 {
		t.Fatal(list)
	}
	// 沒有發佈用的 MCP 工具
	for _, n := range ToolNames() {
		if strings.Contains(n, "publish") {
			t.Fatal(n)
		}
	}
	// 排第二批
	sub2 := voiceCallOK(t, a, "ops_voice_submit", map[string]any{"scripts": "launch/1.1", "limit": 2})
	if sub2["note"] != "前面還有 1 批,輪到時自動送出" {
		t.Fatal(sub2["note"])
	}
	if sub2["job"].(map[string]any)["title"] != nil || sub2["job"].(map[string]any)["limit"] != float64(2) {
		t.Fatal(sub2["job"])
	}
	// 只有試算執行過外部程式
	if len(r.calls) != 1 {
		t.Fatal(r.calls)
	}
}

func TestMCPVoiceErrors(t *testing.T) {
	a, r := voiceTestApp(t)
	for _, tc := range []struct {
		name string
		args map[string]any
		msg  string
	}{
		{"ops_voice_submit", map[string]any{}, "至少要指定一份腳本,或選「全部腳本」"},
		{"ops_voice_submit", map[string]any{"scripts": []any{"launch/nope"}}, "找不到腳本 src/content/live/launch/nope.json"},
		{"ops_voice_submit", map[string]any{"scripts": []any{"launch/1.1.voice.json"}, "dry_run": true}, "launch/1.1.voice.json 是產生出來的清單,請給腳本本身"},
		{"ops_voice_submit", map[string]any{"all": "true"}, "至少要指定一份腳本,或選「全部腳本」"}, // 字串 "true" 不算(同 Swift 的 as? Bool)
		{"ops_voice_jobs", map[string]any{"id": "VJ-9"}, "找不到旁白批次 VJ-9"},
	} {
		text, isErr := voiceCallTool(t, a, tc.name, tc.args)
		if !isErr || text != tc.msg {
			t.Errorf("%s %v:%q", tc.name, tc.args, text)
		}
	}
	if len(r.calls) != 0 {
		t.Fatal(r.calls)
	}
	// all = 1(NSNumber)算 true
	out := voiceCallOK(t, a, "ops_voice_submit", map[string]any{"all": 1})
	if out["job"].(map[string]any)["all"] != true {
		t.Fatal(out)
	}
	empty := voiceCallOK(t, a, "ops_voice_jobs", map[string]any{})
	if len(empty["jobs"].([]any)) != 1 {
		t.Fatal(empty)
	}
}
