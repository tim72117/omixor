// src/lib/viewStore.ts 的單元測試(npm test;node:test,不加測試框架依賴)。
// 計時器用假的時鐘(advance 手動往前推),請求用 deferred(測試決定什麼時候、用什麼版本回來)。
import { test } from "node:test";
import assert from "node:assert/strict";
import { ViewStore, type LoadResult, type StoreClock } from "../src/lib/viewStore.ts";

function fakeClock() {
  let now = 0;
  let id = 0;
  const timers = new Map<number, { at: number; f: () => void }>();
  const clock: StoreClock = {
    setTimeout: (f, ms) => (timers.set(++id, { at: now + ms, f }), id),
    clearTimeout: (t) => void timers.delete(t as number),
    now: () => now,
  };
  const advance = (ms: number) => {
    const end = now + ms;
    for (;;) {
      const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      timers.delete(due[0]);
      now = due[1].at;
      due[1].f();
    }
    now = end;
  };
  return { clock, advance, pending: () => timers.size };
}

/** 每次呼叫 load 都產生一個等著被 resolve / reject 的請求 */
function fakeServer() {
  const calls: { resolve: (r: LoadResult) => void; reject: (e: unknown) => void }[] = [];
  const load = () => new Promise<LoadResult>((resolve, reject) => calls.push({ resolve, reject }));
  return { calls, load };
}

const flush = () => new Promise<void>((r) => globalThis.setTimeout(r, 0));

function setup() {
  const c = fakeClock();
  const store = new ViewStore({ clock: c.clock, debounceMs: 80 });
  const srv = fakeServer();
  let changes = 0;
  const sub = (key = "view:issues", opts = {}) => store.subscribe(key, srv.load, [key.replace(/^view:/, "")], opts, () => changes++);
  return { store, srv, sub, ...c, changes: () => changes };
}

test("同一個 view 多個訂閱者只發一個請求,通知也只重讀一次", async () => {
  const { store, srv, sub, advance } = setup();
  sub(); sub(); sub();
  assert.equal(srv.calls.length, 1, "掛載三個只讀一次");
  srv.calls[0].resolve({ data: [1], version: 3 });
  await flush();
  assert.deepEqual(store.get("view:issues").data, [1]);
  assert.equal(store.get("view:issues").status, "ready");
  sub(); // 已經有資料:第四個訂閱者直接用快取
  assert.equal(srv.calls.length, 1);
  store.notify(["issues"], 4);
  store.notify(["issues"], 5); // 80ms 內的通知合併
  advance(80);
  assert.equal(srv.calls.length, 2, "通知只重讀一次");
  store.notify(["services"], 6); // 不相干的主題
  advance(200);
  assert.equal(srv.calls.length, 2);
});

test("沒人看的 view 收到通知只標記 stale,下次訂閱才重讀", async () => {
  const { store, srv, sub, advance, pending } = setup();
  const off = sub();
  srv.calls[0].resolve({ data: "a", version: 1 });
  await flush();
  off();
  store.notify(["issues"], 2);
  advance(1000);
  assert.equal(srv.calls.length, 1, "沒人看不重讀");
  assert.equal(pending(), 0);
  sub();
  assert.equal(srv.calls.length, 2, "重新訂閱時重讀");
  assert.equal(store.get("view:issues").data, "a", "重讀期間保留舊資料");
  assert.equal(store.get("view:issues").loading, true);
});

test("取消訂閱時還沒做的重讀,下次訂閱補做", async () => {
  const { srv, sub, store, advance } = setup();
  const off = sub();
  srv.calls[0].resolve({ data: 1, version: 1 });
  await flush();
  store.notify(["issues"], 2);
  off(); // 計時器還沒到就離開頁面
  advance(200);
  assert.equal(srv.calls.length, 1);
  sub();
  assert.equal(srv.calls.length, 2);
});

test("晚回來的舊回應丟掉", async () => {
  const { store, srv, sub } = setup();
  sub();
  store.reload("view:issues"); // 例如做完動作後手動重讀
  assert.equal(srv.calls.length, 2);
  srv.calls[1].resolve({ data: "new", version: 2 });
  await flush();
  srv.calls[0].resolve({ data: "old", version: 1 });
  srv.calls[0].reject(new Error("x"));
  await flush();
  const s = store.get("view:issues");
  assert.equal(s.data, "new");
  assert.equal(s.error, undefined);
  assert.equal(s.version, 2);
});

test("錯誤保留上一次的資料;下次成功清掉錯誤", async () => {
  const { store, srv, sub } = setup();
  sub();
  srv.calls[0].resolve({ data: [1] });
  await flush();
  store.reload("view:issues");
  srv.calls[1].reject(new Error("HTTP 500"));
  await flush();
  let s = store.get("view:issues");
  assert.equal(s.error, "HTTP 500");
  assert.equal(s.status, "error");
  assert.deepEqual(s.data, [1]);
  store.reload("view:issues");
  srv.calls[2].resolve({ data: [2] });
  await flush();
  s = store.get("view:issues");
  assert.equal(s.error, undefined);
  assert.equal(s.status, "ready");
});

