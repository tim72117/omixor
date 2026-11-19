// omixor 虛擬辦公室檢視器：載入場景與角色、打光、命令列、對外 API
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { CSS2DRenderer, CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js';
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js';
import { NavGrid } from './nav.js';
import { createParser } from './command.js';
import { Agent, World, STATE_LABEL } from './agents.js';
import { detectConsole, createBridge } from './bridge.js';

const BASE = new URL('../../', import.meta.url); // office3d/
const DEFAULT_HEX = '#8E8E93';
/** 只接受 #RRGGBB，其他（含任何可能注入 CSS / HTML 的字串）都換成預設色 */
const safeHex = h => (typeof h === 'string' && /^#[0-9a-fA-F]{6}$/.test(h) ? h : DEFAULT_HEX);
/** 建立元素：只用 textContent / DOM API，不拼 HTML */
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = String(text);
  return e;
}
const $ = sel => document.querySelector(sel);
const loadingEl = $('#loading');
const setLoading = t => { if (loadingEl) loadingEl.textContent = t; };

// ---------------------------------------------------------------------- 渲染器 / 場景
const app = $('#app');
const renderer = new THREE.WebGLRenderer({ antialias: true });
renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
renderer.setSize(app.clientWidth, app.clientHeight);
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFSoftShadowMap;
renderer.toneMapping = THREE.ACESFilmicToneMapping;
renderer.toneMappingExposure = 1.0;
app.appendChild(renderer.domElement);

const labelRenderer = new CSS2DRenderer();
labelRenderer.setSize(app.clientWidth, app.clientHeight);
labelRenderer.domElement.className = 'labels';
app.appendChild(labelRenderer.domElement);

const scene = new THREE.Scene();
scene.background = new THREE.Color(0xdfe7ee);
const pmrem = new THREE.PMREMGenerator(renderer);
scene.environment = pmrem.fromScene(new RoomEnvironment(), 0.04).texture;
scene.environmentIntensity = 0.45;

const camera = new THREE.PerspectiveCamera(30, app.clientWidth / app.clientHeight, 0.1, 300);
const controls = new OrbitControls(camera, renderer.domElement);
controls.enableDamping = true;
controls.maxPolarAngle = Math.PI * 0.47;
controls.minDistance = 3;
controls.maxDistance = 90;

const VIEWS = {
  iso: { pos: [-26, 34, 36], target: [0.4, 0.5, -0.6] },
  top: { pos: [0.4, 62, 1.5], target: [0.4, 0, -0.6] },
};
function setView(name) {
  const v = VIEWS[name];
  camera.position.set(...v.pos);
  controls.target.set(...v.target);
  controls.update();
}
setView('iso');

// 燈光：環境光 + 北方窗外的暖色平行光（陰影）
scene.add(new THREE.HemisphereLight(0xf4f1ea, 0x8a8178, 1.1));
const sun = new THREE.DirectionalLight(0xfff1dc, 2.4);
sun.position.set(-8, 22, -16);
sun.target.position.set(0, 0, 0);
sun.castShadow = true;
sun.shadow.mapSize.set(4096, 4096);
Object.assign(sun.shadow.camera, { left: -18, right: 18, top: 14, bottom: -14, near: 1, far: 70 });
sun.shadow.bias = -0.0004;
sun.shadow.normalBias = 0.025;
scene.add(sun, sun.target);
const fill = new THREE.DirectionalLight(0xdfe8ff, 0.5);
fill.position.set(12, 14, 14);
scene.add(fill);

// ---------------------------------------------------------------------- 載入
const loader = new GLTFLoader();
async function getJSON(name) {
  const r = await fetch(new URL(name, BASE));
  if (!r.ok) throw new Error(`${name}: HTTP ${r.status}`);
  return r.json();
}

let world, parser, agents = [];
const officeRoot = new THREE.Group();
scene.add(officeRoot);
const pickables = [];
let selected = null;

function prepMesh(o, cast = true) {
  if (!o.isMesh) return;
  const mats = Array.isArray(o.material) ? o.material : [o.material];
  const transparent = mats.some(m => m.transparent || m.opacity < 1);
  o.castShadow = cast && !transparent;
  o.receiveShadow = true;
}

async function init() {
  setLoading('載入辦公室場景…');
  const [office, chars, navJson, placesJson] = await Promise.all([
    loader.loadAsync(new URL('office.glb', BASE).href),
    loader.loadAsync(new URL('characters.glb', BASE).href),
    getJSON('navgrid.json'),
    getJSON('places.json'),
  ]);
  setLoading('整理場景…');
  office.scene.traverse(o => {
    if (o.userData && o.userData.cutaway) o.visible = false;
    prepMesh(o);
  });
  officeRoot.add(office.scene);

  const nav = new NavGrid(navJson);
  const list = [];
  placesJson.agents.forEach((raw, i) => {
    const info = { ...raw, name: String(raw.name ?? raw.id), hex: safeHex(raw.hex) };
    const root = chars.scene.getObjectByName(info.id);
    if (!root) { console.warn('characters.glb 沒有角色', info.id); return; }
    let a;
    try { a = new Agent(info, root, i); } catch (err) { console.warn('角色模型不完整，略過', info.id, err.message); return; }
    root.traverse(o => { prepMesh(o); if (o.isMesh) { o.userData.agentId = info.id; pickables.push(o); } });
    scene.add(root);
    list.push(a);
    // 名牌：掛在頭上，跟著坐下 / 走路（名稱用 textContent，顏色已驗證）
    const div = el('div', 'nameplate');
    div.style.setProperty('--c', info.hex);
    div.append(el('span', 'dot'), el('span', 'nm', info.name), el('span', 'st'));
    div.addEventListener('pointerdown', e => { e.stopPropagation(); select(a.id); });
    const lbl = new CSS2DObject(div);
    lbl.position.set(0, 0.48, 0);
    a.b.head.o.add(lbl);
    a.label = div;
  });
  agents = list;
  if (!agents.length) throw new Error('characters.glb 裡沒有任何可用的角色');
  const loadedIds = new Set(agents.map(a => a.id));
  const missing = placesJson.agents.filter(info => !loadedIds.has(info.id)).map(info => info.id);
  world = new World({ nav, places: placesJson, agents, emit: (type, detail) => window.dispatchEvent(new CustomEvent(type, { detail })) });
  world.seatAll();
  // 命令解析器只認得真正載入成功的角色
  parser = createParser({ agents: agents.map(a => a.info), places: placesJson.places });
  buildUI(placesJson);
  addOverlays(nav);
  if (missing.length) logLine(`角色模型沒有載入，已略過：${missing.join('、')}`, true);
  loadingEl?.remove();
  startBridge(placesJson);
  window.dispatchEvent(new CustomEvent('office:ready', { detail: { agents: agents.map(a => a.id), places: placesJson.places.map(p => p.id) } }));
}

// ---------------------------------------------------------------------- 疊加：選取圈、路徑線、導航格
const ring = new THREE.Mesh(new THREE.RingGeometry(0.34, 0.44, 40), new THREE.MeshBasicMaterial({ color: 0xffffff, transparent: true, opacity: 0.9, depthWrite: false }));
ring.rotation.x = -Math.PI / 2;
ring.position.y = 0.025;
ring.visible = false;
scene.add(ring);
const pathLines = new Map();
let gridOverlay = null;

function addOverlays(nav) {
  const cnt = nav.blocked.reduce((s, v) => s + v, 0);
  const geo = new THREE.PlaneGeometry(nav.cell * 0.92, nav.cell * 0.92);
  geo.rotateX(-Math.PI / 2);
  const inst = new THREE.InstancedMesh(geo, new THREE.MeshBasicMaterial({ color: 0xff3b30, transparent: true, opacity: 0.35, depthWrite: false }), cnt);
  const m = new THREE.Matrix4();
  let k = 0;
  for (let j = 0; j < nav.h; j++) for (let i = 0; i < nav.w; i++) {
    if (!nav.blocked[j * nav.w + i]) continue;
    const c = nav.center(i, j);
    m.makeTranslation(c.x, 0.03, c.z);
    inst.setMatrixAt(k++, m);
  }
  inst.visible = false;
  inst.renderOrder = 2;
  scene.add(inst);
  gridOverlay = inst;
}

// 每個角色一條路徑線：只有路徑改變（setPath / 重新規劃）時才重寫頂點；平常只更新起點與 drawRange。
// 頂點 buffer 預先配置，容量不夠時才換新的幾何（舊的 dispose）。
const PATH_Y = 0.04;
function makePathGeometry(cap) {
  const g = new THREE.BufferGeometry();
  const pos = new THREE.BufferAttribute(new Float32Array(cap * 3), 3);
  const dist = new THREE.BufferAttribute(new Float32Array(cap), 1);
  pos.setUsage(THREE.DynamicDrawUsage);
  dist.setUsage(THREE.DynamicDrawUsage);
  g.setAttribute('position', pos);
  g.setAttribute('lineDistance', dist);
  g.setDrawRange(0, 0);
  return g;
}

function updatePathLine(a) {
  let line = pathLines.get(a.id);
  if (!a.path || !a.walking) { if (line) { line.visible = false; line.userData.path = null; } return; }
  if (!line) {
    line = new THREE.Line(makePathGeometry(32), new THREE.LineDashedMaterial({ color: a.hex, dashSize: 0.25, gapSize: 0.15, transparent: true, opacity: 0.9 }));
    line.renderOrder = 3;
    line.frustumCulled = false; // 頂點每幀會變，不重算包圍球
    line.userData = { path: null, cum: null, cap: 32 };
    scene.add(line);
    pathLines.set(a.id, line);
  }
  const ud = line.userData;
  const path = a.path;
  if (ud.path !== path) {
    // 路徑改變：重寫全部頂點與累積長度
    if (path.length > ud.cap) {
      const cap = Math.max(path.length, ud.cap * 2);
      line.geometry.dispose();
      line.geometry = makePathGeometry(cap);
      ud.cap = cap;
    }
    const arr = line.geometry.attributes.position.array;
    const cum = new Float64Array(path.length);
    for (let k = 0; k < path.length; k++) {
      arr[k * 3] = path[k].x; arr[k * 3 + 1] = PATH_Y; arr[k * 3 + 2] = path[k].z;
      if (k) cum[k] = cum[k - 1] + Math.hypot(path[k].x - path[k - 1].x, path[k].z - path[k - 1].z);
    }
    ud.path = path;
    ud.cum = cum;
  }
  // 只畫還沒走的部分：第一個累積長度 > s 的頂點 k，起點（目前位置）寫在 k-1 的位置
  const cum = ud.cum;
  let k = 1;
  while (k < path.length - 1 && cum[k] <= a.s) k++;
  const start = k - 1;
  const geo = line.geometry;
  const pos = geo.attributes.position, dist = geo.attributes.lineDistance;
  pos.array[start * 3] = a.pos.x; pos.array[start * 3 + 1] = PATH_Y; pos.array[start * 3 + 2] = a.pos.y;
  // 被覆寫過的前一個頂點（start-1）不會再畫到，不用還原
  let acc = 0;
  dist.array[start] = 0;
  for (let q = start + 1; q < path.length; q++) {
    const px = pos.array[(q - 1) * 3], pz = pos.array[(q - 1) * 3 + 2];
    acc += Math.hypot(path[q].x - px, path[q].z - pz);
    dist.array[q] = acc;
  }
  pos.needsUpdate = true;
  dist.needsUpdate = true;
  geo.setDrawRange(start, path.length - start);
  line.visible = true;
}

// ---------------------------------------------------------------------- 選取 / 點地板
const ray = new THREE.Raycaster();
const ndc = new THREE.Vector2();
let downAt = null;
renderer.domElement.addEventListener('pointerdown', e => { downAt = [e.clientX, e.clientY]; });
renderer.domElement.addEventListener('pointerup', e => {
  if (!downAt || !world) return;
  const moved = Math.hypot(e.clientX - downAt[0], e.clientY - downAt[1]);
  downAt = null;
  if (moved > 5 || e.button !== 0) return;
  const r = renderer.domElement.getBoundingClientRect();
  ndc.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  ray.setFromCamera(ndc, camera);
  const hitA = ray.intersectObjects(pickables, false)[0];
  if (hitA) { select(hitA.object.userData.agentId); return; }
  const hits = ray.intersectObject(officeRoot, true).filter(h => h.object.visible && isVisible(h.object));
  let p = null;
  if (hits.length) p = hits[0].point;
  else {
    const t = -ray.ray.origin.y / ray.ray.direction.y;
    if (t > 0) p = ray.ray.origin.clone().addScaledVector(ray.ray.direction, t);
  }
  if (!p) return;
  if (!selected) { flash('先點一個角色（或名牌）選取，再點地板讓他走過去', true); return; }
  const a = world.agent(selected);
  bridge?.localOverride([a.id]);
  const res = world.goto(a, { x: p.x, z: p.z });
  logLine(res.ok ? `${a.name} 走到 (${p.x.toFixed(1)}, ${p.z.toFixed(1)})` : res.error, !res.ok);
});
function isVisible(o) { for (let x = o; x; x = x.parent) if (!x.visible) return false; return true; }

function select(id) {
  selected = id && world.agent(id) ? id : null;
  for (const a of agents) a.label.classList.toggle('sel', a.id === selected);
  document.querySelectorAll('#status tr[data-id]').forEach(tr => tr.classList.toggle('sel', tr.dataset.id === selected));
  const a = selected && world.agent(selected);
  ring.visible = !!a;
  if (a) ring.material.color.set(a.hex);
  $('#selected').textContent = a ? `已選取：${a.name}（點地板讓他走過去）` : '未選取角色（點角色或名牌選取）';
}

// ---------------------------------------------------------------------- 命令 / API
function runAction(act) {
  try {
    return world.runAction(act);
  } catch (err) {
    console.error(err);
    return (act.agents || []).map(id => ({ agent: id, ok: false, error: `執行失敗：${err.message}` }));
  }
}

function command(text) {
  const p = parser.parse(text, { selected });
  const out = { ok: p.ok, text, actions: [], errors: [...p.errors] };
  for (const act of p.actions) {
    bridge?.localOverride(act.agents);
    const res = runAction(act);
    const bad = res.filter(r => !r.ok);
    bad.forEach(r => out.errors.push(r.error));
    out.actions.push({ ...act, description: parser.describe(act), results: res });
    logLine(`› ${text.length > 40 ? text.slice(0, 40) + '…' : text}　→ ${parser.describe(act)}${bad.length ? `（${bad.length} 個失敗）` : ''}`, bad.length > 0);
  }
  if (out.errors.length) { out.ok = false; out.errors.forEach(e => logLine(e, true)); flash(out.errors[0], true); }
  else flash('');
  window.dispatchEvent(new CustomEvent('office:command', { detail: out }));
  return out;
}

function resolveAgentArg(x) {
  if (!x || !world) return null;
  return world.agent(String(x)) || world.agent(parser.resolveAgent(String(x))) || null;
}

const api = {
  /** 走到地點（places.json 的 id 或別名）、另一個角色，或 {x, z} */
  goto(agentId, target) {
    if (!world) return { ok: false, error: '尚未載入完成' };
    const a = resolveAgentArg(agentId);
    if (!a) return { ok: false, error: `沒有角色 ${agentId}` };
    bridge?.localOverride([a.id]);
    if (target && typeof target === 'object') return world.goto(a, { x: +target.x, z: +target.z });
    if (world.places.has(target)) return world.goto(a, target);
    const tg = parser.resolveTarget(String(target));
    if (!tg) return { ok: false, error: `找不到地點或角色 ${target}` };
    if (tg.kind === 'agent') return api.find(a.id, tg.id);
    return world.goto(a, tg.id);
  },
  find(agentId, targetAgentId) {
    if (!world) return { ok: false, error: '尚未載入完成' };
    const a = resolveAgentArg(agentId), t = resolveAgentArg(targetAgentId);
    if (a && t && a === t) return { ok: false, error: '不能找自己' };
    if (!a || !t) return { ok: false, error: `沒有角色 ${!a ? agentId : targetAgentId}` };
    bridge?.localOverride([a.id]);
    return world.find(a, t);
  },
  returnToSeat(agentId) {
    if (!world) return [{ ok: false, error: '尚未載入完成' }];
    const ids = agentId ? [agentId] : agents.map(a => a.id);
    return ids.map(id => { const a = resolveAgentArg(id); if (a) bridge?.localOverride([a.id]); return a ? world.returnToSeat(a) : { ok: false, error: `沒有角色 ${id}` }; });
  },
  stop(agentId) {
    if (!world) return { ok: false, error: '尚未載入完成' };
    const a = resolveAgentArg(agentId);
    if (a) bridge?.localOverride([a.id]);
    return a ? world.stop(a) : { ok: false, error: `沒有角色 ${agentId}` };
  },
  command: text => (world ? command(String(text)) : { ok: false, errors: ['尚未載入完成'] }),
  state() {
    if (!world) return [];
    return agents.map(a => ({
      id: a.id, name: a.name, color: a.hex, state: a.walking ? 'walking' : a.state,
      stateLabel: STATE_LABEL[a.walking ? 'walking' : a.state], seated: a.seated,
      position: [+a.pos.x.toFixed(3), 0, +a.pos.y.toFixed(3)], yaw: +a.yaw.toFixed(3),
      task: a.task ? (a.task.type === 'find' ? { type: 'find', target: a.task.targetId } : { type: 'goto', place: a.task.placeId, x: a.task.x, z: a.task.z }) : null,
      where: a.where || null,
    }));
  },
  places: () => (world ? [...world.places.values()].map(p => ({ id: p.id, name: p.name, kind: p.kind, aliases: p.aliases, position: p.position })) : []),
  agents: () => agents.map(a => ({ id: a.id, name: a.name, color: a.hex })),
  select: id => select(id),
  setView,
  /** console 連線狀態：{ mode:'console'|'standalone'|'detecting', status, primary, viewerId } */
  connection: () => ({ mode: connMode, ...(bridge ? bridge.info() : { status: connMode === 'standalone' ? 'standalone' : 'connecting', primary: false, viewerId: null }) }),
  on(type, fn) { const t = type.startsWith('office:') ? type : `office:${type}`; const h = e => fn(e.detail); window.addEventListener(t, h); return () => window.removeEventListener(t, h); },
};
window.office = api;

// ---------------------------------------------------------------------- console 連線（見 bridge.js）
// 由 console 提供（同源有 /api/office/state）時：連 SSE 收命令、執行、回報；否則維持獨立模式。
let bridge = null;
let connMode = 'detecting';
function setConn({ status, primary }) {
  const b = $('#conn');
  if (!b) return;
  const text = {
    standalone: '獨立模式',
    connecting: 'console：連線中…',
    reconnecting: 'console：重連中…',
    connected: primary ? 'console：已連線（主檢視器）' : 'console：已連線（副檢視器，只顯示不回報）',
  }[status] || status;
  b.textContent = text;
  b.className = `conn c-${status}${status === 'connected' && !primary ? ' secondary' : ''}`;
  b.title = status === 'standalone' ? '沒有連到 omixor console（用 console 的 /office/ 開才會接收後端命令）' : '';
}
async function startBridge(placesJson) {
  if (!(await detectConsole({ location: window.location, fetch: (...a) => fetch(...a) }))) {
    connMode = 'standalone';
    setConn({ status: 'standalone', primary: false });
    return;
  }
  connMode = 'console';
  const seats = Object.fromEntries(placesJson.agents.map(a => [a.id, a.seat]));
  let lastStatus = '';
  bridge = createBridge({
    EventSource: window.EventSource,
    fetch: (...a) => fetch(...a),
    runAction: act => runAction(act),
    state: () => api.state(),
    seats,
    onStatus: st => {
      setConn(st);
      if (st.status !== lastStatus) {
        if (st.status === 'connected') logLine('已連上 omixor console，等待後端命令');
        else if (st.status === 'reconnecting' && lastStatus === 'connected') logLine('和 console 的連線中斷，重連中…', true);
        lastStatus = st.status;
      }
    },
    onCommand: (cmd, results) => {
      const bad = results.filter(r => !r.ok);
      logLine(`⇠ console：${cmd.description || cmd.action.type}${bad.length ? `（${bad.length} 個失敗）` : ''}`, bad.length > 0);
      bad.forEach(r => logLine(r.error, true));
    },
  });
  window.addEventListener('office:arrived', e => bridge.arrived(e.detail));
  window.addEventListener('office:state', () => bridge.stateChanged());
  // 背景分頁不會跑 requestAnimationFrame：改用計時器繼續模擬，角色照樣走到、照樣回報抵達
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      if (bgTimer) return;
      let last = performance.now();
      bgTimer = setInterval(() => { const t = performance.now(); simulate((t - last) / 1000); last = t; }, 250);
    } else if (bgTimer) {
      clearInterval(bgTimer);
      bgTimer = null;
      clock.getDelta(); // 背景期間已經模擬過，不要再補
    }
  });
  bridge.start();
}
let bgTimer = null;

