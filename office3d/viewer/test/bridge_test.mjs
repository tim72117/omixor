// bridge.js 單元測試（假的 EventSource / fetch / 計時器）：node viewer/test/bridge_test.mjs
import { createBridge, detectConsole, API } from '../js/bridge.js';

let pass = 0, fail = 0;
const ok = (cond, msg) => { if (cond) pass++; else { fail++; console.log('  FAIL', msg); } };

class FakeES {
  static all = [];
  constructor(url) { this.url = url; this.h = {}; this.closed = false; FakeES.all.push(this); }
  addEventListener(t, f) { (this.h[t] ||= []).push(f); }
  emit(t, data) { for (const f of this.h[t] || []) f({ data: JSON.stringify(data) }); }
  close() { this.closed = true; }
}

function setup({ runAction } = {}) {
  FakeES.all = [];
  const posts = [];
  const timers = [];
  let now = 0;
  const T = {
    setTimeout: (f, ms) => { timers.push({ f, at: now + ms, once: true }); return timers.length; },
    clearTimeout: id => { if (timers[id - 1]) timers[id - 1].f = null; },
    setInterval: (f, ms) => { timers.push({ f, every: ms, at: now + ms }); return timers.length; },
    clearInterval: id => { if (timers[id - 1]) timers[id - 1].f = null; },
    now: () => now,
  };
  const advance = ms => {
    const end = now + ms;
    for (;;) {
      const t = timers.filter(x => x.f && x.at <= end).sort((a, b) => a.at - b.at)[0];
      if (!t) break;
      now = t.at;
      const f = t.f;
      if (t.once) t.f = null; else t.at += t.every;
      f();
    }
    now = end;
  };
  const statuses = [];
  const ran = [];
  const state = [{ id: 'engineer', state: 'idle' }];
  const b = createBridge({
    EventSource: FakeES,
    fetch: async (url, init) => { posts.push({ url, body: JSON.parse(init.body) }); return { ok: true, json: async () => ({ result: { accepted: true, primary: true } }) }; },
    runAction: runAction || (act => { ran.push(act); return act.agents.map(id => ({ agent: id, ok: true })); }),
    state: () => state,
    seats: { engineer: 'seat_engineer', ceo: 'seat_ceo' },
    onStatus: s => statuses.push(s),
    timers: T,
  });
  return { b, posts, statuses, ran, advance, state, es: () => FakeES.all[FakeES.all.length - 1] };
}
const flush = () => new Promise(r => setTimeout(r, 0));

// 連線、hello、snapshot 的 active 命令會執行一次
{
  const t = setup();
  t.b.start();
  ok(t.es().url === API + 'events', 'connects to /api/office/events');
  ok(t.statuses.at(-1).status === 'connecting', 'status connecting');
  t.es().emit('hello', { viewerId: 'v-1', primary: true });
  ok(t.statuses.at(-1).status === 'connected' && t.statuses.at(-1).primary, 'connected as primary');
  const cmd = { id: 'oc-1', action: { type: 'goto', agents: ['engineer'], target: { kind: 'place', id: 'meeting' } }, description: '工程師 去 會議室' };
  t.es().emit('snapshot', { viewerId: 'v-1', primary: true, agents: [], active: [cmd] });
  t.es().emit('command', cmd); // 同一個 id 不重跑
  ok(t.ran.length === 1 && t.ran[0].target.id === 'meeting', 'active command runs once');
  await flush();
  const ack = t.posts.find(p => p.body.type === 'ack');
  ok(ack && ack.body.viewerId === 'v-1' && ack.body.commandId === 'oc-1' && ack.body.results[0].ok, 'ack posted with viewerId');
  ok(Array.isArray(ack.body.agents), 'ack carries state');
  // 抵達對得上 → 帶 commandId
  t.b.arrived({ agent: 'engineer', target: { kind: 'place', id: 'meeting' }, position: [1, 0, 2], seated: true, where: '會議室' });
  await flush();
  const arr = t.posts.find(p => p.body.type === 'arrived');
  ok(arr && arr.body.commandId === 'oc-1' && arr.body.arrived.where === '會議室', 'arrived posted with commandId');
  ok(!t.b.info().pending.engineer, 'pending cleared after arrival');
  t.b.stop();
}