test("開頁:掛載讀一次 + SSE 連上的 \"*\" 不會再讀(回應版本 ≥ 通知版本)", async () => {
  const { srv, sub, store, advance } = setup();
  sub();
  store.notify(["*"], 7); // SSE 連上,請求還沒回來
  advance(80); // 計時到了,請求仍在進行:等它回來再判斷
  assert.equal(srv.calls.length, 1);
  srv.calls[0].resolve({ data: 1, version: 7 });
  await flush();
  advance(200);
  assert.equal(srv.calls.length, 1, "版本已涵蓋,不重讀");
  store.notify(["*"], 7); // 先回來、後收到 "*" 也一樣
  advance(200);
  assert.equal(srv.calls.length, 1);
});

test("回應沒有版本(舊伺服器)時,收到通知照舊重讀", async () => {
  const { srv, sub, store, advance } = setup();
  sub();
  store.notify(["*"], 7);
  advance(80);
  srv.calls[0].resolve({ data: 1 }); // 沒有 X-Omixor-Version
  await flush();
  assert.equal(srv.calls.length, 2, "請求可能比通知早算,回來後補讀一次");
});

test("請求進行中收到更新的變更,回來後補讀", async () => {
  const { srv, sub, store, advance } = setup();
  sub();
  store.notify(["issues"], 9);
  advance(80);
  srv.calls[0].resolve({ data: 1, version: 8 }); // 回應比通知舊
  await flush();
  assert.equal(srv.calls.length, 2);
});

test("斷線後版本作廢,重連的 \"*\" 一律重讀;refreshTopics(沒有版本)一律重讀", async () => {
  const { srv, sub, store, advance } = setup();
  sub();
  srv.calls[0].resolve({ data: 1, version: 50 });
  await flush();
  store.resetVersions(); // 斷線(伺服器可能重啟,版本從 0 算)
  store.notify(["*"], 3);
  advance(80);
  assert.equal(srv.calls.length, 2);
  srv.calls[1].resolve({ data: 2, version: 3 });
  await flush();
  store.notify(["issues"]); // refreshTopics
  advance(80);
  assert.equal(srv.calls.length, 3);
});

test("throttleMs:兩次重讀至少隔這麼久;取訂閱者裡最小的", async () => {
  const { srv, sub, store, advance } = setup();
  const off = sub("view:badges", { throttleMs: 5000 });
  srv.calls[0].resolve({ data: 1, version: 1 });
  await flush();
  store.notify(["badges"], 2);
  advance(4000);
  assert.equal(srv.calls.length, 1);
  advance(1000);
  assert.equal(srv.calls.length, 2);
  srv.calls[1].resolve({ data: 2, version: 2 });
  await flush();
  sub("view:badges"); // 沒有限制的訂閱者
  store.notify(["badges"], 3);
  advance(80);
  assert.equal(srv.calls.length, 3);
  off();
});

test("訂閱者額外的主題也會觸發重讀", async () => {
  const { srv, sub, store, advance } = setup();
  sub("view:me", { topics: ["orgs"] });
  srv.calls[0].resolve({ data: 1, version: 1 });
  await flush();
  store.notify(["orgs"], 2);
  advance(80);
  assert.equal(srv.calls.length, 2);
});

test("沒人看的筆數超過上限時丟掉最舊的", async () => {
  const c = fakeClock();
  const store = new ViewStore({ clock: c.clock, maxIdle: 2 });
  const srv = fakeServer();
  for (const k of ["a", "b", "c"]) {
    const off = store.subscribe(k, srv.load, [], {}, () => {});
    c.advance(1);
    off();
  }
  assert.equal(store.has("a"), false);
  assert.equal(store.has("b"), true);
  assert.equal(store.has("c"), true);
});

test("請求數:開頁(每個 view 5 個元件)+ 一次 SSE 通知", async () => {
  // 以前:每個 useView 各自讀,掛載 5 次 + 連上的 "*" 再 5 次 = 10;每個主題通知 5 次。現在:1 + 0;通知 1 次
  const { store, srv, advance } = setup();
  for (let i = 0; i < 5; i++) store.subscribe("view:issues", srv.load, ["issues"], {}, () => {});
  store.notify(["*"], 1);
  srv.calls[0].resolve({ data: [], version: 1 });
  await flush();
  advance(200);
  assert.equal(store.requests, 1);
  store.notify(["issues"], 2);
  advance(80);
  assert.equal(store.requests, 2);
});
