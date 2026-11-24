// src/lib/navStore.ts 的單元測試(npm test):用假的瀏覽器歷史檢查
// 啟動、push / replace、上一頁不 push(防迴圈)、深度與「返回」、後端導覽的跟隨與 echo、功能關閉時的 guard。
import { test } from "node:test";
import assert from "node:assert/strict";
import { RouteStore, ECHO_TTL_MS, type NavState, type RouteEnv } from "../src/lib/navStore.ts";
import { makeRoute, type Route } from "../src/lib/navRoute.ts";

const SECTIONS = new Set(["overview", "development", "settings", "system"]);

/** 假的瀏覽器歷史:entries + index,back() 會同步觸發 popstate */
function fakeEnv(initialHash = "", initialState: unknown = null) {
  const entries: { hash: string; state: unknown }[] = [{ hash: initialHash, state: initialState }];
  let i = 0;
  let pop: (() => void) | undefined;
  const log = { push: 0, replace: 0 };
  const e = { visible: true, time: 0 };
  const env: RouteEnv = {
    hash: () => entries[i].hash,
    state: () => entries[i].state,
    push: (state: NavState, hash) => { log.push++; entries.splice(i + 1); entries.push({ hash, state }); i++; },
    replace: (state: NavState, hash) => { log.replace++; entries[i] = { hash: hash ?? entries[i].hash, state }; },
    back: () => { if (i > 0) { i--; pop?.(); } },
    onPop: (cb) => { pop = cb; return () => { pop = undefined; }; },
    visible: () => e.visible,
    now: () => e.time,
  };
  return {
    env, log, e, entries,
    forward: () => { if (i < entries.length - 1) { i++; pop?.(); } },
    /** 使用者手動改網址:新增一筆沒有 state 的歷史,然後 popstate */
    typeHash: (hash: string) => { entries.splice(i + 1); entries.push({ hash, state: null }); i++; pop?.(); },
    current: () => entries[i],
  };
}

function setup(initialHash = "", initialState: unknown = null) {
  const h = fakeEnv(initialHash, initialState);
  const announced: Route[] = [];
  const store = new RouteStore(h.env, {
    isSection: (s) => SECTIONS.has(s), fallbackSection: "overview",
    announce: (r) => { announced.push(r); return Promise.resolve(); },
  });
  store.start();
  const route = () => store.getSnapshot().route;
  return { ...h, store, announced, route };
}

test("啟動:網址有合法路由 → 直接用,不 push,告訴後端", () => {
  const t = setup("#/development?tab=issues");
  assert.deepEqual(t.route(), makeRoute("development", undefined, { tab: "issues" }));
  assert.equal(t.log.push, 0);
  assert.equal(t.announced.length, 1);
  assert.equal(t.store.getSnapshot().canGoBack, false);
});

test("啟動:沒有路由 → 等後端的目前頁,用 replace 補上網址", () => {
  const t = setup("");
  assert.equal(t.route(), null);
  t.store.onBackendNav({ section: "settings", focusId: "repos" });
  assert.deepEqual(t.route(), makeRoute("settings", "repos"));
  assert.equal(t.current().hash, "#/settings/repos");
  assert.equal(t.log.push, 0);
  assert.equal(t.announced.length, 0); // 後端本來就在那頁
});

test("啟動:網址無效 → 當作沒有路由;後端不認得的頁面退回總覽", () => {
  const t = setup("#/nope");
  assert.equal(t.route(), null);
  t.store.onBackendNav({ section: "ghost" });
  assert.equal(t.route()!.section, "overview");
});

test("重新整理:history.state 的深度保留,「返回」仍可用", () => {
  const t = setup("#/development", { omx: 3 });
  assert.equal(t.store.getSnapshot().depth, 3);
  assert.equal(t.store.getSnapshot().canGoBack, true);
});

test("導覽:push 並增加深度、告訴後端;同一頁不重複 push", () => {
  const t = setup("#/overview");
  void t.store.navigate("development", "ISS-5");
  assert.equal(t.log.push, 1);
  assert.equal(t.current().hash, "#/development/ISS-5");
  assert.equal(t.store.getSnapshot().depth, 1);
  assert.ok(t.store.getSnapshot().canGoBack);
  assert.deepEqual(t.announced.at(-1), makeRoute("development", "ISS-5"));
  void t.store.navigate("development", "ISS-5");
  assert.equal(t.log.push, 1);
  assert.equal(t.announced.length, 3); // 啟動 + 兩次導覽(同頁仍會告訴後端)
});

test("上一頁 / 下一頁:還原頁面,絕不 push(防迴圈),後端跟著同步", () => {
  const t = setup("#/overview");
  void t.store.navigate("development", "ISS-5");
  void t.store.navigate("settings");
  const pushes = t.log.push;
  t.store.back();
  assert.deepEqual(t.route(), makeRoute("development", "ISS-5"));
  assert.equal(t.log.push, pushes);
  assert.deepEqual(t.announced.at(-1), makeRoute("development", "ISS-5"));
  assert.equal(t.store.getSnapshot().depth, 1);
  t.store.back();
  assert.equal(t.route()!.section, "overview");
  assert.equal(t.store.getSnapshot().canGoBack, false);
  t.store.back(); // 已經沒有上一頁:不動
  assert.equal(t.route()!.section, "overview");
  t.forward();
  assert.equal(t.route()!.section, "development");
  assert.equal(t.log.push, pushes);
});

