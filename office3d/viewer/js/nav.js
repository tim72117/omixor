// 導航格與 A* 尋路（純 JS，不依賴 three.js，可直接在 Node 測試）
// 座標一律是 three.js 的地面平面 (x, z)。

const SQRT2 = Math.SQRT2;
const NEAR_COST = 2.2; // 貼著障礙物的格子額外成本倍率（讓路徑盡量走在走道中央）

export class NavGrid {
  constructor(json) {
    this.cell = json.cell;
    this.w = json.width;
    this.h = json.height;
    this.ox = json.origin[0];
    this.oz = json.origin[1];
    this.blocked = new Uint8Array(this.w * this.h);
    for (let j = 0; j < this.h; j++) {
      const row = json.rows[j];
      for (let i = 0; i < this.w; i++) this.blocked[j * this.w + i] = row.charCodeAt(i) === 49 ? 1 : 0;
    }
    // near[k] = 1：八鄰格中有障礙
    this.near = new Uint8Array(this.w * this.h);
    for (let j = 0; j < this.h; j++) {
      for (let i = 0; i < this.w; i++) {
        if (this.blocked[j * this.w + i]) continue;
        let n = 0;
        for (let dj = -1; dj <= 1 && !n; dj++) {
          for (let di = -1; di <= 1; di++) {
            if (!this.free(i + di, j + dj)) { n = 1; break; }
          }
        }
        this.near[j * this.w + i] = n;
      }
    }
  }

  inside(i, j) { return i >= 0 && j >= 0 && i < this.w && j < this.h; }
  free(i, j) { return this.inside(i, j) && !this.blocked[j * this.w + i]; }
  cellOf(x, z) { return [Math.floor((x - this.ox) / this.cell), Math.floor((z - this.oz) / this.cell)]; }
  center(i, j) { return { x: this.ox + (i + 0.5) * this.cell, z: this.oz + (j + 0.5) * this.cell }; }
  isFreeAt(x, z) { const [i, j] = this.cellOf(x, z); return this.free(i, j); }

  /** 離 (x,z) 最近的可走格；extraBlocked(i,j) 可額外排除格子 */
  nearestFree(x, z, maxR = 24, extraBlocked = null) {
    const [ci, cj] = this.cellOf(x, z);
    const ok = (i, j) => this.free(i, j) && !(extraBlocked && extraBlocked(i, j));
    if (ok(ci, cj)) return [ci, cj];
    for (let r = 1; r <= maxR; r++) {
      let best = null, bd = Infinity;
      for (let dj = -r; dj <= r; dj++) {
        for (let di = -r; di <= r; di++) {
          if (Math.max(Math.abs(di), Math.abs(dj)) !== r) continue;
          const i = ci + di, j = cj + dj;
          if (!ok(i, j)) continue;
          const c = this.center(i, j);
          const d = (c.x - x) ** 2 + (c.z - z) ** 2;
          if (d < bd) { bd = d; best = [i, j]; }
        }
      }
      if (best) return best;
    }
    return null;
  }

  /** 線段 a→b 是否全程在可走格上（radius>0 時兩側也要可走） */
  lineFree(ax, az, bx, bz, radius = 0.12) {
    const dx = bx - ax, dz = bz - az;
    const len = Math.hypot(dx, dz);
    const steps = Math.max(1, Math.ceil(len / (this.cell * 0.25)));
    const nx = len > 1e-6 ? -dz / len : 0, nz = len > 1e-6 ? dx / len : 0;
    for (let s = 0; s <= steps; s++) {
      const t = s / steps;
      const x = ax + dx * t, z = az + dz * t;
      if (!this.isFreeAt(x, z)) return false;
      if (radius > 0 && (!this.isFreeAt(x + nx * radius, z + nz * radius) || !this.isFreeAt(x - nx * radius, z - nz * radius))) return false;
    }
    return true;
  }

