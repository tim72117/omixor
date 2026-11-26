package core

import (
	"strings"
	"testing"
)

// 舊 server 不會把任務結果貼到 Issue:只有領取回應明示會貼,提示詞才叫角色不要自己留言
func TestRunnerSystemPromptFollowsServerCapability(t *testing.T) {
	const auto, manual = "系統會自動把 result 貼到該 Issue 的留言", "需要在 Issue 說明時,用 issue_update 留言。\n"
	for _, c := range []struct {
		autoPosts  bool
		want, none string
	}{{true, auto, manual}, {false, manual, auto}} {
		got := runnerSystemPrompt(Agent{ID: "eng", Name: "工程"}, AgentRunConfig{}, c.autoPosts)
		if !strings.Contains(got, c.want) || strings.Contains(got, c.none) || !strings.Contains(got, "5. 不要把任何憑證") {
			t.Errorf("autoPostsResult=%v:\n%s", c.autoPosts, got)
		}
	}
}
