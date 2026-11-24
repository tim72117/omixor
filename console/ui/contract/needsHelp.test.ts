// 需要處理(blocked 任務)的前端純邏輯:判斷、排序、問題文字、摺起顯示、回覆檢查。
import { test } from "node:test";
import assert from "node:assert/strict";
import { blockQuestion, excerpt, needsHelpByIssue, needsHelpTasks, pendingCount, REPLY_LIMIT, replyProblem, taskNeedsHelp } from "../src/sections/automation/needsHelpModel.ts";

const mk = (o: Record<string, unknown>) => ({ id: "T-1", status: "blocked", updatedAt: "2026-01-02T00:00:00Z", result: "", notes: [], ...o }) as any;

test("只有 blocked 算需要處理,等最久的在前", () => {
  const list = [mk({ id: "T-1", updatedAt: "2026-01-03T00:00:00Z" }), mk({ id: "T-2", status: "queued" }), mk({ id: "T-3", updatedAt: "2026-01-01T00:00:00Z" }), mk({ id: "T-4", status: "done" })];
  assert.deepEqual(needsHelpTasks(list).map((t) => t.id), ["T-3", "T-1"]);
  assert.equal(taskNeedsHelp(mk({ status: "inProgress" })), false);
});

test("問題文字:有結果用結果,否則最後一則備註(帶作者),都沒有是空字串", () => {
  assert.deepEqual(blockQuestion(mk({ result: " 要選哪個? ", notes: [{ text: "n", author: "a" }] })), { text: "要選哪個?" });
  assert.deepEqual(blockQuestion(mk({ notes: [{ text: "舊", author: "a" }, { text: " 新的問題 ", author: "agent:pm" }] })), { text: "新的問題", author: "agent:pm" });
  assert.deepEqual(blockQuestion(mk({})), { text: "" });
});

test("摺起顯示:超過行數或字數才截斷", () => {
  assert.deepEqual(excerpt("一行"), { short: "一行", truncated: false });
  assert.deepEqual(excerpt("1\n2\n3"), { short: "1\n2", truncated: true });
  const long = "字".repeat(200);
  assert.deepEqual(excerpt(long), { short: "字".repeat(160), truncated: true });
});

test("回覆檢查:空白、太長不能送", () => {
  assert.ok(replyProblem("  "));
  assert.equal(replyProblem("好"), "");
  assert.ok(replyProblem("字".repeat(REPLY_LIMIT + 1)));
  assert.equal(replyProblem("字".repeat(REPLY_LIMIT)), "");
});

test("依 Issue 分組:只收需要處理的、對得到 Issue 的,組內等最久的在前", () => {
  const issueOf: Record<string, string> = { "T-1": "i1", "T-2": "i1", "T-3": "i2", "T-4": "i1" };
  const list = [
    mk({ id: "T-1", updatedAt: "2026-01-03T00:00:00Z" }), mk({ id: "T-2", updatedAt: "2026-01-01T00:00:00Z" }),
    mk({ id: "T-3", status: "queued" }), mk({ id: "T-4", status: "done" }), mk({ id: "T-5" }), // T-5 沒有 Issue
  ];
  const m = needsHelpByIssue(list, (t) => issueOf[t.id]);
  assert.deepEqual([...m.keys()], ["i1"]);
  assert.deepEqual(m.get("i1")!.map((t) => t.id), ["T-2", "T-1"]);
  assert.equal(needsHelpByIssue([], () => "x").size, 0);
});

test("待決定分頁的數字 = 待處理的決定 + 需要處理的任務(任務數與 needsHelpTasks 同一個判斷)", () => {
  const tasks = [mk({ id: "T-1" }), mk({ id: "T-2", status: "queued" }), mk({ id: "T-3" })];
  assert.equal(pendingCount(2, needsHelpTasks(tasks).length), 4);
  assert.equal(pendingCount(0, 0), 0);
});
