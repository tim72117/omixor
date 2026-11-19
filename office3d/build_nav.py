"""
omixor 虛擬辦公室 — 從場景產生導航格 navgrid.json 與具名地點 places.json

用法：
    blender -b -P build_nav.py          # 開啟 office.blend（唯讀，不存檔）並輸出兩個 JSON
build_office.py 重建場景時也會在匯出後自動呼叫 generate()。

做法：把高度介於 Z_LO..Z_HI 的所有網格三角形投影到地面（0.25 m 一格，保守光柵化：
跟三角形投影有重疊的格子都算障礙）。椅子、凳子、地毯、地墊不算障礙（角色可以走到椅子上就座）。
接著把障礙依角色身體半徑往外膨脹 INFLATE_R（格子中心可走 = 角色中心站在那裡身體不會穿進障礙），
但門口（會議室、CEO 辦公室、錄音間）保留膨脹前的可走格，座位/椅子/高腳凳這些「精確站位」
也保留它所在的格子，並沿膨脹前的可走格挖一條到膨脹後可走區的通道（只會讓更多格子可走，不會封住走道）。

輸出座標一律是 glTF / three.js（Y 朝上）：x = Blender x，y = Blender z，z = -Blender y。
yaw = three.js rotation.y，讓角色本地 -Z（模型正面）朝向該方向：yaw = atan2(-dx, -dz)。
"""
import json
import math
import os
from collections import deque

import bpy
import numpy as np
from mathutils import Vector

HERE = os.path.dirname(os.path.abspath(__file__))
CELL = 0.25
X0, X1, Y0, Y1 = -14.0, 14.0, -9.0, 9.0  # Blender 地板範圍
Z_LO, Z_HI = 0.10, 1.85  # 這個高度區間內的東西會擋路
EPS = 0.003  # 格子邊緣剛好貼齊時不算重疊（要小於最薄玻璃厚度的一半）
BODY_R = 0.28     # 與 viewer/js/agents.js 的 BODY_R 一致（兩角色中心最近距離的一半）
INFLATE_R = 0.25  # 障礙往外膨脹的距離（約一格；比 BODY_R 略小，避免 1 m 寬的走道被封死）
SKIP_KEYS = ("chair", "stool", "rug", "carpet", "entry_mat", "floor", "ground", "ceiling", "roof",
             "tree", "_sign", "sign_text", "nameplate", "pendant")

ROLES = [
    ("ceo", "CEO", "purple", "#AF52DE", ["執行長", "老闆", "boss"]),
    ("product-manager", "產品經理", "indigo", "#5856D6", ["pm", "產品", "PM"]),
    ("engineer", "工程師", "green", "#34C759", ["工程", "eng", "dev", "developer"]),
    ("course-dev", "課程開發", "indigo", "#5856D6", ["課程", "course", "coursedev", "課程開發者"]),
    ("narrator", "旁白製作", "purple", "#AF52DE", ["旁白", "配音", "voice"]),
    ("marketing", "行銷", "pink", "#FF2D55", ["mkt", "marketer", "行銷人員"]),
    ("visitor-analyst", "訪客分析師", "teal", "#30B0C7", ["分析師", "analyst", "訪客分析", "數據"]),
    ("triager", "Issue 整理員", "orange", "#FF9500", ["整理員", "issue整理員", "triage", "issue"]),
]
ZONE_EXTRA_ALIASES = {
    "ceo": ["CEO 辦公室", "CEO辦公室", "老闆辦公室", "ceo office", "office"],
    "narrator": ["錄音間", "錄音室", "booth", "studio", "recording booth"],
    "engineer": ["伺服器", "server rack"],
    "product-manager": ["看板", "kanban"],
    "visitor-analyst": ["儀表板", "dashboard"],
    "marketing": ["海報牆", "posters"],
    "course-dev": ["書架", "bookshelf"],
    "triager": ["分類櫃", "inbox"],
}


def to3(p):
    """Blender (x, y) → three.js [x, 0, z]"""
    return [round(p[0], 3), 0.0, round(-p[1], 3)]


def yaw_of(dx, dy):
    """Blender 平面方向 → three.js rotation.y"""
    return round(math.atan2(-dx, dy), 4)