// ---------------------------------------------------------------------- UI
function logLine(text, isErr = false) {
  const ul = $('#log');
  const li = document.createElement('li');
  const t = new Date();
  const two = n => String(n).padStart(2, '0');
  li.append(el('time', null, `${two(t.getHours())}:${two(t.getMinutes())}:${two(t.getSeconds())}`), document.createTextNode(String(text)));
  if (isErr) li.className = 'err';
  ul.prepend(li);
  while (ul.children.length > 80) ul.lastChild.remove();
}
function flash(text, isErr = false) {
  const el = $('#msg');
  el.textContent = text;
  el.className = isErr ? 'err' : '';
}

function buildUI(placesJson) {
  const form = $('#cmd-form');
  const input = $('#cmd');
  const hist = [];
  let hi = -1;
  form.addEventListener('submit', e => {
    e.preventDefault();
    const v = input.value.trim();
    if (!v) return;
    command(v);
    hist.unshift(v); hi = -1;
    input.value = '';
  });
  input.addEventListener('keydown', e => {
    if (e.key === 'ArrowUp' && hist.length) { hi = Math.min(hist.length - 1, hi + 1); input.value = hist[hi]; e.preventDefault(); }
    if (e.key === 'ArrowDown') { hi = Math.max(-1, hi - 1); input.value = hi >= 0 ? hist[hi] : ''; e.preventDefault(); }
  });
  $('#btn-return').addEventListener('click', () => command('全部 回座位'));
  $('#btn-iso').addEventListener('click', () => setView('iso'));
  $('#btn-top').addEventListener('click', () => setView('top'));
  $('#btn-grid').addEventListener('click', e => { gridOverlay.visible = !gridOverlay.visible; e.target.classList.toggle('on', gridOverlay.visible); });
  // 地點快捷
  const dl = $('#place-list');
  for (const p of placesJson.places) {
    const o = document.createElement('option');
    o.value = p.name;
    dl.appendChild(o);
  }
  const chips = $('#examples');
  for (const ex of ['工程師 去 會議室', 'ceo 找 旁白', '行銷 去 ceo 的座位', '產品經理、課程開發 去 茶水間', 'all go lounge', '全部 回座位']) {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = ex;
    b.addEventListener('click', () => command(ex));
    chips.appendChild(b);
  }
  const tb = $('#status tbody');
  for (const a of agents) {
    const tr = document.createElement('tr');
    tr.dataset.id = a.id;
    const nameTd = el('td');
    const dot = el('span', 'dot');
    dot.style.background = safeHex(a.hex);
    nameTd.append(dot, document.createTextNode(a.name));
    tr.append(nameTd, el('td', 'st'), el('td', 'wh'));
    tr.addEventListener('click', () => select(selected === a.id ? null : a.id));
    tb.appendChild(tr);
    a.row = tr;
  }
  select(null);
  logLine('就緒：輸入「工程師 去 會議室」或點角色後點地板');
}

