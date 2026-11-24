// 待決定 / 需要協助的前端純邏輯:篩選、排序、計數、選項整理、權限、送出的參數、錯誤訊息。
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  answerLabel, answerProblem, buildAddArgs, buildAnswerArgs, canAnswer, canReopen, canToIssue, canWithdraw, countByStatus, DECISION_FILTERS_DEFAULT,
  draftProblem, emptyDraft, errorMessage, filterDecisions, groupBySource, issueSummary, normalizeOptions, optionSummary, relativeTimeZh, sortDecisions,
  statusLabel, summaryText, type Viewer,
} from "../src/sections/development/decisionModel.ts";

const mk = (o: Record<string, unknown>) => ({
  id: "DEC-1", kind: "decision", title: "t", body: "", options: [], status: "open", answer: "", answerNote: "", sourceIssue: "", blocks: [],
  createdBy: "amy@x.com", decidedBy: "", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", ...o,
}) as any;
const human: Viewer = { me: "amy@x.com", isAdmin: false, isHuman: true };

test("預設篩選是待處理;種類、狀態、來源 Issue(不分大小寫)、待我處理", () => {
  const list = [mk({ id: "DEC-1" }), mk({ id: "DEC-2", kind: "help", sourceIssue: "ISS-37" }), mk({ id: "DEC-3", status: "decided" }), mk({ id: "DEC-4", status: "withdrawn" })];
  const ids = (f: Partial<typeof DECISION_FILTERS_DEFAULT>, v = human) => filterDecisions(list, { ...DECISION_FILTERS_DEFAULT, ...f }, v).map((d) => d.id);
  assert.deepEqual(ids({}), ["DEC-1", "DEC-2"]);
  assert.deepEqual(ids({ status: "all" }), ["DEC-1", "DEC-2", "DEC-3", "DEC-4"]);
  assert.deepEqual(ids({ status: "decided" }), ["DEC-3"]);
  assert.deepEqual(ids({ kind: "help" }), ["DEC-2"]);
  assert.deepEqual(ids({ issue: "iss-37", status: "all" }), ["DEC-2"]);
  assert.deepEqual(ids({ mine: true, status: "all" }), ["DEC-1", "DEC-2"], "待我處理 = 待處理的");
  assert.deepEqual(ids({ mine: true, status: "all" }, { ...human, isHuman: false }), [], "角色沒有「我」");
});

test("排序:待處理在前、同狀態新的在前;計數與 Issue 摘要", () => {
  const list = [mk({ id: "DEC-1", status: "decided" }), mk({ id: "DEC-2", createdAt: "2026-01-02T00:00:00Z" }), mk({ id: "DEC-3" }), mk({ id: "DEC-4", status: "withdrawn", sourceIssue: "ISS-1" })];
  assert.deepEqual(sortDecisions(list).map((d) => d.id), ["DEC-2", "DEC-3", "DEC-1", "DEC-4"]);
  assert.deepEqual(countByStatus(list), { open: 2, decided: 1, withdrawn: 1, all: 4 });
  const c = issueSummary(list, "iss-1");
  assert.equal(c.all, 1);
  assert.equal(summaryText(countByStatus([mk({}), mk({ status: "decided" })])), "待處理 1 · 已處理 1");
  assert.equal(summaryText(countByStatus([])), "");
  assert.deepEqual(groupBySource([mk({ id: "a" }), mk({ id: "b", sourceIssue: "ISS-2" }), mk({ id: "c", sourceIssue: "ISS-1" })]).map((g) => g.issue), ["ISS-1", "ISS-2", ""]);
});

test("相對時間與顯示文字", () => {
  const now = Date.parse("2026-01-10T00:00:00Z");
  assert.equal(relativeTimeZh("2026-01-09T23:59:30Z", now), "剛剛");
  assert.equal(relativeTimeZh("2026-01-09T23:50:00Z", now), "10 分鐘前");
  assert.equal(relativeTimeZh("2026-01-09T20:00:00Z", now), "4 小時前");
  assert.equal(relativeTimeZh("2026-01-07T00:00:00Z", now), "3 天前");
  assert.equal(relativeTimeZh("2025-11-10T00:00:00Z", now), "2 個月前");
  assert.equal(relativeTimeZh("2026-02-01T00:00:00Z", now), "剛剛", "未來視為剛剛");
  assert.equal(relativeTimeZh("", now), "—");
  assert.equal(relativeTimeZh("nope", now), "—");
  assert.equal(statusLabel({ kind: "help", status: "decided" }), "已處理");
  assert.equal(statusLabel({ kind: "decision", status: "decided" }), "已決定");
  const opts = [{ id: "a", label: "A" }, { id: "b", label: "B", recommended: true }, { id: "c", label: "C" }, { id: "d", label: "D" }];
  assert.equal(optionSummary(opts), "A、B(建議)、C…等 4 項");
  assert.equal(optionSummary([]), "");
  assert.equal(answerLabel({ kind: "decision", answer: "b", options: opts }), "B");
  assert.equal(answerLabel({ kind: "decision", answer: "other", options: opts }), "其他");
  assert.equal(answerLabel({ kind: "help", answer: "declined", options: [] }), "婉拒");
  assert.equal(answerLabel({ kind: "help", answer: "", options: [] }), "");
});

