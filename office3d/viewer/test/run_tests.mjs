// Node 單元測試（不需安裝任何套件）：node viewer/test/run_tests.mjs
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { NavGrid, pointAlong } from '../js/nav.js';
import { createParser, normalize } from '../js/command.js';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
const navJson = JSON.parse(readFileSync(join(root, 'navgrid.json'), 'utf8'));
const placesJson = JSON.parse(readFileSync(join(root, 'places.json'), 'utf8'));
let pass = 0, fail = 0;
const ok = (cond, msg) => { if (cond) pass++; else { fail++; console.log('  FAIL', msg); } };

// ---------------- 導航格 ----------------
const nav = new NavGrid(navJson);
console.log(`navgrid ${nav.w}x${nav.h} cell=${nav.cell}m origin=(${nav.ox},${nav.oz})`);
const comp = nav.components();
const sizes = new Map();
for (const l of comp.labels) if (l >= 0) sizes.set(l, (sizes.get(l) || 0) + 1);
const main = [...sizes.entries()].sort((a, b) => b[1] - a[1])[0];
console.log(`components=${comp.count} main=${main[1]} cells, free total=${[...sizes.values()].reduce((a, b) => a + b, 0)}`);

const spots = [];
for (const p of placesJson.places) p.spots.forEach((s, k) => spots.push({ place: p.id, k, x: s.position[0], z: s.position[2], sit: !!s.sit }));
let unreachable = 0;
const table = [];
for (const p of placesJson.places) {
  let good = 0;
  p.spots.forEach(s => {
    const c = nav.nearestFree(s.position[0], s.position[2]);
    const lab = c ? comp.labels[c[1] * nav.w + c[0]] : -1;
    if (lab === main[0]) good++; else unreachable++;
  });
  table.push(`${p.id.padEnd(24)} ${String(good).padStart(2)}/${p.spots.length} spots in main component`);
}
console.log(table.join('\n'));
ok(unreachable === 0, `all spots in main component (unreachable=${unreachable})`);

