// 執行日誌頁籤的純邏輯:依角色與結果篩選、歷史裡的角色清單、結果的顯示。
import { test } from "node:test";
import assert from "node:assert/strict";
import { filterRuns, outcomeOf, runRoles } from "../src/sections/automation/runLogModel.ts";

const mk = (taskId: string, agentId: string, outcome: string) => ({ taskId, agentId, agentName: `角色${agentId}`, outcome }) as any;
const history = [mk("T-4", "a", "error"), mk("T-3", "b", "done"), mk("T-2", "a", "done"), mk("T-1", "b", "blocked")];

test("篩選:角色與結果可以同時用,順序不變", () => {
  assert.deepEqual(filterRuns(history, "", "all").map((r) => r.taskId), ["T-4", "T-3", "T-2", "T-1"]);
  assert.deepEqual(filterRuns(history, "a", "all").map((r) => r.taskId), ["T-4", "T-2"]);
  assert.deepEqual(filterRuns(history, "", "done").map((r) => r.taskId), ["T-3", "T-2"]);
  assert.deepEqual(filterRuns(history, "b", "blocked").map((r) => r.taskId), ["T-1"]);
  assert.deepEqual(filterRuns(history, "a", "blocked"), []);
});

test("角色清單:同一個角色只列一次,依第一次出現的順序", () => {
  assert.deepEqual(runRoles(history), [{ id: "a", name: "角色a" }, { id: "b", name: "角色b" }]);
  assert.deepEqual(runRoles([]), []);
});

test("結果顯示:已知的有中文,未知的照原字串", () => {
  assert.equal(outcomeOf("blocked").label, "需要協助");
  assert.equal(outcomeOf("weird").label, "weird");
});