  /**
   * A*：回傳 { ok, points:[{x,z}], raw:[[i,j]], length }。
   * 起點/終點若落在障礙格，會先移到最近的可走格；終點座標本身若所在格可走則保留精確座標。
   */
  findPath(sx, sz, gx, gz, opts = {}) {
    const { smooth = true } = opts;
    const s = this.nearestFree(sx, sz);
    const g = this.nearestFree(gx, gz);
    if (!s || !g) return { ok: false, reason: 'no-free-cell', points: [], raw: [], length: 0 };
    const W = this.w;
    const si = s[1] * W + s[0], gi = g[1] * W + g[0];
    const N = this.w * this.h;
    const gScore = new Float64Array(N).fill(Infinity);
    const came = new Int32Array(N).fill(-1);
    const closed = new Uint8Array(N);
    const heap = new MinHeap();
    const hfun = (i, j) => {
      const dx = Math.abs(i - g[0]), dy = Math.abs(j - g[1]);
      return (dx + dy) + (SQRT2 - 2) * Math.min(dx, dy);
    };
    gScore[si] = 0;
    heap.push(si, hfun(s[0], s[1]));
    let found = si === gi;
    while (heap.size && !found) {
      const cur = heap.pop();
      if (closed[cur]) continue;
      closed[cur] = 1;
      if (cur === gi) { found = true; break; }
      const ci = cur % W, cj = (cur / W) | 0;
      for (let dj = -1; dj <= 1; dj++) {
        for (let di = -1; di <= 1; di++) {
          if (!di && !dj) continue;
          const ni = ci + di, nj = cj + dj;
          if (!this.free(ni, nj)) continue;
          if (di && dj && (!this.free(ci + di, cj) || !this.free(ci, cj + dj))) continue; // 不切角
          const nk = nj * W + ni;
          if (closed[nk]) continue;
          let c = di && dj ? SQRT2 : 1;
          if (this.near[nk]) c *= NEAR_COST;
          const ng = gScore[cur] + c;
          if (ng < gScore[nk]) {
            gScore[nk] = ng;
            came[nk] = cur;
            heap.push(nk, ng + hfun(ni, nj));
          }
        }
      }
    }
    if (!found) return { ok: false, reason: 'unreachable', points: [], raw: [], length: 0 };
    const raw = [];
    for (let k = gi; k !== -1; k = came[k]) {
      raw.push([k % W, (k / W) | 0]);
      if (k === si) break;
    }
    raw.reverse();
    let pts = raw.map(([i, j]) => this.center(i, j));
    if (this.isFreeAt(sx, sz)) pts[0] = { x: sx, z: sz };
    else pts.unshift({ x: sx, z: sz }); // 從障礙格內先走到最近的可走格
    const [gci, gcj] = this.cellOf(gx, gz);
    if (gci === g[0] && gcj === g[1]) pts[pts.length - 1] = { x: gx, z: gz };
    else if (Math.hypot(gx - pts[pts.length - 1].x, gz - pts[pts.length - 1].z) < 0.45) pts.push({ x: gx, z: gz });
    if (smooth) pts = this.smooth(pts);
    return { ok: true, points: pts, raw, length: polyLength(pts) };
  }

  /** 拉直路徑（string pulling）：保留能直線到達的最遠點 */
  smooth(pts) {
    if (pts.length <= 2) return pts.slice();
    const out = [pts[0]];
    let a = 0;
    while (a < pts.length - 1) {
      let b = pts.length - 1;
      while (b > a + 1 && !this.lineFree(pts[a].x, pts[a].z, pts[b].x, pts[b].z)) b--;
      out.push(pts[b]);
      a = b;
    }
    return out;
  }

  /** 連通區塊標記：回傳 Int32Array（-1 = 障礙） */
  components() {
    const lab = new Int32Array(this.w * this.h).fill(-1);
    let n = 0;
    const q = new Int32Array(this.w * this.h);
    for (let k0 = 0; k0 < lab.length; k0++) {
      if (this.blocked[k0] || lab[k0] !== -1) continue;
      let qh = 0, qt = 0;
      q[qt++] = k0; lab[k0] = n;
      while (qh < qt) {
        const k = q[qh++];
        const i = k % this.w, j = (k / this.w) | 0;
        for (let dj = -1; dj <= 1; dj++) for (let di = -1; di <= 1; di++) {
          if (!di && !dj) continue;
          const ni = i + di, nj = j + dj;
          if (!this.free(ni, nj)) continue;
          if (di && dj && (!this.free(i + di, j) || !this.free(i, j + dj))) continue;
          const nk = nj * this.w + ni;
          if (lab[nk] !== -1) continue;
          lab[nk] = n; q[qt++] = nk;
        }
      }
      n++;
    }
    return { labels: lab, count: n };
  }
}

export function polyLength(pts) {
  let L = 0;
  for (let k = 1; k < pts.length; k++) L += Math.hypot(pts[k].x - pts[k - 1].x, pts[k].z - pts[k - 1].z);
  return L;
}

/** 沿折線距離 s 處的點與切線方向 */
export function pointAlong(pts, s) {
  if (pts.length === 1) return { x: pts[0].x, z: pts[0].z, dx: 0, dz: 0 };
  let acc = 0;
  for (let k = 1; k < pts.length; k++) {
    const a = pts[k - 1], b = pts[k];
    const L = Math.hypot(b.x - a.x, b.z - a.z);
    if (acc + L >= s || k === pts.length - 1) {
      const t = L > 1e-9 ? Math.min(1, Math.max(0, (s - acc) / L)) : 1;
      return { x: a.x + (b.x - a.x) * t, z: a.z + (b.z - a.z) * t, dx: L > 1e-9 ? (b.x - a.x) / L : 0, dz: L > 1e-9 ? (b.z - a.z) / L : 0 };
    }
    acc += L;
  }
  const p = pts[pts.length - 1];
  return { x: p.x, z: p.z, dx: 0, dz: 0 };
}

class MinHeap {
  constructor() { this.k = []; this.v = []; }
  get size() { return this.k.length; }
  push(key, val) {
    const K = this.k, V = this.v;
    let i = K.length;
    K.push(key); V.push(val);
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (V[p] <= val) break;
      K[i] = K[p]; V[i] = V[p]; i = p;
    }
    K[i] = key; V[i] = val;
  }
  pop() {
    const K = this.k, V = this.v;
    const top = K[0];
    const lk = K.pop(), lv = V.pop();
    if (K.length) {
      let i = 0;
      const n = K.length;
      for (;;) {
        let c = 2 * i + 1;
        if (c >= n) break;
        if (c + 1 < n && V[c + 1] < V[c]) c++;
        if (V[c] >= lv) break;
        K[i] = K[c]; V[i] = V[c]; i = c;
      }
      K[i] = lk; V[i] = lv;
    }
    return top;
  }
}