// 所有座位 / 地點兩兩互通（實際跑 A*）
const keyPts = placesJson.places.map(p => ({ id: p.id, x: p.position[0], z: p.position[2] }));
let pairs = 0, pairFail = 0, maxLen = 0, t0 = Date.now();
for (let a = 0; a < keyPts.length; a++) for (let b = 0; b < keyPts.length; b++) {
  if (a === b) continue;
  const r = nav.findPath(keyPts[a].x, keyPts[a].z, keyPts[b].x, keyPts[b].z);
  pairs++;
  if (!r.ok) { pairFail++; console.log('  no path', keyPts[a].id, '->', keyPts[b].id, r.reason); }
  else {
    maxLen = Math.max(maxLen, r.length);
    const end = r.points[r.points.length - 1];
    if (Math.hypot(end.x - keyPts[b].x, end.z - keyPts[b].z) > 0.3) { pairFail++; console.log('  end mismatch', keyPts[a].id, keyPts[b].id); }
    // 平滑後的路徑每段都要在可走格上（起點可能在障礙格內，跳過第一段）
    for (let k = 2; k < r.points.length; k++) {
      const p = r.points[k - 1], q = r.points[k];
      if (!nav.lineFree(p.x, p.z, q.x, q.z, 0)) { pairFail++; console.log('  segment crosses obstacle', keyPts[a].id, keyPts[b].id, k); break; }
    }
  }
}
const dt = Date.now() - t0;
console.log(`A* place→place: ${pairs} pairs, failures=${pairFail}, longest=${maxLen.toFixed(1)}m, avg ${(dt / pairs).toFixed(2)}ms/path`);
ok(pairFail === 0, 'all place pairs connected by A*');
// spot 對 spot：每個 spot 到 seat_ceo
let spotFail = 0;
for (const s of spots) {
  const r = nav.findPath(s.x, s.z, keyPts[0].x, keyPts[0].z);
  if (!r.ok) { spotFail++; console.log('  spot unreachable', s.place, s.k); }
}
console.log(`spots→seat_ceo: ${spots.length} spots, failures=${spotFail}`);
ok(spotFail === 0, 'all spots reach seat_ceo');
// 障礙已依身體寬度膨脹：每個站位本身（含座位、椅子、高腳凳）都在可走格上，並且能走到
{
  const notFree = spots.filter(s => !nav.isFreeAt(s.x, s.z));
  ok(notFree.length === 0, `every spot position is on a free cell after inflation (${notFree.map(s => `${s.place}#${s.k}`).join(',')})`);
  const st = navJson.stats || {};
  console.log(`inflate ${navJson.params?.inflate_m} m: free ${st.free_before_inflate} → ${st.free} (doorways restored ${JSON.stringify(st.door_cells_restored)}, carved ${st.spot_cells_carved})`);
  ok(st.free < st.free_before_inflate && navJson.params?.inflate_cells >= 1, 'navgrid is inflated');
  // 門口：門洞中心那一格可走，且門兩側互通
  const doors = { meeting: [-8.2, -3.5, -8.2, -2.6, -8.2, -4.4], ceo: [8.5, -4.4, 7.7, -4.4, 9.3, -4.4], narrator_booth: [10.0, 2.03, 9.2, 2.03, 10.8, 2.03] };
  for (const [id, [x, z, ax, az, bx, bz]] of Object.entries(doors)) {
    const r = nav.findPath(ax, az, bx, bz);
    ok(nav.isFreeAt(x, z) && r.ok && r.length < 3, `doorway ${id} passable (path ${r.length.toFixed(2)} m)`);
  }
}
// 牆內一定不可走、障礙格不在路徑上
ok(!nav.isFreeAt(-20, 0), 'outside floor is blocked');
const pa = pointAlong([{ x: 0, z: 0 }, { x: 1, z: 0 }, { x: 1, z: 1 }], 1.5);
ok(Math.abs(pa.x - 1) < 1e-9 && Math.abs(pa.z - 0.5) < 1e-9, 'pointAlong');

// ---------------- 命令解析 ----------------
const P = createParser({ agents: placesJson.agents, places: placesJson.places });
const cases = [
  ['工程師 去 會議室', { type: 'goto', agents: ['engineer'], target: 'meeting' }],
  ['engineer go meeting', { type: 'goto', agents: ['engineer'], target: 'meeting' }],
  ['engineer go to the meeting room', { type: 'goto', agents: ['engineer'], target: 'meeting' }],
  ['工程師去會議室', { type: 'goto', agents: ['engineer'], target: 'meeting' }],
  ['ceo 找 旁白', { type: 'find', agents: ['ceo'], target: 'narrator' }],
  ['ceo find narrator', { type: 'find', agents: ['ceo'], target: 'narrator' }],
  ['CEO 去找 旁白製作', { type: 'find', agents: ['ceo'], target: 'narrator' }],
  ['全部 回座位', { type: 'return', agents: placesJson.agents.map(a => a.id) }],
  ['all return', { type: 'return', agents: placesJson.agents.map(a => a.id) }],
  ['行銷 去 ceo 的座位', { type: 'goto', agents: ['marketing'], target: 'seat_ceo' }],
  ['marketing go ceo seat', { type: 'goto', agents: ['marketing'], target: 'seat_ceo' }],
  ["marketing go to ceo's seat", { type: 'goto', agents: ['marketing'], target: 'seat_ceo' }],
  ['行銷 去 CEO 辦公室', { type: 'goto', agents: ['marketing'], target: 'zone_ceo' }],
  ['旁白 回 錄音間', { type: 'goto', agents: ['narrator'], target: 'zone_narrator' }],
  ['訪客分析師 去 茶水間', { type: 'goto', agents: ['visitor-analyst'], target: 'pantry' }],
  ['visitor-analyst go lounge', { type: 'goto', agents: ['visitor-analyst'], target: 'lounge' }],
  ['visitor analyst go pantry', { type: 'goto', agents: ['visitor-analyst'], target: 'pantry' }],
  ['Issue 整理員 去 入口櫃台', { type: 'goto', agents: ['triager'], target: 'reception' }],
  ['triager go reception', { type: 'goto', agents: ['triager'], target: 'reception' }],
  ['產品經理、工程師 去 會議室', { type: 'goto', agents: ['product-manager', 'engineer'], target: 'meeting' }],
  ['pm and engineer go meeting', { type: 'goto', agents: ['product-manager', 'engineer'], target: 'meeting' }],
  ['課程開發 去 西側公共角落', { type: 'goto', agents: ['course-dev'], target: 'commons' }],
  ['課程開發 去 工程師 的工作區', { type: 'goto', agents: ['course-dev'], target: 'zone_engineer' }],
  ['行銷 去 ceo', { type: 'find', agents: ['marketing'], target: 'ceo' }],
  ['工程師 停', { type: 'stop', agents: ['engineer'] }],
  ['大家 去 休息區', { type: 'goto', agents: placesJson.agents.map(a => a.id), target: 'lounge' }],
  ['all go entrance', { type: 'goto', agents: placesJson.agents.map(a => a.id), target: 'entrance' }],
  ['工程師 到 窗邊', { type: 'goto', agents: ['engineer'], target: 'window' }],
];
for (const [text, exp] of cases) {
  const r = P.parse(text);
  const a = r.actions[0];
  const good = r.ok && a && a.type === exp.type && JSON.stringify(a.agents) === JSON.stringify(exp.agents) &&
    (exp.target === undefined || a.target?.id === exp.target);
  ok(good, `parse "${text}" → ${JSON.stringify(r)}`);
}
// 用選取角色
{
  const r = P.parse('去 會議室', { selected: 'engineer' });
  ok(r.ok && r.actions[0].agents[0] === 'engineer' && r.actions[0].target.id === 'meeting', 'selected agent default');
}
// 多條命令
{
  const r = P.parse('工程師 去 會議室；ceo 找 工程師');
  ok(r.ok && r.actions.length === 2 && r.actions[1].type === 'find', 'multi command');
}
// 錯誤
for (const bad of ['工程師 去 火星', 'xyz go meeting', '', '去 會議室', '工程師', 'ceo 找 ceo']) {
  const r = P.parse(bad);
  ok(!r.ok && r.errors.length > 0, `error expected for "${bad}" → ${JSON.stringify(r)}`);
  if (!r.ok) console.log(`  error ok: "${bad}" → ${r.errors[0].slice(0, 70)}`);
}
ok(normalize('  Visitor-Analyst ') === 'visitoranalyst', 'normalize');

// ---------------- 角色邏輯（agents.js，假的 three.js；另開行程以便註冊 three 替身）----------------
{
  const r = spawnSync(process.execPath, [join(here, 'agents_test.mjs')], { encoding: 'utf8' });
  process.stdout.write(r.stdout.split('\n').filter(l => l.includes('FAIL') || l.startsWith('agents_test') || l.startsWith('[')).map(l => '  ' + l).join('\n') + '\n');
  if (r.stderr) process.stdout.write(r.stderr);
  ok(r.status === 0, 'agents_test.mjs (random seed, stop, blocked goal, missing agents, find line of sight, pose)');
}

// ---------------- console 連線（bridge.js，假的 EventSource / fetch）----------------
{
  const r = spawnSync(process.execPath, [join(here, 'bridge_test.mjs')], { encoding: 'utf8' });
  process.stdout.write(r.stdout.split('\n').filter(l => l.includes('FAIL') || l.startsWith('bridge_test')).map(l => '  ' + l).join('\n') + '\n');
  if (r.stderr) process.stdout.write(r.stderr);
  ok(r.status === 0, 'bridge_test.mjs (console SSE: snapshot, ack, arrived, superseded, secondary viewer, reconnect, detect)');
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
