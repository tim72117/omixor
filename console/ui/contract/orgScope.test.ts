// src/lib/orgScope.ts:非系統工作組織不發整台一份的 view 請求
import { test } from "node:test";
import assert from "node:assert/strict";
import { createOrgScope, SYSTEM_ONLY_MESSAGE, type OrgScopeIndex } from "../src/lib/orgScope.ts";

test("系統工作組織照常讀;其他組織只讀分組織的 view", async () => {
  let idx: OrgScopeIndex = { perUser: true, current: "a", system: "a" };
  let asked = 0;
  const scope = createOrgScope(async () => (asked++, idx));
  const read: string[] = [];
  const load = (name: string) => scope.load(name, async () => (read.push(name), { data: "x" }));
  await load("people");
  await load("alerts");
  assert.deepEqual([read, asked], [["people", "alerts"], 1]);

  idx = { perUser: true, current: "b", system: "a" };
  scope.invalidate();
  read.length = 0;
  assert.deepEqual(await load("people"), { data: [] });
  const refused = await load("connections").then(() => "", (e: Error) => e.message);
  assert.equal(refused, SYSTEM_ONLY_MESSAGE);
  await load("agents");
  await load("me");
  assert.deepEqual(read, ["agents", "me"]);
});

test("讀不到組織清單(還沒登入)照常發請求,下次再問", async () => {
  let fail = true;
  const scope = createOrgScope(async () => {
    if (fail) throw new Error("401");
    return { perUser: true, current: "b", system: "a" };
  });
  let reads = 0;
  const load = () => scope.load("people", async () => (reads++, { data: [1] }));
  await load();
  assert.equal(reads, 1);
  fail = false;
  assert.deepEqual(await load(), { data: [] });
  assert.equal(reads, 1);
});
