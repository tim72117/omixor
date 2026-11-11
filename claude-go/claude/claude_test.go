package claude

import (
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	got := Options{Model: "sonnet", Effort: "low", NoTools: true, Resume: "abc"}.Args("-hi")
	want := []string{"-p", "--output-format", "stream-json", "--verbose",
		"--model", "sonnet", "--effort", "low", "--tools", "", "--resume", "abc", "--", "-hi"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// 零值不傳
	if got := (Options{}).Args("x"); len(got) != 6 {
		t.Fatalf("零值應只有固定參數：%q", got)
	}
	got = Options{Tools: []string{"Read", "Grep"}, AllowedTools: []string{"Bash(git *)"}}.Args("x")
	if !slices.Contains(got, "Read,Grep") || !slices.Contains(got, "Bash(git *)") {
		t.Fatalf("工具沒有傳：%q", got)
	}
}

const sample = `{"type":"system","subtype":"init","session_id":"s1","model":"claude-haiku-4-5","tools":[],"permissionMode":"default"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":50}
{"type":"assistant","session_id":"s1","message":{"model":"claude-haiku-4-5","content":[{"type":"thinking","thinking":""},{"type":"text","text":"O"},{"type":"text","text":"K"}]}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":0.03,"resetsAt":1794189600}}}}

{"type":"result","subtype":"success","is_error":false,"result":"OK","session_id":"s1","num_turns":1,"duration_ms":2844,"total_cost_usd":0.002,"usage":{"input_tokens":10,"output_tokens":77}}
`

func TestParse(t *testing.T) {
	var kinds []string
	var text string
	res, err := Parse(strings.NewReader(sample), func(ev Event) {
		switch e := ev.(type) {
		case *Init:
			kinds = append(kinds, "init:"+e.Model)
		case *Assistant:
			kinds = append(kinds, "assistant")
			text = e.Text()
		case *RateLimit:
			kinds = append(kinds, "limit:"+e.Info.RateLimitType)
			if w := e.Info.Windows["five_hour"]; w.Utilization != 0.03 {
				t.Errorf("額度 %v", w)
			}
		case *Result:
			kinds = append(kinds, "result")
		case *Other:
			kinds = append(kinds, "other:"+e.Subtype)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"init:claude-haiku-4-5", "other:thinking_tokens", "assistant", "limit:five_hour", "result"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("事件 %q", kinds)
	}
	if text != "OK" || res.Text != "OK" || res.SessionID != "s1" || res.Usage.OutputTokens != 77 {
		t.Fatalf("text=%q res=%+v", text, res)
	}
}

func TestParseBadLine(t *testing.T) {
	if _, err := Parse(strings.NewReader("not json\n"), nil); err == nil {
		t.Fatal("應該報錯")
	}
}