class Grid:
    def __init__(self):
        self.nx = int(round((X1 - X0) / CELL))
        self.ny = int(round((Y1 - Y0) / CELL))
        self.blocked = np.zeros((self.ny, self.nx), dtype=bool)  # [iy, ix]，iy 沿 Blender +y

    def cell_of(self, x, y):
        return int(math.floor((x - X0) / CELL)), int(math.floor((y - Y0) / CELL))

    def center(self, ix, iy):
        return X0 + (ix + 0.5) * CELL, Y0 + (iy + 0.5) * CELL

    def inside(self, ix, iy):
        return 0 <= ix < self.nx and 0 <= iy < self.ny

    def free(self, ix, iy):
        return self.inside(ix, iy) and not self.blocked[iy, ix]

    def rasterize(self, tris):
        """tris: (T,3,2) 已投影到地面的三角形"""
        mn = tris.min(axis=1)
        mx = tris.max(axis=1)
        ix0 = np.floor((mn[:, 0] + EPS - X0) / CELL).astype(int)
        ix1 = np.floor((mx[:, 0] - EPS - X0) / CELL).astype(int)
        iy0 = np.floor((mn[:, 1] + EPS - Y0) / CELL).astype(int)
        iy1 = np.floor((mx[:, 1] - EPS - Y0) / CELL).astype(int)
        ix1 = np.maximum(ix1, ix0)
        iy1 = np.maximum(iy1, iy0)
        for t in range(len(tris)):
            a0, a1 = max(ix0[t], 0), min(ix1[t], self.nx - 1)
            b0, b1 = max(iy0[t], 0), min(iy1[t], self.ny - 1)
            if a0 > a1 or b0 > b1:
                continue
            if (a1 - a0) <= 1 and (b1 - b0) <= 1:
                self.blocked[b0:b1 + 1, a0:a1 + 1] = True
                continue
            P = tris[t]
            xs = X0 + np.arange(a0, a1 + 1) * CELL
            ys = Y0 + np.arange(b0, b1 + 1) * CELL
            gx, gy = np.meshgrid(xs, ys)
            lo_x, hi_x = gx + EPS, gx + CELL - EPS
            lo_y, hi_y = gy + EPS, gy + CELL - EPS
            hit = np.ones_like(gx, dtype=bool)
            for k in range(3):  # SAT：三角形三條邊的法線
                e = P[(k + 1) % 3] - P[k]
                n = np.array([-e[1], e[0]])
                ln = np.hypot(*n)
                if ln < 1e-9:
                    continue
                n /= ln
                tp = P @ n
                tmin, tmax = tp.min(), tp.max()
                # 方塊在 n 上的投影範圍
                c1 = np.minimum(lo_x * n[0], hi_x * n[0]) + np.minimum(lo_y * n[1], hi_y * n[1])
                c2 = np.maximum(lo_x * n[0], hi_x * n[0]) + np.maximum(lo_y * n[1], hi_y * n[1])
                hit &= ~((c2 < tmin) | (c1 > tmax))
            self.blocked[b0:b1 + 1, a0:a1 + 1] |= hit

    def nearest_free(self, x, y, max_r=12):
        ix, iy = self.cell_of(x, y)
        if self.free(ix, iy):
            return ix, iy
        best = None
        for r in range(1, max_r + 1):
            for dy in range(-r, r + 1):
                for dx in range(-r, r + 1):
                    if max(abs(dx), abs(dy)) != r:
                        continue
                    if self.free(ix + dx, iy + dy):
                        cx, cy = self.center(ix + dx, iy + dy)
                        d = (cx - x) ** 2 + (cy - y) ** 2
                        if best is None or d < best[0]:
                            best = (d, ix + dx, iy + dy)
            if best:
                return best[1], best[2]
        return None

    def inflate(self, cells):
        """障礙往外膨脹 cells 格（圓形結構元素）。回傳膨脹前的 blocked 副本"""
        raw = self.blocked.copy()
        if cells <= 0:
            return raw
        out = raw.copy()
        for dy in range(-cells, cells + 1):
            for dx in range(-cells, cells + 1):
                if (dx or dy) and dx * dx + dy * dy <= (cells + 0.5) ** 2:
                    sh = np.zeros_like(raw)
                    ys0, ys1 = max(0, dy), self.ny + min(0, dy)
                    xs0, xs1 = max(0, dx), self.nx + min(0, dx)
                    sh[ys0:ys1, xs0:xs1] = raw[ys0 - dy:ys1 - dy, xs0 - dx:xs1 - dx]
                    out |= sh
        self.blocked = out
        return raw

    def restore_rect(self, raw, x0, y0, x1, y1):
        """矩形範圍（Blender 座標）內恢復膨脹前可走的格子；回傳恢復的格數"""
        n = 0
        a0, b0 = self.cell_of(min(x0, x1), min(y0, y1))
        a1, b1 = self.cell_of(max(x0, x1), max(y0, y1))
        for iy in range(max(0, b0), min(self.ny - 1, b1) + 1):
            for ix in range(max(0, a0), min(self.nx - 1, a1) + 1):
                if self.blocked[iy, ix] and not raw[iy, ix]:
                    self.blocked[iy, ix] = False
                    n += 1
        return n

    def carve(self, raw, x, y, reach):
        """讓 (x,y) 所在格可走，並沿膨脹前的可走格挖最短通道接到主要可走區（reach=True 的格子）。
        回傳恢復的格數；-1 表示接不上"""
        ix, iy = self.cell_of(x, y)
        if not self.inside(ix, iy):
            return 0
        if raw[iy, ix]:
            # 原本就在障礙格內（不該發生）：改從膨脹前最近的可走格開始
            best = None
            for r in range(1, 4):
                for dy in range(-r, r + 1):
                    for dx in range(-r, r + 1):
                        jx, jy = ix + dx, iy + dy
                        if self.inside(jx, jy) and not raw[jy, jx]:
                            cx, cy = self.center(jx, jy)
                            d = (cx - x) ** 2 + (cy - y) ** 2
                            if best is None or d < best[0]:
                                best = (d, jx, jy)
                if best:
                    break
            if not best:
                return 0
            ix, iy = best[1], best[2]
        if reach[iy, ix]:
            return 0
        # BFS（4 鄰）在膨脹前可走格上找最近的主要可走區格子
        prev = {(ix, iy): None}
        q = deque([(ix, iy)])
        goal = None
        while q:
            c = q.popleft()
            if reach[c[1], c[0]]:
                goal = c
                break
            for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                nb = (c[0] + dx, c[1] + dy)
                if nb in prev or not self.inside(*nb) or raw[nb[1], nb[0]]:
                    continue
                prev[nb] = c
                q.append(nb)
        if goal is None:
            return -1
        n = 0
        c = prev[goal]
        while c is not None:
            if self.blocked[c[1], c[0]]:
                self.blocked[c[1], c[0]] = False
                n += 1
            c = prev[c]
        return n

    def clearance_ok(self, ix, iy):
        return all(self.free(ix + dx, iy + dy) for dx in (-1, 0, 1) for dy in (-1, 0, 1))

    def bfs(self, start):
        seen = np.zeros_like(self.blocked)
        q = deque([start])
        seen[start[1], start[0]] = True
        while q:
            ix, iy = q.popleft()
            for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1), (1, 1), (1, -1), (-1, 1), (-1, -1)):
                jx, jy = ix + dx, iy + dy
                if not self.free(jx, jy) or seen[jy, jx]:
                    continue
                if dx and dy and not (self.free(ix + dx, iy) and self.free(ix, iy + dy)):
                    continue  # 不切角
                seen[jy, jx] = True
                q.append((jx, jy))
        return seen


