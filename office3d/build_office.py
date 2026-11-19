"""
omixor 虛擬辦公室 — 程序化 3D 場景產生器（場景的唯一來源）

用法（在 office3d/ 目錄外或內都可以）：
    blender -b -P build_office.py                       # 重建場景、存 .blend、匯出 .glb、渲染全部
    blender -b -P build_office.py -- --no-render        # 只重建 + 存檔 + 匯出，不渲染
    blender -b -P build_office.py -- --only=overview,engineer   # 只渲染指定攝影機
    blender -b -P build_office.py -- --samples=32       # 改渲染取樣數

每次執行都會先清空成空場景再重建，不依賴任何外部素材（貼圖全用 numpy 程序化產生並打包）。
"""
import bpy
import bmesh
import math
import os
import sys
import random

import numpy as np
from mathutils import Matrix, Vector, Euler

PI = math.pi
HERE = os.path.dirname(os.path.abspath(__file__))
ARGV = sys.argv[sys.argv.index("--") + 1:] if "--" in sys.argv else []
NO_RENDER = "--no-render" in ARGV
ONLY = None
SAMPLES = 64
for _a in ARGV:
    if _a.startswith("--only="):
        ONLY = _a.split("=", 1)[1].split(",")
    if _a.startswith("--samples="):
        SAMPLES = int(_a.split("=", 1)[1])

# ---------------------------------------------------------------------------
# 角色資料
# ---------------------------------------------------------------------------
ROLES = [
    dict(id="ceo", name="CEO", job="訂方向與目標、統整各角色回報做取捨", color="purple", symbol="crown"),
    dict(id="product-manager", name="產品經理", job="決定接下來做什麼、排優先順序", color="indigo", symbol="brain.head.profile"),
    dict(id="engineer", name="工程師", job="修程式、評估技術做法", color="green", symbol="wrench.and.screwdriver"),
    dict(id="course-dev", name="課程開發", job="規劃與撰寫課程腳本", color="indigo", symbol="doc.text"),
    dict(id="narrator", name="旁白製作", job="把課程旁白做成語音", color="purple", symbol="waveform"),
    dict(id="marketing", name="行銷", job="帶更多訪客認識課程", color="pink", symbol="megaphone"),
    dict(id="visitor-analyst", name="訪客分析師", job="盯網站訪客與播放數字", color="teal", symbol="chart.line.uptrend.xyaxis"),
    dict(id="triager", name="Issue 整理員", job="整理未解決的 Issue", color="orange", symbol="tray.full"),
]
ROLE = {r["id"]: r for r in ROLES}
APPLE = {  # Apple 系統色（sRGB）
    "purple": "#AF52DE", "indigo": "#5856D6", "green": "#34C759",
    "pink": "#FF2D55", "teal": "#30B0C7", "orange": "#FF9500",
}

# 工作區（世界座標，公尺）。開放式工位：人坐在 seat，面向 +Y（北，窗戶方向）
POD_ORIGIN = {
    "course-dev": (-5.0, 5.4), "product-manager": (0.0, 5.4), "engineer": (5.0, 5.4),
    "marketing": (-5.0, -0.6), "triager": (0.0, -0.6), "visitor-analyst": (5.0, -0.6),
}
FLOOR_X, FLOOR_Y, CEIL = 14.0, 9.0, 3.2   # 地板範圍 x∈[-14,14], y∈[-9,9]，天花板高 3.2m


# ---------------------------------------------------------------------------
# 色彩工具
# ---------------------------------------------------------------------------
def srgb(h):
    h = h.lstrip("#")
    return tuple(int(h[i:i + 2], 16) / 255 for i in (0, 2, 4))


def lin(h):
    if isinstance(h, str):
        h = srgb(h)
    return tuple((c / 12.92) if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4 for c in h)


def mix(a, b, t):
    return tuple(x * (1 - t) + y * t for x, y in zip(a, b))


def role_lin(rid):
    return lin(APPLE[ROLE[rid]["color"]])


# ---------------------------------------------------------------------------
# 場景重置
# ---------------------------------------------------------------------------
bpy.ops.wm.read_factory_settings(use_empty=True)
try:
    import addon_utils
    addon_utils.enable("io_scene_gltf2", default_set=True)
except Exception as e:  # noqa
    print("gltf addon enable:", e)

SC = bpy.context.scene
SC.name = "omixor_office"
SC.unit_settings.system = "METRIC"
NAMES = set()


def uniq(name):
    base, i = name, 1
    while name in NAMES or name in bpy.data.objects:
        i += 1
        name = f"{base}_{i:02d}"
    NAMES.add(name)
    return name


def new_coll(name, parent=None):
    c = bpy.data.collections.new(name)
    (parent or SC.collection).children.link(c)
    return c


COLL = {"cur": SC.collection}


def set_coll(c):
    COLL["cur"] = c


# ---------------------------------------------------------------------------
# 材質
# ---------------------------------------------------------------------------
MATS = {}


def mat(name, color=(0.8, 0.8, 0.8), rough=0.5, metal=0.0, emit=None, strength=0.0,
        alpha=1.0, img=None, emit_img=False, coat=0.0, spec=0.5):
    if name in MATS:
        return MATS[name]
    if isinstance(color, str):
        color = lin(color)
    m = bpy.data.materials.new(name)
    if m.node_tree is None:
        m.use_nodes = True
    nt = m.node_tree
    b = nt.nodes.get("Principled BSDF")
    b.inputs["Base Color"].default_value = (*color, 1.0)
    b.inputs["Roughness"].default_value = rough
    b.inputs["Metallic"].default_value = metal
    b.inputs["Specular IOR Level"].default_value = spec
    if coat:
        b.inputs["Coat Weight"].default_value = coat
    if emit is not None:
        if isinstance(emit, str):
            emit = lin(emit)
        b.inputs["Emission Color"].default_value = (*emit, 1.0)
        b.inputs["Emission Strength"].default_value = strength
    if img is not None:
        t = nt.nodes.new("ShaderNodeTexImage")
        t.image = img
        t.location = (-400, 200)
        nt.links.new(t.outputs["Color"], b.inputs["Base Color"])
        if emit_img:
            nt.links.new(t.outputs["Color"], b.inputs["Emission Color"])
            b.inputs["Emission Strength"].default_value = strength
    if alpha < 1.0:
        b.inputs["Alpha"].default_value = alpha
        m.surface_render_method = "BLENDED"
        try:
            m.use_transparent_shadow = True
        except Exception:
            pass
    m.diffuse_color = (*color, alpha)
    MATS[name] = m
    return m


# ---------------------------------------------------------------------------
# 程序化貼圖（numpy 畫圖，打包進 .blend / .glb）
# ---------------------------------------------------------------------------
def rgbv(c):
    if isinstance(c, str):
        return np.array(srgb(c), np.float32)
    return np.array(c[:3], np.float32)


