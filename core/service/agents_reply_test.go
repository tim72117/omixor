package service_test

import (
	"strings"
	"testing"

	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// 回覆需要協助的任務:備註以本人署名、任務退回待領取(T-3 是 blocked)
func TestReplyTask(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, boss, _, _ := transferEnv(t, st)
		out, err := svc.ReplyTask(boss, "o1", "T-3", "  選 A 方案  ")
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "queued" {
			t.Fatalf("要退回待領取:%s", out.Status)
		}
		got := taskOf(t, st, "T-3")
		if got.Status != "queued" || len(got.Notes) == 0 {
			t.Fatalf("狀態與備註:%+v", got)
		}
		n := got.Notes[len(got.Notes)-1]
		if n.Text != "選 A 方案" || n.Author != storetest.User(t, st, "boss@x").ID {
			t.Fatalf("備註要去頭尾空白並以回覆的人署名(不是角色):%+v", n)
		}
		found := false
		for _, a := range auditActions(st) {
			found = found || a == "agent.task.reply"
		}
		if !found {
			t.Fatal("要寫稽核 agent.task.reply")
		}
	})
}

func TestReplyTaskRejections(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, boss, alice, _ := transferEnv(t, st)
		_, err := svc.ReplyTask(boss, "o1", "T-3", "   ")
		wantCode(t, "空白回覆", err, 400)
		_, err = svc.ReplyTask(boss, "o1", "T-3", strings.Repeat("字", service.MaxTaskReplyRunes+1))
		wantCode(t, "回覆太長", err, 400)
		_, err = svc.ReplyTask(boss, "o1", "T-1", "答覆") // queued
		wantCode(t, "待領取的任務不能回覆", err, 409)
		_, err = svc.ReplyTask(boss, "o1", "T-4", "答覆") // done
		wantCode(t, "已完成的任務不能回覆", err, 409)
		_, err = svc.ReplyTask(boss, "o1", "T-99", "答覆")
		wantCode(t, "找不到任務", err, 404)
		_, err = svc.ReplyTask(alice, "o1", "T-3", "答覆")
		wantCode(t, "一般成員不能回覆", err, 403)
		if got := taskOf(t, st, "T-3"); got.Status != "blocked" || len(got.Notes) != 0 {
			t.Fatalf("被拒絕的回覆不能改動任務:%+v", got)
		}
	})
}