test("選項整理:去空白、去重、最多一個建議;草稿檢查與送出參數", () => {
  const o = (label: string, recommended = false, description = "") => ({ label, description, recommended });
  assert.deepEqual(normalizeOptions([o(" A ", true, " x "), o(""), o("a"), o("B", true), o("C")]), [{ label: "A", description: "x", recommended: true }, { label: "B" }, { label: "C" }]);
  const d = emptyDraft();
  assert.equal(draftProblem(d), "請填標題");
  d.title = "要不要上線";
  assert.ok(/至少要有 2 個/.test(draftProblem(d)));
  d.options = [o("好", true), o("不好")];
  assert.equal(draftProblem(d), "");
  d.sourceIssue = "bad";
  assert.ok(/來源 Issue/.test(draftProblem(d)));
  d.sourceIssue = "iss-37";
  assert.deepEqual(buildAddArgs(d), { kind: "decision", title: "要不要上線", options: [{ label: "好", recommended: true }, { label: "不好" }], source_issue: "ISS-37" });
  const h = { ...emptyDraft(), kind: "help" as const, title: "備份 Cloud SQL", body: " 背景 " };
  assert.equal(draftProblem(h), "", "help 不需要選項");
  assert.deepEqual(buildAddArgs(h), { kind: "help", title: "備份 Cloud SQL", body: "背景" }, "help 不帶 options");
});

test("回答:other 與 declined 要說明;help 只有完成帶證據", () => {
  const dec = mk({ options: [{ id: "opt1", label: "A" }, { id: "opt2", label: "B" }] });
  const help = mk({ kind: "help" });
  assert.ok(/請選/.test(answerProblem(dec, { choice: "", note: "", evidence: "" })));
  assert.ok(/沒有這個選項/.test(answerProblem(dec, { choice: "zzz", note: "", evidence: "" })));
  assert.ok(/說明/.test(answerProblem(dec, { choice: "other", note: " ", evidence: "" })));
  assert.equal(answerProblem(dec, { choice: "other", note: "改走 C", evidence: "" }), "");
  assert.equal(answerProblem(dec, { choice: "opt1", note: "", evidence: "" }), "");
  assert.ok(/原因/.test(answerProblem(help, { choice: "declined", note: "", evidence: "" })));
  assert.equal(answerProblem(help, { choice: "done", note: "", evidence: "" }), "");
  assert.deepEqual(buildAnswerArgs(dec, { choice: "opt1", note: " ok ", evidence: "ignored" }), { id: "DEC-1", answer: "opt1", note: "ok" });
  assert.deepEqual(buildAnswerArgs(help, { choice: "done", note: "", evidence: " v1.2 " }), { id: "DEC-1", answer: "done", evidence: "v1.2" });
  assert.deepEqual(buildAnswerArgs(help, { choice: "declined", note: "沒空", evidence: "x" }), { id: "DEC-1", answer: "declined", note: "沒空" });
});

test("權限:回答、由它開 Issue 只有人;撤回要提出者或管理員;重新開啟要管理員或原處理人", () => {
  const open = mk({}), decided = mk({ status: "decided", decidedBy: "Bob", decidedByActor: { id: "u2", kind: "user", handle: "bob@x.com", name: "Bob", status: "active" } });
  const bot: Viewer = { me: undefined, isAdmin: false, isHuman: false };
  assert.equal(canAnswer(open, human).ok, true);
  assert.equal(canAnswer(open, bot).ok, false);
  assert.equal(canAnswer(decided, human).ok, false, "已處理不能再回答");
  assert.equal(canToIssue(open, human).ok, true);
  assert.equal(canToIssue(mk({ sourceIssue: "ISS-1" }), human).ok, false, "已有來源 Issue");
  assert.equal(canToIssue(open, bot).ok, false);
  assert.equal(canWithdraw(open, human).ok, true, "提出者(createdBy 是 email)");
  assert.equal(canWithdraw(mk({ createdBy: "Carol", createdByActor: { id: "u3", kind: "user", handle: "carol@x.com", name: "Carol", status: "active" } }), human).ok, false);
  assert.equal(canWithdraw(mk({ createdBy: "Carol" }), { ...human, isAdmin: true }).ok, true);
  assert.equal(canWithdraw(mk({ createdBy: "x", createdByActor: { id: "a", kind: "agent", handle: "amy@x.com", name: "角色", status: "active" } }), human).ok, false, "角色不算本人");
  assert.equal(canWithdraw(decided, { ...human, isAdmin: true }).ok, false, "已處理不能撤回");
  assert.equal(canReopen(decided, { me: "BOB@x.com", isAdmin: false, isHuman: true }).ok, true, "原處理人(不分大小寫)");
  assert.equal(canReopen(decided, human).ok, false);
  assert.equal(canReopen(decided, { ...human, isAdmin: true }).ok, true);
  assert.equal(canReopen(open, { ...human, isAdmin: true }).ok, false);
  const p = canReopen(decided, human);
  assert.ok(!p.ok && p.reason.length > 0, "不能按時有 tooltip 說明");
});

test("錯誤訊息:403 / 404 / 409 有清楚的說明,其餘原樣", () => {
  assert.ok(/沒有權限/.test(errorMessage({ status: 403, message: "x" })));
  assert.ok(/找不到這筆.*DEC-9/.test(errorMessage({ status: 404, message: "找不到 DEC-9" })));
  assert.ok(/狀態已經變了/.test(errorMessage({ status: 409, message: "已處理" })));
  assert.equal(errorMessage(new Error("boom")), "boom");
  assert.equal(errorMessage(undefined), "操作失敗");
});