class Canvas:
    def __init__(s, w, h, bg="#000000"):
        s.w, s.h = w, h
        s.a = np.ones((h, w, 4), np.float32)
        s.a[..., :3] = rgbv(bg)

    def rect(s, x, y, w, h, c, a=1.0):
        x0, y0 = max(0, int(round(x))), max(0, int(round(y)))
        x1, y1 = min(s.w, int(round(x + w))), min(s.h, int(round(y + h)))
        if x1 > x0 and y1 > y0:
            s.a[y0:y1, x0:x1, :3] = s.a[y0:y1, x0:x1, :3] * (1 - a) + rgbv(c) * a

    def vgrad(s, x, y, w, h, c1, c2):
        c1, c2 = rgbv(c1), rgbv(c2)
        for i in range(int(h)):
            t = i / max(1, h - 1)
            s.rect(x, y + i, w, 1, c1 * (1 - t) + c2 * t)

    def circle(s, cx, cy, r, c, a=1.0, inner=0.0, a0=None, a1=None):
        x0, x1 = max(0, int(cx - r - 1)), min(s.w, int(cx + r + 2))
        y0, y1 = max(0, int(cy - r - 1)), min(s.h, int(cy + r + 2))
        if x1 <= x0 or y1 <= y0:
            return
        yy, xx = np.mgrid[y0:y1, x0:x1]
        d = np.hypot(xx - cx, yy - cy)
        m = (d <= r) & (d >= inner)
        if a0 is not None:
            ang = (np.arctan2(yy - cy, xx - cx) + 2 * PI) % (2 * PI)
            m &= (ang >= a0) & (ang < a1)
        sub = s.a[y0:y1, x0:x1, :3]
        sub[m] = sub[m] * (1 - a) + rgbv(c) * a

    def line(s, pts, c, t=2):
        col = rgbv(c)
        for (x0, y0), (x1, y1) in zip(pts[:-1], pts[1:]):
            n = int(max(abs(x1 - x0), abs(y1 - y0)) * 2) + 2
            xs, ys = np.linspace(x0, x1, n), np.linspace(y0, y1, n)
            for dx in range(-(t // 2), t - t // 2):
                for dy in range(-(t // 2), t - t // 2):
                    xi = np.clip((xs + dx).astype(int), 0, s.w - 1)
                    yi = np.clip((ys + dy).astype(int), 0, s.h - 1)
                    s.a[yi, xi, :3] = col

    def area(s, pts, base, c, a=0.35):
        xs = np.array([p[0] for p in pts], float)
        ys = np.array([p[1] for p in pts], float)
        for x in range(int(xs[0]), int(xs[-1])):
            y = np.interp(x, xs, ys)
            s.rect(x, y, 1, base - y, c, a)

    def textline(s, x, y, w, c, h=5, a=1.0):
        s.rect(x, y, w, h, c, a)

    def para(s, x, y, w, n, c, gap=11, h=5, rng=None):
        rng = rng or random.Random(1)
        for i in range(n):
            ww = w if i < n - 1 else w * rng.uniform(0.3, 0.7)
            s.textline(x, y + i * gap, ww * rng.uniform(0.85, 1.0), c, h)

    def image(s, name):
        img = bpy.data.images.new(name, s.w, s.h, alpha=False)
        img.pixels.foreach_set(np.ascontiguousarray(np.clip(s.a[::-1], 0, 1)).ravel())
        img.pack()
        return img


def tex_wood():
    W = H = 1024
    rng = np.random.default_rng(7)
    rows, ph = 8, 128
    a = np.zeros((H, W, 3), np.float32)
    base = [rgbv("#B89272"), rgbv("#B08B6A"), rgbv("#BE987A"), rgbv("#AA8564"), rgbv("#B48F70"), rgbv("#B98F6C")]
    yy, xx = np.mgrid[0:ph, 0:W]
    for r in range(rows):
        L = int(rng.choice([512, 1024]))
        off = int(rng.integers(0, W))
        seg = ((xx + off) % W) // L
        cols = np.array([base[rng.integers(0, len(base))] * rng.uniform(0.96, 1.04) for _ in range(4)])
        c = cols[seg % 4]
        k = 2 * PI / W
        grain = (0.035 * np.sin(yy * 0.55 + 7 * np.sin(xx * k * 3 + r * 2.1) + seg * 5)
                 + 0.025 * np.sin(xx * k * 40 + yy * 0.2) + rng.normal(0, 0.012, (ph, W)))
        c = c * (1 + grain)[..., None]
        seam = ((xx + off) % L) < 2
        c[seam] *= 0.7
        c[:2] *= 0.75
        a[r * ph:(r + 1) * ph] = c
    cv = Canvas(W, H)
    cv.a[..., :3] = np.clip(a, 0, 1)
    return cv.image("tex_wood_floor")


def tex_code():
    rng = random.Random(3)
    c = Canvas(512, 320, "#1E1F29")
    c.rect(0, 0, 512, 18, "#14151C")
    for i, w in enumerate([70, 60, 80]):
        c.rect(78 + i * 86, 3, w, 15, "#262837" if i else "#2E3144")
    c.rect(0, 18, 72, 302, "#191A23")
    for i in range(20):
        c.textline(8 + (i % 3) * 6, 26 + i * 13, rng.randint(25, 50), "#6C7086", 4)
    pal = ["#FF79C6", "#8BE9FD", "#50FA7B", "#F1FA8C", "#BD93F9", "#C0C4D6", "#C0C4D6", "#FFB86C"]
    for i in range(22):
        y = 26 + i * 13
        c.textline(80, y, 10, "#4B4F66", 4)
        x = 100 + rng.choice([0, 0, 12, 24, 24, 36])
        for _ in range(rng.randint(1, 5)):
            w = rng.randint(14, 60)
            if x + w > 500:
                break
            c.textline(x, y, w, rng.choice(pal), 4)
            x += w + 6
    c.rect(78, 26 + 9 * 13 - 3, 434, 11, "#FFFFFF", 0.06)
    c.rect(0, 306, 512, 14, "#34C759")
    return c.image("tex_screen_code")


def tex_terminal():
    rng = random.Random(4)
    c = Canvas(512, 320, "#0C0E12")
    c.rect(0, 0, 512, 16, "#22252C")
    for i, col in enumerate(["#FF5F57", "#FEBC2E", "#28C840"]):
        c.circle(12 + i * 14, 8, 4, col)
    for i in range(24):
        y = 24 + i * 12
        if i % 5 == 0:
            c.textline(8, y, 14, "#34C759", 4)
            c.textline(26, y, rng.randint(60, 200), "#E6E6E6", 4)
        else:
            c.textline(8, y, rng.randint(40, 300), rng.choice(["#9AA0A6", "#9AA0A6", "#7EE787", "#79C0FF"]), 4)
    c.rect(8, 24 + 24 * 12 - 2, 8, 9, "#E6E6E6")
    return c.image("tex_screen_terminal")


def tex_ci():
    c = Canvas(512, 320, "#0D1117")
    c.rect(0, 0, 512, 28, "#161B22")
    c.textline(12, 11, 120, "#E6EDF3", 6)
    rng = random.Random(5)
    for i in range(10):
        y = 40 + i * 27
        c.rect(8, y, 496, 23, "#161B22")
        ok = i != 3
        c.circle(22, y + 11, 6, "#3FB950" if ok else "#F85149")
        c.textline(36, y + 6, rng.randint(140, 260), "#E6EDF3", 5)
        c.textline(36, y + 14, rng.randint(60, 120), "#7D8590", 3)
        c.rect(430, y + 6, 60, 10, "#238636" if ok else "#DA3633")
    return c.image("tex_screen_ci")


def tex_roadmap():
    rng = random.Random(6)
    c = Canvas(512, 320, "#F7F7FB")
    c.rect(0, 0, 512, 26, "#5856D6")
    c.textline(12, 10, 110, "#FFFFFF", 7)
    for i in range(7):
        x = 110 + i * 57
        c.rect(x, 30, 1, 290, "#DADAE6")
        c.textline(x + 8, 34, 30, "#8E8EA0", 4)
    cols = ["#5856D6", "#30B0C7", "#FF9500", "#34C759", "#FF2D55", "#AF52DE"]
    for i in range(9):
        y = 50 + i * 29
        c.textline(10, y + 6, rng.randint(50, 85), "#55556A", 5)
        x0 = 110 + rng.randint(0, 220)
        c.rect(x0, y, rng.randint(70, 180), 17, cols[i % len(cols)])
    c.rect(110 + 3 * 57 + 20, 30, 2, 290, "#FF3B30")
    return c.image("tex_screen_roadmap")


def tex_doc(name="tex_screen_doc", accent="#5856D6", bg="#FFFFFF"):
    rng = random.Random(len(name))
    c = Canvas(512, 320, "#E9E9EE")
    c.rect(0, 0, 512, 20, "#F6F6F8")
    c.rect(0, 20, 110, 300, "#F2F2F6")
    for i in range(14):
        c.textline(10 + (i % 2) * 8, 32 + i * 16, rng.randint(40, 85), "#9A9AAA", 4)
    c.rect(130, 28, 360, 292, bg)
    c.textline(150, 44, 200, accent, 10)
    y = 70
    for _ in range(4):
        c.para(150, y, 320, rng.randint(3, 5), "#6E6E7E", 11, 4, rng)
        y += 62
        c.textline(150, y - 12, 140, "#2C2C3A", 6)
    return c.image(name)


def tex_daw():
    rng = np.random.default_rng(8)
    c = Canvas(512, 320, "#17151C")
    c.rect(0, 0, 512, 22, "#24212C")
    for i in range(0, 512, 32):
        c.rect(i, 22, 1, 298, "#2C2834")
    cols = ["#AF52DE", "#BF5AF2", "#64D2FF", "#FF9F0A"]
    for t in range(4):
        y0 = 30 + t * 72
        c.rect(0, y0, 70, 64, "#2A2632")
        c.rect(6, y0 + 8, 40, 5, "#D8D2E4")
        c.rect(74, y0, 438, 64, "#1E1B24")
        env = np.abs(np.sin(np.linspace(0, rng.uniform(4, 9), 438))) * (0.5 + 0.5 * rng.random(438))
        for x in range(438):
            if rng.random() < 0.08 and t > 0:
                continue
            a = env[x] * 28 * (0.6 + 0.4 * rng.random())
            c.rect(74 + x, y0 + 32 - a, 1, 2 * a + 1, cols[t])
    c.rect(300, 22, 2, 298, "#FF3B30")
    return c.image("tex_screen_daw")


def tex_social():
    rng = random.Random(9)
    c = Canvas(512, 320, "#FFF5F8")
    c.rect(0, 0, 512, 26, "#FF2D55")
    c.textline(12, 10, 90, "#FFFFFF", 7)
    grads = [("#FF2D55", "#FF9500"), ("#5856D6", "#AF52DE"), ("#30B0C7", "#34C759"),
             ("#FF9500", "#FFCC00"), ("#AF52DE", "#FF2D55"), ("#007AFF", "#30B0C7")]
    for i in range(6):
        x, y = 16 + (i % 3) * 164, 38 + (i // 3) * 140
        c.vgrad(x, y, 150, 100, *grads[i])
        c.circle(x + 40, y + 45, 22, "#FFFFFF", 0.5)
        c.textline(x, y + 106, rng.randint(80, 140), "#444455", 5)
        c.rect(x, y + 118, rng.randint(30, 120), 6, "#FF2D55")
    return c.image("tex_screen_social")


def tex_dashboard(w=1024, h=576, name="tex_screen_dashboard"):
    rng = np.random.default_rng(10)
    c = Canvas(w, h, "#0B1220")
    s = w / 1024
    c.rect(0, 0, w, 40 * s, "#111A2E")
    c.textline(20 * s, 14 * s, 220 * s, "#E8F1FF", 12 * s)
    cols = ["#30B0C7", "#34C759", "#FF9500", "#AF52DE"]
    for i in range(4):
        x = 20 * s + i * 250 * s
        c.rect(x, 56 * s, 230 * s, 96 * s, "#131D33")
        c.textline(x + 16 * s, 72 * s, 90 * s, "#7F8BA6", 7 * s)
        c.textline(x + 16 * s, 94 * s, 120 * s, "#F2F6FF", 26 * s)
        c.rect(x + 16 * s, 132 * s, 198 * s, 6 * s, "#1E2A45")
        c.rect(x + 16 * s, 132 * s, (60 + 40 * i) * s, 6 * s, cols[i])
    # 主折線圖（上升趨勢）
    gx, gy, gw, gh = 20 * s, 168 * s, 620 * s, 390 * s
    c.rect(gx, gy, gw, gh, "#131D33")
    for i in range(6):
        c.rect(gx + 20 * s, gy + 40 * s + i * 60 * s, gw - 40 * s, 1, "#22304F")
    n = 40
    xs = np.linspace(gx + 20 * s, gx + gw - 20 * s, n)
    trend = np.linspace(0.15, 0.9, n) + 0.06 * np.sin(np.arange(n) * 0.9) + rng.normal(0, 0.025, n)
    ys = gy + gh - 20 * s - trend * (gh - 80 * s)
    pts = list(zip(xs, ys))
    c.area(pts, gy + gh - 20 * s, "#30B0C7", 0.25)
    c.line(pts, "#4FD8F0", max(2, int(4 * s)))
    trend2 = np.linspace(0.1, 0.55, n) + 0.04 * np.cos(np.arange(n) * 0.7)
    c.line(list(zip(xs, gy + gh - 20 * s - trend2 * (gh - 80 * s))), "#34C759", max(2, int(3 * s)))
    c.circle(xs[-1], ys[-1], 8 * s, "#FFFFFF")
    # 長條圖
    bx, by, bw, bh = 656 * s, 168 * s, 348 * s, 190 * s
    c.rect(bx, by, bw, bh, "#131D33")
    for i in range(10):
        hh = (40 + 12 * i + rng.uniform(-15, 15)) * s
        c.rect(bx + 20 * s + i * 32 * s, by + bh - 16 * s - hh, 20 * s, hh, "#FF9500" if i == 9 else "#30B0C7")
    # 甜甜圈圖
    dx, dy = 656 * s, 372 * s
    c.rect(dx, dy, 348 * s, 186 * s, "#131D33")
    a0 = 0.0
    for frac, col in [(0.42, "#30B0C7"), (0.26, "#34C759"), (0.18, "#FF9500"), (0.14, "#AF52DE")]:
        c.circle(dx + 95 * s, dy + 93 * s, 70 * s, col, 1.0, 44 * s, a0, a0 + frac * 2 * PI)
        a0 += frac * 2 * PI
    for i, col in enumerate(["#30B0C7", "#34C759", "#FF9500", "#AF52DE"]):
        c.rect(dx + 190 * s, dy + 40 * s + i * 30 * s, 14 * s, 14 * s, col)
        c.textline(dx + 214 * s, dy + 44 * s + i * 30 * s, 100 * s, "#B9C4DD", 7 * s)
    return c.image(name)


def tex_issues():
    rng = random.Random(11)
    c = Canvas(512, 320, "#FFFFFF")
    c.rect(0, 0, 512, 26, "#FF9500")
    c.textline(12, 10, 110, "#FFFFFF", 7)
    c.rect(0, 26, 512, 22, "#F6F8FA")
    for i, w in enumerate([60, 50, 70]):
        c.rect(12 + i * 80, 32, w, 10, "#D0D7DE")
    labels = ["#D73A4A", "#0075CA", "#A2EEEF", "#7057FF", "#FF9500", "#008672"]
    for i in range(10):
        y = 54 + i * 26
        c.rect(0, y + 24, 512, 1, "#E5E7EB")
        c.circle(20, y + 11, 6, "#1A7F37" if i % 4 else "#8250DF")
        c.textline(34, y + 6, rng.randint(140, 260), "#1F2328", 5)
        c.textline(34, y + 15, rng.randint(60, 120), "#8C959F", 3)
        x = 320
        for _ in range(rng.randint(1, 2)):
            c.rect(x, y + 5, 52, 12, rng.choice(labels))
            x += 58
        c.circle(486, y + 11, 8, rng.choice(["#FFB86C", "#8BE9FD", "#BD93F9"]))
    return c.image("tex_screen_issues")


def tex_okr():
    c = Canvas(512, 320, "#FBFAFF")
    c.rect(0, 0, 512, 54, "#AF52DE")
    c.textline(20, 18, 180, "#FFFFFF", 12)
    c.textline(20, 36, 120, "#EBD7F7", 6)
    cols = ["#AF52DE", "#5856D6", "#34C759"]
    for i in range(3):
        y = 76 + i * 78
        c.rect(20, y, 472, 64, "#FFFFFF")
        c.rect(20, y, 4, 64, cols[i])
        c.textline(36, y + 12, 220, "#2B2340", 8)
        c.rect(36, y + 38, 300, 10, "#ECE6F5")
        c.rect(36, y + 38, [240, 170, 120][i], 10, cols[i])
        c.circle(440, y + 32, 22, cols[i], 1.0, 15)
    return c.image("tex_screen_okr")


def tex_whiteboard():
    c = Canvas(1024, 512, "#FDFDFB")
    blue, red, black, grn = "#1F4FB8", "#D62F2F", "#24242A", "#1E8A3E"
    # 目標靶
    for r, col in [(120, red), (85, "#FDFDFB"), (78, red), (45, "#FDFDFB"), (38, red)]:
        c.circle(170, 260, r, col, 1.0, r - 6 if col != "#FDFDFB" else 0)
    c.line([(60, 120), (170, 260)], black, 4)
    c.line([(170, 260), (150, 240), (172, 238)], black, 4)
    c.textline(60, 420, 220, purple_hex := "#AF52DE", 14)
    # 季度欄位
    for i in range(4):
        x = 340 + i * 165
        c.line([(x, 60), (x + 150, 60), (x + 150, 330), (x, 330), (x, 60)], blue, 3)
        c.textline(x + 20, 76, 60, blue, 12)
        for j in range(3):
            sticky = ["#FFE066", "#FF8FB1", "#8FD3FF", "#A8E6A1"][(i + j) % 4]
            c.rect(x + 14 + (j % 2) * 66, 110 + j * 66, 58, 54, sticky)
            c.textline(x + 22 + (j % 2) * 66, 124 + j * 66, 40, "#555555", 4)
    # 成長折線
    pts = [(360, 470), (460, 440), (560, 452), (660, 400), (760, 380), (860, 340), (960, 300)]
    c.line(pts, grn, 5)
    c.line([(940, 296), (962, 298), (952, 318)], grn, 5)
    c.line([(340, 480), (990, 480)], black, 3)
    c.line([(340, 480), (340, 360)], black, 3)
    return c.image("tex_whiteboard_goals")


def tex_slide():
    c = Canvas(1024, 576, "#FFFFFF")
    c.vgrad(0, 0, 1024, 120, "#5856D6", "#AF52DE")
    c.textline(60, 40, 420, "#FFFFFF", 26)
    c.textline(60, 80, 260, "#E6E0FA", 10)
    for i in range(4):
        c.circle(76, 190 + i * 70, 9, "#5856D6")
        c.textline(100, 182 + i * 70, 320 - i * 30, "#3A3A4A", 14)
    bx, by = 560, 470
    for i, hh in enumerate([90, 140, 120, 200, 260, 300]):
        c.rect(bx + i * 70, by - hh, 46, hh, "#30B0C7" if i < 5 else "#FF9500")
    c.rect(540, by, 440, 3, "#3A3A4A")
    return c.image("tex_screen_slide")


def tex_poster(i):
    sets = [("#FF2D55", "#FF9500", "#FFFFFF"), ("#5856D6", "#30B0C7", "#FFE066"), ("#1C1C28", "#AF52DE", "#FF2D55")]
    a, b, acc = sets[i]
    c = Canvas(256, 384, a)
    c.vgrad(0, 0, 256, 384, a, b)
    if i == 0:
        c.circle(128, 150, 90, "#FFFFFF", 0.25)
        c.circle(128, 150, 55, acc, 0.9)
        c.rect(108, 120, 40, 60, a)
    elif i == 1:
        for k in range(5):
            c.rect(30 + k * 42, 250 - k * 35, 30, 30 + k * 35, acc)
    else:
        for k in range(4):
            c.circle(128, 170, 100 - k * 22, [acc, "#FFFFFF", b, acc][k], 0.9)
    c.textline(24, 300, 200, "#FFFFFF", 18)
    c.textline(24, 328, 150, acc, 8)
    c.textline(24, 346, 110, "#FFFFFF", 6, 0.7)
    return c.image(f"tex_poster_{i + 1}")


def tex_analytics():
    rng = np.random.default_rng(12)
    c = Canvas(512, 320, "#F4F8FA")
    c.rect(0, 0, 512, 24, "#30B0C7")
    c.textline(10, 9, 100, "#FFFFFF", 6)
    c.rect(10, 34, 330, 176, "#FFFFFF")
    xs = np.linspace(24, 326, 30)
    ys = 190 - np.linspace(20, 140, 30) - 10 * np.sin(np.arange(30)) + rng.normal(0, 4, 30)
    c.area(list(zip(xs, ys)), 200, "#30B0C7", 0.25)
    c.line(list(zip(xs, ys)), "#1A93A9", 3)
    c.rect(350, 34, 152, 82, "#FFFFFF")
    c.textline(362, 48, 60, "#8A9AA5", 5)
    c.textline(362, 66, 100, "#123040", 18)
    c.rect(350, 128, 152, 82, "#FFFFFF")
    c.circle(426, 169, 32, "#30B0C7", 1, 20, 0, 4.2)
    c.circle(426, 169, 32, "#FF9500", 1, 20, 4.2, 2 * PI)
    c.rect(10, 220, 492, 92, "#FFFFFF")
    for i in range(5):
        c.textline(22, 232 + i * 16, 160, "#5B6B76", 5)
        c.rect(300, 232 + i * 16, int(rng.uniform(40, 190)), 7, "#7FD3E3")
    return c.image("tex_screen_analytics")


# ---------------------------------------------------------------------------
# 網格建構器
# ---------------------------------------------------------------------------
def MT(loc=(0, 0, 0), rot=(0, 0, 0), scale=(1, 1, 1)):
    return (Matrix.Translation(Vector(loc)) @ Euler(rot, "XYZ").to_matrix().to_4x4()
            @ Matrix.Diagonal((scale[0], scale[1], scale[2], 1.0)))


class MB:
    """把多個基本形狀組成一個網格物件（多材質）。座標為物件本地座標。"""

    def __init__(self):
        self.bm = bmesh.new()
        self.uv = self.bm.loops.layers.uv.verify()
        self.mats = []

    def mi(self, m):
        if m not in self.mats:
            self.mats.append(m)
        return self.mats.index(m)

    def _apply(self, verts, m, smooth=False, nonsmooth_len=None):
        i = self.mi(m)
        for f in {f for v in verts for f in v.link_faces}:
            f.material_index = i
            f.smooth = smooth and (nonsmooth_len is None or len(f.verts) != nonsmooth_len)

    def box(self, loc, size, m, rot=(0, 0, 0), bevel=0.0, seg=2):
        r = bmesh.ops.create_cube(self.bm, size=1.0, matrix=MT(loc, rot, size), calc_uvs=True)
        v = r["verts"]
        self._apply(v, m)
        if bevel > 0:
            edges = list({e for x in v for e in x.link_edges})
            bmesh.ops.bevel(self.bm, geom=edges, offset=bevel, segments=seg, affect="EDGES",
                            profile=0.5, clamp_overlap=True)
        return v

    def cyl(self, loc, r, depth, m, rot=(0, 0, 0), r2=None, seg=20, smooth=True, scale=(1, 1, 1), matrix=None):
        mtx = matrix if matrix is not None else MT(loc, rot, scale)
        rr = bmesh.ops.create_cone(self.bm, cap_ends=True, cap_tris=False, segments=seg, radius1=r,
                                   radius2=r if r2 is None else r2, depth=depth, matrix=mtx, calc_uvs=True)
        self._apply(rr["verts"], m, smooth, seg)
        return rr["verts"]

    def sphere(self, loc, r, m, scale=(1, 1, 1), rot=(0, 0, 0), u=16, v=8, smooth=True):
        rr = bmesh.ops.create_uvsphere(self.bm, u_segments=u, v_segments=v, radius=r,
                                       matrix=MT(loc, rot, scale), calc_uvs=True)
        self._apply(rr["verts"], m, smooth)

    def quad(self, pts, m, uvs=((0, 0), (1, 0), (1, 1), (0, 1))):
        vs = [self.bm.verts.new(Vector(p)) for p in pts]
        f = self.bm.faces.new(vs)
        f.material_index = self.mi(m)
        for lp, uv in zip(f.loops, uvs):
            lp[self.uv].uv = uv
        return f

    def screen(self, center, w, h, m, rot=(0, 0, 0)):
        """在本地 XZ 平面建立朝 -Y 的矩形（UV 0..1），再以 rot/center 變換"""
        X = MT(center, rot)
        pts = [X @ Vector(p) for p in ((-w / 2, 0, -h / 2), (w / 2, 0, -h / 2), (w / 2, 0, h / 2), (-w / 2, 0, h / 2))]
        return self.quad(pts, m)

    def torus(self, loc, R, r, m, rot=(0, 0, 0), seg=24, rseg=8, arc=2 * PI, smooth=True):
        X = MT(loc, rot)
        closed = abs(arc - 2 * PI) < 1e-6
        n = seg if closed else seg + 1
        rings = []
        for i in range(n):
            a = arc * i / seg
            ring = []
            for j in range(rseg):
                b = 2 * PI * j / rseg
                p = Vector(((R + r * math.cos(b)) * math.cos(a), (R + r * math.cos(b)) * math.sin(a), r * math.sin(b)))
                ring.append(self.bm.verts.new(X @ p))
            rings.append(ring)
        idx, faces = self.mi(m), []
        for i in range(seg):
            r0, r1 = rings[i], rings[(i + 1) % n]
            for j in range(rseg):
                f = self.bm.faces.new((r0[j], r1[j], r1[(j + 1) % rseg], r0[(j + 1) % rseg]))
                f.material_index, f.smooth = idx, smooth
                faces.append(f)
        bmesh.ops.recalc_face_normals(self.bm, faces=faces)

    def finish(self, name):
        self.bm.normal_update()
        me = bpy.data.meshes.new(name)
        self.bm.to_mesh(me)
        self.bm.free()
        for m in self.mats:
            me.materials.append(m)
        return me


def obj(name, data, parent=None, loc=(0, 0, 0), rot=(0, 0, 0), cutaway=False, coll=None):
    o = bpy.data.objects.new(uniq(name), data)
    (coll or COLL["cur"]).objects.link(o)
    if parent is not None:
        o.parent = parent
    o.location = loc
    o.rotation_euler = rot
    if cutaway:
        o["cutaway"] = True
    return o


def empty(name, parent=None, loc=(0, 0, 0), rot=(0, 0, 0), kind="PLAIN_AXES", size=0.5, coll=None):
    o = obj(name, None, parent, loc, rot, coll=coll)
    o.empty_display_type = kind
    o.empty_display_size = size
    return o


def build(name, fn, parent=None, loc=(0, 0, 0), rot=(0, 0, 0), cutaway=False, cache=None):
    """fn(mb) 填內容；cache 鍵相同時共用網格資料（glTF 會共用 mesh）"""
    if cache and cache in MESH_CACHE:
        me = MESH_CACHE[cache]
    else:
        mb = MB()
        fn(mb)
        me = mb.finish(name)
        if cache:
            MESH_CACHE[cache] = me
    return obj(name, me, parent, loc, rot, cutaway)


MESH_CACHE = {}

FONT = None
try:
    FONT = bpy.data.fonts.load("/System/Library/Fonts/Supplemental/Arial Unicode.ttf")
except Exception as e:  # noqa
    print("CJK font not found, fallback to builtin:", e)


def text(name, body, parent, loc, rot, size, m, extrude=0.003):
    cu = bpy.data.curves.new("tmp_text", "FONT")
    cu.body = body
    cu.size = size
    cu.extrude = extrude
    cu.align_x = "CENTER"
    cu.align_y = "CENTER"
    cu.resolution_u = 3
    if FONT and any(ord(ch) > 127 for ch in body):
        cu.font = FONT
    tmp = bpy.data.objects.new("tmp_text", cu)
    SC.collection.objects.link(tmp)
    dg = bpy.context.evaluated_depsgraph_get()
    me = bpy.data.meshes.new_from_object(tmp.evaluated_get(dg))
    bpy.data.objects.remove(tmp)
    bpy.data.curves.remove(cu)
    me.name = name
    me.materials.clear()
    me.materials.append(m)
    return obj(name, me, parent, loc, rot)


# ---------------------------------------------------------------------------
# 共用材質
# ---------------------------------------------------------------------------
IMG = {}
IMG["wood"] = tex_wood()
for fn in (tex_code, tex_terminal, tex_ci, tex_roadmap, tex_daw, tex_social, tex_issues, tex_okr,
           tex_whiteboard, tex_slide, tex_analytics):
    im = fn()
    IMG[im.name] = im
IMG["tex_screen_doc"] = tex_doc()
IMG["tex_screen_spec"] = tex_doc("tex_screen_spec", "#5856D6", "#FFFFFF")
IMG["tex_screen_script"] = tex_doc("tex_screen_script", "#AF52DE", "#FFFDF5")
IMG["tex_screen_dashboard"] = tex_dashboard()
for i in range(3):
    im = tex_poster(i)
    IMG[im.name] = im

M = dict(
    floor=mat("floor_wood", (1, 1, 1), 0.42, img=IMG["wood"], coat=0.15),
    wall=mat("wall_paint_warm", "#EFE9E1", 0.85),
    wall_accent=mat("wall_paint_sage", "#A9B7A0", 0.85),
    ceiling=mat("ceiling_white", "#F4F2EE", 0.9),
    baseboard=mat("baseboard_white", "#E7E2DA", 0.6),
    glass=mat("glass_clear", "#D9ECF2", 0.04, alpha=0.16, spec=0.8),
    glass_dark=mat("glass_smoke", "#2A2E35", 0.08, alpha=0.45),
    metal_dark=mat("metal_black", "#2A2B2E", 0.38, 0.85),
    metal=mat("metal_aluminium", "#B9BCC0", 0.3, 1.0),
    chrome=mat("metal_chrome", "#DADDE0", 0.12, 1.0),
    black=mat("plastic_black", "#1B1B1E", 0.45),
    plastic_gray=mat("plastic_gray", "#5A5D63", 0.5),
    plastic_white=mat("plastic_white", "#ECECEA", 0.4),
    desk_wood=mat("wood_oak", "#C8A37A", 0.45),
    walnut=mat("wood_walnut", "#6B4A33", 0.4, coat=0.3),
    felt=mat("felt_warm_gray", "#B8B1A7", 0.95),
    fabric_gray=mat("fabric_charcoal", "#3E4045", 0.9),
    fabric_sofa=mat("fabric_oat", "#CDBFA9", 0.92),
    fabric_teal=mat("fabric_deep_teal", "#2F5E63", 0.9),
    fabric_rust=mat("fabric_rust", "#B1603C", 0.9),
    leather=mat("leather_cognac", "#7A4A2C", 0.5),
    paper=mat("paper_white", "#F7F6F2", 0.8),
    paper2=mat("paper_cream", "#EDE6D3", 0.85),
    cardboard=mat("cardboard", "#B58E61", 0.9),
    pot=mat("ceramic_white", "#EDEAE4", 0.35),
    pot2=mat("terracotta", "#B5694A", 0.75),
    pot3=mat("ceramic_charcoal", "#38393B", 0.4),
    soil=mat("soil", "#3B2A1E", 1.0),
    leaf=mat("leaf_green", "#3E7A3A", 0.6),
    leaf2=mat("leaf_deep", "#2C5E2E", 0.6),
    leaf3=mat("leaf_light", "#6BA34F", 0.6),
    bark=mat("bark", "#5A4232", 0.9),
    screen_off=mat("screen_black", "#0B0C0E", 0.15),
    whiteboard=mat("whiteboard_surface", "#F6F6F4", 0.25),
    cork=mat("cork", "#C49A68", 0.95),
    carpet=mat("carpet_slate", "#59626B", 1.0),
    mat_entry=mat("carpet_entry", "#3B3B3E", 1.0),
    moss=mat("moss_wall", "#5D7B3C", 1.0),
    foam=mat("foam_charcoal", "#2B2B30", 1.0),
    led_white=mat("led_white", "#FFFFFF", 0.3, emit="#FFF4E0", strength=6.0),
    lamp_warm=mat("lamp_warm", "#FFE2B0", 0.4, emit="#FFD59A", strength=8.0),
    panel_light=mat("ceiling_panel_light", "#FFFFFF", 0.3, emit="#FFF6EA", strength=5.0),
    red_led=mat("led_red", "#FF3B30", 0.3, emit="#FF3B30", strength=6.0),
    green_led=mat("led_green", "#34C759", 0.3, emit="#34C759", strength=6.0),
    blue_led=mat("led_blue", "#0A84FF", 0.3, emit="#0A84FF", strength=6.0),
    pcb=mat("pcb_green", "#1F6B3A", 0.5),
    copper=mat("copper", "#C17F4A", 0.3, 1.0),
    grass=mat("ground_grass", "#7C9B63", 0.95),
    plaza=mat("ground_paving", "#BDB8AF", 0.85),
    duck=mat("rubber_duck", "#FFD60A", 0.45),
    orange_plastic=mat("plastic_orange", "#FF8A00", 0.5),
    coffee=mat("coffee", "#2B1A10", 0.2),
    fruit_r=mat("fruit_red", "#C8302B", 0.35),
    fruit_y=mat("fruit_yellow", "#E9C23A", 0.4),
    fruit_g=mat("fruit_green", "#7FB241", 0.4),
)
STICKY = [mat("sticky_yellow", "#FFE066", 0.8), mat("sticky_pink", "#FF8FB1", 0.8),
          mat("sticky_blue", "#8FD3FF", 0.8), mat("sticky_green", "#A8E6A1", 0.8),
          mat("sticky_orange", "#FFB35C", 0.8)]
BOOK = [mat(f"book_{i}", c, 0.7) for i, c in enumerate(
    ["#8C2F39", "#2F4B7C", "#3C6E47", "#D9A441", "#5C4B8A", "#B5532C", "#2E2E33", "#E8DCC4", "#4F8A8B", "#A63D6B"])]


def screen_mat(key, strength=2.2):
    return mat(f"screen_{key}", (1, 1, 1), 0.2, img=IMG[key], emit_img=True, strength=strength)


def role_mats(rid):
    c = role_lin(rid)
    return dict(
        solid=mat(f"role_{rid}_solid", c, 0.45),
        felt=mat(f"role_{rid}_felt", c, 0.95),
        rug=mat(f"role_{rid}_rug", mix(c, lin("#EFE6DA"), 0.35), 1.0),
        led=mat(f"role_{rid}_led", c, 0.3, emit=c, strength=7.0),
        sign=mat(f"role_{rid}_sign", c, 0.4, emit=c, strength=0.6),
        text=mat("sign_text_white", "#FFFFFF", 0.4, emit="#FFFFFF", strength=1.5),
    )


# ---------------------------------------------------------------------------
# 道具建構函式（本地座標；工位家具一律「人面向 +Y」）
# ---------------------------------------------------------------------------
def f_chair(fabric, exec_=False, simple=False):
    def fn(mb):
        if simple:  # 會議/休息椅：四腳
            for sx in (-0.2, 0.2):
                for sy in (-0.18, 0.2):
                    mb.cyl((sx, sy, 0.22), 0.015, 0.44, M["metal_dark"], seg=10)
            mb.box((0, 0.02, 0.46), (0.46, 0.46, 0.05), fabric, bevel=0.02)
            mb.box((0, -0.22, 0.72), (0.44, 0.04, 0.42), fabric, rot=(0.1, 0, 0), bevel=0.018)
            return
        for i in range(5):
            a = i * 2 * PI / 5 + PI / 2
            mb.box((0.15 * math.cos(a), 0.15 * math.sin(a), 0.075), (0.30, 0.04, 0.03), M["metal_dark"], rot=(0, 0, a))
            mb.sphere((0.29 * math.cos(a), 0.29 * math.sin(a), 0.03), 0.03, M["black"], u=10, v=6)
        mb.cyl((0, 0, 0.27), 0.026, 0.34, M["chrome"], seg=14)
        mb.box((0, 0.02, 0.48), (0.5, 0.48, 0.08), fabric, bevel=0.03, seg=3)
        bh = 0.68 if exec_ else 0.5
        mb.box((0, -0.25, 0.55), (0.06, 0.04, 0.2), M["metal_dark"])
        mb.box((0, -0.27, 0.56 + bh / 2 + 0.05), (0.47, 0.07, bh), fabric, rot=(0.12, 0, 0), bevel=0.03, seg=3)
        for sx in (-0.27, 0.27):
            mb.box((sx, -0.02, 0.6), (0.03, 0.03, 0.22), M["metal_dark"])
            mb.box((sx, 0.0, 0.71), (0.06, 0.26, 0.03), M["black"], bevel=0.01)
    return fn


def f_desk(w=1.6, d=0.8, top=None, pedestal=True):
    top = top or M["desk_wood"]

    def fn(mb):
        cy = 0.15 + d / 2
        mb.box((0, cy, 0.725), (w, d, 0.03), top, bevel=0.004)
        for sx in (-w / 2 + 0.06, w / 2 - 0.06):
            mb.box((sx, cy, 0.02), (0.05, d - 0.1, 0.04), M["metal_dark"])
            mb.box((sx, cy, 0.37), (0.05, 0.05, 0.67), M["metal_dark"])
            mb.box((sx, cy, 0.69), (0.05, d - 0.14, 0.04), M["metal_dark"])
        mb.box((0, cy + d / 2 - 0.08, 0.5), (w - 0.18, 0.015, 0.34), M["metal_dark"])
        if pedestal:
            mb.box((w / 2 - 0.32, cy + 0.04, 0.3), (0.4, d - 0.2, 0.56), M["plastic_white"], bevel=0.008)
            for k in range(3):
                mb.box((w / 2 - 0.32, cy - d / 2 + 0.135, 0.12 + k * 0.18), (0.12, 0.012, 0.012), M["metal"])
    return fn


def f_monitor(w=0.6, h=0.34, smat=None):
    def fn(mb):
        mb.box((0, 0.06, 0.006), (0.24, 0.17, 0.012), M["metal_dark"], bevel=0.003)
        zc = 0.13 + h / 2
        mb.box((0, 0.075, (zc + 0.01) / 2), (0.045, 0.025, zc), M["metal_dark"])
        mb.box((0, 0.02, zc), (w + 0.02, 0.025, h + 0.02), M["black"], bevel=0.004)
        mb.screen((0, 0.0065, zc), w, h, smat or M["screen_off"])
    return fn


def f_keyboard(mech=False, accent=None):
    def fn(mb):
        if not mech:
            mb.box((0, 0, 0.009), (0.43, 0.13, 0.018), M["metal"], bevel=0.003)
            mb.box((0, 0.0, 0.019), (0.40, 0.11, 0.003), M["plastic_white"])
            mb.box((0.33, 0.0, 0.012), (0.06, 0.1, 0.024), M["plastic_white"], bevel=0.01)
            return
        mb.box((0, 0, 0.015), (0.37, 0.13, 0.03), M["metal_dark"], bevel=0.004)
        for r in range(5):
            for c in range(15):
                x = -0.165 + c * 0.0235
                y = -0.048 + r * 0.024
                if r == 0 and 3 < c < 10:
                    continue
                km = accent if (r == 4 and c == 0) or (r == 2 and c == 14) else (M["plastic_gray"] if c in (0, 14) else M["plastic_white"])
                mb.box((x, y, 0.036), (0.019, 0.019, 0.012), km, bevel=0.002, seg=1)
        mb.box((0, -0.048, 0.036), (0.13, 0.019, 0.012), M["plastic_white"], bevel=0.002, seg=1)
    return fn


def f_mouse(mb, loc):
    mb.sphere((loc[0], loc[1], loc[2] + 0.012), 0.03, M["black"], scale=(0.65, 1.0, 0.45), u=12, v=8)


def f_mug(mb, loc, m):
    x, y, z = loc
    mb.cyl((x, y, z + 0.0475), 0.04, 0.095, m, seg=18)
    mb.cyl((x, y, z + 0.093), 0.034, 0.004, M["coffee"], seg=18, smooth=False)
    mb.torus((x + 0.045, y, z + 0.05), 0.022, 0.006, m, rot=(PI / 2, 0, 0), seg=12, rseg=6)


def f_plant(kind="bush", seed=1, scale=1.0, pot=None):
    def fn(mb):
        rng = random.Random(seed)
        s = scale
        pm = pot or M["pot"]
        if kind == "desk":
            mb.cyl((0, 0, 0.05), 0.055, 0.1, pm, r2=0.065, seg=16)
            mb.cyl((0, 0, 0.098), 0.058, 0.004, M["soil"], seg=16, smooth=False)
            for i in range(9):
                a, t = rng.uniform(0, 2 * PI), rng.uniform(0.5, 1.2)
                R = Euler((t, 0, a)).to_matrix()
                c = Vector((0, 0, 0.1)) + R @ Vector((0, 0.05, 0))
                mb.sphere(c, 0.05, M["leaf3"], scale=(0.35, 1, 0.12), rot=(t, 0, a), u=8, v=4)
            return
        ph = 0.38 * s
        mb.cyl((0, 0, ph / 2), 0.15 * s, ph, pm, r2=0.19 * s, seg=20)
        mb.cyl((0, 0, ph - 0.03 * s), 0.175 * s, 0.01, M["soil"], seg=20, smooth=False)
        top = ph - 0.03 * s
        if kind == "tall":
            mb.cyl((0, 0, top + 0.55 * s), 0.02 * s, 1.1 * s, M["bark"], seg=8)
            for i in range(26):
                a, t = rng.uniform(0, 2 * PI), rng.uniform(-0.3, 0.9)
                h = top + rng.uniform(0.45, 1.25) * s
                L = rng.uniform(0.11, 0.16) * s
                R = Euler((t, 0, a)).to_matrix()
                c = Vector((0, 0, h)) + R @ Vector((0, L * 0.9, 0))
                mb.sphere(c, L, rng.choice([M["leaf"], M["leaf2"]]), scale=(0.62, 1, 0.1), rot=(t, 0, a), u=8, v=4)
        elif kind == "snake":
            for i in range(14):
                a = rng.uniform(0, 2 * PI)
                L = rng.uniform(0.35, 0.65) * s
                t = PI / 2 - rng.uniform(0.0, 0.25)
                R = Euler((t, 0, a)).to_matrix()
                c = Vector((0.05 * math.cos(a), 0.05 * math.sin(a), top)) + R @ Vector((0, L, 0))
                mb.sphere(c, L, rng.choice([M["leaf2"], M["leaf3"]]), scale=(0.09, 1, 0.025), rot=(t, 0, a), u=8, v=4)
        else:  # bush
            for i in range(34):
                a, t = rng.uniform(0, 2 * PI), rng.uniform(0.25, 1.3)
                L = rng.uniform(0.14, 0.22) * s
                R = Euler((t, 0, a)).to_matrix()
                c = Vector((0, 0, top + rng.uniform(0.0, 0.12) * s)) + R @ Vector((0, L * 1.1, 0))
                mb.sphere(c, L, rng.choice([M["leaf"], M["leaf2"], M["leaf3"]]), scale=(0.45, 1, 0.08), rot=(t, 0, a), u=8, v=4)
    return fn


def f_sofa(L=2.0, fabric=None, n=2, legs=None):
    fab = fabric or M["fabric_sofa"]

    def fn(mb):
        for sx in (-L / 2 + 0.08, L / 2 - 0.08):
            for sy in (-0.33, 0.33):
                mb.cyl((sx, sy, 0.05), 0.022, 0.1, legs or M["walnut"], r2=0.016, seg=10)
        mb.box((0, 0, 0.21), (L, 0.85, 0.22), fab, bevel=0.03, seg=3)
        cw = (L - 0.3) / n
        for i in range(n):
            x = -L / 2 + 0.15 + cw * (i + 0.5)
            mb.box((x, 0.07, 0.385), (cw - 0.012, 0.66, 0.13), fab, bevel=0.045, seg=3)
            mb.box((x, -0.25, 0.65), (cw - 0.02, 0.18, 0.42), fab, rot=(-0.16, 0, 0), bevel=0.06, seg=3)
        mb.box((0, -0.36, 0.55), (L - 0.02, 0.14, 0.55), fab, bevel=0.03, seg=3)
        for sx in (-L / 2 + 0.075, L / 2 - 0.075):
            mb.box((sx, 0.0, 0.42), (0.15, 0.85, 0.42), fab, bevel=0.04, seg=3)
    return fn


def f_coffee_table(w=1.1, d=0.6, h=0.4, top=None, round_=False):
    def fn(mb):
        t = top or M["walnut"]
        if round_:
            mb.cyl((0, 0, h - 0.02), w / 2, 0.04, t, seg=32)
            mb.cyl((0, 0, (h - 0.04) / 2), 0.04, h - 0.04, M["metal_dark"], seg=12)
            mb.cyl((0, 0, 0.01), w * 0.3, 0.02, M["metal_dark"], seg=24)
            return
        mb.box((0, 0, h - 0.02), (w, d, 0.04), t, bevel=0.006)
        for sx in (-w / 2 + 0.06, w / 2 - 0.06):
            for sy in (-d / 2 + 0.06, d / 2 - 0.06):
                mb.cyl((sx, sy, (h - 0.04) / 2), 0.016, h - 0.04, M["metal_dark"], seg=10)
    return fn


def f_books(mb, x0, x1, y, z, depth=0.22, rng=None, lean=True):
    rng = rng or random.Random(1)
    x = x0
    while x < x1 - 0.03:
        t = rng.uniform(0.018, 0.045)
        h = rng.uniform(0.18, 0.28)
        if x + t > x1:
            break
        if rng.random() < 0.07 and lean:
            x += 0.05
            continue
        mb.box((x + t / 2, y, z + h / 2), (t, depth * rng.uniform(0.85, 1.0), h), rng.choice(BOOK), bevel=0.002, seg=1)
        x += t + 0.002


def f_bookshelf(w=1.0, d=0.35, h=1.9, shelves=5, seed=1, wood=None, fill=0.9):
    def fn(mb):
        wd = wood or M["desk_wood"]
        rng = random.Random(seed)
        mb.box((-w / 2 + 0.01, 0, h / 2), (0.02, d, h), wd)
        mb.box((w / 2 - 0.01, 0, h / 2), (0.02, d, h), wd)
        mb.box((0, d / 2 - 0.005, h / 2), (w, 0.01, h), wd)
        for i in range(shelves + 1):
            z = 0.04 + i * (h - 0.06) / shelves
            mb.box((0, 0, z), (w - 0.04, d, 0.025), wd)
            if i < shelves and rng.random() < fill:
                xe = rng.uniform(0.6, 1.0) * (w / 2 - 0.03)
                f_books(mb, -w / 2 + 0.03, xe, 0.0, z + 0.0125, d * 0.8, rng)
                if xe < w / 2 - 0.2:
                    k = rng.randint(2, 5)
                    for j in range(k):
                        mb.box((w / 2 - 0.15, 0, z + 0.025 + j * 0.03), (0.22, d * 0.7, 0.028), rng.choice(BOOK), rot=(0, 0, rng.uniform(-0.1, 0.1)))
    return fn


def f_paper_stack(mb, loc, n, rng, size=(0.21, 0.297)):
    x, y, z = loc
    for i in range(n):
        mb.box((x + rng.uniform(-0.01, 0.01), y + rng.uniform(-0.01, 0.01), z + 0.003 + i * 0.006),
               (size[0], size[1], 0.005), rng.choice([M["paper"], M["paper"], M["paper2"]]), rot=(0, 0, rng.uniform(-0.08, 0.08)))


def f_lamp_desk(mb, loc, m):
    x, y, z = loc
    mb.cyl((x, y, z + 0.01), 0.07, 0.02, m, seg=20)
    mb.cyl((x, y, z + 0.2), 0.008, 0.38, M["metal_dark"], rot=(0, 0.25, 0), seg=8)
    mb.cyl((x + 0.1, y, z + 0.4), 0.008, 0.25, M["metal_dark"], rot=(0, 1.3, 0), seg=8)
    mb.cyl((x + 0.21, y, z + 0.37), 0.055, 0.08, m, r2=0.02, seg=16)
    mb.cyl((x + 0.21, y, z + 0.329), 0.045, 0.004, M["lamp_warm"], seg=16, smooth=False)


def f_whiteboard(w, h, surf):
    """牆面白板，朝 -Y，本地原點在白板中心"""
    def fn(mb):
        mb.box((0, 0.012, 0), (w + 0.04, 0.02, h + 0.04), M["metal"], bevel=0.005)
        mb.screen((0, -0.0005, 0), w, h, surf)
        mb.box((0, -0.04, -h / 2 - 0.02), (w * 0.6, 0.07, 0.02), M["metal"])
        for i, c in enumerate([M["black"], M["red_led"], M["blue_led"]]):
            mb.cyl((-0.15 + i * 0.08, -0.05, -h / 2 + 0.0), 0.008, 0.12, c, rot=(0, PI / 2, 0), seg=8)
    return fn


def f_pendant(mb, loc, top=CEIL, shade=None, r=0.22):
    x, y, z = loc
    mb.cyl((x, y, (top + z) / 2), 0.004, top - z, M["metal_dark"], seg=6)
    mb.cyl((x, y, top - 0.01), 0.05, 0.02, M["metal_dark"], seg=12)
    mb.cyl((x, y, z - 0.09), r, 0.18, shade or M["metal_dark"], r2=0.04, seg=24)
    mb.cyl((x, y, z - 0.181), r * 0.9, 0.004, M["lamp_warm"], seg=24, smooth=False)


# ---------------------------------------------------------------------------
# 燈光 / 攝影機
# ---------------------------------------------------------------------------
LIGHT_COLL = new_coll("lighting")
CAM_COLL = new_coll("cameras")


def light(name, kind, loc, energy, color=(1, 0.96, 0.9), size=0.5, size_y=None, rot=(0, 0, 0), parent=None, shadow=True):
    L = bpy.data.lights.new(name, kind)
    L.energy = energy
    L.color = color
    if kind == "AREA":
        L.shape = "RECTANGLE"
        L.size = size
        L.size_y = size_y or size
    elif kind in ("POINT", "SPOT"):
        L.shadow_soft_size = size
    L.use_shadow = shadow
    return obj(name, L, parent, loc, rot, coll=LIGHT_COLL)


def look_rot(loc, target):
    return (Vector(target) - Vector(loc)).to_track_quat("-Z", "Y").to_euler()


def camera(name, loc, target, lens=24, ortho=None):
    c = bpy.data.cameras.new(name)
    c.lens = lens
    c.clip_start, c.clip_end = 0.05, 200
    if ortho:
        c.type = "ORTHO"
        c.ortho_scale = ortho
    return obj(name, c, None, loc, look_rot(loc, target), coll=CAM_COLL)


# ---------------------------------------------------------------------------
# 建築：地板、牆、窗、天花板
# ---------------------------------------------------------------------------
ARCH = new_coll("architecture")
set_coll(ARCH)
arch_root = empty("office_root", size=1.0)


def f_floor(mb):
    t = 1.44  # 木紋貼圖一個循環 = 1.44m
    X, Y = FLOOR_X, FLOOR_Y
    mb.quad([(-X, -Y, 0), (X, -Y, 0), (X, Y, 0), (-X, Y, 0)], M["floor"],
            uvs=[(-X / t, -Y / t), (X / t, -Y / t), (X / t, Y / t), (-X / t, Y / t)])


build("floor", f_floor, arch_root)


def f_ground(mb):
    mb.quad([(-60, -60, -0.03), (60, -60, -0.03), (60, 60, -0.03), (-60, 60, -0.03)], M["grass"])
    mb.box((0, 11.5, -0.02), (40, 4.6, 0.02), M["plaza"])
    mb.box((18.5, 0, -0.02), (4.6, 28, 0.02), M["plaza"])


build("exterior_ground", f_ground, arch_root)

WT = 0.2  # 外牆厚度


def f_wall_north(mb):  # 北面落地窗
    y = FLOOR_Y + 0.06
    mb.box((0, y, 0.04), (2 * FLOOR_X + 0.4, 0.14, 0.08), M["metal_dark"])
    mb.box((0, y, CEIL - 0.05), (2 * FLOOR_X + 0.4, 0.14, 0.1), M["metal_dark"])
    mb.box((0, y + 0.02, CEIL / 2), (2 * FLOOR_X, 0.02, CEIL), M["glass"])
    n = 19
    for i in range(n):
        x = -FLOOR_X + i * (2 * FLOOR_X) / (n - 1)
        mb.box((x, y, CEIL / 2), (0.06, 0.14, CEIL), M["metal_dark"])
    mb.box((0, y, 0.9), (2 * FLOOR_X, 0.1, 0.04), M["metal_dark"])
    mb.box((0, y + 0.1, CEIL + 0.15), (2 * FLOOR_X + 0.4, 0.3, 0.3), M["wall"])


build("wall_north_windows", f_wall_north, arch_root)


def f_wall_east(mb):  # 東牆（實牆，背景牆）
    x = FLOOR_X + WT / 2
    mb.box((x, 0, CEIL / 2 + 0.1), (WT, 2 * FLOOR_Y + 0.4, CEIL + 0.2), M["wall"])
    mb.box((FLOOR_X - 0.01, 0, 0.05), (0.02, 2 * FLOOR_Y, 0.1), M["baseboard"])


build("wall_east", f_wall_east, arch_root)


def f_wall_south(mb):  # 南牆含入口（俯視總覽時剖切隱藏）
    y = -FLOOR_Y - WT / 2
    dx0, dx1 = 0.5, 2.5
    mb.box(((-FLOOR_X - WT + dx0) / 2, y, CEIL / 2 + 0.1), (dx0 + FLOOR_X + WT, WT, CEIL + 0.2), M["wall"])
    mb.box(((dx1 + FLOOR_X + WT) / 2, y, CEIL / 2 + 0.1), (FLOOR_X + WT - dx1, WT, CEIL + 0.2), M["wall"])
    mb.box(((dx0 + dx1) / 2, y, 2.65 + 0.33), (dx1 - dx0, WT, 0.85), M["wall"])
    for x in (dx0 + 0.03, (dx0 + dx1) / 2, dx1 - 0.03):
        mb.box((x, y, 1.3), (0.05, 0.08, 2.6), M["metal_dark"])
    mb.box(((dx0 + dx1) / 2, y, 2.58), (dx1 - dx0, 0.08, 0.06), M["metal_dark"])
    mb.box(((dx0 + dx1) / 2, y, 1.3), (dx1 - dx0 - 0.06, 0.02, 2.5), M["glass"])
    for x in ((dx0 + dx1) / 2 - 0.12, (dx0 + dx1) / 2 + 0.12):
        mb.cyl((x, y + 0.06, 1.1), 0.012, 0.6, M["chrome"], seg=8)


build("wall_south_entrance", f_wall_south, arch_root, cutaway=True)


def f_wall_west(mb):
    x = -FLOOR_X - WT / 2
    mb.box((x, 0, CEIL / 2 + 0.1), (WT, 2 * FLOOR_Y + 0.4, CEIL + 0.2), M["wall"])


build("wall_west", f_wall_west, arch_root, cutaway=True)

CEIL_LIGHTS = []
for ix in range(7):
    for iy in range(5):
        x, y = -12 + ix * 4, -7 + iy * 3.5
        if 9.5 < x < 14.5 and -3 < y < 3:  # 錄音間有自己的屋頂
            continue
        CEIL_LIGHTS.append((x, y))


def f_ceiling(mb):
    mb.box((0, 0, CEIL + 0.03), (2 * FLOOR_X, 2 * FLOOR_Y, 0.06), M["ceiling"])
    for x, y in CEIL_LIGHTS:
        mb.box((x, y, CEIL - 0.008), (1.2, 0.6, 0.016), M["panel_light"])
        mb.box((x, y, CEIL - 0.004), (1.26, 0.66, 0.008), M["metal"])


build("ceiling", f_ceiling, arch_root, cutaway=True)

for i, (x, y) in enumerate(CEIL_LIGHTS):
    light(f"light_ceiling_{i + 1:02d}", "AREA", (x, y, CEIL - 0.03), 95, size=1.2, size_y=0.6, shadow=True)

# 外部樹木（窗外景）
def f_tree(seed):
    def fn(mb):
        rng = random.Random(seed)
        h = rng.uniform(4.0, 6.0)
        mb.cyl((0, 0, h * 0.25), 0.12, h * 0.5, M["bark"], seg=10)
        for k in range(3):
            mb.sphere((rng.uniform(-0.3, 0.3), rng.uniform(-0.3, 0.3), h * (0.55 + 0.15 * k)), rng.uniform(1.0, 1.5) * (1 - 0.15 * k),
                      rng.choice([M["leaf"], M["leaf2"], M["leaf3"]]), scale=(1, 1, 0.85), u=12, v=8)
    return fn


trees = empty("exterior_trees", arch_root)
for i in range(9):
    rng = random.Random(100 + i)
    build("tree", f_tree(i % 3), trees, (-13 + i * 3.3 + rng.uniform(-0.6, 0.6), 15.5 + rng.uniform(-1.5, 1.5), 0), cache=f"tree{i % 3}")
for i in range(4):
    build("tree", f_tree(i % 3), trees, (22 + random.Random(i).uniform(-1, 1), -8 + i * 5.5, 0), cache=f"tree{i % 3}")


# ---------------------------------------------------------------------------
# 通用工位
# ---------------------------------------------------------------------------
def hanging_sign(rid, parent, loc, width=1.0, label=None):
    rm = role_mats(rid)
    x, y, z = loc
    label = label or ROLE[rid]["name"]

    def fn(mb):
        mb.box((0, 0, 0), (width, 0.04, 0.26), rm["sign"], bevel=0.012)
        for sx in (-width / 2 + 0.08, width / 2 - 0.08):
            mb.cyl((sx, 0, (CEIL - z) / 2 + 0.13 / 2), 0.0025, CEIL - z - 0.13, M["metal"], seg=6)
    s = build(f"{rid}_sign", fn, parent, loc)
    sz = 0.13 if len(label) <= 6 else 0.1
    text(f"{rid}_sign_text_front", label, s, (0, -0.0215, 0), (PI / 2, 0, 0), sz, rm["text"], 0.002)
    text(f"{rid}_sign_text_back", label, s, (0, 0.0215, 0), (PI / 2, 0, PI), sz, rm["text"], 0.002)
    empty(f"nameplate_{rid}", parent, (x, y, z), kind="SPHERE", size=0.08)
    return s


def make_zone(rid, origin, rot_z=0.0):
    r = ROLE[rid]
    col = new_coll(f"role_{rid}")
    set_coll(col)
    z = empty(f"zone_{rid}", None, (origin[0], origin[1], 0), (0, 0, rot_z), kind="CUBE", size=0.3)
    z["role_id"] = rid
    z["role_name"] = r["name"]
    z["job"] = r["job"]
    z["color_name"] = r["color"]
    z["color_hex"] = APPLE[r["color"]]
    z["sf_symbol"] = r["symbol"]
    return z


def seat(rid, parent, loc, rot_z=0.0):
    s = empty(f"seat_{rid}", parent, loc, (0, 0, rot_z), kind="SINGLE_ARROW", size=0.6)
    s.empty_display_type = "ARROWS"
    s["role_id"] = rid
    s["note"] = "角色站位（地面高度 z=0）；本地 +Y 為角色面向（glTF 中為 -Z）"
    return s


def workstation(rid, parent, monitors=(), keyboard="std", chair_exec=False, desk_w=1.6, chair_fabric=None):
    """在 parent 本地座標建立：桌、椅、座位、螢幕、鍵盤滑鼠。人坐在 (0,-0.3) 面向 +Y"""
    rm = role_mats(rid)
    build(f"{rid}_desk", f_desk(desk_w), parent, cache=f"desk{desk_w}")
    fab = chair_fabric or M["fabric_gray"]
    build(f"{rid}_chair", f_chair(fab, exec_=chair_exec), parent, (0, -0.32, 0), (0, 0, 0.12), cache=f"chair_{fab.name}_{chair_exec}")
    seat(rid, parent, (0, -0.3, 0))
    for i, (mx, my, rz, w, h, sm) in enumerate(monitors):
        build(f"{rid}_monitor_{i + 1}", f_monitor(w, h, sm), parent, (mx, my, 0.74), (0, 0, rz))
    if keyboard:
        build(f"{rid}_keyboard", f_keyboard(keyboard == "mech", rm["solid"]), parent, (0, 0.36, 0.74),
              cache=None if keyboard == "mech" else "kb_std")

    def acc(mb):
        f_mouse(mb, (0.33, 0.36, 0.74))
        mb.box((0.33, 0.36, 0.741), (0.22, 0.18, 0.002), M["fabric_gray"])
    build(f"{rid}_mouse", acc, parent)


def pod_shell(rid, z, part_h=1.25, part_w=2.4, rug=(3.2, 3.0), sign_at=(0, 0.25, 2.55)):
    """工位外殼：角色色地毯、隔間矮牆（含色塊與 LED 燈條）、吊牌"""
    rm = role_mats(rid)

    def f_rug(mb):
        mb.box((0, 0.25, 0.006), (rug[0], rug[1], 0.012), rm["rug"], bevel=0.004, seg=1)
        mb.box((0, 0.25, 0.0125), (rug[0] - 0.16, rug[1] - 0.16, 0.001), rm["rug"])
    build(f"{rid}_rug", f_rug, z)

    def f_part(mb):
        y = 1.12
        mb.box((0, y, part_h / 2), (part_w, 0.06, part_h), M["felt"], bevel=0.008)
        mb.box((0, y, part_h + 0.012), (part_w + 0.02, 0.08, 0.024), M["desk_wood"], bevel=0.004)
        for sx in (-part_w / 2 + 0.1, part_w / 2 - 0.1):
            mb.box((sx, y, 0.015), (0.06, 0.4, 0.03), M["metal_dark"])
        mb.box((-part_w / 2 + 0.3, y - 0.032, part_h / 2 + 0.05), (0.4, 0.006, part_h - 0.3), rm["felt"])
        mb.box((0, y - 0.042, part_h - 0.04), (part_w - 0.08, 0.012, 0.018), rm["led"])
        mb.box((0, y + 0.042, part_h - 0.04), (part_w - 0.08, 0.012, 0.018), rm["led"])
    build(f"{rid}_partition", f_part, z)
    hanging_sign(rid, z, sign_at)


def add_role_camera(rid, origin, off=(-2.3, -2.75, 1.95), tgt=(0.15, 0.7, 0.95), lens=24):
    ox, oy = origin
    camera(f"cam_{rid}", (ox + off[0], oy + off[1], off[2]), (ox + tgt[0], oy + tgt[1], tgt[2]), lens)


ZONES = {}

# ---------------------------------------------------------------------------
# 產品經理：看板牆（便利貼 roadmap）、雙螢幕
# ---------------------------------------------------------------------------
rid = "product-manager"
o = POD_ORIGIN[rid]
z = ZONES[rid] = make_zone(rid, o)
pod_shell(rid, z, part_h=2.1)
workstation(rid, z, monitors=[(-0.31, 0.78, 0.12, 0.56, 0.32, screen_mat("tex_screen_roadmap")),
                              (0.31, 0.78, -0.12, 0.56, 0.32, screen_mat("tex_screen_spec"))])


def f_kanban(mb):
    rng = random.Random(21)
    y = 1.12 - 0.035
    mb.box((0, y, 1.67), (2.24, 0.012, 0.78), M["whiteboard"])
    mb.box((0, y + 0.003, 1.67), (2.28, 0.01, 0.82), M["metal"])
    heads = [STICKY[2], STICKY[0], STICKY[1], STICKY[3]]
    for c in range(4):
        x0 = -1.08 + c * 0.55
        mb.box((x0 + 0.26, y - 0.008, 2.0), (0.5, 0.004, 0.07), role_mats(rid)["solid"])
        if c:
            mb.box((x0, y - 0.008, 1.62), (0.006, 0.004, 0.66), M["plastic_gray"])
        for k in range(rng.randint(4, 7)):
            sx = x0 + 0.07 + (k % 3) * 0.15 + rng.uniform(-0.01, 0.01)
            sz = 1.88 - (k // 3) * 0.13 + rng.uniform(-0.01, 0.01)
            mb.box((sx, y - 0.01, sz), (0.1, 0.004, 0.1), rng.choice(heads if c < 3 else [STICKY[3]]),
                   rot=(0, rng.uniform(-0.12, 0.12), 0))
    # roadmap 時間軸條
    mb.box((0, y - 0.008, 1.33), (2.1, 0.004, 0.012), M["black"])
    for i in range(6):
        mb.cyl((-1.0 + i * 0.4, y - 0.01, 1.33), 0.022, 0.006, STICKY[i % 5], rot=(PI / 2, 0, 0), seg=12, smooth=False)


build(f"{rid}_kanban_wall", f_kanban, z)


def f_pm_desk(mb):
    rng = random.Random(22)
    f_mug(mb, (-0.62, 0.45, 0.74), STICKY[2])
    f_paper_stack(mb, (0.6, 0.62, 0.74), 3, rng)
    for i in range(5):
        mb.box((-0.5 + i * 0.03, 0.68, 0.742 + i * 0.002), (0.076, 0.076, 0.004), STICKY[i % 5], rot=(0, 0, rng.uniform(-0.3, 0.3)))
    mb.box((0.62, 0.3, 0.745), (0.15, 0.21, 0.01), M["black"], rot=(0, 0, 0.2), bevel=0.003)


build(f"{rid}_desk_items", f_pm_desk, z)
build(f"{rid}_plant", f_plant("snake", 3, 1.0, M["pot3"]), z, (1.4, 0.75, 0), cache="snake3")
add_role_camera(rid, o)

# ---------------------------------------------------------------------------
# 工程師：三螢幕、機械鍵盤、伺服器小機櫃、開發板
# ---------------------------------------------------------------------------
rid = "engineer"
o = POD_ORIGIN[rid]
z = ZONES[rid] = make_zone(rid, o)
pod_shell(rid, z)
workstation(rid, z, keyboard="mech", monitors=[
    (0.0, 0.8, 0.0, 0.58, 0.33, screen_mat("tex_screen_code")),
    (-0.6, 0.68, 0.45, 0.5, 0.29, screen_mat("tex_screen_terminal")),
    (0.6, 0.68, -0.45, 0.5, 0.29, screen_mat("tex_screen_ci"))])


def f_rack(mb):
    w, d, h = 0.6, 0.6, 1.0
    mb.box((0, 0, h / 2 + 0.05), (w, d, h), M["metal_dark"], bevel=0.008)
    for sx in (-w / 2 + 0.05, w / 2 - 0.05):
        for sy in (-d / 2 + 0.05, d / 2 - 0.05):
            mb.cyl((sx, sy, 0.025), 0.02, 0.05, M["black"], seg=8)
    for u in range(6):
        zz = 0.16 + u * 0.14
        mb.box((0, -d / 2 - 0.003, zz), (w - 0.06, 0.008, 0.11), M["black"], bevel=0.003)
        for v in range(8):
            mb.box((-0.2 + v * 0.025, -d / 2 - 0.009, zz), (0.012, 0.004, 0.07), M["plastic_gray"])
        for k in range(3):
            mb.box((0.15 + k * 0.03, -d / 2 - 0.009, zz + 0.02), (0.012, 0.004, 0.012),
                   [M["green_led"], M["blue_led"], M["green_led"] if u % 2 else M["red_led"]][k])
    # 開發板（放在機櫃頂）
    for i, (bx, by, rz) in enumerate([(-0.13, -0.05, 0.1), (0.14, 0.08, -0.2)]):
        X = MT((bx, by, h + 0.06), (0, 0, rz))
        mb.box(X @ Vector((0, 0, 0.012)), (0.18, 0.12, 0.012), M["pcb"])
        mb.box(X @ Vector((0.02, 0, 0.026)), (0.045, 0.045, 0.008), M["black"])
        mb.box(X @ Vector((-0.06, 0.03, 0.026)), (0.03, 0.02, 0.008), M["black"])
        mb.box(X @ Vector((0.075, -0.035, 0.03)), (0.03, 0.035, 0.022), M["metal"])
        for k in range(10):
            mb.box(X @ Vector((-0.08 + k * 0.012, 0.052, 0.026)), (0.006, 0.006, 0.014), M["copper"])
        mb.box(X @ Vector((-0.07, -0.045, 0.022)), (0.008, 0.008, 0.004), M["green_led"] if i else M["red_led"])
    mb.cyl((0.0, 0.02, h + 0.075), 0.004, 0.25, M["orange_plastic"], rot=(0, PI / 2, 0.3), seg=6)


build(f"{rid}_server_rack", f_rack, z, (1.75, 0.55, 0))


def f_eng_desk(mb):
    # 橡皮鴨、提神飲料、耳機架
    mb.sphere((-0.66, 0.36, 0.775), 0.035, M["duck"], scale=(1.2, 1, 0.9), u=12, v=8)
    mb.sphere((-0.64, 0.36, 0.825), 0.023, M["duck"], u=12, v=8)
    mb.cyl((-0.615, 0.36, 0.823), 0.008, 0.02, M["orange_plastic"], rot=(0, PI / 2, 0), r2=0.002, seg=8)
    mb.cyl((0.62, 0.3, 0.80), 0.032, 0.12, role_mats(rid)["solid"], seg=16)
    mb.cyl((0.62, 0.3, 0.862), 0.026, 0.004, M["metal"], seg=16, smooth=False)
    mb.box((-0.3, 0.32, 0.745), (0.12, 0.07, 0.01), M["pcb"])
    mb.box((-0.3, 0.32, 0.755), (0.03, 0.03, 0.006), M["black"])


build(f"{rid}_desk_items", f_eng_desk, z)
build(f"{rid}_plant", f_plant("bush", 4, 0.9, M["pot2"]), z, (-1.45, 0.65, 0), cache="bush4")
add_role_camera(rid, o)

# ---------------------------------------------------------------------------
# 課程開發：書架、成堆文件與筆記、單螢幕＋繪圖板
# ---------------------------------------------------------------------------
rid = "course-dev"
o = POD_ORIGIN[rid]
z = ZONES[rid] = make_zone(rid, o)
pod_shell(rid, z, rug=(3.6, 3.0))
workstation(rid, z, monitors=[(0.1, 0.8, 0.0, 0.62, 0.36, screen_mat("tex_screen_script"))])
build(f"{rid}_bookshelf", f_bookshelf(1.0, 0.35, 1.95, 5, seed=31), z, (-1.85, 0.85, 0))
build(f"{rid}_bookshelf_low", f_bookshelf(0.9, 0.32, 0.8, 2, seed=32), z, (1.65, 0.85, 0))


def f_cd_desk(mb):
    rng = random.Random(33)
    f_paper_stack(mb, (-0.58, 0.62, 0.74), 14, rng)
    f_paper_stack(mb, (-0.36, 0.7, 0.74), 8, rng)
    f_paper_stack(mb, (0.62, 0.66, 0.74), 6, rng)
    for i in range(4):  # 筆記本
        mb.box((-0.55 + rng.uniform(-0.02, 0.02), 0.32, 0.745 + i * 0.012), (0.15, 0.21, 0.011), BOOK[i + 2], rot=(0, 0, rng.uniform(-0.3, 0.3)))
    # 繪圖板 + 筆
    mb.box((0.35, 0.36, 0.746), (0.26, 0.18, 0.012), M["black"], bevel=0.004, rot=(0, 0, -0.1))
    mb.box((0.35, 0.36, 0.7525), (0.2, 0.13, 0.001), M["plastic_gray"], rot=(0, 0, -0.1))
    mb.cyl((0.42, 0.3, 0.758), 0.005, 0.15, M["black"], rot=(0, PI / 2, 0.5), seg=8)
    # 打開的筆記本
    mb.box((0.0, 0.42, 0.744), (0.3, 0.2, 0.008), M["paper2"], rot=(0, 0, 0.05))
    mb.box((0.0, 0.42, 0.749), (0.004, 0.2, 0.002), M["black"], rot=(0, 0, 0.05))
    # 便利貼在隔板
    for i in range(6):
        mb.box((-1.0 + i * 0.14, 1.085, 1.05 + (i % 2) * 0.1), (0.1, 0.004, 0.1), M["paper"] if i % 3 else STICKY[i % 5])
    f_lamp_desk(mb, (0.62, 0.92, 0.74), role_mats(rid)["solid"])


build(f"{rid}_desk_items", f_cd_desk, z)
build(f"{rid}_floor_boxes", lambda mb: [mb.box((1.2 + 0.0 * i, -0.2 + i * 0.0, 0.15 + i * 0.3), (0.4, 0.3, 0.3), M["cardboard"], rot=(0, 0, 0.1 * i), bevel=0.005) for i in range(2)], z)
add_role_camera(rid, o, off=(-0.9, -3.4, 2.0), tgt=(-0.5, 0.7, 0.95))

# ---------------------------------------------------------------------------
# 行銷：海報牆、大聲公、展示架
# ---------------------------------------------------------------------------
rid = "marketing"
o = POD_ORIGIN[rid]
z = ZONES[rid] = make_zone(rid, o)
pod_shell(rid, z, part_h=2.05)
workstation(rid, z, monitors=[(0.25, 0.8, 0.0, 0.6, 0.34, screen_mat("tex_screen_social"))])


def f_posters(mb):
    for i in range(3):
        x = -0.75 + i * 0.75
        mb.box((x, 1.08, 1.64), (0.5, 0.02, 0.7), M["black"], bevel=0.004)
        mb.screen((x, 1.069, 1.64), 0.45, 0.65, mat(f"poster_{i + 1}", (1, 1, 1), 0.5, img=IMG[f"tex_poster_{i + 1}"],
                                                       emit_img=True, strength=0.25))


build(f"{rid}_poster_wall", f_posters, z)


def f_megaphone(mb):
    rm = role_mats(rid)
    mb.cyl((0, 0, 0), 0.04, 0.22, rm["solid"], r2=0.1, rot=(0, PI / 2, 0), seg=20)
    mb.cyl((0.112, 0, 0), 0.097, 0.004, M["black"], rot=(0, PI / 2, 0), seg=20, smooth=False)
    mb.cyl((-0.13, 0, 0), 0.035, 0.05, M["plastic_white"], rot=(0, PI / 2, 0), seg=16)
    mb.box((-0.06, 0, -0.07), (0.03, 0.025, 0.1), M["black"], rot=(0, 0.2, 0))


build(f"{rid}_megaphone", f_megaphone, z, (-0.45, 0.6, 0.84), (0, 0, 2.6))


def f_laptop(mb):
    mb.box((0, 0, 0.006), (0.32, 0.22, 0.012), M["metal"], bevel=0.003)
    mb.box((0, 0.11, 0.115), (0.32, 0.008, 0.21), M["metal"], rot=(-0.25, 0, 0))
    mb.screen((0, 0.098, 0.115), 0.29, 0.18, screen_mat("tex_screen_analytics", 1.6), rot=(-0.25, 0, 0))


build(f"{rid}_laptop", f_laptop, z, (-0.42, 0.45, 0.74), (0, 0, 0.15))


def f_display_rack(mb):
    rng = random.Random(51)
    mb.box((0, 0, 0.015), (0.5, 0.36, 0.03), M["metal_dark"])
    mb.box((0, 0.1, 0.75), (0.03, 0.03, 1.45), M["metal_dark"])
    for k in range(4):
        zz = 0.35 + k * 0.33
        mb.box((0, 0.02, zz), (0.46, 0.14, 0.01), M["metal_dark"], rot=(0.35, 0, 0))
        for j in range(2):
            mb.box((-0.11 + j * 0.22, 0.03, zz + 0.1), (0.2, 0.012, 0.26), rng.choice(STICKY[:3] + [role_mats(rid)["solid"], M["paper"]]), rot=(0.35, 0, 0))


build(f"{rid}_display_rack", f_display_rack, z, (1.75, 0.6, 0), (0, 0, 0.2))


def f_banner(mb):
    mb.box((0, 0, 0.03), (0.8, 0.22, 0.06), M["metal"], bevel=0.01)
    mb.cyl((0.0, 0.06, 1.0), 0.01, 1.95, M["metal"], seg=8)
    mb.screen((0, 0.0, 1.05), 0.78, 1.9, mat("banner_roll", (1, 1, 1), 0.5, img=IMG["tex_poster_1"], emit_img=True, strength=0.2))


build(f"{rid}_rollup_banner", f_banner, z, (-1.85, 0.7, 0), (0, 0, 0.25))
build(f"{rid}_plant", f_plant("bush", 5, 0.8, M["pot"]), z, (1.65, -0.6, 0), cache="bush5")
add_role_camera(rid, o)

# ---------------------------------------------------------------------------
# 訪客分析師：大型儀表板螢幕
# ---------------------------------------------------------------------------
rid = "visitor-analyst"
o = POD_ORIGIN[rid]
z = ZONES[rid] = make_zone(rid, o)
pod_shell(rid, z, part_h=1.25, sign_at=(1.9, 0.1, 2.55))
workstation(rid, z, monitors=[(-0.3, 0.8, 0.1, 0.56, 0.32, screen_mat("tex_screen_analytics")),
                              (0.32, 0.8, -0.1, 0.56, 0.32, screen_mat("tex_screen_dashboard", 1.8))])


def f_dash_wall(mb):
    rm = role_mats(rid)
    y = 1.2
    mb.box((0, y + 0.05, 1.55), (3.0, 0.1, 3.0 - 0.0), M["walnut"], bevel=0.01)
    mb.box((0, y + 0.05, 0.03), (3.1, 0.6, 0.06), M["metal_dark"])
    mb.box((0, y - 0.03, 1.95), (2.44, 0.05, 1.4), M["black"], bevel=0.006)
    mb.screen((0, y - 0.056, 1.95), 2.36, 1.33, screen_mat("tex_screen_dashboard", 2.6))
    mb.box((0, y - 0.005, 1.17), (2.9, 0.012, 0.02), rm["led"])
    mb.box((0, y - 0.005, 2.95), (2.9, 0.012, 0.02), rm["led"])


build(f"{rid}_dashboard_wall", f_dash_wall, z)


def f_va_desk(mb):
    rng = random.Random(61)
    f_mug(mb, (-0.66, 0.4, 0.74), M["plastic_white"])
    f_paper_stack(mb, (0.62, 0.55, 0.74), 2, rng)
    # 印出的圖表
    mb.box((0.62, 0.55, 0.756), (0.19, 0.27, 0.001), mat("print_chart", (1, 1, 1), 0.8, img=IMG["tex_screen_analytics"]))


build(f"{rid}_desk_items", f_va_desk, z)
build(f"{rid}_plant", f_plant("tall", 6, 1.0, M["pot"]), z, (-1.75, 0.7, 0), cache="tall6")
add_role_camera(rid, o, off=(-2.6, -3.3, 1.9), tgt=(0.2, 0.9, 1.35))

# ---------------------------------------------------------------------------
# Issue 整理員：收件匣托盤、分類櫃、便利貼
# ---------------------------------------------------------------------------
rid = "triager"
o = POD_ORIGIN[rid]
z = ZONES[rid] = make_zone(rid, o)
pod_shell(rid, z)
workstation(rid, z, monitors=[(0.15, 0.8, 0.0, 0.6, 0.34, screen_mat("tex_screen_issues"))])


def f_trays(mb):
    rm = role_mats(rid)
    rng = random.Random(71)
    for k in range(3):
        zz = k * 0.085
        mb.box((0, 0, zz + 0.004), (0.26, 0.34, 0.008), M["metal_dark"])
        mb.box((-0.13, 0, zz + 0.03), (0.006, 0.34, 0.06), M["metal_dark"])
        mb.box((0.13, 0, zz + 0.03), (0.006, 0.34, 0.06), M["metal_dark"])
        mb.box((0, 0.17, zz + 0.03), (0.26, 0.006, 0.06), M["metal_dark"])
        mb.box((0, -0.17, zz + 0.015), (0.26, 0.006, 0.03), M["metal_dark"])
        mb.box((0, -0.174, zz + 0.015), (0.08, 0.004, 0.02), [rm["solid"], STICKY[0], STICKY[2]][k])
        f_paper_stack(mb, (0, 0.0, zz + 0.008), rng.randint(3, 8), rng)
    for sx in (-0.125, 0.125):
        for sy in (-0.16, 0.16):
            mb.box((sx, sy, 0.13), (0.012, 0.012, 0.26), M["metal_dark"])


build(f"{rid}_inbox_trays", f_trays, z, (-0.55, 0.6, 0.74), (0, 0, 0.1))


def f_sorter(mb):
    rm = role_mats(rid)
    rng = random.Random(72)
    w, d, h = 0.92, 0.4, 1.2
    wd = M["desk_wood"]
    mb.box((0, 0, h / 2), (w, d, h), wd, bevel=0.005)
    cols, rows = 4, 4
    cw, ch = (w - 0.04) / cols, (h - 0.1) / rows
    for r in range(rows):
        for c in range(cols):
            x = -w / 2 + 0.02 + cw * (c + 0.5)
            zz = 0.07 + ch * (r + 0.5)
            mb.box((x, -0.02, zz), (cw - 0.02, d - 0.02, ch - 0.02), M["plastic_white"])  # 內襯（凸出表示格子）
            mb.box((x, -d / 2 - 0.002, zz), (cw - 0.02, 0.004, ch - 0.02), M["black"])
            n = rng.randint(1, 5)
            for k in range(n):
                mb.box((x - cw / 2 + 0.03 + k * 0.025, -d / 2 - 0.01, zz - ch / 2 + 0.11),
                       (0.015, 0.03, 0.18), rng.choice([rm["solid"], STICKY[0], STICKY[2], STICKY[3], M["paper"]]),
                       rot=(0, rng.uniform(-0.15, 0.15), 0))
            mb.box((x, -d / 2 - 0.006, zz - ch / 2 + 0.02), (0.08, 0.004, 0.025),
                   rm["solid"] if (r + c) % 3 == 0 else M["paper"])


build(f"{rid}_sorting_cabinet", f_sorter, z, (1.75, 0.75, 0))


def f_tri_sticky(mb):
    rng = random.Random(73)
    for i in range(16):
        side = -1 if i < 8 else 1
        x = side * (0.75 + (i % 4) * 0.1) - (0.05 if side < 0 else 0)
        zz = 0.98 + ((i % 8) // 4) * 0.13
        mb.box((x + rng.uniform(-0.01, 0.01), 1.085, zz), (0.08, 0.004, 0.08), rng.choice(STICKY), rot=(0, rng.uniform(-0.15, 0.15), 0))
    for i in range(3):  # 螢幕邊框上的便利貼
        mb.box((0.15 + 0.31 + 0.01, 0.785, 1.1 - i * 0.09), (0.004, 0.06, 0.06), STICKY[i])
    f_mug(mb, (0.66, 0.42, 0.74), STICKY[4])


build(f"{rid}_sticky_notes", f_tri_sticky, z)
add_role_camera(rid, o)

# ---------------------------------------------------------------------------
# CEO：獨立玻璃角落辦公室、大辦公桌、目標牆白板、沙發會客區
# ---------------------------------------------------------------------------
rid = "ceo"
CEO_O = (11.25, 6.25)
z = ZONES[rid] = make_zone(rid, CEO_O)
rm = role_mats(rid)


def f_ceo_glass(mb):
    # 西牆 x=-2.75（本地），南牆 y=-2.75；門洞在西牆 y∈[-2.35,-1.35]
    x0, y0, x1, y1 = -2.75, -2.75, 2.75, 2.75
    H = CEIL

    def run(a, b, fixed, along_x, gaps=()):
        L = b - a
        if along_x:
            mb.box(((a + b) / 2, fixed, 0.02), (L, 0.08, 0.04), M["metal_dark"])
            mb.box(((a + b) / 2, fixed, H - 0.03), (L, 0.08, 0.06), M["metal_dark"])
        else:
            mb.box((fixed, (a + b) / 2, 0.02), (0.08, L, 0.04), M["metal_dark"])
            mb.box((fixed, (a + b) / 2, H - 0.03), (0.08, L, 0.06), M["metal_dark"])
        segs, cur = [], a
        for g0, g1 in gaps:
            segs.append((cur, g0))
            cur = g1
        segs.append((cur, b))
        for s0, s1 in segs:
            n = max(1, int(round((s1 - s0) / 1.2)))
            for i in range(n + 1):
                p = s0 + i * (s1 - s0) / n
                if along_x:
                    mb.box((p, fixed, H / 2), (0.05, 0.08, H), M["metal_dark"])
                else:
                    mb.box((fixed, p, H / 2), (0.08, 0.05, H), M["metal_dark"])
            c, l = (s0 + s1) / 2, s1 - s0
            if along_x:
                mb.box((c, fixed, H / 2), (l, 0.012, H - 0.08), M["glass"])
                mb.box((c, fixed, 1.1), (l, 0.016, 0.14), rm["solid"])
            else:
                mb.box((fixed, c, H / 2), (0.012, l, H - 0.08), M["glass"])
                mb.box((fixed, c, 1.1), (0.016, l, 0.14), rm["solid"])
        for g0, g1 in gaps:  # 門楣
            if not along_x:
                mb.box((fixed, (g0 + g1) / 2, 2.2), (0.08, g1 - g0, 0.06), M["metal_dark"])
                mb.box((fixed, (g0 + g1) / 2, (2.2 + H) / 2), (0.012, g1 - g0, H - 2.2), M["glass"])
    run(y0, y1, x0, False, gaps=((-2.35, -1.35),))
    run(x0, x1, y0, True)


build("ceo_glass_walls", f_ceo_glass, z)

ceo_ws = empty("ceo_workstation", z, (1.25, 0.6, 0), (0, 0, PI / 2))
build("ceo_desk", f_desk(2.0, 0.9, M["walnut"], pedestal=False), ceo_ws)
build("ceo_chair", f_chair(M["leather"], exec_=True), ceo_ws, (0, -0.32, 0), (0, 0, -0.1))
seat(rid, ceo_ws, (0, -0.3, 0))
build("ceo_monitor_1", f_monitor(0.7, 0.4, screen_mat("tex_screen_okr")), ceo_ws, (0.15, 0.85, 0.74))
build("ceo_keyboard", f_keyboard(False), ceo_ws, (0.1, 0.38, 0.74), cache="kb_std")


def f_ceo_desk_items(mb):
    rng = random.Random(81)
    f_mouse(mb, (0.45, 0.38, 0.74))
    f_mug(mb, (-0.75, 0.45, 0.74), M["plastic_white"])
    f_paper_stack(mb, (-0.55, 0.85, 0.74), 4, rng)
    mb.box((-0.5, 0.4, 0.746), (0.16, 0.23, 0.012), M["black"], rot=(0, 0, 0.3), bevel=0.004)  # 平板
    # 皇冠擺飾（crown）
    mb.cyl((0.82, 0.9, 0.77), 0.05, 0.06, rm["solid"], seg=16)
    for k in range(5):
        a = k * 2 * PI / 5
        mb.cyl((0.82 + 0.04 * math.cos(a), 0.9 + 0.04 * math.sin(a), 0.82), 0.014, 0.05, rm["solid"], r2=0.0, seg=6)
    f_lamp_desk(mb, (-0.85, 0.95, 0.74), M["metal_dark"])


build("ceo_desk_items", f_ceo_desk_items, ceo_ws)
# 目標牆：東牆上的白板 + 三張 OKR 框
build("ceo_goal_whiteboard", f_whiteboard(2.6, 1.3, mat("whiteboard_goals", (1, 1, 1), 0.3, img=IMG["tex_whiteboard_goals"],
                                                       emit_img=True, strength=0.15)), z, (2.72, 0.7, 1.55), (0, 0, -PI / 2))


def f_okr_frames(mb):
    for i in range(3):
        y = -1.6 + i * 0.0
        mb.box((0, -0.55 + i * 0.38 - 1.05, 0), (0.03, 0.3, 0.4), M["walnut"])
        mb.box((-0.016, -0.55 + i * 0.38 - 1.05, 0.02), (0.002, 0.24, 0.3), STICKY[i])
        mb.box((-0.018, -0.55 + i * 0.38 - 1.05, -0.12), (0.002, 0.2, 0.04), rm["solid"])


build("ceo_goal_cards", f_okr_frames, z, (2.73, 0.0, 1.7))
# 會客沙發區
build("ceo_rug", lambda mb: mb.box((0, 0, 0.006), (3.2, 2.4, 0.012), rm["rug"], bevel=0.004, seg=1), z, (-1.0, -1.35, 0))
build("ceo_sofa", f_sofa(2.0, M["fabric_sofa"], 2), z, (-1.0, -2.2, 0), cache="sofa2_oat")
build("ceo_armchair", f_sofa(0.95, M["fabric_teal"], 1), z, (-2.25, -0.95, 0), (0, 0, -PI / 2), cache="arm_teal")
build("ceo_coffee_table", f_coffee_table(1.0, 0.55), z, (-1.0, -1.15, 0), cache="ct_walnut")
build("ceo_table_items", lambda mb: (f_paper_stack(mb, (-0.15, 0, 0.4), 3, random.Random(2)),
                                     mb.cyl((0.25, 0.05, 0.47), 0.06, 0.14, M["pot"], seg=16),
                                     mb.sphere((0.25, 0.05, 0.56), 0.07, M["leaf3"], u=10, v=6)), z, (-1.0, -1.15, 0))
build("ceo_credenza", lambda mb: (mb.box((0, 0, 0.35), (1.8, 0.45, 0.66), M["walnut"], bevel=0.008),
                                  [mb.cyl((x, 0, 0.01), 0.02, 0.04, M["metal_dark"], seg=8) for x in (-0.8, 0.8)],
                                  f_books(mb, -0.8, -0.25, 0.0, 0.68, 0.2, random.Random(5)),
                                  mb.box((0.35, 0, 0.73), (0.28, 0.2, 0.1), M["metal"], bevel=0.01)), z, (0.2, 2.45, 0), (0, 0, PI))
build("ceo_plant", f_plant("tall", 7, 1.15, M["pot3"]), z, (-2.3, 2.3, 0), cache="tall7")
build("ceo_floor_lamp", lambda mb: (mb.cyl((0, 0, 0.01), 0.16, 0.02, M["metal_dark"], seg=20),
                                    mb.cyl((0, 0, 0.8), 0.012, 1.6, M["metal_dark"], seg=8),
                                    mb.cyl((0, 0, 1.6), 0.2, 0.3, M["lamp_warm"], r2=0.17, seg=24)), z, (-2.4, -2.35, 0))
light("light_ceo_floorlamp", "POINT", (CEO_O[0] - 2.4, CEO_O[1] - 2.35, 1.55), 40, (1, 0.8, 0.55), size=0.15, parent=None)
hanging_sign(rid, z, (-0.9, -0.2, 2.55))
camera("cam_ceo", (CEO_O[0] - 2.4, CEO_O[1] - 2.4, 2.1), (CEO_O[0] + 1.6, CEO_O[1] + 0.9, 1.0), 22)

# ---------------------------------------------------------------------------
# 旁白製作：小型錄音間（吸音棉）、麥克風架與防噴罩、耳機、混音器
# ---------------------------------------------------------------------------
rid = "narrator"
NAR_O = (12.0, 0.0)
z = ZONES[rid] = make_zone(rid, NAR_O)
rm = role_mats(rid)
BH = 2.5  # 錄音間高度


def f_booth(mb):
    t = 0.12
    # 北、南牆
    for y in (2.5 - t / 2, -2.5 + t / 2):
        mb.box((0, y, BH / 2), (4.0, t, BH), M["wall"])
    # 西牆：下牆、窗、上牆、門
    xw = -2.0 + t / 2
    mb.box((xw, 0.4, 0.45), (t, 4.2 - 0.0, 0.9), M["wall"])
    mb.box((xw, 0.4, 2.3), (t, 4.2, 0.4), M["wall"])
    mb.box((xw, 2.25, 1.5), (t, 0.5, 1.2), M["wall"])
    mb.box((xw, -1.55, 1.5), (t, 0.3, 1.2), M["wall"])
    mb.box((xw, 0.35, 1.5), (0.03, 3.5, 1.2), M["glass_dark"])
    for yy in (-1.4, 0.35, 2.0):
        mb.box((xw, yy, 1.5), (t + 0.02, 0.04, 1.2), M["metal_dark"])
    mb.box((xw, 0.35, 0.9), (t + 0.03, 3.5, 0.03), M["metal_dark"])
    mb.box((xw, 0.35, 2.1), (t + 0.03, 3.5, 0.03), M["metal_dark"])
    # 門楣（門板是另一個物件 narrator_booth_door，開著讓角色能走進錄音間）
    mb.box((xw, -2.03, 2.3), (t, 0.8, 0.4), M["wall"])
    # 外牆角色色燈條與色塊
    mb.box((-2.0 - 0.005, 0.2, BH - 0.06), (0.012, 4.6, 0.02), rm["led"])
    mb.box((-2.0 - 0.004, 1.7, 0.45), (0.008, 1.2, 0.7), rm["felt"])


build("narrator_booth_walls", f_booth, z)


def f_booth_door(mb):
    # 本地原點 = 門鉸鏈（門洞南端）；關門時門板沿本地 +Y 展開，這裡整片轉 90° 往外（西）打開
    mb.box((0, 0.4, 1.05), (0.06, 0.8, 2.1), M["walnut"], bevel=0.005)
    mb.box((-0.035, 0.4, 1.4), (0.01, 0.12, 0.6), M["glass_dark"])
    mb.cyl((-0.07, 0.71, 1.0), 0.012, 0.18, M["chrome"], seg=8)
    mb.cyl((0.07, 0.71, 1.0), 0.012, 0.18, M["chrome"], seg=8)


build("narrator_booth_door", f_booth_door, z, (-2.0 + 0.06 - 0.03, -2.43, 0), (0, 0, PI / 2 + 0.05))
build("narrator_booth_roof", lambda mb: mb.box((0, 0, BH + 0.03), (4.0, 5.0, 0.06), M["wall"]), z, cutaway=True)


def f_foam(wall):
    def fn(mb):
        s = 0.2
        if wall == "east":
            ny, nz = int(4.8 / s), int(2.3 / s)
            for i in range(ny):
                for j in range(nz):
                    y, zz = -2.4 + (i + 0.5) * s, 0.15 + (j + 0.5) * s
                    tile = ((i // 3) + (j // 3)) % 2
                    m = rm["felt"] if (i // 3 + j // 3 * 2) % 7 == 3 else M["foam"]
                    X = Matrix.Translation((1.97, y, zz)) @ Euler((0, -PI / 2, 0)).to_matrix().to_4x4() @ Euler((0, 0, PI / 4 + tile * 0)).to_matrix().to_4x4()
                    mb.cyl(None, s * 0.7, 0.07, m, r2=0.0, seg=4, smooth=False, matrix=X @ Matrix.Translation((0, 0, 0.035)))
        else:
            sgn = 1 if wall == "north" else -1
            nx, nz = int(3.6 / s), int(2.3 / s)
            for i in range(nx):
                for j in range(nz):
                    x, zz = -1.85 + (i + 0.5) * s, 0.15 + (j + 0.5) * s
                    m = rm["felt"] if (i // 3 + j // 3 * 2) % 7 == 3 else M["foam"]
                    X = Matrix.Translation((x, sgn * (2.5 - 0.12), zz)) @ Euler((sgn * PI / 2, 0, 0)).to_matrix().to_4x4() @ Euler((0, 0, PI / 4)).to_matrix().to_4x4()
                    mb.cyl(None, s * 0.7, 0.07, m, r2=0.0, seg=4, smooth=False, matrix=X @ Matrix.Translation((0, 0, 0.035)))
    return fn


for w in ("north", "south", "east"):
    build(f"narrator_foam_{w}", f_foam(w), z)

build("narrator_rug", lambda mb: mb.box((0, 0, 0.006), (3.0, 3.0, 0.012), rm["rug"], bevel=0.004, seg=1), z, (0.4, 0.2, 0))
nar_ws = empty("narrator_workstation", z, (0.55, 0.2, 0), (0, 0, -PI / 2))
build("narrator_desk", f_desk(1.6, 0.8), nar_ws, cache="desk1.6")
build("narrator_chair", f_chair(M["fabric_gray"]), nar_ws, (0, -0.32, 0), cache="chair_fabric_charcoal_False")
seat(rid, nar_ws, (0, -0.3, 0))
build("narrator_monitor_1", f_monitor(0.56, 0.32, screen_mat("tex_screen_daw")), nar_ws, (0.0, 0.85, 0.74))


def f_mic(mb):
    # 夾在桌邊的懸臂麥克風架
    mb.box((-0.55, 0.9, 0.73), (0.06, 0.08, 0.06), M["metal_dark"])
    mb.cyl((-0.55, 0.9, 0.92), 0.012, 0.36, M["metal_dark"], seg=8)
    p0, p1, p2 = Vector((-0.55, 0.9, 1.1)), Vector((-0.35, 0.55, 1.42)), Vector((-0.08, 0.3, 1.3))
    for a, b in ((p0, p1), (p1, p2)):
        d = b - a
        q = d.to_track_quat("Z", "Y")
        mb.cyl(None, 0.01, d.length, M["metal_dark"], seg=8, matrix=Matrix.Translation((a + b) / 2) @ q.to_matrix().to_4x4())
        mb.sphere(b, 0.018, M["metal_dark"], u=8, v=6)
    # 防震架 + 麥克風本體
    mb.torus((-0.08, 0.3, 1.24), 0.05, 0.006, M["black"], rot=(0, 0, 0), seg=16, rseg=6)
    mb.cyl((-0.08, 0.3, 1.24), 0.026, 0.17, M["metal"], seg=16)
    mb.sphere((-0.08, 0.3, 1.33), 0.027, M["metal_dark"], u=12, v=8)
    mb.cyl((-0.08, 0.3, 1.14), 0.02, 0.04, M["black"], seg=12)
    # 防噴罩（鵝頸 + 圓框 + 網面）
    mb.cyl(None, 0.004, 0.25, M["metal_dark"], seg=6,
           matrix=Matrix.Translation((-0.1, 0.2, 1.21)) @ Vector((0.02, -0.2, 0.1)).to_track_quat("Z", "Y").to_matrix().to_4x4())
    mb.torus((-0.08, 0.12, 1.3), 0.075, 0.006, M["black"], rot=(PI / 2, 0, 0), seg=24, rseg=6)
    mb.cyl((-0.08, 0.12, 1.3), 0.072, 0.002, mat("popfilter_mesh", "#111114", 0.6, alpha=0.55), rot=(PI / 2, 0, 0), seg=24, smooth=False)


build("narrator_mic_arm", f_mic, nar_ws)


def f_headphones(mb):
    mb.torus((0, 0, 0.1), 0.085, 0.012, M["black"], rot=(PI / 2, 0, 0), seg=16, rseg=6, arc=PI)
    for sx in (-0.085, 0.085):
        mb.cyl((sx, 0, 0.045), 0.045, 0.035, M["black"], rot=(0, PI / 2, 0), seg=16)
        mb.cyl((sx * 0.85, 0, 0.045), 0.04, 0.012, rm["solid"], rot=(0, PI / 2, 0), seg=16)


build("narrator_headphones", f_headphones, nar_ws, (0.55, 0.42, 0.74), (PI / 2, 0, 0.4))


def f_mixer(mb):
    mb.box((0, 0, 0.025), (0.38, 0.26, 0.05), M["metal_dark"], rot=(0.12, 0, 0), bevel=0.005)
    for c in range(6):
        x = -0.15 + c * 0.06
        for r in range(3):
            mb.cyl((x, 0.07 - r * 0.04, 0.06 + r * 0.004), 0.009, 0.02, [M["plastic_white"], rm["solid"], M["red_led"]][r] if r < 2 else M["plastic_white"], seg=10)
        mb.box((x, -0.08, 0.04), (0.006, 0.08, 0.004), M["black"])
        mb.box((x, -0.08 + (c % 3) * 0.02, 0.048), (0.024, 0.012, 0.014), M["plastic_white"])
    for k in range(6):
        mb.box((0.17, 0.09 - k * 0.012, 0.058), (0.008, 0.008, 0.004), M["green_led"] if k > 1 else M["red_led"])


build("narrator_mixer", f_mixer, nar_ws, (0.3, 0.55, 0.74))


def f_speakers(mb):
    for sx in (-0.68, 0.68):
        mb.box((sx, 0.85, 0.9), (0.16, 0.2, 0.26), M["black"], bevel=0.008)
        mb.cyl((sx, 0.749, 0.86), 0.055, 0.004, M["plastic_gray"], rot=(PI / 2, 0, 0), seg=20, smooth=False)
        mb.cyl((sx, 0.749, 0.96), 0.022, 0.004, M["plastic_gray"], rot=(PI / 2, 0, 0), seg=16, smooth=False)


build("narrator_studio_monitors", f_speakers, nar_ws)


def f_onair(mb):
    mb.box((0, 0, 0), (0.05, 0.5, 0.16), M["black"], bevel=0.008)
    mb.box((-0.027, 0, 0), (0.004, 0.44, 0.11), M["red_led"])


onair = build("narrator_on_air_light", f_onair, z, (-2.08, -2.03, 2.3))
text("narrator_on_air_text", "ON AIR", onair, (-0.031, 0, 0), (PI / 2, 0, -PI / 2), 0.07, mat("onair_text", "#FFFFFF", 0.3, emit="#FFFFFF", strength=3))
light("light_narrator_booth", "AREA", (NAR_O[0] + 0.3, NAR_O[1], BH - 0.05), 160, (1, 0.9, 0.8), size=1.6, size_y=1.2)
light("light_narrator_desk", "POINT", (NAR_O[0] + 1.3, NAR_O[1] + 0.3, 1.6), 25, (0.85, 0.65, 1.0), size=0.2)


def f_nar_sign(mb):
    mb.box((0, 0, 0), (0.04, 1.2, 0.28), rm["sign"], bevel=0.012)


nsign = build("narrator_sign", f_nar_sign, z, (-2.1, 0.9, 2.28))
text("narrator_sign_text", ROLE[rid]["name"], nsign, (-0.0215, 0, 0), (PI / 2, 0, -PI / 2), 0.14, rm["text"], 0.002)
empty("nameplate_narrator", z, (-2.1, 0.9, 2.28), kind="SPHERE", size=0.08)
camera("cam_narrator", (NAR_O[0] - 0.3, NAR_O[1] - 1.95, 1.75), (NAR_O[0] + 1.35, NAR_O[1] + 0.35, 1.0), 20)

# ---------------------------------------------------------------------------
# 共用區：會議室
# ---------------------------------------------------------------------------
SH = new_coll("shared_areas")
set_coll(new_coll("shared_meeting", SH))
MEET_O = (-10.75, 6.25)
zm = empty("zone_meeting", None, (*MEET_O, 0), kind="CUBE", size=0.3)
zm["zone"] = "meeting"


def f_meet_walls(mb):
    H = CEIL
    # 東側實牆（掛電視），本地 x=3.25
    mb.box((3.25 + 0.075, 0, H / 2), (0.15, 5.5, H), M["wall_accent"])
    mb.box((3.25 - 0.01, 0, 0.05), (0.02, 5.5, 0.1), M["baseboard"])
    # 南側玻璃（y=-2.75），門洞 x∈[2.0,3.1]
    y = -2.75
    mb.box((-0.15, y, 0.02), (6.3, 0.08, 0.04), M["metal_dark"])
    mb.box((0.0, y, H - 0.03), (6.6, 0.08, 0.06), M["metal_dark"])
    for x in (-3.25, -2.0, -0.75, 0.5, 1.95, 3.15):
        mb.box((x, y, H / 2), (0.05, 0.08, H), M["metal_dark"])
    mb.box((-0.65, y, H / 2), (5.2, 0.012, H - 0.08), M["glass"])
    mb.box((-0.65, y, 1.1), (5.2, 0.016, 0.12), mat("meeting_frost", "#FFFFFF", 0.6, alpha=0.6))
    mb.box((2.55, y, 2.2), (1.2, 0.08, 0.06), M["metal_dark"])
    mb.box((2.55, y, (2.2 + H) / 2), (1.2, 0.012, H - 2.2), M["glass"])


build("meeting_walls", f_meet_walls, zm)
build("meeting_carpet", lambda mb: mb.box((0, 0, 0.004), (6.4, 5.4, 0.008), M["carpet"]), zm)


def f_table(mb):
    mb.box((0, 0, 0.72), (3.6, 1.2, 0.04), M["desk_wood"], bevel=0.01)
    for sx in (-1.3, 1.3):
        mb.box((sx, 0, 0.36), (0.08, 0.8, 0.7), M["metal_dark"])
        mb.box((sx, 0, 0.015), (0.12, 0.95, 0.03), M["metal_dark"])
    mb.box((0, 0, 0.745), (0.3, 0.12, 0.012), M["black"])  # 線材盒
    mb.cyl((0.6, 0, 0.75), 0.07, 0.02, M["plastic_gray"], seg=24)  # 會議電話


build("meeting_table", f_table, zm, (-0.3, 0, 0))
k = 0
for x in (-1.4, -0.3, 0.8):
    for side in (-1, 1):
        k += 1
        build(f"meeting_chair_{k:02d}", f_chair(M["fabric_rust"], simple=True), zm,
              (x - 0.3, side * 0.95, 0), (0, 0, 0 if side < 0 else PI), cache="chair_simple_rust")
build("meeting_chair_head", f_chair(M["fabric_rust"], simple=True), zm, (-2.6, 0, 0), (0, 0, -PI / 2), cache="chair_simple_rust")


def f_tv(mb):
    mb.box((0, 0.02, 0), (1.96, 0.04, 1.12), M["black"], bevel=0.006)
    mb.screen((0, -0.001, 0), 1.9, 1.07, screen_mat("tex_screen_slide", 2.0))
    mb.box((0, 0.0, -0.75), (1.2, 0.3, 0.06), M["walnut"])


build("meeting_tv", f_tv, zm, (3.2, 0.0, 1.5), (0, 0, -PI / 2))
build("meeting_credenza", lambda mb: (mb.box((0, 0, 0.35), (1.8, 0.45, 0.7), M["walnut"], bevel=0.008)), zm, (2.95, 1.6, 0), (0, 0, -PI / 2))
build("meeting_whiteboard_mobile", lambda mb: (f_whiteboard(1.6, 0.95, mat("whiteboard_notes", (1, 1, 1), 0.3, img=IMG["tex_screen_roadmap"]))(mb),
                                               [mb.box((sx, 0.04, -0.75), (0.04, 0.04, 1.0), M["metal"]) for sx in (-0.75, 0.75)],
                                               [mb.box((sx, 0.04, -1.29), (0.05, 0.5, 0.04), M["metal"]) for sx in (-0.75, 0.75)]),
      zm, (-2.4, 2.2, 1.35), (0, 0, PI + 0.5))


def f_meet_items(mb):
    rng = random.Random(91)
    for x, y, r in [(-1.4, -0.35, 0.1), (0.8, 0.35, PI - 0.2), (-0.3, 0.38, PI)]:
        mb.box((x - 0.3, y, 0.751), (0.31, 0.22, 0.012), M["metal"], rot=(0, 0, r), bevel=0.003)
    f_paper_stack(mb, (-1.1, -0.2, 0.74), 3, rng)
    f_mug(mb, (0.2, -0.3, 0.74), M["plastic_white"])
    f_mug(mb, (-0.8, 0.3, 0.74), STICKY[1])
    for x in (-1.4, -0.3, 0.8):
        f_pendant(mb, (x, 0, 2.2), r=0.2)


build("meeting_items", f_meet_items, zm)
for x in (-1.4, -0.3, 0.8):
    light(f"light_meeting_pendant_{int(x * 10)}", "POINT", (MEET_O[0] + x, MEET_O[1], 1.95), 35, (1, 0.82, 0.6), size=0.1)
build("meeting_plant", f_plant("bush", 9, 1.0, M["pot2"]), zm, (-2.8, -2.2, 0), cache="bush9")
build("meeting_plant_b", f_plant("snake", 10, 1.0, M["pot"]), zm, (2.85, -2.2, 0), cache="snake10")
camera("cam_meeting", (MEET_O[0] - 2.8, MEET_O[1] - 2.4, 2.1), (MEET_O[0] + 2.0, MEET_O[1] + 0.5, 1.0), 20)

# ---------------------------------------------------------------------------
# 共用區：茶水間 / 休息區
# ---------------------------------------------------------------------------
set_coll(new_coll("shared_lounge", SH))
LOUNGE_O = (11.25, -6.25)
zl = empty("zone_lounge", None, (*LOUNGE_O, 0), kind="CUBE", size=0.3)
zl["zone"] = "lounge"


def f_pantry(mb):
    x = 2.45  # 沿東牆
    y0, y1 = -2.6, 1.0
    L = y1 - y0
    yc = (y0 + y1) / 2
    mb.box((x, yc, 0.05), (0.56, L, 0.1), M["black"])
    mb.box((x, yc, 0.47), (0.6, L, 0.74), M["plastic_white"], bevel=0.004)
    n = 6
    for i in range(n):
        yy = y0 + (i + 0.5) * L / n
        mb.box((x - 0.302, yy, 0.47), (0.004, L / n - 0.01, 0.72), M["plastic_white"])
        mb.box((x - 0.31, yy, 0.78), (0.012, 0.2, 0.012), M["metal"])
    mb.box((x - 0.01, yc, 0.865), (0.64, L + 0.02, 0.03), M["walnut"], bevel=0.003)
    # 吊櫃 + 背板
    mb.box((x + 0.27, yc, 1.25), (0.02, L, 0.7), mat("tile_backsplash", "#DCE6E2", 0.25))
    mb.box((x + 0.1, yc, 1.95), (0.35, L, 0.7), M["plastic_white"], bevel=0.004)
    for i in range(n):
        yy = y0 + (i + 0.5) * L / n
        mb.box((x - 0.077, yy, 1.95), (0.004, L / n - 0.01, 0.68), M["plastic_white"])
    mb.box((x - 0.06, yc, 1.585), (0.04, L - 0.1, 0.012), M["led_white"])
    # 水槽
    mb.box((x - 0.02, -1.6, 0.882), (0.42, 0.55, 0.004), M["metal"])
    mb.box((x - 0.02, -1.6, 0.884), (0.36, 0.48, 0.002), M["metal_dark"])
    mb.cyl((x + 0.18, -1.6, 1.0), 0.015, 0.24, M["chrome"], seg=10)
    mb.cyl((x + 0.08, -1.6, 1.12), 0.012, 0.2, M["chrome"], rot=(0, PI / 2, 0), seg=10)
    # 咖啡機
    mb.box((x, 0.3, 1.07), (0.32, 0.3, 0.38), M["metal_dark"], bevel=0.01)
    mb.box((x - 0.12, 0.3, 0.98), (0.1, 0.18, 0.04), M["chrome"])
    mb.cyl((x - 0.12, 0.3, 0.92), 0.035, 0.08, M["plastic_white"], seg=14)
    mb.box((x - 0.162, 0.3, 1.18), (0.002, 0.12, 0.07), M["blue_led"])
    # 水壺、水果盆
    mb.cyl((x - 0.05, -0.5, 0.97), 0.07, 0.18, M["chrome"], r2=0.05, seg=16)
    mb.cyl((x - 0.1, -0.9, 0.91), 0.15, 0.06, M["walnut"], r2=0.18, seg=20)
    rng = random.Random(1)
    for i in range(7):
        mb.sphere((x - 0.1 + rng.uniform(-0.08, 0.08), -0.9 + rng.uniform(-0.08, 0.08), 0.96 + (i // 4) * 0.04),
                  0.04, rng.choice([M["fruit_r"], M["fruit_y"], M["fruit_g"]]), u=10, v=6)
    # 冰箱
    mb.box((x, 1.45, 0.93), (0.68, 0.72, 1.86), M["metal"], bevel=0.02, seg=3)
    mb.box((x - 0.345, 1.45, 1.28), (0.004, 0.7, 0.008), M["metal_dark"])
    mb.box((x - 0.36, 1.2, 0.95), (0.025, 0.025, 0.5), M["metal_dark"])
    mb.box((x - 0.36, 1.2, 1.55), (0.025, 0.025, 0.3), M["metal_dark"])


build("lounge_pantry", f_pantry, zl)


def f_bar(mb):
    mb.box((0, 0, 1.03), (1.8, 0.65, 0.04), M["desk_wood"], bevel=0.006)
    for sx in (-0.75, 0.75):
        mb.box((sx, 0, 0.51), (0.06, 0.5, 1.0), M["metal_dark"])
        mb.box((sx, 0, 0.015), (0.1, 0.6, 0.03), M["metal_dark"])


build("lounge_bar_table", f_bar, zl, (0.4, 0.2, 0), (0, 0, PI / 2))


def f_stool(mb):
    mb.cyl((0, 0, 0.73), 0.19, 0.05, M["leather"], seg=24)
    mb.cyl((0, 0, 0.37), 0.025, 0.7, M["metal_dark"], seg=10)
    mb.torus((0, 0, 0.3), 0.15, 0.01, M["metal_dark"], seg=20, rseg=6)
    mb.cyl((0, 0, 0.01), 0.2, 0.02, M["metal_dark"], seg=24)


for i, y in enumerate((-0.45, 0.2, 0.85)):
    build(f"lounge_stool_{i + 1:02d}", f_stool, zl, (-0.15, y, 0), cache="stool")
    build(f"lounge_stool_{i + 4:02d}", f_stool, zl, (0.95, y, 0), cache="stool")
build("lounge_bar_items", lambda mb: (f_mug(mb, (0.3, 0.6, 1.05), STICKY[3]), f_mug(mb, (0.45, -0.3, 1.05), M["plastic_white"]),
                                      mb.box((0.4, 0.0, 1.06), (0.3, 0.22, 0.012), M["metal"], rot=(0, 0, 0.4), bevel=0.003)), zl)
build("lounge_rug", lambda mb: mb.box((0, 0, 0.006), (3.0, 2.4, 0.012), mat("rug_lounge", "#C9B79C", 1.0), bevel=0.004, seg=1), zl, (-1.4, -1.5, 0))
build("lounge_sofa", f_sofa(2.2, M["fabric_teal"], 3), zl, (-1.4, -2.4, 0))
build("lounge_armchair", f_sofa(0.95, M["fabric_rust"], 1), zl, (-2.45, -1.2, 0), (0, 0, -PI / 2), cache="arm_rust")
build("lounge_coffee_table", f_coffee_table(0.8, 0.8, 0.42, round_=True), zl, (-1.4, -1.35, 0))
build("lounge_magazines", lambda mb: f_paper_stack(mb, (0, 0, 0.42), 4, random.Random(7)), zl, (-1.3, -1.35, 0))
build("lounge_plant_a", f_plant("tall", 11, 1.1, M["pot2"]), zl, (-2.5, -2.45, 0), cache="tall11")
build("lounge_plant_b", f_plant("bush", 12, 1.0, M["pot"]), zl, (-0.1, -2.45, 0), cache="bush12")
build("lounge_pendants", lambda mb: [f_pendant(mb, (0.4, y, 1.85), shade=mat("pendant_sage", "#8FA58A", 0.6), r=0.18) for y in (-0.5, 0.4)], zl)
for i, y in enumerate((-0.5, 0.4)):
    light(f"light_lounge_pendant_{i + 1}", "POINT", (LOUNGE_O[0] + 0.4, LOUNGE_O[1] + y, 1.6), 30, (1, 0.8, 0.55), size=0.1)
build("lounge_wall_shelf", lambda mb: (mb.box((0, 0, 0), (0.25, 1.4, 0.03), M["walnut"]),
                                      f_books(mb, -0.6, 0.0, 0, 0.015, 0.2, random.Random(3)) if False else None,
                                      [mb.cyl((0, -0.4 + i * 0.3, 0.07), 0.04, 0.12, STICKY[i % 5], seg=14) for i in range(4)]),
      zl, (2.6, -3.1 + 0.6, 1.6 + 0.0), (0, 0, 0))
camera("cam_lounge", (LOUNGE_O[0] - 2.6, LOUNGE_O[1] + 2.4, 2.0), (LOUNGE_O[0] + 1.5, LOUNGE_O[1] - 1.0, 0.9), 20)

# ---------------------------------------------------------------------------
# 共用區：入口櫃台
# ---------------------------------------------------------------------------
set_coll(new_coll("shared_reception", SH))
REC_O = (1.5, -7.0)
zr = empty("zone_reception", None, (*REC_O, 0), kind="CUBE", size=0.3)
zr["zone"] = "reception"


def f_rec_desk(mb):
    # 櫃台：正面朝 -Y（入口）
    mb.box((0, 0, 0.53), (2.8, 0.5, 1.06), M["plastic_white"], bevel=0.01)
    for i in range(27):
        mb.box((-1.3 + i * 0.1, -0.255, 0.5), (0.06, 0.02, 0.92), M["desk_wood"])
    mb.box((0, -0.05, 1.08), (2.9, 0.62, 0.04), M["walnut"], bevel=0.006)
    mb.box((0, 0.45, 0.73), (2.6, 0.5, 0.03), M["plastic_white"])
    mb.box((0, -0.27, 0.05), (2.7, 0.02, 0.04), M["led_white"])


build("reception_desk", f_rec_desk, zr)
build("reception_monitor", f_monitor(0.5, 0.28, screen_mat("tex_screen_okr")), zr, (0.5, 0.45, 0.745), (0, 0, PI))
build("reception_chair", f_chair(M["fabric_gray"]), zr, (0.3, 1.05, 0), (0, 0, PI), cache="chair_fabric_charcoal_False")
build("reception_desk_items", lambda mb: (mb.cyl((-0.9, -0.1, 1.14), 0.05, 0.08, M["pot"], seg=16),
                                          mb.sphere((-0.9, -0.1, 1.2), 0.07, M["leaf3"], u=10, v=6),
                                          mb.box((0.9, -0.1, 1.105), (0.2, 0.14, 0.01), M["metal"], rot=(0, 0, 0.3))), zr)


def f_feature_wall(mb):
    mb.box((0, 0, 1.15), (3.4, 0.14, 2.3), M["walnut"], bevel=0.008)
    mb.box((0, -0.075, 1.25), (2.6, 0.02, 1.4), M["moss"])
    rng = random.Random(9)
    for i in range(120):  # 苔蘚牆的凹凸
        mb.sphere((rng.uniform(-1.25, 1.25), -0.085, rng.uniform(0.6, 1.9)), rng.uniform(0.03, 0.07),
                  rng.choice([M["moss"], M["leaf2"], M["leaf3"]]), scale=(1, 0.4, 1), u=6, v=4)


fw = build("reception_feature_wall", f_feature_wall, zr, (0, 1.9, 0))
text("reception_logo", "omixor", fw, (0, -0.12, 1.55), (PI / 2, 0, 0), 0.42, mat("logo_white", "#FFFFFF", 0.3, emit="#FFFFFF", strength=2.0), 0.03)
build("reception_entry_mat", lambda mb: mb.box((0, 0, 0.006), (2.4, 1.4, 0.012), M["mat_entry"]), zr, (0, -1.25, 0))
build("reception_armchair_a", f_sofa(0.9, M["fabric_teal"], 1), zr, (-3.0, -0.3, 0), (0, 0, -PI / 2), cache="arm_teal")
build("reception_armchair_b", f_sofa(0.9, M["fabric_teal"], 1), zr, (-3.0, -1.4, 0), (0, 0, -PI / 2), cache="arm_teal")
build("reception_side_table", f_coffee_table(0.5, 0.5, 0.5, round_=True), zr, (-3.0, -0.85, 0))
build("reception_plant", f_plant("tall", 13, 1.2, M["pot3"]), zr, (-1.9, 0.9, 0), cache="tall13")
build("reception_plant_b", f_plant("bush", 14, 1.0, M["pot3"]), zr, (2.1, 1.0, 0), cache="bush14")
camera("cam_reception", (REC_O[0] - 2.8, REC_O[1] - 1.6, 1.9), (REC_O[0] + 0.3, REC_O[1] + 1.2, 1.1), 22)

# ---------------------------------------------------------------------------
# 其他：西側公共角落（圓桌、置物櫃、印表機、植物）
# ---------------------------------------------------------------------------
set_coll(new_coll("shared_commons", SH))
zc = empty("zone_commons", None, (-10.75, -2.5, 0), kind="CUBE", size=0.3)
zc["zone"] = "commons"
build("commons_table", f_coffee_table(1.1, 1.1, 0.74, top=M["desk_wood"], round_=True), zc, (0.5, 0.5, 0))
for i in range(4):
    a = i * PI / 2 + PI / 4
    build(f"commons_chair_{i + 1:02d}", f_chair(M["fabric_teal"], simple=True), zc,
          (0.5 + 0.8 * math.cos(a), 0.5 + 0.8 * math.sin(a), 0), (0, 0, a + PI / 2), cache="chair_simple_teal")


def f_lockers(mb):
    rng = random.Random(4)
    for i in range(8):
        y = -1.6 + i * 0.42
        mb.box((0, y, 0.95), (0.5, 0.4, 1.9), M["plastic_white"] if i % 3 else mat("locker_sage", "#A9B7A0", 0.5), bevel=0.006)
        mb.box((0.255, y + 0.12, 1.1), (0.012, 0.02, 0.15), M["metal_dark"])
        for k in range(4):
            mb.box((0.252, y, 1.6 + k * 0.03), (0.004, 0.25, 0.012), M["plastic_gray"])


build("commons_lockers", f_lockers, zc, (-2.95, 1.2, 0))


def f_printer(mb):
    mb.box((0, 0, 0.35), (0.6, 0.55, 0.7), M["plastic_white"], bevel=0.01)
    mb.box((0, 0, 0.9), (0.55, 0.5, 0.4), M["plastic_white"], bevel=0.01)
    mb.box((0, -0.1, 1.11), (0.5, 0.35, 0.02), M["plastic_gray"])
    mb.box((0.18, -0.26, 1.0), (0.14, 0.02, 0.1), M["black"], rot=(0.3, 0, 0))
    mb.box((0, -0.25, 0.78), (0.4, 0.15, 0.02), M["paper"])


build("commons_printer", f_printer, zc, (-2.85, -2.2, 0), (0, 0, -PI / 2))


def f_cooler(mb):
    mb.box((0, 0, 0.5), (0.32, 0.32, 1.0), M["plastic_white"], bevel=0.01)
    mb.cyl((0, 0, 1.2), 0.13, 0.4, mat("water_blue", "#7FC4E8", 0.05, alpha=0.6), seg=20)
    mb.box((0, -0.165, 0.85), (0.1, 0.02, 0.06), M["blue_led"])


build("commons_water_cooler", f_cooler, zc, (-2.9, -3.2, 0), (0, 0, -PI / 2))
build("commons_bookshelf", f_bookshelf(1.2, 0.35, 1.2, 3, seed=41), zc, (2.0, 4.0, 0), (0, 0, PI))
for i, (px, py, kind, s) in enumerate([(-2.8, 4.6, "tall", 1.2), (2.5, -5.6, "bush", 1.1), (-2.9, -5.6, "tall", 1.0),
                                       (2.8, 2.2, "snake", 1.0), (-0.5, -5.8, "bush", 0.9)]):
    build(f"commons_plant_{i + 1:02d}", f_plant(kind, 50 + i, s, [M["pot"], M["pot2"], M["pot3"]][i % 3]), zc, (px, py, 0))
build("commons_rug", lambda mb: mb.box((0, 0, 0.006), (2.8, 2.8, 0.012), mat("rug_commons", "#B9AE9A", 1.0), bevel=0.004, seg=1), zc, (0.5, 0.5, 0))

# 開放區的大型植物與走道裝飾
set_coll(SH)
deco = empty("decor_open_plan", None)
for i, (px, py, kind, s) in enumerate([(-7.0, 8.4, "tall", 1.25), (7.6, 8.4, "tall", 1.2), (-2.5, 8.4, "bush", 1.0),
                                       (2.5, 8.4, "snake", 1.1), (-2.5, 2.7, "bush", 0.9), (7.6, 2.6, "bush", 1.0),
                                       (-7.0, -3.4, "snake", 1.0), (7.5, -3.0, "tall", 1.1), (5.3, -5.2, "bush", 1.0)]):
    build(f"decor_plant_{i + 1:02d}", f_plant(kind, 60 + i, s, [M["pot"], M["pot2"], M["pot3"]][i % 3]), deco, (px, py, 0))
build("decor_bench_window", lambda mb: (mb.box((0, 0, 0.42), (3.0, 0.45, 0.06), M["desk_wood"], bevel=0.008),
                                        [mb.box((sx, 0, 0.2), (0.06, 0.4, 0.4), M["metal_dark"]) for sx in (-1.3, 1.3)],
                                        [mb.box((-0.8 + 0.5 * i, 0.0, 0.53), (0.4, 0.35, 0.16), [M["fabric_teal"], M["fabric_rust"], M["fabric_sofa"], M["fabric_teal"]][i], bevel=0.05, seg=3) for i in range(4)]),
      deco, (-5.0 + 2.5, 8.55, 0))

# ---------------------------------------------------------------------------
# 日光 / 環境
# ---------------------------------------------------------------------------
world = bpy.data.worlds.new("world_sky")
SC.world = world
if world.node_tree is None:  # Blender 4.x 要先開 use_nodes 才有 node_tree(5.x 一直是開的)
    world.use_nodes = True
wnt = world.node_tree
bg = wnt.nodes.get("Background")
grad_tc = wnt.nodes.new("ShaderNodeTexCoord")
sep = wnt.nodes.new("ShaderNodeSeparateXYZ")
ramp = wnt.nodes.new("ShaderNodeValToRGB")
ramp.color_ramp.elements[0].position = 0.0
ramp.color_ramp.elements[0].color = (*lin("#DCE7EE"), 1)
ramp.color_ramp.elements[1].position = 0.6
ramp.color_ramp.elements[1].color = (*lin("#8FB6DA"), 1)
wnt.links.new(grad_tc.outputs["Generated"], sep.inputs[0])
wnt.links.new(sep.outputs["Z"], ramp.inputs["Fac"])
wnt.links.new(ramp.outputs["Color"], bg.inputs["Color"])
bg.inputs["Strength"].default_value = 0.8
sun_dir = Vector((-0.35, -0.75, -0.6)).normalized()
light("light_sun", "SUN", (0, 20, 15), 2.6, (1.0, 0.94, 0.85), rot=sun_dir.to_track_quat("-Z", "Y").to_euler())
bpy.data.lights["light_sun"].angle = math.radians(2.0)

# ---------------------------------------------------------------------------
# 總覽攝影機
# ---------------------------------------------------------------------------
az, el, dist = math.radians(237), math.radians(40), 70
tgt = Vector((0.4, 0.6, 0.8))
cam_pos = tgt + dist * Vector((math.cos(el) * math.cos(az), math.cos(el) * math.sin(az), math.sin(el)))
cam_overview = camera("cam_overview", cam_pos, tgt, ortho=35.5)

# 角色 metadata 也寫在場景上（glTF extras）
SC["omixor_roles"] = ";".join(f"{r['id']}|{r['name']}|{r['color']}|{r['symbol']}" for r in ROLES)

# ---------------------------------------------------------------------------
# 渲染設定
# ---------------------------------------------------------------------------
R = SC.render
try:
    R.engine = "BLENDER_EEVEE"
except TypeError:  # Blender 4.x 的識別名是 BLENDER_EEVEE_NEXT(5.x 改回 BLENDER_EEVEE)
    R.engine = "BLENDER_EEVEE_NEXT"
ee = SC.eevee
ee.taa_render_samples = SAMPLES
ee.use_raytracing = True
try:
    ee.ray_tracing_method = "SCREEN"
    ee.ray_tracing_options.resolution_scale = "2"
except Exception as e:  # noqa
    print("raytrace opts:", e)
ee.use_fast_gi = True
ee.use_shadows = True
ee.shadow_ray_count = 2
try:
    ee.shadow_pool_size = "1024"
except Exception as e:  # noqa
    print("shadow pool:", e)
SC.view_settings.view_transform = "AgX"
try:
    SC.view_settings.look = "AgX - Medium High Contrast"
except Exception:
    pass
SC.view_settings.exposure = 0.0
R.image_settings.file_format = "PNG"
R.film_transparent = False
SC.camera = cam_overview
R.resolution_x, R.resolution_y = 1920, 1080

# ---------------------------------------------------------------------------
# 存檔 / 匯出
# ---------------------------------------------------------------------------
stats_faces = sum(len(o.data.polygons) for o in bpy.data.objects if o.type == "MESH")
stats = {t: sum(1 for o in bpy.data.objects if o.type == t) for t in ("MESH", "EMPTY", "LIGHT", "CAMERA")}
print(f"[office] objects={len(bpy.data.objects)} {stats} faces={stats_faces}")

bpy.context.preferences.filepaths.save_version = 0  # 不產生 .blend1 備份
blend_path = os.path.join(HERE, "office.blend")
bpy.ops.wm.save_as_mainfile(filepath=blend_path, compress=True)
print("[office] saved", blend_path)

glb_path = os.path.join(HERE, "office.glb")
bpy.ops.export_scene.gltf(
    filepath=glb_path, export_format="GLB", export_apply=True, export_extras=True,
    export_cameras=True, export_lights=False, export_yup=True,
    export_image_format="JPEG", export_jpeg_quality=88, export_materials="EXPORT",
)
print("[office] exported", glb_path, os.path.getsize(glb_path) // 1024, "KB")

# 導航格 navgrid.json ＋ 具名地點 places.json（給 viewer/ 用；--no-nav 可略過）
if "--no-nav" not in ARGV:
    import importlib.util
    sys.dont_write_bytecode = True  # 不在 office3d/ 留 __pycache__
    _spec = importlib.util.spec_from_file_location("omixor_build_nav", os.path.join(HERE, "build_nav.py"))
    _nav = importlib.util.module_from_spec(_spec)
    _spec.loader.exec_module(_nav)
    _nav.generate(HERE, SC)

# ---------------------------------------------------------------------------
# 渲染
# ---------------------------------------------------------------------------
if not NO_RENDER:
    os.makedirs(os.path.join(HERE, "renders"), exist_ok=True)
    cut = [o for o in bpy.data.objects if o.get("cutaway")]
    jobs = [("overview", cam_overview, (1920, 1080), True)]
    for r in ROLES:
        jobs.append((r["id"], bpy.data.objects[f"cam_{r['id']}"], (1280, 720), False))
    for key, cam, (w, h), cutaway in jobs:
        if ONLY and key not in ONLY:
            continue
        for o in cut:
            o.hide_render = cutaway
        SC.camera = cam
        R.resolution_x, R.resolution_y = w, h
        R.filepath = os.path.join(HERE, "renders", f"{key}.png")
        bpy.ops.render.render(write_still=True)
        print("[office] rendered", R.filepath)
    for o in cut:
        o.hide_render = False
    SC.camera = cam_overview
    R.resolution_x, R.resolution_y = 1920, 1080
print("[office] done")