let uiT = 0;
function refreshStatus() {
  for (const a of agents) {
    const st = a.walking ? (a.state === 'waiting' ? 'waiting' : 'walking') : a.state;
    const label = STATE_LABEL[st];
    let where;
    if (a.walking) where = `→ ${a.task ? a.task.label : ''}`;
    else where = (a.seated ? '坐在 ' : '') + (a.where || '');
    const stCell = a.row.querySelector('.st');
    if (stCell.textContent !== label) { stCell.textContent = label; stCell.className = `st s-${st}`; }
    a.row.querySelector('.wh').textContent = where;
    const ls = a.label.querySelector('.st');
    ls.className = `st s-${st}`;
  }
}

// ---------------------------------------------------------------------- 主迴圈
// 固定步長：累積實際經過時間，每 SIM_STEP 秒跑一次 world.update；低 FPS 時一幀補跑多步，
// 模擬速度跟真實時間一致。一幀最多補 MAX_STEPS 步（= 1 秒），更多的（例如分頁切回來）直接丟掉，避免暴衝。
const SIM_STEP = 1 / 60;
const MAX_STEPS = 60;
const clock = new THREE.Clock();
let simAcc = 0;
function simulate(real) {
  simAcc += real;
  let n = 0;
  while (simAcc >= SIM_STEP && n < MAX_STEPS) { world.update(SIM_STEP); simAcc -= SIM_STEP; n++; }
  if (simAcc >= SIM_STEP) simAcc = 0; // 超過上限的時間丟掉
}
function frame() {
  const real = clock.getDelta();
  if (world) {
    if (!bgTimer) simulate(real);
    for (const a of agents) updatePathLine(a);
    const s = selected && world.agent(selected);
    if (s) ring.position.set(s.pos.x, 0.025, s.pos.y);
    uiT += real;
    if (uiT > 0.15) { uiT = 0; refreshStatus(); }
  }
  controls.update();
  renderer.render(scene, camera);
  labelRenderer.render(scene, camera);
  requestAnimationFrame(frame);
}
frame();

window.addEventListener('resize', () => {
  camera.aspect = app.clientWidth / app.clientHeight;
  camera.updateProjectionMatrix();
  renderer.setSize(app.clientWidth, app.clientHeight);
  labelRenderer.setSize(app.clientWidth, app.clientHeight);
});

init().catch(err => {
  console.error(err);
  setLoading(`載入失敗：${err.message}。請在 office3d/ 目錄下用靜態伺服器開啟（見 viewer/README.md）`);
  loadingEl?.classList.add('err');
});