// 抵達目標不同（被手動操作改掉）→ superseded；return 用座位比對
{
  const t = setup();
  t.b.start();
  t.es().emit('hello', { viewerId: 'v-2', primary: true });
  t.es().emit('command', { id: 'oc-2', action: { type: 'return', agents: ['engineer'] } });
  t.b.arrived({ agent: 'engineer', target: { kind: 'place', id: 'pantry' } });
  await flush();
  ok(t.posts.some(p => p.body.type === 'superseded' && p.body.commandId === 'oc-2'), 'mismatched arrival → superseded');
  ok(t.posts.some(p => p.body.type === 'arrived' && p.body.commandId === ''), 'arrival still reported without commandId');
  t.es().emit('command', { id: 'oc-3', action: { type: 'return', agents: ['engineer'] } });
  t.b.arrived({ agent: 'engineer', target: { kind: 'place', id: 'seat_engineer' } });
  await flush();
  ok(t.posts.some(p => p.body.type === 'arrived' && p.body.commandId === 'oc-3'), 'return matches seat');
  t.es().emit('command', { id: 'oc-4', action: { type: 'goto', agents: ['engineer'], target: { kind: 'place', id: 'lounge' } } });
  t.b.localOverride(['engineer']);
  await flush();
  ok(t.posts.some(p => p.body.type === 'superseded' && p.body.commandId === 'oc-4'), 'local override → superseded');
  t.b.stop();
}

// 失敗的結果：ack ok=false，不等抵達
{
  const t = setup({ runAction: act => act.agents.map(id => ({ agent: id, ok: false, error: '找不到路' })) });
  t.b.start();
  t.es().emit('hello', { viewerId: 'v-3', primary: true });
  t.es().emit('command', { id: 'oc-5', action: { type: 'goto', agents: ['engineer'], target: { kind: 'place', id: 'meeting' } } });
  await flush();
  const ack = t.posts.find(p => p.body.type === 'ack');
  ok(ack && ack.body.results[0].ok === false && ack.body.results[0].error === '找不到路', 'failed ack');
  ok(!t.b.info().pending.engineer, 'failed agent not pending');
  t.b.stop();
}

// 副檢視器：照樣執行，但不回報；收到 role 變主檢視器後才回報
{
  const t = setup();
  t.b.start();
  t.es().emit('hello', { viewerId: 'v-4', primary: false });
  t.es().emit('command', { id: 'oc-6', action: { type: 'stop', agents: ['engineer'] } });
  await flush();
  ok(t.ran.length === 1 && t.posts.length === 0, 'secondary runs but does not report');
  t.es().emit('role', { primary: true });
  await flush();
  ok(t.posts.some(p => p.body.type === 'state' && p.body.viewerId === 'v-4'), 'promoted viewer posts state');
  t.b.stop();
}

// 斷線：重連中 → 退避後重連；收到 hello 回到已連線；心跳逾時也會重連
{
  const t = setup();
  t.b.start();
  const first = t.es();
  first.emit('hello', { viewerId: 'v-5', primary: true });
  first.onerror();
  ok(first.closed && t.statuses.at(-1).status === 'reconnecting', 'error → reconnecting');
  t.advance(999);
  ok(FakeES.all.length === 1, 'waits backoff');
  t.advance(2);
  ok(FakeES.all.length === 2 && t.statuses.at(-1).status === 'reconnecting', 'reconnect after 1s, still reconnecting');
  t.es().onerror();
  t.advance(1500);
  ok(FakeES.all.length === 2, 'backoff doubles (2s)');
  t.advance(600);
  ok(FakeES.all.length === 3, 'second reconnect');
  t.es().emit('hello', { viewerId: 'v-6', primary: true });
  ok(t.statuses.at(-1).status === 'connected' && t.b.info().viewerId === 'v-6', 'connected again with new viewer id');
  t.advance(52000);
  ok(FakeES.all.length >= 4, 'stale stream (no heartbeat) → reconnect');
  t.b.stop();
}

// 偵測：?standalone、404、非 JSON、正常
{
  const loc = s => ({ search: s });
  const res = (status, ct, body) => async () => ({ ok: status === 200, headers: { get: () => ct }, json: async () => body });
  ok(await detectConsole({ location: loc('?standalone'), fetch: res(200, 'application/json', { viewerCount: 0 }) }) === false, '?standalone forces standalone');
  ok(await detectConsole({ location: loc(''), fetch: res(404, 'text/html', null) }) === false, '404 → standalone (python http.server)');
  ok(await detectConsole({ location: loc(''), fetch: res(200, 'text/html', null) }) === false, 'html → standalone');
  ok(await detectConsole({ location: loc(''), fetch: async () => { throw new Error('offline'); } }) === false, 'network error → standalone');
  ok(await detectConsole({ location: loc(''), fetch: res(200, 'application/json; charset=utf-8', { viewerCount: 1 }) }) === true, 'console detected');
}

console.log(`bridge_test ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
