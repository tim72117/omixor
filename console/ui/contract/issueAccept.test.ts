// Issue 接受 / 放回的前端純邏輯:功能偵測、放回按鈕的顯示條件。
import { test } from "node:test";
import assert from "node:assert/strict";
import { canRelease, supportsAccept } from "../src/sections/development/issueAcceptModel.ts";

const human = { assignee: undefined, assigneeActor: { id: "usr_1", kind: "user", handle: "amy@x.com", name: "Amy", email: "Amy@X.com", status: "active" } } as const;
const role = { assignee: "cto", assigneeActor: { id: "agt_1", kind: "agent", handle: "cto", name: "研發長", status: "active" } } as const;

test("功能偵測:沒宣告、沒給、空的一律不支援", () => {
  assert.equal(supportsAccept(["issue_accept"]), true);
  assert.equal(supportsAccept(["other"]), false);
  assert.equal(supportsAccept([]), false);
  assert.equal(supportsAccept(undefined), false);
  assert.equal(supportsAccept(null), false);
});

test("放回只對接受者本人或組織管理員顯示", () => {
  assert.equal(canRelease(human, "amy@x.com", false), true, "接受者本人(不分大小寫,handle 或 email)");
  assert.equal(canRelease(human, " AMY@x.com ", false), true);
  assert.equal(canRelease(human, "bob@x.com", false), false, "別人看不到");
  assert.equal(canRelease(human, undefined, false), false, "不知道我是誰就不顯示");
  assert.equal(canRelease(human, "bob@x.com", true), true, "組織管理員");
  assert.equal(canRelease(role, "cto", false), false, "角色負責的不走放回按鈕");
  assert.equal(canRelease(role, "x", true), false);
  assert.equal(canRelease({ assignee: undefined, assigneeActor: undefined }, "amy@x.com", true), false, "沒人接受");
});
