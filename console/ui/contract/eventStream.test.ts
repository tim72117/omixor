// src/lib/eventStream.ts:登入前的 401、session 過期讓瀏覽器放棄連線後要自己重建;登入成功馬上重建
import { test } from "node:test";
import assert from "node:assert/strict";
import { createEventStream, type EventSourceLike } from "../src/lib/eventStream.ts";

function setup() {
  const sources: (EventSourceLike & { closed: boolean })[] = [];
  const timers: { f: () => void; ms: number; cancelled: boolean }[] = [];
  const seen = { messages: [] as string[], lost: 0, gaveUp: 0 };
  const stream = createEventStream({
    open: () => {
      const es = { onmessage: null, onopen: null, onerror: null, readyState: 0, closed: false, close() { this.closed = true; } } as EventSourceLike & { closed: boolean };
      sources.push(es);
      return es;
    },
    message: (d) => void seen.messages.push(d),
    lost: () => void seen.lost++,
    gaveUp: () => void seen.gaveUp++,
    setTimeout: (f, ms) => (timers.push({ f, ms, cancelled: false }), timers.length - 1),
    clearTimeout: (id) => void (timers[id as number].cancelled = true),
    minDelayMs: 100,
    maxDelayMs: 300,
  });
  const fail = (readyState: number) => { const es = sources.at(-1)!; es.readyState = readyState; es.onerror?.(); };
  const fire = () => { const t = timers.at(-1)!; assert.equal(t.cancelled, false); t.f(); };
  return { stream, sources, timers, seen, fail, fire };
}

test("瀏覽器會自己重連的中斷不另外建連線;放棄後加倍間隔重建,連上後歸零", () => {
  const s = setup();
  s.stream.start();
  s.stream.start();
  assert.equal(s.sources.length, 1);
  s.fail(0); // CONNECTING
  assert.deepEqual([s.sources.length, s.timers.length, s.seen.lost, s.seen.gaveUp], [1, 0, 1, 0]);
  for (let i = 0; i < 4; i++) { s.fail(2); s.fire(); } // 401
  assert.deepEqual(s.timers.map((t) => t.ms), [100, 200, 300, 300]);
  assert.equal(s.sources.length, 5);
  assert.equal(s.seen.gaveUp, 4);
  s.sources.at(-1)!.onopen?.();
  s.sources.at(-1)!.onmessage?.({ data: "x" });
  assert.deepEqual(s.seen.messages, ["x"]);
  s.fail(2);
  assert.equal(s.timers.at(-1)!.ms, 100);
});

test("登入成功馬上重建(取消等待中的重試);登出後關掉不再重連", () => {
  const s = setup();
  s.stream.start();
  s.fail(2);
  s.stream.restart();
  assert.equal(s.timers[0].cancelled, true);
  assert.equal(s.sources.length, 2);
  s.stream.stop();
  assert.equal(s.sources[1].closed, true);
  s.stream.start(); // 畫面重畫時的 connect() 不能在登出狀態又連上
  assert.equal(s.sources.length, 2);
  s.stream.restart();
  assert.equal(s.sources.length, 3);
});
