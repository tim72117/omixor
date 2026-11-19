// agents.js 的單元 / 小型模擬測試（假的 three.js）：node viewer/test/agents_test.mjs
// 涵蓋：可注入亂數（固定種子）、stop 釋放站位、終點被擋不瞬移、缺角色不崩潰、找人站位要有視線、站位姿勢欄位。
import { register } from 'node:module';
import { readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const stub = pathToFileURL(join(here, 'three_stub.mjs')).href;
register('data:text/javascript,' + encodeURIComponent(`
export async function resolve(spec, ctx, next) {
  if (spec === 'three') return { url: ${JSON.stringify(stub)}, shortCircuit: true };
  return next(spec, ctx);
}`));
const { Agent, World, mulberry32 } = await import('../js/agents.js');
const { NavGrid } = await import('../js/nav.js');
const { createParser } = await import('../js/command.js');
const { Vector3, Quaternion } = await import('./three_stub.mjs');

const root = join(here, '..', '..');
const navJson = JSON.parse(readFileSync(join(root, 'navgrid.json'), 'utf8'));
const placesJson = JSON.parse(readFileSync(join(root, 'places.json'), 'utf8'));
const nav = new NavGrid(navJson);

let pass = 0, fail = 0;
const ok = (c, m) => { if (c) { pass++; console.log('  ok  ', m); } else { fail++; console.log('  FAIL', m); } };

function fakeRoot() {
  const nodes = {};
  return {
    userData: { height: 1.7 }, position: new Vector3(), rotation: new Vector3(),
    getObjectByName(n) { return nodes[n] ||= { name: n, quaternion: new Quaternion(), position: new Vector3(0, 0.85, 0), add() {} }; },
  };
}
function makeWorld({ seed = 1, only = null } = {}) {
  const infos = placesJson.agents.filter(i => !only || only.includes(i.id));
  const agents = infos.map(info => new Agent(info, fakeRoot(), placesJson.agents.indexOf(info)));
  const world = new World({ nav, places: placesJson, agents, random: mulberry32(seed) });
  world.seatAll();
  return world;
}
function sim(world, seconds, until = () => false, onStep = null) {
  const dt = 1 / 60;
  let t = 0;
  while (t < seconds) {
    world.update(dt);
    t += dt;
    if (onStep) onStep(t);
    if (until()) break;
  }
  return t;
}
const still = w => () => w.agents.every(a => !a.walking);
const P = id => placesJson.places.find(p => p.id === id);

// ---------------------------------------------------------------- 3) 可注入亂數
console.log('[3] injectable random / fixed seed');
{
  const r1 = mulberry32(42), r2 = mulberry32(42);
  const s1 = Array.from({ length: 5 }, r1), s2 = Array.from({ length: 5 }, r2);
  ok(JSON.stringify(s1) === JSON.stringify(s2) && s1.every(v => v >= 0 && v < 1), 'mulberry32 is deterministic and in [0,1)');
  const w1 = makeWorld({ seed: 7 }), w2 = makeWorld({ seed: 7 }), w3 = makeWorld({ seed: 8 });
  ok(w1.agents.every((a, i) => a.t === w2.agents[i].t), 'same seed → same initial animation phase');
  ok(w1.agents.some((a, i) => a.t !== w3.agents[i].t), 'different seed → different phase');
  // 同種子跑同一組命令，結果完全一樣
  const run = w => {
    for (const id of ['engineer', 'marketing', 'triager', 'ceo', 'narrator', 'course-dev', 'product-manager', 'visitor-analyst']) w.goto(w.agent(id), 'lounge');
    sim(w, 60, still(w));
    return w.agents.map(a => `${a.id}:${a.pos.x.toFixed(4)},${a.pos.y.toFixed(4)}`).join('|');
  };
  ok(run(makeWorld({ seed: 3 })) === run(makeWorld({ seed: 3 })), 'two runs with the same seed end in identical positions');
  // 預設亂數 = Math.random
  const orig = Math.random;
  let called = 0;
  Math.random = () => { called++; return 0.5; };
  const agents = [new Agent(placesJson.agents[0], fakeRoot(), 0)];
  new World({ nav, places: placesJson, agents });
  Math.random = orig;
  ok(called === 1 && agents[0].t === 5, 'World defaults to Math.random');
}

// ---------------------------------------------------------------- 4) stop 釋放站位保留
console.log('[4] stop releases reservation');
{
  const w = makeWorld();
  const eng = w.agent('engineer');
  ok(w.goto(eng, 'meeting').ok, 'engineer goto meeting');
  const key = eng.reservation;
  ok(key && key.startsWith('meeting#') && w.reserved.get(key) === 'engineer', `reservation taken (${key})`);
  sim(w, 1.5);
  ok(eng.walking, 'engineer is walking');
  const r = w.stop(eng);
  ok(r.ok && !eng.walking && eng.task === null, 'World.stop stops the agent');
  ok(eng.reservation === null && !w.reserved.has(key), 'stop while walking released the meeting reservation');
  // 釋放後別人可以拿到那個站位
  const pm = w.agent('product-manager');
  const pick = w.pickSpot(pm, w.places.get('meeting'));
  ok(pick && pick.key !== null, `released spot can be picked again (${pick && pick.key})`);
  // 坐在自己座位上 stop：保留不放（人還在那裡，避免別人坐到身上）
  const mk = w.agent('marketing');
  const seatKey = mk.reservation;
  w.stop(mk);
  ok(mk.reservation === seatKey && w.reserved.get(seatKey) === 'marketing', 'stop while sitting on own seat keeps that reservation');
  // runAction 的 stop 也走 World.stop
  const tri = w.agent('triager');
  w.goto(tri, 'pantry');
  const tk = tri.reservation;
  sim(w, 1);
  const res = w.runAction({ type: 'stop', agents: ['triager'] });
  ok(res[0].ok && tri.reservation === null && !w.reserved.has(tk), 'runAction stop releases reservation');
}

// ---------------------------------------------------------------- 5) 終點被擋：等待，不瞬移、不重疊
console.log('[5] blocked goal: wait, no teleport, no overlap');
{
  const w = makeWorld({ only: ['ceo', 'marketing', 'triager'] });
  const ceo = w.agent('ceo'), mk = w.agent('marketing'), tri = w.agent('triager');
  // ceo 離開座位（先走去會議室，座位保留釋放）
  w.goto(ceo, 'meeting');
  sim(w, 90, () => !ceo.walking);
  ok(w.reserved.get('seat_ceo#0') === undefined, 'ceo seat is free after ceo left');
  const seat = P('seat_ceo').spots[0].position;
  ok(w.goto(mk, 'seat_ceo').ok && mk.task.spotKey === 'seat_ceo#0', 'marketing heads to ceo seat spot 0');
  // marketing 走在路上時，triager 被放到座位上站著（沒保留，例如點地板走過去的）
  sim(w, 2);
  tri.place(seat[0], seat[2], 0, false);
  tri.state = 'arrived';
  w.reserve(tri, null);
  let maxStep = 0, minD = Infinity, prev = { x: mk.pos.x, z: mk.pos.y };
  sim(w, 60, () => !mk.walking, () => {
    maxStep = Math.max(maxStep, Math.hypot(mk.pos.x - prev.x, mk.pos.y - prev.z));
    prev = { x: mk.pos.x, z: mk.pos.y };
    minD = Math.min(minD, Math.hypot(mk.pos.x - tri.pos.x, mk.pos.y - tri.pos.y));
  });
  ok(mk.state === 'arrived', `marketing finished (where=${mk.where}, seated=${mk.seated})`);
  ok(maxStep < 0.06, `no teleport: max move per frame ${maxStep.toFixed(3)}m`);
  ok(minD > 0.45, `never overlapped the blocker: min distance ${minD.toFixed(2)}m`);
  ok(!(mk.seated && Math.hypot(mk.pos.x - seat[0], mk.pos.y - seat[2]) < 0.05), 'did not sit on the occupied seat');
  ok(mk.reservation !== 'seat_ceo#0', `moved to another spot (${mk.reservation})`);
  // 對照：座位空著時會真的走到並坐下
  const w2 = makeWorld({ only: ['ceo', 'marketing'] });
  w2.goto(w2.agent('ceo'), 'meeting'); sim(w2, 90, () => !w2.agent('ceo').walking);
  const m2 = w2.agent('marketing');
  w2.goto(m2, 'seat_ceo'); sim(w2, 90, () => !m2.walking);
  ok(m2.seated && Math.hypot(m2.pos.x - seat[0], m2.pos.y - seat[2]) < 0.05, 'free seat: walked all the way and sat');
  // 兩個人搶同一張會議椅：最後不會坐在同一個點
  const w3 = makeWorld();
  for (const id of ['engineer', 'product-manager', 'course-dev', 'triager', 'marketing', 'visitor-analyst', 'narrator', 'ceo']) w3.goto(w3.agent(id), 'meeting');
  sim(w3, 120, still(w3));
  const seated = w3.agents.filter(a => a.seated).map(a => `${a.pos.x.toFixed(2)},${a.pos.y.toFixed(2)}`);
  let minAll = Infinity;
  for (let i = 0; i < w3.agents.length; i++) for (let j = i + 1; j < w3.agents.length; j++) minAll = Math.min(minAll, Math.hypot(w3.agents[i].pos.x - w3.agents[j].pos.x, w3.agents[i].pos.y - w3.agents[j].pos.y));
  ok(new Set(seated).size === seated.length && minAll > 0.45, `8 agents → 7 meeting chairs: ${seated.length} seated, no shared seat, min spacing ${minAll.toFixed(2)}m`);
}

// ---------------------------------------------------------------- 6) 缺角色模型
console.log('[6] missing agents do not crash');
{
  const only = placesJson.agents.map(a => a.id).filter(id => id !== 'narrator');
  const w = makeWorld({ only });
  const parser = createParser({ agents: w.agents.map(a => a.info), places: placesJson.places });
  const p = parser.parse('旁白 去 會議室');
  ok(!p.ok && p.errors.length > 0, `parser (loaded agents only) rejects missing agent: ${p.errors[0]}`);
  const p2 = parser.parse('全部 回座位');
  ok(p2.ok && p2.actions[0].agents.length === only.length && !p2.actions[0].agents.includes('narrator'), '"all" covers only loaded agents');
  let threw = false, res = [];
  try {
    res = [
      ...w.runAction({ type: 'goto', agents: ['narrator'], target: { kind: 'place', id: 'meeting' } }),
      ...w.runAction({ type: 'find', agents: ['ceo'], target: { kind: 'agent', id: 'narrator' } }),
      ...w.runAction({ type: 'return', agents: ['narrator'] }),
      ...w.runAction({ type: 'stop', agents: ['narrator'] }),
    ];
    w.goto(undefined, 'meeting'); w.find(undefined, w.agent('ceo')); w.stop(undefined); w.returnToSeat(undefined);
  } catch (e) { threw = e; }
  ok(!threw, `no exception for missing agents ${threw ? threw.message : ''}`);
  ok(res.length === 4 && res.every(r => !r.ok && typeof r.error === 'string'), `errors returned: ${res.map(r => r.error).join(' / ')}`);
  ok(w.runAction({ type: 'goto', agents: ['engineer'], target: { kind: 'place', id: 'meeting' } })[0].ok, 'loaded agents still work');
}

// ---------------------------------------------------------------- 7) 找人站位要跟目標有視線
console.log('[7] find stand point has line of sight');
{
  const w = makeWorld();
  let total = 0, bad = [], fallback = 0;
  // 每個坐在座位上的人，被從每個共用地點來的人找
  const starts = placesJson.places.filter(p => p.kind === 'shared').map(p => p.spots[0].position);
  for (const tg of w.agents) {
    for (const s of starts) {
      const finder = w.agents.find(a => a !== tg);
      const save = { x: finder.pos.x, z: finder.pos.y };
      finder.pos.set(s[0], s[2]);
      const best = w.findStandPoint(finder, tg);
      finder.pos.set(save.x, save.z);
      total++;
      if (!best) { bad.push(`${tg.id}:none`); continue; }
      if (best.fallback) { fallback++; continue; }
      const d = Math.hypot(best.x - tg.pos.x, best.z - tg.pos.y);
      if (!w.sightFree(tg.pos.x, tg.pos.y, best.x, best.z) || d > 1.65 || !nav.isFreeAt(best.x, best.z)) bad.push(`${tg.id}@${d.toFixed(2)}`);
    }
  }
  ok(bad.length === 0 && fallback === 0, `${total} find stand points all free, within 1.6 m and in sight (bad=${bad.join(',')}, fallback=${fallback})`);
  // 從隔板另一側（北邊窗邊）找坐著的工程師：站位要在工程師這一側（隔板南邊），不是隔著桌子
  const eng = w.agent('engineer'), pm = w.agent('product-manager');
  pm.pos.set(eng.pos.x, eng.pos.y - 2.2); // three.js -z = 北
  const best = w.findStandPoint(pm, eng);
  const partitionZ = -6.32; // engineer_partition：Blender y ∈ [6.32, 6.72] → three.js z ≤ -6.32
  ok(best && !best.fallback && best.z > partitionZ && w.sightFree(eng.pos.x, eng.pos.y, best.x, best.z),
    `finder north of the partition stands on the engineer's side (z=${best && best.z.toFixed(2)} > ${partitionZ})`);
  // 實際走過去、抵達時看得到對方
  const w2 = makeWorld();
  const a2 = w2.agent('course-dev'), t2 = w2.agent('visitor-analyst');
  ok(w2.find(a2, t2).ok, 'course-dev find visitor-analyst');
  sim(w2, 90, () => !a2.walking);
  const d2 = Math.hypot(a2.pos.x - t2.pos.x, a2.pos.y - t2.pos.y);
  ok(a2.state === 'arrived' && d2 < 1.7 && w2.sightFree(t2.pos.x, t2.pos.y, a2.pos.x, a2.pos.y), `arrived next to target with line of sight (${d2.toFixed(2)} m)`);
}

// ---------------------------------------------------------------- 10) 姿勢欄位
console.log('[10] explicit pose field');
{
  const POSES = new Set(['desk', 'chair', 'stand']);
  const spots = placesJson.places.flatMap(p => p.spots.map((s, k) => ({ p, s, k })));
  ok(spots.every(({ s }) => POSES.has(s.pose)), `every spot has pose ∈ desk|chair|stand (${spots.length} spots)`);
  ok(spots.every(({ s }) => !s.sit || s.pose !== 'stand') && spots.every(({ s }) => s.sit || s.pose === 'stand'), 'sit spots are desk/chair, others stand');
  ok(placesJson.places.filter(p => p.kind === 'seat').every(p => p.spots[0].pose === 'desk'), 'seat spot 0 is desk');
  ok(P('meeting').spots.every(s => s.pose === 'chair'), 'meeting chairs are chair');
  const w = makeWorld();
  const eng = w.agent('engineer');
  ok(eng.pose === 'desk', 'seated at own desk after seatAll → pose desk');
  w.goto(eng, 'meeting'); sim(w, 60, () => !eng.walking);
  ok(eng.seated && eng.pose === 'chair', 'meeting chair → pose chair');
  w.goto(eng, 'pantry'); sim(w, 60, () => !eng.walking);
  ok(!eng.seated && eng.pose === 'stand', 'pantry → pose stand');
  // 坐姿手臂由 pose 決定，不看 where 字串
  const arm = pose => {
    eng.pose = pose; eng.sitBlend = 1; eng.walkBlend = 0; eng.where = '隨便什麼地方（不含關鍵字）';
    let v = null;
    const orig = eng.setRot;
    eng.setRot = function (k, x, ...rest) { if (k === 'forearm_R') v = x; return orig.call(this, k, x, ...rest); };
    eng.animate();
    eng.setRot = orig;
    return v;
  };
  const desk = arm('desk'), chair = arm('chair');
  ok(Math.abs(desk - 0.95) < 1e-9 && Math.abs(chair - 0.95 * 0.6) < 1e-9, `animate reads pose: forearm desk=${desk} chair=${chair.toFixed(3)}`);
}

console.log(`\nagents_test: ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
