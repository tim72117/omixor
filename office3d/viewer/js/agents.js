// 角色控制：沿路徑移動、走路/坐下/閒置動畫、避讓、找人
import * as THREE from 'three';
import { pointAlong, polyLength } from './nav.js';

const WALK_SPEED = 1.35;   // m/s
const ACCEL = 3.0;         // m/s²
const STRIDE = 1.25;       // 一個完整步態週期走的距離（m）
const TURN_RATE = 7.0;     // 轉向平滑係數
const BODY_R = 0.28;       // 兩個角色中心最近距離的一半
const REPLAN_EVERY = 0.45; // 找人時多久檢查一次目標是否移動（秒）
const SIT_HIP_Y = 0.56;    // 坐下時髖關節高度（椅墊約 0.52 m）
const END_ZONE = 0.45;     // 離終點這麼近時，前方有人就停下等待（不再側閃）
const END_WAIT = 1.2;      // 終點被擋時最多等多久（秒），之後改選別的站位 / 就地停下
const SIT_SNAP = 0.05;     // 離座位這麼近才算真的走到，可以坐下
const NO_SIDE_ZONE = 0.9;  // 剩餘距離小於這個值時不再側移（讓角色回到路徑上、準確走到終點）
const OCCUPIED_R = 0.5;    // 站位附近這個距離內有人 = 被佔用
const MAX_GOAL_RETRIES = 3;

