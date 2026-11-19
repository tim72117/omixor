// 連 omixor console 後端（純 JS，可在 Node 測試；不碰 DOM 與 three.js）
//
// 由 console 提供（http://127.0.0.1:5191/office/viewer/）時：
//   GET  /api/office/events  SSE：hello、snapshot、command、role、ping
//   POST /api/office/report  回報 ack（runAction 結果）、arrived（抵達）、superseded（被手動操作取代）、state（角色狀態）
// 多個檢視器同時開：命令全部都會執行，但只有「主檢視器」（最早連上的）回報；後端會用 role 事件通知誰接手。
// 用 python3 -m http.server 開時 /api/office/state 是 404 → 獨立模式（什麼都不做）。

export const API = '/api/office/';
const MAX_BACKOFF = 15000;
const STALE_MS = 45000; // 這麼久沒收到任何事件（心跳 15 秒一次）就重連
const STATE_THROTTLE = 300;
const WALK_POLL = 3000;

/** 是否由 console 提供：?standalone 強制獨立模式；否則同源 GET /api/office/state 要回 JSON */
export async function detectConsole({ location, fetch }) {
  try {
    const q = new URLSearchParams(location?.search || '');
    if (q.has('standalone')) return false;
    const r = await fetch(API + 'state', { headers: { Accept: 'application/json' }, credentials: 'same-origin' });
    if (!r.ok) return false;
    const ct = (r.headers && r.headers.get && r.headers.get('content-type')) || '';
    if (!ct.includes('json')) return false;
    const j = await r.json();
    return !!j && typeof j === 'object' && 'viewerCount' in j;
  } catch {
    return false;
  }
}

/** 動作預期的抵達目標（比對 office:arrived 的 target，判斷是不是這個命令造成的抵達） */
function expectedTarget(act, agentId, seats) {
  if (act.type === 'goto' && act.target) return { kind: 'place', id: act.target.id };
  if (act.type === 'find' && act.target) return { kind: 'agent', id: act.target.id };
  if (act.type === 'return') return { kind: 'place', id: seats[agentId] || `seat_${agentId}` };
  return null;
}

/**
 * @param {object} o
 * @param {Function} o.EventSource
 * @param {Function} o.fetch
 * @param {Function} o.runAction  act → [{agent, ok, error?, already?}]（World.runAction）
 * @param {Function} o.state      () → office.state()
 * @param {object}   o.seats      { agentId: 'seat_<id>' }
 * @param {Function} [o.onStatus] ({ status:'connecting'|'connected'|'reconnecting'|'standalone', primary, viewerId })
 * @param {Function} [o.onCommand] (cmd, results) 收到並執行了後端的命令
 * @param {object}   [o.timers]   { setTimeout, clearTimeout, setInterval, clearInterval, now }（測試用）
 */