test("上一頁:回到之後的導覽可以從新位置繼續(歷史被截斷,深度正確)", () => {
  const t = setup("#/overview");
  void t.store.navigate("development");
  t.store.back();
  void t.store.navigate("settings");
  assert.equal(t.store.getSnapshot().depth, 1);
  assert.equal(t.current().hash, "#/settings");
});

test("上一頁到無效網址 → 退回總覽(replace,不 push);手動改網址補上 state", () => {
  const t = setup("#/overview");
  t.typeHash("#/garbage");
  assert.equal(t.route()!.section, "overview");
  assert.equal(t.current().hash, "#/overview");
  assert.equal(t.log.push, 0);
  assert.equal(t.store.getSnapshot().depth, 1);
  t.typeHash("#/settings");
  assert.equal(t.route()!.section, "settings");
  assert.equal(t.store.getSnapshot().depth, 2);
  assert.deepEqual(t.current().state, { omx: 2 });
});

test("子狀態:replace 不新增歷史、push 新增;同值不寫;focusId 一律 replace 並拿掉", () => {
  const t = setup("#/development");
  t.store.setParams({ tab: "issues" }, "replace");
  assert.equal(t.log.push, 0);
  assert.equal(t.current().hash, "#/development?tab=issues");
  t.store.setParams({ issue: "a" }, "push");
  assert.equal(t.log.push, 1);
  t.store.setParams({ issue: "a" }, "push");
  assert.equal(t.log.push, 1);
  t.store.back();
  assert.deepEqual(t.route()!.params, { tab: "issues" });

  void t.store.navigate("development", "ISS-9");
  const n = t.log.push;
  t.announced.length = 0;
  t.store.setParams({ tab: "issues", issue: "z" }, "push"); // 解開 focusId:不新增歷史
  assert.equal(t.log.push, n);
  assert.deepEqual(t.route(), makeRoute("development", undefined, { tab: "issues", issue: "z" }));
  assert.deepEqual(t.announced.at(-1), makeRoute("development", undefined, { tab: "issues", issue: "z" })); // 後端也清掉 focusId
});

test("clearFocus:replace、不新增歷史;沒有 focusId 時什麼都不做", () => {
  const t = setup("#/overview");
  void t.store.navigate("development", "ISS-5");
  const n = t.log.push;
  t.store.clearFocus();
  assert.equal(t.log.push, n);
  assert.equal(t.current().hash, "#/development");
  const a = t.announced.length;
  t.store.clearFocus();
  assert.equal(t.announced.length, a);
});

test("後端導覽:自己送出的 echo 忽略(含連點兩次、中間的 echo 被合併)", () => {
  const t = setup("#/overview");
  t.store.onBackendNav({ section: "overview" }); // 第一次觀察
  void t.store.navigate("development");
  void t.store.navigate("settings");
  const pushes = t.log.push;
  t.store.onBackendNav({ section: "development" }); // 第一個 echo 晚到:不能把畫面切回去
  assert.equal(t.route()!.section, "settings");
  t.store.onBackendNav({ section: "settings" });
  assert.equal(t.route()!.section, "settings");
  assert.equal(t.log.push, pushes);
});

test("後端導覽:別處(MCP)推來的會 push 跟過去,而且不回送後端", () => {
  const t = setup("#/overview");
  t.store.onBackendNav({ section: "overview" });
  const sent = t.announced.length;
  t.store.onBackendNav({ section: "development", focusId: "ISS-7" });
  assert.deepEqual(t.route(), makeRoute("development", "ISS-7"));
  assert.equal(t.log.push, 1);
  assert.equal(t.announced.length, sent);
  assert.ok(t.store.getSnapshot().canGoBack);
});

test("後端導覽:背景分頁不跟、同一頁不動、echo 過期後算別處的導覽", () => {
  const t = setup("#/overview");
  t.store.onBackendNav({ section: "overview" });
  t.e.visible = false;
  t.store.onBackendNav({ section: "settings" });
  assert.equal(t.route()!.section, "overview");
  t.e.visible = true;
  t.store.onBackendNav({ section: "overview" }); // 回到跟自己一樣:不動
  assert.equal(t.log.push, 0);
  void t.store.navigate("development");
  t.e.time += ECHO_TTL_MS + 1;
  t.store.onBackendNav({ section: "system" }); // 自己送的 echo 已過期,這個是別處的
  assert.equal(t.route()!.section, "system");
});

test("redirect(功能關閉):replace,不新增歷史", () => {
  const t = setup("#/system");
  t.store.redirect("overview");
  assert.equal(t.log.push, 0);
  assert.equal(t.current().hash, "#/overview");
});

test("訂閱:變更才通知,取消訂閱後不再通知", () => {
  const t = setup("#/overview");
  let n = 0;
  const off = t.store.subscribe(() => n++);
  void t.store.navigate("settings");
  assert.equal(n, 1);
  off();
  void t.store.navigate("system");
  assert.equal(n, 1);
});