/** 簡單可重現的亂數產生器（測試用固定種子） */
export function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a = (a + 0x6D2B79F5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const BONES = ['hip', 'torso', 'head', 'upperarm_L', 'forearm_L', 'upperarm_R', 'forearm_R', 'thigh_L', 'shin_L', 'thigh_R', 'shin_R'];
const _q = new THREE.Quaternion();
const _e = new THREE.Euler();

export const STATE_LABEL = { idle: '閒置', walking: '走路中', arrived: '抵達', waiting: '等待讓路' };

function angDiff(a, b) {
  let d = b - a;
  while (d > Math.PI) d -= 2 * Math.PI;
  while (d < -Math.PI) d += 2 * Math.PI;
  return d;
}
/** 點 (px,pz) 到線段 a→b 的距離 */
export function segDist(px, pz, ax, az, bx, bz) {
  const dx = bx - ax, dz = bz - az;
  const L2 = dx * dx + dz * dz;
  const t = L2 > 1e-12 ? Math.max(0, Math.min(1, ((px - ax) * dx + (pz - az) * dz) / L2)) : 0;
  return Math.hypot(px - (ax + dx * t), pz - (az + dz * t));
}
export function yawTo(dx, dz) { return Math.atan2(-dx, -dz); }

export class Agent {
  constructor(info, root, index) {
    this.id = info.id;
    this.name = info.name;
    this.hex = info.hex;
    this.info = info;
    this.index = index;
    this.root = root;
    const ud = root.userData || {};
    this.height = ud.height || 1.75;
    this.holdL = !!ud.hold_L;
    this.holdR = !!ud.hold_R;
    this.b = {};
    for (const k of BONES) {
      const o = root.getObjectByName(`${this.id}_${k}`);
      if (!o) throw new Error(`characters.glb 缺少節點 ${this.id}_${k}`);
      this.b[k] = { o, q: o.quaternion.clone(), p: o.position.clone() };
    }
    root.position.set(0, 0, 0);
    root.rotation.set(0, 0, 0);
    this.pos = new THREE.Vector2(0, 0); // (x, z)
    this.yaw = 0;
    this.targetYaw = 0;
    this.speed = 0;
    this.state = 'idle';
    this.seated = false;
    this.sitBlend = 0;
    this.walkBlend = 0;
    this.phase = 0;
    this.t = 0;             // 動畫時間；初始相位由 World 的亂數設定（可注入種子）
    this.pose = 'stand';    // 'desk' | 'chair' | 'stand'：坐下時的姿勢（來自 places.json 站位的 pose）
    this.endWaitT = 0;      // 終點被擋已等待的時間
    this.path = null;       // [{x,z}]
    this.pathLen = 0;
    this.s = 0;
    this.task = null;       // {type:'goto'|'find', ...}
    this.waitT = 0;
    this.side = 0;          // 避讓用的側移量（公尺）
    this.where = '';        // 目前所在地點（顯示用）
    this.reservation = null;
  }

  place(x, z, yaw, seated) {
    this.pos.set(x, z);
    this.yaw = this.targetYaw = yaw;
    this.seated = !!seated;
    this.sitBlend = seated ? 1 : 0;
    this.applyRoot();
  }

  applyRoot() {
    this.root.position.set(this.pos.x, 0, this.pos.y);
    this.root.rotation.set(0, this.yaw, 0);
  }

  get walking() { return this.state === 'walking' || this.state === 'waiting'; }

  setPath(points) {
    this.path = points;
    this.pathLen = polyLength(points);
    this.s = 0;
    this.waitT = 0;
    this.endWaitT = 0;
    this.state = 'walking';
    this.seated = false;
  }

  /** 只清掉路徑與任務；要一併釋放站位保留請用 World.stop(agent) */
  stop() {
    this.path = null;
    this.task = null;
    this.side = 0;
    this.endWaitT = 0;
    this.state = this.seated ? 'arrived' : 'idle';
  }

  // ------------------------------------------------------------------ 每格更新
  update(dt, world) {
    this.t += dt;
    if (this.walking && this.path) this.updateMove(dt, world);
    else this.speed = Math.max(0, this.speed - ACCEL * 2 * dt);

    if (!this.walking) {
      // 找人抵達後持續面向對方
      if (this.task && this.task.type === 'find' && this.state === 'arrived') {
        const tg = world.agent(this.task.targetId);
        if (tg) this.targetYaw = yawTo(tg.pos.x - this.pos.x, tg.pos.y - this.pos.y);
      }
    }
    const d = angDiff(this.yaw, this.targetYaw);
    this.yaw += d * Math.min(1, dt * TURN_RATE);
    this.sitBlend += ((this.seated ? 1 : 0) - this.sitBlend) * Math.min(1, dt * 5);
    const moving = this.speed > 0.05 ? 1 : 0;
    this.walkBlend += (moving - this.walkBlend) * Math.min(1, dt * 8);
    this.phase += dt * this.speed * (2 * Math.PI / STRIDE);
    this.applyRoot();
    this.animate();
  }

  updateMove(dt, world) {
    const task = this.task;
    // 找人：目標移動就重新規劃
    if (task && task.type === 'find') {
      task.replanT = (task.replanT || 0) + dt;
      const tg = world.agent(task.targetId);
      if (tg && task.replanT > REPLAN_EVERY) {
        task.replanT = 0;
        const moved = Math.hypot(tg.pos.x - task.lastX, tg.pos.y - task.lastZ);
        if (moved > 0.4) world.planFind(this, tg, true);
        if (!this.path) return;
      }
      if (tg && Math.hypot(tg.pos.x - this.pos.x, tg.pos.y - this.pos.y) < 0.85 && this.s > 0.3 &&
          world.sightFree(tg.pos.x, tg.pos.y, this.pos.x, this.pos.y)) {
        this.arrive(world);
        return;
      }
    }
    // 避讓：前方近距離有別人
    let desired = WALK_SPEED;
    const ahead = pointAlong(this.path, this.s + 0.3);
    let fx = ahead.x - this.pos.x, fz = ahead.z - this.pos.y;
    const fl = Math.hypot(fx, fz) || 1;
    fx /= fl; fz /= fl;
    let blocker = null, bd = Infinity;
    for (const o of world.agents) {
      if (o === this) continue;
      if (task && task.type === 'find' && o.id === task.targetId) continue;
      const dx = o.pos.x - this.pos.x, dz = o.pos.y - this.pos.y;
      const dist = Math.hypot(dx, dz);
      if (dist > 2 * BODY_R + 0.25 || dist < 1e-4) continue;
      const dot = (dx * fx + dz * fz) / dist;
      if (dot < 0.35) continue;
      if (dist < bd) { bd = dist; blocker = o; }
    }
    let wantSide = 0;
    const remainingNow = this.pathLen - this.s;
    const end = this.path[this.path.length - 1];
    // 對方就站在終點上：不往前擠，提早停下等
    const blockerAtGoal = !!blocker && Math.hypot(blocker.pos.x - end.x, blocker.pos.y - end.z) < 2 * BODY_R;
    if (blocker && remainingNow < END_ZONE && !blockerAtGoal) {
      // 快到終點：只有對方真的擋在「我 → 終點」之間才算被擋
      if (segDist(blocker.pos.x, blocker.pos.y, this.pos.x, this.pos.y, end.x, end.z) >= 2 * BODY_R * 0.9) blocker = null;
    }
    if (blocker && (remainingNow < END_ZONE || blockerAtGoal)) {
      // 終點被擋：停下等待，不瞬移；逾時後交給 World 改選站位或就地停下
      desired = 0;
      this.state = 'waiting';
      this.endWaitT += dt;
      if (this.endWaitT > END_WAIT) {
        this.endWaitT = 0;
        world.goalBlocked(this);
        return;
      }
    } else if (blocker) {
      const yieldToOther = blocker.walking && blocker.index < this.index && this.waitT < 1.4;
      if (yieldToOther) {
        desired = 0;
        this.waitT += dt;
        this.state = 'waiting';
      } else {
        // 往側邊閃：選對方相對位置的另一側
        const cross = fx * (blocker.pos.y - this.pos.y) - fz * (blocker.pos.x - this.pos.x);
        wantSide = cross > 0 ? -0.42 : 0.42;
        desired = WALK_SPEED * 0.6;
        this.state = 'walking';
      }
    } else {
      this.waitT = Math.max(0, this.waitT - dt * 0.5);
      this.endWaitT = Math.max(0, this.endWaitT - dt);
      this.state = 'walking';
    }
    const settle = remainingNow < NO_SIDE_ZONE && !blocker; // 快到終點且前方沒人：收回側移，準確走到終點
    if (settle) wantSide = 0;
    this.side += (wantSide - this.side) * Math.min(1, dt * (settle ? 6 : 3));
    // 還在從椅子上站起來
    if (this.sitBlend > 0.35) desired = 0;
    // 終點前減速
    const remaining = this.pathLen - this.s;
    desired = Math.min(desired, Math.max(0.35, remaining * 2.2));
    this.speed += Math.max(-ACCEL * 2 * dt, Math.min(ACCEL * dt, desired - this.speed));
    this.s = Math.min(this.pathLen, this.s + this.speed * dt);
    const p = pointAlong(this.path, this.s);
    // 側移（只在側邊格子可走時才套用）
    let px = p.x, pz = p.z;
    if (Math.abs(this.side) > 0.01) {
      const sx = p.x + -p.dz * this.side, sz = p.z + p.dx * this.side;
      if (world.nav.isFreeAt(sx, sz)) { px = sx; pz = sz; } else this.side *= 0.8;
    }
    const mx = px - this.pos.x, mz = pz - this.pos.y;
    this.pos.set(px, pz);
    const look = pointAlong(this.path, this.s + 0.5);
    const lx = look.x - px, lz = look.z - pz;
    if (Math.hypot(lx, lz) > 0.05) this.targetYaw = yawTo(lx, lz);
    else if (Math.hypot(mx, mz) > 1e-4) this.targetYaw = yawTo(mx, mz);
    if (this.s >= this.pathLen - 1e-3) {
      // 走完路徑：要坐下但還差一點（側移殘留）→ 直線補走最後一小段（只補一次）
      if (task && task.type === 'goto' && task.sit && !task.approached) {
        const d = Math.hypot(task.x - this.pos.x, task.z - this.pos.y);
        if (d >= SIT_SNAP && d < 0.6 && world.nav.lineFree(this.pos.x, this.pos.y, task.x, task.z, 0)) {
          task.approached = true;
          const keep = this.speed;
          this.setPath([{ x: this.pos.x, z: this.pos.y }, { x: task.x, z: task.z }]);
          this.speed = keep;
          return;
        }
      }
      this.arrive(world);
    }
  }

  arrive(world) {
    const task = this.task;
    this.path = null;
    this.speed = Math.min(this.speed, 0.4);
    this.side = 0;
    this.endWaitT = 0;
    this.state = 'arrived';
    this.pose = 'stand';
    if (task && task.type === 'goto') {
      if (task.yaw !== undefined && task.yaw !== null) this.targetYaw = task.yaw;
      // 只有真的走到座位（距離極小）而且座位上沒有別人時才坐下
      if (task.sit && Math.hypot(task.x - this.pos.x, task.z - this.pos.y) < SIT_SNAP && !world.occupant(task.x, task.z, this, 2 * BODY_R * 0.8)) {
        this.seated = true;
        this.pos.set(task.x, task.z);
        this.pose = task.pose || 'chair';
      }
      this.where = task.label;
    } else if (task && task.type === 'find') {
      const tg = world.agent(task.targetId);
      if (tg) this.targetYaw = yawTo(tg.pos.x - this.pos.x, tg.pos.y - this.pos.y);
      this.where = `${tg ? tg.name : task.targetId} 旁邊`;
    }
    world.emitArrived(this, task);
  }

  // ------------------------------------------------------------------ 動畫
  setRot(k, x, y = 0, z = 0) {
    const bone = this.b[k];
    _e.set(x, y, z, 'XYZ');
    _q.setFromEuler(_e);
    bone.o.quaternion.copy(bone.q).multiply(_q);
  }

  animate() {
    const w = this.walkBlend * (1 - this.sitBlend);
    const sb = this.sitBlend;
    const ph = this.phase;
    const sw = Math.sin(ph);
    const breathe = Math.sin(this.t * 1.7 + this.index);
    const idle = 1 - w;
    // 髖：走路上下起伏、坐下時降低並往後
    const hip = this.b.hip;
    const bob = w * 0.028 * (Math.abs(Math.cos(ph)) - 0.6);
    const hy = hip.p.y + bob;
    hip.o.position.set(hip.p.x, hy + (SIT_HIP_Y - hy) * sb, hip.p.z + 0.12 * sb);
    this.setRot('hip', 0, w * 0.06 * sw, 0);
    // 軀幹：前傾、扭轉、呼吸
    this.setRot('torso', w * 0.06 + idle * 0.012 * breathe + sb * 0.04, -w * 0.12 * sw + idle * 0.03 * Math.sin(this.t * 0.37 + this.index), 0);
    this.setRot('head', -w * 0.03 + idle * 0.02 * Math.sin(this.t * 0.9 + this.index * 2), idle * 0.18 * Math.sin(this.t * 0.31 + this.index * 1.3), idle * 0.03 * Math.sin(this.t * 0.53));
    // 腿
    const thighSit = Math.PI / 2 * 0.98, shinSit = -Math.PI / 2 + 0.12;
    const thL = 0.48 * sw * w, thR = -0.48 * sw * w;
    const shL = -w * (0.08 + 0.75 * Math.max(0, Math.cos(ph))), shR = -w * (0.08 + 0.75 * Math.max(0, -Math.cos(ph)));
    this.setRot('thigh_L', thL + (thighSit - thL) * sb, 0, -0.05 * sb);
    this.setRot('thigh_R', thR + (thighSit - thR) * sb, 0, 0.05 * sb);
    this.setRot('shin_L', shL + (shinSit - shL) * sb);
    this.setRot('shin_R', shR + (shinSit - shR) * sb);
    // 手臂（抱東西的手擺動小）
    const armAmp = 0.5;
    const ampL = this.holdL ? 0.12 : 1, ampR = this.holdR ? 0.5 : 1;
    const uaL = -armAmp * sw * w * ampL, uaR = armAmp * sw * w * ampR;
    const sitArm = this.pose === 'desk' ? 1 : 0.6; // 坐在辦公桌前手放桌上，一般椅子手放腿上
    const uaSit = 0.32 * sitArm, faSit = 0.95 * sitArm;
    const brA = idle * 0.025 * breathe;
    this.setRot('upperarm_L', this.holdL ? uaL * (1 - sb) + brA : uaL + (uaSit - uaL) * sb + brA, 0, 0);
    this.setRot('upperarm_R', this.holdR ? uaR * (1 - sb) + brA : uaR + (uaSit - uaR) * sb + brA, 0, 0);
    this.setRot('forearm_L', this.holdL ? 0 : (0.25 * w + faSit * sb));
    this.setRot('forearm_R', this.holdR ? 0.1 * w : (0.25 * w + faSit * sb));
  }
}

// ---------------------------------------------------------------------- 世界：資料、指派站位、尋路
export class World {
  /**
   * @param {object} o
   * @param {NavGrid} o.nav
   * @param {object} o.places   places.json
   * @param {Agent[]} o.agents  只放真正載入成功的角色
   * @param {Function} [o.emit]
   * @param {Function} [o.random] 亂數函式（預設 Math.random；測試可注入固定種子的 PRNG）
   */
  constructor({ nav, places, agents, emit, random }) {
    this.nav = nav;
    this.placesJson = places;
    this.places = new Map(places.places.map(p => [p.id, p]));
    this.agents = agents;
    this.byId = new Map(agents.map(a => [a.id, a]));
    this.reserved = new Map(); // spotKey → agentId
    this.emit = emit || (() => {});
    this.random = random || Math.random;
    for (const a of agents) a.t = this.random() * 10; // 呼吸 / 閒置動畫的初始相位
    this._comp = null;
  }

  agent(id) { return this.byId.get(id); }

  /** 導航格連通區塊標記（延遲計算） */
  compLabel(x, z) {
    if (!this._comp) this._comp = this.nav.components().labels;
    const c = this.nav.nearestFree(x, z);
    return c ? this._comp[c[1] * this.nav.w + c[0]] : -1;
  }

  seatAll() {
    for (const a of this.agents) {
      const seat = this.places.get(a.info.seat);
      const s = seat.spots[0];
      a.place(s.position[0], s.position[2], s.yaw, true);
      a.pose = s.pose || 'desk';
      a.where = seat.name;
      a.state = 'idle';
      this.reserve(a, `${seat.id}#0`, { x: s.position[0], z: s.position[2] });
    }
  }

  reserve(a, key, pos = null) {
    if (a.reservation) this.reserved.delete(a.reservation);
    a.reservation = key;
    a.reservationPos = key ? pos : null;
    if (key) this.reserved.set(key, a.id);
  }

  /** (x,z) 附近 r 以內、不是 except 的角色（站著不動或坐著的才算佔用；走路中的會離開） */
  occupant(x, z, except, r = OCCUPIED_R, includeWalking = false) {
    for (const o of this.agents) {
      if (o === except) continue;
      if (!includeWalking && o.walking) continue;
      if (Math.hypot(o.pos.x - x, o.pos.y - z) < r) return o;
    }
    return null;
  }

  /** 從 (tx,tz) 看 (x,z) 的視線是否沒被障礙擋住（目標本身所在附近 0.3 m 不檢查：座位格可能貼著桌子） */
  sightFree(tx, tz, x, z) {
    const dx = x - tx, dz = z - tz;
    const len = Math.hypot(dx, dz);
    if (len < 1e-6) return true;
    const step = this.nav.cell * 0.25;
    for (let d = Math.min(0.3, len); d <= len + 1e-9; d += step) {
      const t = d / len;
      if (!this.nav.isFreeAt(tx + dx * t, tz + dz * t)) return false;
    }
    return this.nav.isFreeAt(x, z);
  }

  /** 為角色挑選地點上的站位；exclude = 不要的 spot key */
  pickSpot(a, place, exclude = null) {
    const spots = place.spots;
    const order = spots.map((s, k) => k);
    // 依距離排序（座位類保留第 0 個 = 座位本身優先）
    if (place.kind !== 'seat' && place.kind !== 'workspace') {
      order.sort((i, j) => {
        const di = Math.hypot(spots[i].position[0] - a.pos.x, spots[i].position[2] - a.pos.y);
        const dj = Math.hypot(spots[j].position[0] - a.pos.x, spots[j].position[2] - a.pos.y);
        return di - dj;
      });
    }
    for (const k of order) {
      const key = `${place.id}#${k}`;
      if (exclude && exclude.has(key)) continue;
      const who = this.reserved.get(key);
      if (who && who !== a.id) continue;
      // 沒保留但有人站在那裡（例如找人時停在旁邊）也跳過
      if (this.occupant(spots[k].position[0], spots[k].position[2], a)) continue;
      return { k, key, spot: spots[k] };
    }
    // 全滿：在最近的站位附近找一格，離所有已被保留 / 佔用 / 別人要去的點都至少 0.65 m（不用亂數，結果可重現）
    const s = spots[order[0]];
    const p = this.freePointNear(a, s.position[0], s.position[2], place);
    if (!p) return null;
    return { k: -1, key: null, spot: { position: [p.x, 0, p.z], yaw: s.yaw, pose: 'stand' } };
  }

  /** 別人已佔用或將要佔用的點 */
  claimedPoints(a, place = null) {
    const pts = [];
    for (const o of this.agents) {
      if (o === a) continue;
      pts.push({ x: o.pos.x, z: o.pos.y });
      if (o.task && o.task.type === 'goto') pts.push({ x: o.task.x, z: o.task.z });
      if (o.reservationPos) pts.push(o.reservationPos);
    }
    if (place) {
      place.spots.forEach((sp, k) => {
        const who = this.reserved.get(`${place.id}#${k}`);
        if (who && who !== a.id) pts.push({ x: sp.position[0], z: sp.position[2] });
      });
    }
    return pts;
  }

  /** (x,z) 附近、與角色同一連通區、離 claimed 點都至少 gap 的可走格中心 */
  freePointNear(a, x, z, place = null, gap = 0.65) {
    const claimed = this.claimedPoints(a, place);
    const myComp = this.compLabel(a.pos.x, a.pos.y);
    const nav = this.nav;
    const c = nav.nearestFree(x, z, 24, (i, j) => {
      if (this._comp[j * nav.w + i] !== myComp) return true;
      const q = nav.center(i, j);
      return claimed.some(p => Math.hypot(p.x - q.x, p.z - q.z) < gap);
    });
    return c ? nav.center(c[0], c[1]) : null;
  }

  /** 走到地點 id（或 {x,z}） */
  goto(a, target, label) {
    if (!a) return { ok: false, error: '沒有這個角色' };
    let x, z, yaw = null, sit = false, pose = 'stand', key = null, name = label;
    if (typeof target === 'string') {
      const place = this.places.get(target);
      if (!place) return { ok: false, error: `沒有地點 ${target}` };
      const pick = this.pickSpot(a, place);
      if (!pick) return { ok: false, error: `${a.name} 在 ${place.name} 找不到空位` };
      x = pick.spot.position[0]; z = pick.spot.position[2]; yaw = pick.spot.yaw; sit = !!pick.spot.sit;
      pose = pick.spot.pose || (sit ? 'chair' : 'stand');
      key = pick.key;
      name = place.name;
    } else {
      x = target.x; z = target.z;
      if (!Number.isFinite(x) || !Number.isFinite(z)) return { ok: false, error: '座標不正確' };
      const c = this.nav.nearestFree(x, z);
      if (!c) return { ok: false, error: '那裡走不到' };
      if (!this.nav.isFreeAt(x, z)) { const p = this.nav.center(c[0], c[1]); x = p.x; z = p.z; }
      name = name || `(${x.toFixed(1)}, ${z.toFixed(1)})`;
    }
    const r = this.nav.findPath(a.pos.x, a.pos.y, x, z);
    if (!r.ok) return { ok: false, error: `${a.name} 找不到路到 ${name}` };
    this.reserve(a, key, key ? { x, z } : null);
    a.task = { type: 'goto', placeId: typeof target === 'string' ? target : null, spotKey: key, x, z, yaw, sit, pose, label: name, retries: 0 };
    a.where = '';
    if (r.length < 0.05) { a.arrive(this); return { ok: true, already: true }; }
    a.setPath(r.points);
    this.emit('office:state', { agent: a.id, state: a.state });
    return { ok: true, length: r.length };
  }

  /** 終點被別人擋住太久：改選同地點的其他站位（或附近空格）；重試太多次就在原地停下（不坐下） */
  goalBlocked(a) {
    const task = a.task;
    if (!task || task.type !== 'goto' || task.retries >= MAX_GOAL_RETRIES) { this.arriveHere(a); return; }
    task.retries++;
    let x, z, yaw = task.yaw, sit = false, pose = 'stand', key = null;
    const place = task.placeId ? this.places.get(task.placeId) : null;
    if (place) {
      task.tried = task.tried || new Set();
      if (task.spotKey) task.tried.add(task.spotKey);
      const pick = this.pickSpot(a, place, task.tried);
      if (!pick) { this.arriveHere(a); return; }
      x = pick.spot.position[0]; z = pick.spot.position[2]; yaw = pick.spot.yaw;
      sit = !!pick.spot.sit; pose = pick.spot.pose || (sit ? 'chair' : 'stand'); key = pick.key;
    } else {
      const p = this.freePointNear(a, task.x, task.z, null, 2 * BODY_R + 0.05);
      if (!p) { this.arriveHere(a); return; }
      x = p.x; z = p.z;
    }
    const r = this.nav.findPath(a.pos.x, a.pos.y, x, z);
    if (!r.ok) { this.arriveHere(a); return; }
    this.reserve(a, key, key ? { x, z } : null);
    Object.assign(task, { x, z, yaw, sit, pose, spotKey: key, approached: false });
    if (r.length < 0.05) { a.arrive(this); return; }
    a.setPath(r.points);
  }

  /** 就地停下（不坐下、不瞬移），釋放沒走到的站位 */
  arriveHere(a) {
    if (a.task && a.task.type === 'goto') a.task.sit = false;
    this.reserve(a, null);
    a.arrive(this);
  }

  /** 停下：清掉任務；還沒走到的站位保留要釋放（已經在站位上的保留，避免別人走進來重疊） */
  stop(a) {
    if (!a) return { ok: false, error: '沒有這個角色' };
    a.stop();
    const rp = a.reservationPos;
    if (a.reservation && !(rp && Math.hypot(rp.x - a.pos.x, rp.z - a.pos.y) < SIT_SNAP * 2)) this.reserve(a, null);
    this.emit('office:state', { agent: a.id, state: a.state });
    return { ok: true };
  }

  /** 走到另一個角色旁邊 */
  find(a, tg) {
    if (!a || !tg) return { ok: false, error: '沒有這個角色' };
    if (a === tg) return { ok: false, error: '不能找自己' };
    this.reserve(a, null);
    a.task = { type: 'find', targetId: tg.id, lastX: tg.pos.x, lastZ: tg.pos.y, replanT: 0, label: `${tg.name} 旁邊` };
    a.where = '';
    const ok = this.planFind(a, tg, false);
    if (!ok) { a.task = null; return { ok: false, error: `${a.name} 找不到路到 ${tg.name}` }; }
    this.emit('office:state', { agent: a.id, state: a.state });
    return { ok: true };
  }

  /**
   * 找人時要站的位置：目標周圍 0.8 / 1.05 m 兩圈，必須可走、沒被別人站、跟目標之間有視線（不隔著桌子 / 隔板），
   * 而且跟自己同一個連通區；找不到再放寬到 1.3 / 1.6 m；都沒有才退回「離目標最近的可走格」。偏好朝向自己這一側。
   */
  findStandPoint(a, tg) {
    const tx = tg.pos.x, tz = tg.pos.y;
    const base = Math.atan2(a.pos.y - tz, a.pos.x - tx);
    const myComp = this.compLabel(a.pos.x, a.pos.y);
    const tryRings = radii => {
      for (let k = 0; k < 12; k++) {
        const ang = base + (k % 2 ? 1 : -1) * Math.ceil(k / 2) * (Math.PI / 6);
        for (const r of radii) {
          const x = tx + Math.cos(ang) * r, z = tz + Math.sin(ang) * r;
          if (!this.nav.isFreeAt(x, z)) continue;
          if (this.agents.some(o => o !== a && o !== tg && Math.hypot(o.pos.x - x, o.pos.y - z) < 0.55)) continue;
          if (!this.sightFree(tx, tz, x, z)) continue;
          if (this.compLabel(x, z) !== myComp) continue;
          return { x, z, fallback: false };
        }
      }
      return null;
    };
    const best = tryRings([0.8, 1.05]) || tryRings([1.3, 1.6]);
    if (best) return best;
    const c = this.nav.nearestFree(tx, tz);
    if (!c) return null;
    return { ...this.nav.center(c[0], c[1]), fallback: true };
  }

  planFind(a, tg, keepMoving) {
    const tx = tg.pos.x, tz = tg.pos.y;
    a.task.lastX = tx; a.task.lastZ = tz;
    const best = this.findStandPoint(a, tg);
    if (!best) return false;
    const r = this.nav.findPath(a.pos.x, a.pos.y, best.x, best.z);
    if (!r.ok) return false;
    const close = Math.hypot(a.pos.x - tx, a.pos.y - tz) < 0.85 && this.sightFree(tx, tz, a.pos.x, a.pos.y);
    if (r.length < 0.1 || close) {
      a.path = null;
      a.arrive(this);
      return true;
    }
    a.setPath(r.points);
    if (keepMoving) a.state = 'walking';
    return true;
  }

  returnToSeat(a) {
    if (!a) return { ok: false, error: '沒有這個角色' };
    return this.goto(a, a.info.seat);
  }

  /** 執行命令解析器產生的一個動作；不存在的角色回傳錯誤，不丟例外 */
  runAction(act) {
    const results = [];
    for (const id of act.agents || []) {
      const a = this.agent(id);
      let r;
      if (!a) r = { ok: false, error: `沒有角色 ${id}（角色模型可能沒有載入）` };
      else if (act.type === 'goto') r = this.goto(a, act.target && act.target.id);
      else if (act.type === 'find') {
        const tg = act.target && this.agent(act.target.id);
        r = tg ? this.find(a, tg) : { ok: false, error: `沒有角色 ${act.target && act.target.id}（角色模型可能沒有載入）` };
      } else if (act.type === 'return') r = this.returnToSeat(a);
      else if (act.type === 'stop') r = this.stop(a);
      else r = { ok: false, error: `不支援的動作 ${act.type}` };
      results.push({ agent: id, ...r });
    }
    return results;
  }

  emitArrived(a, task) {
    let target = null;
    if (task?.type === 'goto') target = task.placeId ? { kind: 'place', id: task.placeId } : { kind: 'point', x: task.x, z: task.z };
    if (task?.type === 'find') target = { kind: 'agent', id: task.targetId };
    this.emit('office:arrived', { agent: a.id, target, position: [a.pos.x, 0, a.pos.y], seated: a.seated, where: a.where });
    this.emit('office:state', { agent: a.id, state: a.state });
  }

  update(dt) {
    for (const a of this.agents) a.update(dt, this);
  }
}