export function createBridge(o) {
  const T = {
    setTimeout: (...a) => setTimeout(...a), clearTimeout: id => clearTimeout(id),
    setInterval: (...a) => setInterval(...a), clearInterval: id => clearInterval(id),
    now: () => Date.now(), ...(o.timers || {}),
  };
  const seats = o.seats || {};
  let es = null, viewerId = null, primary = false, status = 'connecting';
  let backoff = 1000, retryTimer = null, lastEvent = 0, stopped = false;
  let stateTimer = null, watchTimer = null;
  const seen = new Set();      // 執行過的命令 id（snapshot 的 active 不重跑）
  const pending = new Map();   // agentId → { cmd, expect }

  const emitStatus = () => o.onStatus?.({ status, primary, viewerId });
  const setStatus = s => { status = s; emitStatus(); };

  function post(body) {
    if (!primary || !viewerId) return Promise.resolve(null);
    let agents = [];
    try { agents = o.state() || []; } catch { agents = []; }
    return Promise.resolve()
      .then(() => o.fetch(API + 'report', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ viewerId, ...body, agents }),
      }))
      .then(r => (r && r.ok ? r.json() : null))
      .then(j => {
        const res = j && j.result;
        if (res && res.accepted === false && res.primary === false && primary) { primary = false; emitStatus(); }
        return res || null;
      })
      .catch(() => null);
  }

  function remember(id) {
    seen.add(id);
    if (seen.size > 1000) seen.delete(seen.values().next().value);
  }

  function run(cmd) {
    if (!cmd || typeof cmd.id !== 'string' || !cmd.action || seen.has(cmd.id)) return null;
    remember(cmd.id);
    const act = cmd.action;
    const agents = Array.isArray(act.agents) ? act.agents.map(String) : [];
    const clean = { type: String(act.type), agents, target: act.target ? { kind: String(act.target.kind), id: String(act.target.id) } : undefined };
    // 先登記預期的抵達（goto 已經在目的地時，runAction 裡就會同步發出 office:arrived）
    for (const id of agents) {
      const exp = expectedTarget(clean, id, seats);
      if (exp) pending.set(id, { cmd: cmd.id, expect: exp });
      else pending.delete(id);
    }
    let results;
    try { results = o.runAction(clean) || []; } catch (err) { results = agents.map(id => ({ agent: id, ok: false, error: `執行失敗：${err.message}` })); }
    for (const r of results) {
      if (!r.ok) { const p = pending.get(r.agent); if (p && p.cmd === cmd.id) pending.delete(r.agent); }
    }
    post({ type: 'ack', commandId: cmd.id, results: results.map(r => ({ agent: String(r.agent), ok: !!r.ok, error: r.error ? String(r.error) : '', already: !!r.already })) });
    o.onCommand?.(cmd, results);
    return results;
  }

  /** office:arrived */
  function arrived(d) {
    if (!d || !d.agent) return;
    const p = pending.get(d.agent);
    let commandId = '';
    if (p) {
      pending.delete(d.agent);
      const t = d.target;
      if (t && p.expect && t.kind === p.expect.kind && t.id === p.expect.id) commandId = p.cmd;
      else post({ type: 'superseded', commandId: p.cmd, agent: d.agent, error: '被檢視器上的操作取代' });
    }
    post({ type: 'arrived', commandId, agent: d.agent, arrived: { target: d.target || null, position: d.position, seated: !!d.seated, where: d.where || '' } });
  }

  /** 檢視器上手動操作（命令列、點地板、window.office API）：這些角色還沒抵達的後端命令算被取代 */
  function localOverride(ids) {
    for (const id of ids || []) {
      const p = pending.get(id);
      if (!p) continue;
      pending.delete(id);
      post({ type: 'superseded', commandId: p.cmd, agent: id, error: '被檢視器上的操作取代' });
    }
  }

  /** office:state（開始走、停下、抵達）：節流後回報全部角色狀態 */
  function stateChanged() {
    if (!primary || stateTimer) return;
    stateTimer = T.setTimeout(() => { stateTimer = null; post({ type: 'state' }); }, STATE_THROTTLE);
  }

  function handle(type, data) {
    lastEvent = T.now();
    if (type === 'hello') {
      viewerId = data.viewerId; primary = !!data.primary; backoff = 1000;
      setStatus('connected');
    } else if (type === 'snapshot') {
      viewerId = data.viewerId || viewerId; primary = !!data.primary;
      emitStatus();
      for (const cmd of data.active || []) run(cmd);
      post({ type: 'state' });
    } else if (type === 'command') {
      run(data);
    } else if (type === 'role') {
      primary = !!data.primary;
      emitStatus();
      post({ type: 'state' });
    }
  }

  function scheduleReconnect() {
    if (stopped || retryTimer) return;
    setStatus('reconnecting');
    const wait = backoff;
    backoff = Math.min(MAX_BACKOFF, backoff * 2);
    retryTimer = T.setTimeout(() => { retryTimer = null; connect(); }, wait);
  }

  function connect() {
    if (stopped) return;
    if (es) { try { es.close(); } catch { /* 已關 */ } }
    viewerId = null; primary = false;
    if (status !== 'reconnecting') setStatus('connecting');
    lastEvent = T.now();
    es = new o.EventSource(API + 'events', { withCredentials: true });
    for (const type of ['hello', 'snapshot', 'command', 'role', 'ping']) {
      es.addEventListener(type, e => {
        let data = {};
        try { data = JSON.parse(e.data || '{}'); } catch { return; }
        handle(type, data || {});
      });
    }
    // 自己控制重連（退避），不用 EventSource 內建的固定間隔
    es.onerror = () => {
      try { es.close(); } catch { /* 已關 */ }
      es = null;
      viewerId = null; primary = false;
      scheduleReconnect();
    };
  }

  function start() {
    stopped = false;
    connect();
    // 心跳逾時就重連；走路中每 3 秒回報一次位置
    watchTimer = T.setInterval(() => {
      if (es && T.now() - lastEvent > STALE_MS) { try { es.close(); } catch { /* 已關 */ } es = null; scheduleReconnect(); }
      if (primary) {
        let walking = false;
        try { walking = (o.state() || []).some(s => s.state === 'walking' || s.state === 'waiting'); } catch { /* 忽略 */ }
        if (walking) post({ type: 'state' });
      }
    }, WALK_POLL);
  }

  function stop() {
    stopped = true;
    if (es) { try { es.close(); } catch { /* 已關 */ } es = null; }
    if (retryTimer) T.clearTimeout(retryTimer);
    if (watchTimer) T.clearInterval(watchTimer);
    if (stateTimer) T.clearTimeout(stateTimer);
    retryTimer = watchTimer = stateTimer = null;
  }

  return {
    start, stop, arrived, stateChanged, localOverride,
    info: () => ({ status, primary, viewerId, pending: Object.fromEntries([...pending].map(([k, v]) => [k, v.cmd])) }),
    _handle: handle, // 測試用
  };
}
