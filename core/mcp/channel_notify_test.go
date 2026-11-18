package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

type fakeTasks struct {
	mu     sync.Mutex
	tasks  []model.AgentTask
	agents []model.Agent
	seen   []service.TaskFilter
}

func (f *fakeTasks) ListAgentTasks(org string, fl service.TaskFilter) ([]model.AgentTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, fl)
	out := []model.AgentTask{}
	for _, t := range f.tasks {
		if t.Status == "queued" && (fl.AgentID == "" || t.AgentID == fl.AgentID) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeTasks) ListAgents(org string) ([]model.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.agents == nil {
		return []model.Agent{{ID: "dev", Active: true}, {ID: "pm", Active: true}, {ID: "old", Active: false}}, nil
	}
	return f.agents, nil
}

func (f *fakeTasks) set(ts ...model.AgentTask) { f.mu.Lock(); f.tasks = ts; f.mu.Unlock() }

func task(id, agent, status string) model.AgentTask {
	return model.AgentTask{ID: id, AgentID: agent, Status: status, Title: "做 " + id, Priority: "high", UpdatedAt: model.Time{Time: time.Unix(1000, 0)}}
}

// 頻道:initialize 宣告 claude/channel;initialized 之後開始查,第一次把既有待領取任務合成一則,
// 之後新出現(或被退回待領取)的任務每筆一則,meta 帶 task_id 等;其他要求回 Method not found
func TestChannelNotifiesQueuedTasks(t *testing.T) {
	f := &fakeTasks{}
	f.set(task("T-1", "dev", "queued"), task("T-2", "pm", "inProgress"), task("T-9", "old", "queued"))
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- mcp.RunChannel(ctx, inR, outW, mcp.ChannelConfig{Tasks: f, Org: "lab", Server: "https://omixor.io", Interval: 20 * time.Millisecond, Logf: t.Logf})
		outW.Close()
	}()
	msgs := make(chan map[string]any, 32)
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("輸出不是 JSON:%s", sc.Text())
			}
			msgs <- m
		}
		close(msgs)
	}()
	next := func() map[string]any {
		select {
		case m := <-msgs:
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("等不到訊息")
			return nil
		}
	}
	write := func(s string) { fmt.Fprintln(inW, s) }

	write(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{}}}`)
	init := next()
	res := init["result"].(map[string]any)
	if _, ok := res["capabilities"].(map[string]any)["experimental"].(map[string]any)["claude/channel"]; !ok {
		t.Fatalf("要宣告 claude/channel:%v", res)
	}
	ins := res["instructions"].(string)
	for _, want := range []string{"agent_task_claim", "id=<task_id>", "https://omixor.io", "org 屬性"} {
		if !strings.Contains(ins, want) {
			t.Fatalf("instructions 要有 %q:%s", want, ins)
		}
	}
	if strings.Contains(ins, "帶 agent 與 task_id") {
		t.Fatal("領取工具的參數叫 id,不是 task_id")
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if e := next()["error"]; e == nil {
		t.Fatal("沒有工具:tools/list 要回錯誤")
	}

	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	backlog := next()
	if backlog["method"] != "notifications/claude/channel" {
		t.Fatalf("第一則要是頻道通知:%v", backlog)
	}
	p := backlog["params"].(map[string]any)
	if c := p["content"].(string); !strings.Contains(c, "T-1") || strings.Contains(c, "T-2") || strings.Contains(c, "T-9") {
		t.Fatalf("第一次只列待領取、角色啟用中的:%s", c)
	}
	if p["meta"].(map[string]any)["kind"] != "backlog" {
		t.Fatalf("meta:%v", p["meta"])
	}

	// 新任務 T-3,以及 T-2 被退回待領取:各一則;T-1 已經通知過不再送
	f.set(task("T-1", "dev", "queued"), task("T-2", "pm", "queued"), task("T-3", "dev", "queued"))
	got := map[string]bool{}
	for len(got) < 2 {
		m := next()
		meta := m["params"].(map[string]any)["meta"].(map[string]any)
		if meta["kind"] != "task_queued" || meta["org"] != "lab" || meta["priority"] != "high" {
			t.Fatalf("meta:%v", meta)
		}
		got[meta["task_id"].(string)] = true
	}
	if !got["T-2"] || !got["T-3"] {
		t.Fatalf("要通知 T-2、T-3:%v", got)
	}
	// 再查幾次都沒有新的:不能重複送
	select {
	case m := <-msgs:
		t.Fatalf("不該重複通知:%v", m)
	case <-time.After(100 * time.Millisecond):
	}

	// T-1 在兩次查詢之間被領走又退回(UpdatedAt 變了):再通知一次,kind 是 task_changed
	t1 := task("T-1", "dev", "queued")
	t1.UpdatedAt = model.Time{Time: time.Unix(2000, 0)}
	f.set(t1, task("T-2", "pm", "queued"), task("T-3", "dev", "queued"))
	m := next()
	meta := m["params"].(map[string]any)["meta"].(map[string]any)
	if meta["task_id"] != "T-1" || meta["kind"] != "task_changed" {
		t.Fatalf("退回要再通知:%v", meta)
	}

	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// 指定角色時只查那個角色
func TestChannelAgentFilter(t *testing.T) {
	f := &fakeTasks{}
	inR, inW := io.Pipe()
	go func() {
		fmt.Fprintln(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		time.Sleep(60 * time.Millisecond)
		inW.Close()
	}()
	_ = mcp.RunChannel(context.Background(), inR, io.Discard, mcp.ChannelConfig{Tasks: f, Org: "lab", AgentID: "dev", Interval: 10 * time.Millisecond})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 || f.seen[0].AgentID != "dev" || f.seen[0].Status != "queued" {
		t.Fatalf("查詢條件:%v", f.seen)
	}
}

// OrgFunc:每次查詢重讀組織;讀不到就先不送,讀到後當第一次;換組織時重新開始,通知帶新組織
func TestChannelFollowsCurrentOrg(t *testing.T) {
	f := &fakeTasks{}
	f.set(task("T-1", "dev", "queued"))
	var mu sync.Mutex
	org, orgErr := "", fmt.Errorf("console 沒開")
	cur := func() (string, error) { mu.Lock(); defer mu.Unlock(); return org, orgErr }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		_ = mcp.RunChannel(context.Background(), inR, outW, mcp.ChannelConfig{Tasks: f, OrgFunc: cur, Interval: 15 * time.Millisecond, Logf: t.Logf})
		outW.Close()
	}()
	msgs := make(chan map[string]any, 32)
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			_ = json.Unmarshal(sc.Bytes(), &m)
			msgs <- m
		}
		close(msgs)
	}()
	fmt.Fprintln(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	select {
	case m := <-msgs:
		t.Fatalf("讀不到組織時不該送:%v", m)
	case <-time.After(80 * time.Millisecond):
	}
	meta := func() map[string]any {
		select {
		case m := <-msgs:
			return m["params"].(map[string]any)["meta"].(map[string]any)
		case <-time.After(3 * time.Second):
			t.Fatal("等不到通知")
			return nil
		}
	}
	mu.Lock()
	org, orgErr = "a", nil
	mu.Unlock()
	if m := meta(); m["kind"] != "backlog" || m["org"] != "a" {
		t.Fatalf("console 開了之後要送待領清單:%v", m)
	}
	mu.Lock()
	org = "b"
	mu.Unlock()
	if m := meta(); m["kind"] != "backlog" || m["org"] != "b" {
		t.Fatalf("換組織要重新開始、帶新組織:%v", m)
	}
	inW.Close()
}
