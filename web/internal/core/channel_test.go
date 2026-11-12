package core

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 對話:畫面的視圖與動作(本機內建核心,身分是 ops-web);綁 Issue 重複時回傳既有的、@ 角色寫動態、未讀與徽章
func TestChannelViewAndActions(t *testing.T) {
	a := newTestApp(t)
	agents, err := a.AgentOps().ListAgents(a.currentOrgNow())
	if err != nil || len(agents) == 0 {
		t.Fatal(agents, err)
	}
	g := agents[0]
	c, err := Action(a, "channel_create", map[string]any{"name": "改版", "members": []any{"agent:" + g.ID}})
	if err != nil {
		t.Fatal(err)
	}
	id := c.(*model.ChannelSummary).ID
	if _, err := Action(a, "channel_post", map[string]any{"id": id, "text": "  "}); err == nil {
		t.Fatal("空白訊息要錯誤")
	}
	if _, err := Action(a, "channel_post", map[string]any{"id": id, "text": "@" + g.Name + " 看一下"}); err != nil {
		t.Fatal(err)
	}
	p, err := Action(a, "channel_messages", map[string]any{"id": id})
	if err != nil || len(p.(*service.MessagePage).Messages) != 1 || p.(*service.MessagePage).Messages[0].Mentions[0] != "agent:"+g.ID {
		t.Fatal(p, err)
	}
	v, _ := View(a, "channels")
	m := v.(map[string]any)
	if m["me"] != "user:ops-web" || len(m["channels"].([]any)) != 1 {
		t.Fatal(m)
	}
	// 角色的 inbox 看得到提到它的訊息
	x, err := a.AgentOps().ChannelInbox(a.currentOrgNow(), "agent:"+g.ID)
	if err != nil || len(x.Mentions) != 1 {
		t.Fatal(x, err)
	}
	// 動態牆:建立頻道、提到角色
	a.Mu.Lock()
	act := a.C["activity"]
	a.Mu.Unlock()
	found := 0
	for _, it := range act {
		if it["section"] == "channels" {
			found++
		}
	}
	if found != 2 {
		t.Fatal("動態牆要有建立頻道與提到角色", found)
	}
}