def obstacle_triangles(scene):
    deps = bpy.context.evaluated_depsgraph_get()
    out = []
    used = []
    for o in scene.objects:
        if o.type != "MESH":
            continue
        n = o.name.lower()
        if any(k in n for k in SKIP_KEYS):
            continue
        oe = o.evaluated_get(deps)
        me = oe.to_mesh()
        me.calc_loop_triangles()
        nv, nt = len(me.vertices), len(me.loop_triangles)
        if nt == 0:
            oe.to_mesh_clear()
            continue
        co = np.empty(nv * 3, dtype=np.float64)
        me.vertices.foreach_get("co", co)
        co = co.reshape(-1, 3)
        tri = np.empty(nt * 3, dtype=np.int64)
        me.loop_triangles.foreach_get("vertices", tri)
        tri = tri.reshape(-1, 3)
        oe.to_mesh_clear()
        mw = np.array(o.matrix_world, dtype=np.float64)
        w = co @ mw[:3, :3].T + mw[:3, 3]
        T = w[tri]  # (nt,3,3)
        zmin, zmax = T[:, :, 2].min(axis=1), T[:, :, 2].max(axis=1)
        keep = (zmax > Z_LO) & (zmin < Z_HI)
        T = T[keep]
        xy = T[:, :, :2]
        mn, mx = xy.min(axis=1), xy.max(axis=1)
        keep2 = (mx[:, 0] > X0) & (mn[:, 0] < X1) & (mx[:, 1] > Y0) & (mn[:, 1] < Y1)
        if keep2.any():
            out.append(xy[keep2])
            used.append(o.name)
    return (np.concatenate(out) if out else np.zeros((0, 3, 2))), used


