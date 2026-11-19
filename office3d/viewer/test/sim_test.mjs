// 無頭模擬：用假的 three.js 跑角色移動邏輯，確認命令都能走到、角色不會疊在一起。
//   node viewer/test/sim_test.mjs
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

function fakeRoot(id) {
  const nodes = {};
  return {
    userData: { height: 1.7 }, position: new Vector3(), rotation: new Vector3(),
    getObjectByName(n) { return nodes[n] ||= { name: n, quaternion: new Quaternion(), position: new Vector3(0, 0.85, 0), add() {} }; },
  };
}
const events = [];
const agents = placesJson.agents.map((info, i) => new Agent(info, fakeRoot(info.id), i));
// 固定種子：結果可重現（SEED 環境變數可換種子）
const SEED = Number(process.env.SEED) || 20261118;
const world = new World({ nav, places: placesJson, agents, emit: (t, d) => events.push([t, d]), random: mulberry32(SEED) });
world.seatAll();
const P = createParser({ agents: placesJson.agents, places: placesJson.places });

let fail = 0;
const check = (c, m) => { if (!c) { fail++; console.log('  FAIL', m); } else console.log('  ok  ', m); };

function run(cmd) {
  const p = P.parse(cmd);
  if (!p.ok) throw new Error(p.errors.join());
  for (const act of p.actions) for (const r of world.runAction(act)) {
    if (!r.ok) console.log('  cmd error', cmd, r.agent, r.error);
  }
}
function simulate(maxT, until) {
  let t = 0, minD = Infinity, minPair = '';
  const dt = 1 / 60;
  while (t < maxT) {
    world.update(dt);
    t += dt;
    for (let i = 0; i < agents.length; i++) for (let j = i + 1; j < agents.length; j++) {
      const a = agents[i], b = agents[j];
      if (!a.walking && !b.walking) continue;
      const d = Math.hypot(a.pos.x - b.pos.x, a.pos.y - b.pos.y);
      if (d < minD) { minD = d; minPair = `${a.id}/${b.id}`; }
    }
    for (const a of agents) if (!nav.isFreeAt(a.pos.x, a.pos.y) && a.walking) {
      const c = nav.cellOf(a.pos.x, a.pos.y);
      a._inWall = (a._inWall || 0) + 1;
      if (a._inWall === 1) a._wallAt = c;
    }
    if (until()) break;
  }
  return { t, minD, minPair };
}
const allStill = () => agents.every(a => !a.walking);
const place = id => world.places.get(id);

// 1) 工程師去會議室
run('工程師 去 會議室');
let r = simulate(60, allStill);
let eng = world.agent('engineer');
check(eng.state === 'arrived' && eng.seated, `engineer arrived & seated at meeting in ${r.t.toFixed(1)}s (where=${eng.where})`);

// 2) ceo 找 旁白（旁白同時走去茶水間 → 目標移動）
run('旁白 去 茶水間');
run('ceo 找 旁白');
r = simulate(90, allStill);
const ceo = world.agent('ceo'), nar = world.agent('narrator');
const dcn = Math.hypot(ceo.pos.x - nar.pos.x, ceo.pos.y - nar.pos.y);
check(ceo.state === 'arrived' && dcn < 1.3, `ceo found moving narrator: dist=${dcn.toFixed(2)}m t=${r.t.toFixed(1)}s narrator at ${nar.where}`);

// 3) 行銷去 ceo 的座位（ceo 不在座位）→ 坐下
run('行銷 去 ceo 的座位');
r = simulate(90, allStill);
const mk = world.agent('marketing');
const seat = place('seat_ceo').spots[0].position;
check(mk.seated && Math.hypot(mk.pos.x - seat[0], mk.pos.y - seat[2]) < 0.05, `marketing sat in ceo seat (t=${r.t.toFixed(1)}s)`);

// 4) 全部去休息區（8 人搶 6 個站位）→ 最小距離
run('全部 去 休息區');
r = simulate(120, allStill);
check(agents.every(a => a.state === 'arrived'), `all arrived at lounge in ${r.t.toFixed(1)}s, min distance while walking=${r.minD.toFixed(2)}m (${r.minPair})`);
let minRest = Infinity;
for (let i = 0; i < agents.length; i++) for (let j = i + 1; j < agents.length; j++) minRest = Math.min(minRest, Math.hypot(agents[i].pos.x - agents[j].pos.x, agents[i].pos.y - agents[j].pos.y));
check(minRest > 0.3, `lounge final min spacing ${minRest.toFixed(2)}m`);

// 5) 全部回座位：每個人回到自己的座位並坐下
run('all return');
r = simulate(150, allStill);
const back = agents.filter(a => { const s = place(a.info.seat).spots[0].position; return a.seated && Math.hypot(a.pos.x - s[0], a.pos.y - s[2]) < 0.05; });
check(back.length === agents.length, `all back in own seats: ${back.length}/${agents.length} in ${r.t.toFixed(1)}s`);

// 6) 每個角色走到每個共用地點（逐一，避免互擋造成的不確定）
const shared = placesJson.places.filter(p => p.kind === 'shared').map(p => p.id);
let tours = 0, tourFail = 0, tourMin = { d: Infinity };
function minSpacing() {
  let d = Infinity, pair = '';
  for (let i = 0; i < agents.length; i++) for (let j = i + 1; j < agents.length; j++) {
    const x = Math.hypot(agents[i].pos.x - agents[j].pos.x, agents[i].pos.y - agents[j].pos.y);
    if (x < d) { d = x; pair = `${agents[i].id}/${agents[j].id}`; }
  }
  return { d, pair };
}
for (const pid of shared) {
  run(`all go ${pid}`);
  r = simulate(150, allStill);
  for (const a of agents) { tours++; if (a.state !== 'arrived') { tourFail++; console.log('   not arrived', a.id, pid); } }
  const sp = minSpacing();
  if (sp.d < tourMin.d) tourMin = { ...sp, pid };
}
check(tourFail === 0, `all 8 agents reached each of ${shared.length} shared places (${tours} trips)`);
check(tourMin.d > 0.45, `no two agents overlap after arriving at any shared place: min spacing ${tourMin.d.toFixed(2)}m (${tourMin.pair} @ ${tourMin.pid})`);
run('all return'); simulate(150, allStill);

// 坐著的人都在不同的座位上（不會兩個人坐同一個位子）
{
  const seatedPos = agents.filter(a => a.seated).map(a => `${a.pos.x.toFixed(2)},${a.pos.y.toFixed(2)}`);
  check(new Set(seatedPos).size === seatedPos.length, `no two seated agents share a seat (${seatedPos.length} seated)`);
}
const walls = agents.filter(a => a._inWall).map(a => `${a.id}x${a._inWall}@${a._wallAt}`);
check(walls.length === 0, `no frame inside blocked cell while walking ${walls.join(' ')}`);
const arrivedEvents = events.filter(e => e[0] === 'office:arrived').length;
check(arrivedEvents > 50, `office:arrived events emitted: ${arrivedEvents}`);
console.log(fail ? `\n${fail} failed` : '\nall simulation checks passed');
process.exit(fail ? 1 : 0);