def world_xy(o, local=(0, 0, 0)):
    p = o.matrix_world @ Vector(local)
    return (p.x, p.y)


def forward_xy(o):
    f = o.matrix_world.to_3x3() @ Vector((0, 1, 0))
    return (f.x, f.y)


def generate(here=HERE, scene=None):
    scene = scene or bpy.context.scene
    G = Grid()
    tris, used = obstacle_triangles(scene)
    G.rasterize(tris)
    # 外圈一格當牆
    G.blocked[0, :] = G.blocked[-1, :] = True
    G.blocked[:, 0] = G.blocked[:, -1] = True
    OB = bpy.data.objects
    problems = []
    free_raw = int((~G.blocked).sum())
    inflate_cells = int(round(INFLATE_R / CELL))
    raw = G.inflate(inflate_cells)
    free_inflated = int((~G.blocked).sum())
    # 門口保持可通行：門洞寬度 + 牆兩側各 0.8 m 的範圍內恢復膨脹前的可走格
    # （門洞座標與 build_office.py 一致：f_meet_walls、f_ceo_glass、f_booth）
    doorways = []
    mz = world_xy(OB["zone_meeting"])   # 南側玻璃 y=-2.75（本地），門洞 x∈[2.0, 3.1]
    doorways.append(("meeting", mz[0] + 2.0, mz[1] - 2.75 - 0.8, mz[0] + 3.1, mz[1] - 2.75 + 0.8))
    cz = world_xy(OB["zone_ceo"])       # 西牆 x=-2.75（本地），門洞 y∈[-2.35, -1.35]
    doorways.append(("ceo", cz[0] - 2.75 - 0.8, cz[1] - 2.35, cz[0] - 2.75 + 0.8, cz[1] - 1.35))
    nz = world_xy(OB["zone_narrator"])  # 西牆 x=-2.0（本地），門洞 y∈[-2.43, -1.63]（門楣中心 -2.03、寬 0.8）
    doorways.append(("narrator_booth", nz[0] - 2.0 - 0.8, nz[1] - 2.43, nz[0] - 2.0 + 0.8, nz[1] - 1.63))
    door_restored = {}
    for name_, x0_, y0_, x1_, y1_ in doorways:
        door_restored[name_] = G.restore_rect(raw, x0_, y0_, x1_, y1_)
    # 精確站位（座位、會議室/公共角落椅子、茶水間高腳凳）保留可走並挖通道
    carved = 0
    main_start = G.nearest_free(0.0, 2.6)  # 中央走道
    reach = G.bfs(main_start)
    exact_pts = [world_xy(OB[f"seat_{rid}"]) for rid, *_ in ROLES]
    for o in OB:
        if o.name.startswith(("meeting_chair", "commons_chair")):
            exact_pts.append(world_xy(o, (0, 0.02, 0)))
        elif o.name.startswith("lounge_stool"):
            exact_pts.append(world_xy(o))
    for x_, y_ in exact_pts:
        n_ = G.carve(raw, x_, y_, reach)
        if n_ < 0:
            problems.append(f"exact spot ({x_:.2f},{y_:.2f}) cannot connect to main area")
        elif n_ > 0:
            carved += n_
            reach = G.bfs(main_start)

    def spot(x, y, yaw, sit=False, exact=False, label="", pose=None):
        """回傳地點上的一個站位。exact=True（座位、椅子）時保留原座標，只要求所在格子連得到。
        pose：抵達後的姿勢，'desk'（坐在自己辦公桌前，手放桌上）、'chair'（坐在一般椅子）、'stand'（站著）"""
        c = G.nearest_free(x, y)
        if c is None:
            problems.append(f"no free cell near {label} ({x:.2f},{y:.2f})")
            return None
        if exact:
            px, py = x, y
        else:
            px, py = G.center(*c)
        s = {"position": to3((px, py)), "yaw": yaw, "cell": [c[0], G.ny - 1 - c[1]]}
        if sit:
            s["sit"] = True
        s["pose"] = pose or ("chair" if sit else "stand")
        return s

    def ring_spots(cx, cy, r_max, n=6, min_gap=0.75, face=None, prefer_clear=True, avoid=()):
        cand = []
        for iy in range(G.ny):
            for ix in range(G.nx):
                if not G.free(ix, iy):
                    continue
                x, y = G.center(ix, iy)
                d = math.hypot(x - cx, y - cy)
                if d > r_max:
                    continue
                pen = 0 if (not prefer_clear or G.clearance_ok(ix, iy)) else 1.5
                cand.append((d + pen, x, y))
        cand.sort()
        picked = []
        for _, x, y in cand:
            if all(math.hypot(x - a, y - b) >= min_gap for a, b in picked + list(avoid)):
                picked.append((x, y))
            if len(picked) >= n:
                break
        out = []
        for x, y in picked:
            fx, fy = (face[0] - x, face[1] - y) if face else (cx - x, cy - y)
            out.append(spot(x, y, yaw_of(fx, fy) if math.hypot(fx, fy) > 1e-3 else 0.0))
        return [s for s in out if s]

    places = []
    agents = []
    for rid, name, cname, hexc, extra in ROLES:
        so = OB[f"seat_{rid}"]
        sx, sy = world_xy(so)
        fx, fy = forward_xy(so)
        yaw = yaw_of(fx, fy)
        seat_spot = spot(sx, sy, yaw, sit=True, exact=True, label=f"seat_{rid}", pose="desk")
        # 座位旁的站位（seat 本地 ±X）
        rx, ry = fy, -fx  # 角色右手方向（Blender）
        side_spots = []
        for k, (lx, ly) in enumerate(((0.8, -0.1), (-0.8, -0.1), (0.55, -0.85), (-0.55, -0.85), (0.0, -1.0))):
            x = sx + rx * lx + fx * ly
            y = sy + ry * lx + fy * ly
            s = spot(x, y, yaw_of(sx - x + fx * 0.6, sy - y + fy * 0.6), label=f"{rid} side")
            if s:
                side_spots.append(s)
        names = [name, rid] + extra
        places.append({
            "id": f"seat_{rid}", "kind": "seat", "owner": rid, "name": f"{name}的座位",
            "aliases": sorted(set([f"{n}的座位" for n in names] + [f"{n}座位" for n in names] +
                                  [f"{n} seat" for n in names] + [f"{n}'s seat" for n in names] +
                                  [f"{n}的位子" for n in names] + [f"seat {rid}", f"seat_{rid}"])),
            "position": seat_spot["position"], "yaw": yaw, "sit": True,
            "spots": [seat_spot] + side_spots,
        })
        zo = OB[f"zone_{rid}"]
        zx, zy = world_xy(zo)
        ws = side_spots[0] if side_spots else seat_spot
        places.append({
            "id": f"zone_{rid}", "kind": "workspace", "owner": rid, "name": f"{name}的工作區",
            "aliases": sorted(set([f"{n}的工作區" for n in names] + [f"{n}工作區" for n in names] +
                                  [f"{n} desk" for n in names] + [f"{n} workspace" for n in names] +
                                  [f"{n}'s desk" for n in names] + [f"zone_{rid}", f"zone {rid}"] +
                                  ZONE_EXTRA_ALIASES.get(rid, []))),
            "position": ws["position"], "yaw": ws["yaw"], "center": to3((zx, zy)),
            "spots": side_spots + [seat_spot],
        })
        agents.append({"id": rid, "name": name, "aliases": [rid, name] + extra, "color": cname, "hex": hexc,
                       "seat": f"seat_{rid}", "zone": f"zone_{rid}"})

    def chairs(prefix):
        out = []
        for o in OB:
            if o.name.startswith(prefix):
                x, y = world_xy(o, (0, 0.02, 0))
                fx, fy = forward_xy(o)
                s = spot(x, y, yaw_of(fx, fy), sit=True, exact=True, label=o.name, pose="chair")
                if s:
                    out.append(s)
        return out

    # 會議室：7 張椅子可就座
    zm = OB["zone_meeting"]
    mx, my = world_xy(OB["meeting_table"])
    meet_spots = chairs("meeting_chair")
    places.append({"id": "meeting", "kind": "shared", "name": "會議室",
                   "aliases": ["會議室", "會議", "開會", "meeting", "meeting room", "conference", "conference room", "zone_meeting"],
                   "position": meet_spots[0]["position"], "yaw": meet_spots[0]["yaw"], "center": to3((mx, my)),
                   "spots": meet_spots})
    # 西側公共角落：圓桌 4 張椅子
    zc = OB["zone_commons"]
    cx, cy = world_xy(OB["commons_table"])
    com_spots = chairs("commons_chair")
    places.append({"id": "commons", "kind": "shared", "name": "西側公共角落",
                   "aliases": ["西側公共角落", "公共角落", "公共區", "公共區域", "圓桌", "commons", "common area", "west corner", "zone_commons"],
                   "position": com_spots[0]["position"], "yaw": com_spots[0]["yaw"], "center": to3((cx, cy)),
                   "spots": com_spots})
    # 印表機（公共角落）
    px, py = world_xy(OB["commons_printer"])
    pf = forward_xy(OB["commons_printer"])
    pr = ring_spots(px - pf[0] * 0.0, py, 1.3, n=3, face=(px, py))
    places.append({"id": "printer", "kind": "shared", "name": "印表機",
                   "aliases": ["印表機", "影印機", "printer", "copier"],
                   "position": pr[0]["position"], "yaw": pr[0]["yaw"], "center": to3((px, py)), "spots": pr})
    # 茶水間：吧台旁的高腳凳
    zl = OB["zone_lounge"]
    bx, by = world_xy(OB["lounge_bar_table"])
    pantry = []
    for o in OB:
        if o.name.startswith("lounge_stool"):
            x, y = world_xy(o)
            s = spot(x, y, yaw_of(bx - x, 0), exact=True, label=o.name)
            if s:
                pantry.append(s)
    pantry.sort(key=lambda s: (s["position"][0], s["position"][2]))
    places.append({"id": "pantry", "kind": "shared", "name": "茶水間",
                   "aliases": ["茶水間", "吧台", "咖啡", "咖啡機", "喝咖啡", "pantry", "kitchen", "coffee", "bar"],
                   "position": pantry[0]["position"], "yaw": pantry[0]["yaw"], "center": to3((bx, by)), "spots": pantry})
    # 休息區：沙發與茶几周圍
    lx, ly = world_xy(OB["lounge_coffee_table"])
    lounge = ring_spots(lx, ly, 1.6, n=6, face=(lx, ly))
    places.append({"id": "lounge", "kind": "shared", "name": "休息區",
                   "aliases": ["休息區", "休息", "沙發", "lounge", "rest area", "sofa", "zone_lounge"],
                   "position": lounge[0]["position"], "yaw": lounge[0]["yaw"], "center": to3((lx, ly)), "spots": lounge})
    # 入口櫃台：櫃台正面（-Y 側）排一排
    zr = OB["zone_reception"]
    rx_, ry_ = world_xy(zr)
    rec = []
    for dx in (0.0, -0.7, 0.7, -1.4, 1.4):
        s = spot(rx_ + dx, ry_ - 0.75, yaw_of(0, 1), label="reception")
        if s:
            rec.append(s)
    places.append({"id": "reception", "kind": "shared", "name": "入口櫃台",
                   "aliases": ["入口櫃台", "櫃台", "櫃檯", "入口櫃檯", "接待", "接待處", "reception", "front desk", "zone_reception"],
                   "position": rec[0]["position"], "yaw": rec[0]["yaw"], "center": to3((rx_, ry_)), "spots": rec})
    # 入口（南牆玻璃門內側）
    ent = ring_spots(1.5, -8.55, 1.6, n=5, face=(1.5, 0.0))
    places.append({"id": "entrance", "kind": "shared", "name": "入口",
                   "aliases": ["入口", "門口", "大門", "entrance", "door", "front door", "exit"],
                   "position": ent[0]["position"], "yaw": ent[0]["yaw"], "center": to3((1.5, -9.0)), "spots": ent})
    # 窗邊長椅
    wx, wy = world_xy(OB["decor_bench_window"])
    win = ring_spots(wx, wy - 0.75, 1.4, n=5, face=(wx, 12.0))
    places.append({"id": "window", "kind": "shared", "name": "窗邊",
                   "aliases": ["窗邊", "窗戶", "落地窗", "窗邊長椅", "window", "bench", "window bench"],
                   "position": win[0]["position"], "yaw": win[0]["yaw"], "center": to3((wx, wy)), "spots": win})
    # 中央走道
    mid = ring_spots(0.0, 2.6, 1.6, n=6, face=None)
    places.append({"id": "center", "kind": "shared", "name": "中央走道",
                   "aliases": ["中央走道", "中間", "中央", "走道", "center", "middle", "hall", "hallway"],
                   "position": mid[0]["position"], "yaw": mid[0]["yaw"], "center": to3((0.0, 2.6)), "spots": mid})

    # ---- 連通性檢查 ----
    first = places[0]["spots"][0]["cell"]
    start = (first[0], G.ny - 1 - first[1])
    reach = G.bfs(start)
    free_n = int((~G.blocked).sum())
    reach_n = int(reach.sum())
    report = []
    all_ok = True
    for p in places:
        bad = []
        for i, s in enumerate(p["spots"]):
            ix, iyr = s["cell"]
            if not reach[G.ny - 1 - iyr, ix]:
                bad.append(i)
        ok = not bad
        all_ok &= ok
        report.append({"id": p["id"], "spots": len(p["spots"]), "reachable": ok, "unreachable_spots": bad})

    rows = []
    for j in range(G.ny):  # three.js z 由小到大 = Blender y 由大到小
        iy = G.ny - 1 - j
        rows.append("".join("1" if G.blocked[iy, ix] else "0" for ix in range(G.nx)))
    nav = {
        "version": 1,
        "note": "three.js 座標（Y 朝上）。cell (i,j) 中心 = (origin[0]+(i+0.5)*cell, 0, origin[1]+(j+0.5)*cell)；rows[j][i]：'0' 可走、'1' 障礙",
        "cell": CELL, "width": G.nx, "height": G.ny,
        "origin": [X0, -Y1],
        "bounds": {"minX": X0, "maxX": X1, "minZ": -Y1, "maxZ": -Y0},
        "rows": rows,
        "stats": {"free": free_n, "free_before_inflate": free_raw, "free_after_inflate_only": free_inflated,
                  "door_cells_restored": door_restored, "spot_cells_carved": carved, "reachable_from_first_seat": reach_n, "all_places_reachable": bool(all_ok),
                  "obstacle_objects": len(used), "triangles": int(len(tris))},
        "params": {"z_band": [Z_LO, Z_HI], "skip_name_keys": list(SKIP_KEYS),
                   "inflate_m": INFLATE_R, "inflate_cells": inflate_cells,
                   "doorways": [{"id": d[0], "blender_rect": [round(v, 3) for v in d[1:]]} for d in doorways]},
    }
    pl = {
        "version": 1,
        "note": "three.js 座標（Y 朝上，公尺）。yaw = rotation.y（模型正面為本地 -Z）。spots 是可分配給多個角色的站位；sit=true 表示可坐下；pose 是抵達後的姿勢（desk=坐在辦公桌前、chair=坐椅子、stand=站著）。cell=[i,j] 對應 navgrid",
        "agents": agents,
        "places": places,
    }
    with open(os.path.join(here, "navgrid.json"), "w", encoding="utf-8") as f:
        json.dump(nav, f, ensure_ascii=False, separators=(",", ":"))
    with open(os.path.join(here, "places.json"), "w", encoding="utf-8") as f:
        json.dump(pl, f, ensure_ascii=False, indent=1)
    print(f"[nav] inflate {INFLATE_R} m ({inflate_cells} cell): free {free_raw} -> {free_inflated}; "
          f"doorways restored {door_restored}; exact-spot cells carved {carved}")
    print(f"[nav] grid {G.nx}x{G.ny} cell={CELL} free={free_n} reachable={reach_n} "
          f"obstacle_objects={len(used)} tris={len(tris)}")
    for r in report:
        print(f"[nav]   {r['id']:<24} spots={r['spots']:<2} reachable={r['reachable']} {r['unreachable_spots'] or ''}")
    for p in problems:
        print("[nav] WARN", p)
    print(f"[nav] all places reachable: {all_ok}")
    return all_ok


if __name__ == "__main__":
    # 直接執行：開 office.blend（不存檔）
    if not bpy.data.objects.get("seat_ceo"):
        bpy.ops.wm.open_mainfile(filepath=os.path.join(HERE, "office.blend"))
    generate(HERE)
