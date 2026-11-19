"""
omixor 虛擬辦公室 — 8 個角色的程序化低多邊形模型

用法：
    blender -b -P build_characters.py                 # 重建、存 characters.blend、匯出 characters.glb、渲染預覽
    blender -b -P build_characters.py -- --no-render  # 不渲染預覽圖

每個角色是一組「關節節點」（pivot 在關節），由前端程式擺動做走路 / 坐下動畫：

    <id>                      Empty，原點在腳底中心，面向 +Y（glTF：-Z）。extras 帶角色資料與骨段長度
      <id>_hip                骨盆，pivot 在髖關節高度
        <id>_torso            軀幹，pivot 在腰
          <id>_head           頭，pivot 在脖子根部
          <id>_upperarm_L/R   上臂，pivot 在肩
            <id>_forearm_L/R  前臂＋手（手上的道具也在這裡），pivot 在手肘
        <id>_thigh_L/R        大腿，pivot 在髖關節
          <id>_shin_L/R       小腿＋鞋，pivot 在膝蓋

左右以角色自己為準（面向 +Y 時，L 在 -X、R 在 +X）。節點繞本地 X 軸正轉 = 往前擺。
"""
import bpy
import bmesh
import math
import os
import sys

from mathutils import Matrix, Vector, Euler

PI = math.pi
HERE = os.path.dirname(os.path.abspath(__file__))
ARGV = sys.argv[sys.argv.index("--") + 1:] if "--" in sys.argv else []
NO_RENDER = "--no-render" in ARGV

APPLE = {
    "purple": "#AF52DE", "indigo": "#5856D6", "green": "#34C759",
    "pink": "#FF2D55", "teal": "#30B0C7", "orange": "#FF9500",
}


def srgb(h):
    h = h.lstrip("#")
    return tuple(int(h[i:i + 2], 16) / 255 for i in (0, 2, 4))


def lin(h):
    if isinstance(h, str):
        h = srgb(h)
    return tuple((c / 12.92) if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4 for c in h)


def shade(h, k):
    """把 sRGB 色乘上 k（k<1 變暗、k>1 變亮），回傳 hex"""
    r, g, b = srgb(h)
    if k <= 1:
        c = (r * k, g * k, b * k)
    else:
        t = k - 1
        c = (r + (1 - r) * t, g + (1 - g) * t, b + (1 - b) * t)
    return "#" + "".join(f"{int(max(0, min(1, v)) * 255):02X}" for v in c)


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
SC.name = "omixor_characters"
CHAR_COLL = bpy.data.collections.new("characters")
SC.collection.children.link(CHAR_COLL)

MATS = {}


def mat(name, color, rough=0.6, metal=0.0, emit=None, strength=0.0):
    if name in MATS:
        return MATS[name]
    m = bpy.data.materials.new(name)
    if m.node_tree is None:
        m.use_nodes = True
    b = m.node_tree.nodes.get("Principled BSDF")
    c = lin(color)
    b.inputs["Base Color"].default_value = (*c, 1.0)
    b.inputs["Roughness"].default_value = rough
    b.inputs["Metallic"].default_value = metal
    if emit is not None:
        b.inputs["Emission Color"].default_value = (*lin(emit), 1.0)
        b.inputs["Emission Strength"].default_value = strength
    m.diffuse_color = (*c, 1.0)
    MATS[name] = m
    return m


def MT(loc=(0, 0, 0), rot=(0, 0, 0), scale=(1, 1, 1)):
    return (Matrix.Translation(Vector(loc)) @ Euler(rot, "XYZ").to_matrix().to_4x4()
            @ Matrix.Diagonal((scale[0], scale[1], scale[2], 1.0)))


class MB:
    """多個基本形狀組成一個網格（本地座標，多材質）"""

    def __init__(self):
        self.bm = bmesh.new()
        self.mats = []

    def mi(self, m):
        if m not in self.mats:
            self.mats.append(m)
        return self.mats.index(m)

    def _apply(self, verts, m, smooth):
        i = self.mi(m)
        for f in {f for v in verts for f in v.link_faces}:
            f.material_index = i
            f.smooth = smooth

    def box(self, loc, size, m, rot=(0, 0, 0), bevel=0.0):
        r = bmesh.ops.create_cube(self.bm, size=1.0, matrix=MT(loc, rot, size))
        v = r["verts"]
        self._apply(v, m, False)
        if bevel > 0:
            edges = list({e for x in v for e in x.link_edges})
            res = bmesh.ops.bevel(self.bm, geom=edges, offset=bevel, segments=1, affect="EDGES",
                                  profile=0.5, clamp_overlap=True)
            i = self.mi(m)
            for f in res["faces"]:
                f.material_index = i
        return v

    def cyl(self, loc, r, depth, m, rot=(0, 0, 0), r2=None, seg=10, scale=(1, 1, 1), smooth=True, matrix=None):
        rr = bmesh.ops.create_cone(self.bm, cap_ends=True, cap_tris=False, segments=seg, radius1=r,
                                   radius2=r if r2 is None else r2, depth=depth,
                                   matrix=matrix if matrix is not None else MT(loc, rot, scale))
        self._apply(rr["verts"], m, smooth)
        return rr["verts"]

    def seg(self, a, b, r, m, r2=None, sides=10):
        """從 a 到 b 的圓柱（世界方向任意）"""
        a, b = Vector(a), Vector(b)
        d = b - a
        q = d.to_track_quat("Z", "Y")
        return self.cyl(None, r, d.length, m, r2=r2, seg=sides,
                        matrix=Matrix.Translation((a + b) / 2) @ q.to_matrix().to_4x4())

    def sphere(self, loc, r, m, scale=(1, 1, 1), rot=(0, 0, 0), u=14, v=9, smooth=True):
        rr = bmesh.ops.create_uvsphere(self.bm, u_segments=u, v_segments=v, radius=r, matrix=MT(loc, rot, scale))
        self._apply(rr["verts"], m, smooth)

    def torus(self, loc, R, r, m, rot=(0, 0, 0), seg=16, rseg=6, arc=2 * PI, start=0.0, scale=(1, 1, 1)):
        X = MT(loc, rot, scale)
        closed = abs(arc - 2 * PI) < 1e-6
        n = seg if closed else seg + 1
        rings = []
        for i in range(n):
            a = start + arc * i / seg
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
                f.material_index, f.smooth = idx, True
                faces.append(f)
        bmesh.ops.recalc_face_normals(self.bm, faces=faces)

    def finish(self, name):
        bmesh.ops.recalc_face_normals(self.bm, faces=self.bm.faces[:])
        self.bm.normal_update()
        me = bpy.data.meshes.new(name)
        self.bm.to_mesh(me)
        self.bm.free()
        for m in self.mats:
            me.materials.append(m)
        return me


def node(name, parent, loc, mb=None, rot=(0, 0, 0)):
    data = mb.finish(name) if mb is not None else None
    o = bpy.data.objects.new(name, data)
    CHAR_COLL.objects.link(o)
    o.parent = parent
    o.location = loc
    o.rotation_euler = rot
    if data is None:
        o.empty_display_type = "PLAIN_AXES"
        o.empty_display_size = 0.2
    return o


# ---------------------------------------------------------------------------
# 共用材質
# ---------------------------------------------------------------------------
M = dict(
    eye=mat("ch_eye", "#1E1B24", 0.3),
    white=mat("ch_white", "#F5F5F2", 0.55),
    shirt_white=mat("ch_shirt_white", "#F2F2EE", 0.7),
    mouth=mat("ch_mouth", "#8A3A3A", 0.6),
    black=mat("ch_black", "#1D1D22", 0.45),
    frame=mat("ch_glasses_frame", "#202024", 0.35),
    lens=mat("ch_glasses_lens", "#BFD8E6", 0.05, metal=0.2),
    metal=mat("ch_metal", "#B9BCC2", 0.3, metal=0.9),
    gold=mat("ch_gold", "#D4AF37", 0.3, metal=0.9),
    paper=mat("ch_paper", "#FBFAF5", 0.8),
    sticky_y=mat("ch_sticky_yellow", "#FFE066", 0.8),
    sticky_p=mat("ch_sticky_pink", "#FF8FB1", 0.8),
    sticky_b=mat("ch_sticky_blue", "#8FD3FF", 0.8),
    reflect=mat("ch_reflective", "#E6E9EE", 0.25, metal=0.4, emit="#E6E9EE", strength=0.15),
    rubber=mat("ch_sole", "#EDEBE6", 0.8),
    sole_dark=mat("ch_sole_dark", "#2A2726", 0.8),
)
SKIN = {
    "light": mat("ch_skin_light", "#F3D3BD", 0.7),
    "fair": mat("ch_skin_fair", "#EBC3A3", 0.7),
    "medium": mat("ch_skin_medium", "#D6A27E", 0.7),
    "tan": mat("ch_skin_tan", "#B98060", 0.7),
    "deep": mat("ch_skin_deep", "#7E5440", 0.7),
}

# ---------------------------------------------------------------------------
# 角色定義
# ---------------------------------------------------------------------------
CHARS = [
    dict(id="ceo", name="CEO", color="purple", H=1.84, build=dict(chest=0.175, waist=0.155, sh=0.215, hip=0.15),
         skin="fair", hair="slick", hair_c="#C9C9CF", top="blazer", top_c=shade(APPLE["purple"], 0.55),
         top2=APPLE["purple"], pants="#34343C", shoes="#151517", shoe_style="dress",
         extras=["tie", "pocket_square", "watch"]),
    dict(id="product-manager", name="產品經理", color="indigo", H=1.70, build=dict(chest=0.15, waist=0.135, sh=0.185, hip=0.145),
         skin="light", hair="bob", hair_c="#6B4A32", top="casual_blazer", top_c=APPLE["indigo"],
         top2=shade(APPLE["indigo"], 0.7), pants="#D9C7A3", shoes="#F4F4F2", shoe_style="sneaker",
         extras=["sticky_pad"], hold_L=True),
    dict(id="engineer", name="工程師", color="green", H=1.78, build=dict(chest=0.145, waist=0.135, sh=0.19, hip=0.14),
         skin="medium", hair="spiky", hair_c="#1F1A17", top="hoodie", top_c=APPLE["green"],
         top2=shade(APPLE["green"], 0.72), pants="#3E5D8C", shoes="#2E2E33", shoe_style="sneaker",
         extras=["glasses_round"]),
    dict(id="course-dev", name="課程開發", color="indigo", H=1.66, build=dict(chest=0.15, waist=0.14, sh=0.18, hip=0.155),
         skin="light", hair="bun", hair_c="#9A4A2A", top="sweater", top_c=shade(APPLE["indigo"], 0.85),
         top2=shade(APPLE["indigo"], 1.45), pants="#6F6A66", shoes="#6B4428", shoe_style="boot",
         extras=["notebook", "pencil_ear"], hold_L=True),
    dict(id="narrator", name="旁白製作", color="purple", H=1.75, build=dict(chest=0.16, waist=0.15, sh=0.195, hip=0.145),
         skin="deep", hair="curly", hair_c="#18120F", top="turtleneck", top_c=APPLE["purple"],
         top2=shade(APPLE["purple"], 0.7), pants="#232328", shoes="#18181B", shoe_style="sneaker",
         extras=["headphones", "beard"]),
    dict(id="marketing", name="行銷", color="pink", H=1.68, build=dict(chest=0.145, waist=0.13, sh=0.18, hip=0.15),
         skin="fair", hair="ponytail", hair_c="#E2B65C", top="jacket", top_c=APPLE["pink"],
         top2=shade(APPLE["pink"], 0.72), pants="#F1EEE8", shoes=APPLE["pink"], shoe_style="sneaker",
         extras=["scarf", "megaphone"], hold_R=True),
    dict(id="visitor-analyst", name="訪客分析師", color="teal", H=1.62, build=dict(chest=0.14, waist=0.13, sh=0.175, hip=0.14),
         skin="tan", hair="pixie", hair_c="#2B2320", top="cardigan", top_c=APPLE["teal"],
         top2=shade(APPLE["teal"], 0.7), pants="#2C3550", shoes="#8A8F98", shoe_style="sneaker",
         extras=["glasses_square", "tablet"], hold_L=True),
    dict(id="triager", name="Issue 整理員", color="orange", H=1.79, build=dict(chest=0.185, waist=0.175, sh=0.215, hip=0.16),
         skin="medium", hair="cap", hair_c="#3A2A1E", top="vest", top_c=APPLE["orange"],
         top2="#6E737C", pants="#6B6A4E", shoes="#5A3B22", shoe_style="boot",
         extras=["clipboard", "cargo"], hold_L=True),
]


# ---------------------------------------------------------------------------
# 部件
# ---------------------------------------------------------------------------
def build_head(c, P, mats):
    """頭：pivot 在脖子根部"""
    mb = MB()
    skin, hairm = mats["skin"], mats["hair"]
    rh, hc = P["rh"], P["hc"]
    mb.cyl((0, 0, 0.02), 0.047, 0.1, skin, seg=10)
    mb.sphere((0, 0, hc), rh, skin, scale=(0.9, 0.95, 1.08), u=16, v=10)
    # 臉
    fy = 0.95 * rh
    for sx in (-1, 1):
        mb.sphere((sx * 0.037, fy * 0.9, hc + 0.008), 0.0125, M["eye"], u=8, v=6)
        mb.box((sx * 0.039, fy * 0.9, hc + 0.042), (0.034, 0.012, 0.008), hairm, rot=(0, sx * -0.12, 0))
        mb.sphere((sx * 0.9 * rh, -0.004, hc - 0.002), 0.02, skin, scale=(0.5, 1.0, 1.35), u=8, v=6)
    mb.sphere((0, fy + 0.002, hc - 0.012), 0.014, skin, u=8, v=6)
    mb.box((0, fy * 0.93, hc - 0.05), (0.032, 0.012, 0.006), M["mouth"])

    st = c["hair"]
    cap_c = (0, -0.022, hc + 0.03)
    cap_r = rh * 1.04

    def cap(scale=(0.96, 1.0, 1.0)):
        mb.sphere(cap_c, cap_r, hairm, scale=scale, u=16, v=10)

    def fringe(dz=0.072, w=0.15, h=0.04, tilt=0.35, dy=0.0):
        mb.box((0, rh * 0.62 + dy, hc + dz), (w, 0.06, h), hairm, rot=(tilt, 0, 0), bevel=0.012)

    if st == "slick":  # 灰白油頭（往後梳）
        cap()
        mb.sphere((0, 0.035, hc + 0.085), 0.075, hairm, scale=(1.2, 1.1, 0.6), u=12, v=8)
        mb.sphere((0, -0.03, hc + 0.06), 0.1, hairm, scale=(1.05, 1.0, 0.75), u=12, v=8)
        for sx in (-1, 1):  # 鬢角
            mb.box((sx * 0.092, 0.02, hc - 0.005), (0.016, 0.03, 0.05), hairm)
    elif st == "bob":  # 齊耳短鮑伯＋瀏海
        cap((1.02, 1.03, 1.0))
        mb.sphere((0, -0.03, hc - 0.02), rh * 1.06, hairm, scale=(1.04, 0.95, 0.85), u=16, v=10)
        for sx in (-1, 1):
            mb.box((sx * 0.1, 0.025, hc - 0.035), (0.04, 0.09, 0.12), hairm, bevel=0.015)
        fringe(dz=0.07, w=0.17, h=0.05, tilt=0.5, dy=0.012)
    elif st == "spiky":  # 亂翹短髮
        cap()
        fringe(dz=0.075, w=0.15, h=0.035, tilt=0.6)
        k = 0
        for ring, (dz, rr, n) in enumerate(((0.115, 0.0, 1), (0.1, 0.055, 6), (0.07, 0.085, 8))):
            for i in range(n):
                a = 2 * PI * i / n + ring * 0.4
                bx, by = rr * math.cos(a), rr * math.sin(a) - 0.02
                tip = Vector((bx * 1.6, by * 1.4 - 0.02, hc + dz + 0.07 - ring * 0.012))
                mb.seg((bx, by, hc + dz - 0.02), tip, 0.03, hairm, r2=0.0, sides=5)
                k += 1
    elif st == "bun":  # 盤髮包包頭
        cap((0.97, 1.0, 1.0))
        fringe(dz=0.078, w=0.16, h=0.03, tilt=0.4)
        mb.sphere((0, -0.06, hc + 0.13), 0.055, hairm, scale=(1, 1, 0.9), u=12, v=8)
        mb.torus((0, -0.05, hc + 0.105), 0.035, 0.008, mats["accent"], rot=(0.4, 0, 0), seg=12)
    elif st == "curly":  # 捲短髮（小球堆）
        cap((0.95, 0.98, 0.95))
        for i in range(14):
            a = 2 * PI * i / 14
            mb.sphere((0.085 * math.cos(a), 0.075 * math.sin(a) - 0.03, hc + 0.07 + 0.012 * math.sin(3 * a)), 0.034, hairm, u=8, v=6)
        for i in range(5):
            a = 2 * PI * i / 5
            mb.sphere((0.04 * math.cos(a), 0.04 * math.sin(a) - 0.02, hc + 0.11), 0.035, hairm, u=8, v=6)
    elif st == "ponytail":  # 高馬尾
        cap((0.97, 1.0, 1.0))
        fringe(dz=0.072, w=0.16, h=0.035, tilt=0.45)
        mb.torus((0, -0.1, hc + 0.07), 0.022, 0.009, mats["accent"], rot=(PI / 2 - 0.5, 0, 0), seg=12)
        mb.seg((0, -0.11, hc + 0.07), (0, -0.17, hc - 0.03), 0.04, hairm, r2=0.028, sides=8)
        mb.seg((0, -0.17, hc - 0.03), (0, -0.165, hc - 0.16), 0.028, hairm, r2=0.008, sides=8)
    elif st == "pixie":  # 俐落短髮＋斜瀏海
        cap((0.96, 0.98, 0.97))
        mb.box((-0.02, rh * 0.68, hc + 0.065), (0.17, 0.05, 0.045), hairm, rot=(0.55, 0.0, 0.3), bevel=0.012)
        mb.sphere((0.03, -0.02, hc + 0.09), 0.08, hairm, scale=(1.2, 1.1, 0.6), u=12, v=8)
    elif st == "cap":  # 短髮＋棒球帽
        cap((0.96, 1.0, 0.95))
        cm, cm2 = mats["accent"], mats["accent2"]
        mb.sphere((0, -0.01, hc + 0.045), rh * 1.07, cm, scale=(0.98, 1.02, 0.78), u=16, v=10)
        mb.box((0, -0.012, hc + 0.02), (0.205, 0.22, 0.03), cm)  # 帽緣下沿
        mb.box((0, 0.155, hc + 0.035), (0.17, 0.11, 0.012), cm2, rot=(-0.12, 0, 0), bevel=0.004)  # 帽簷
        mb.sphere((0, -0.01, hc + 0.13), 0.012, cm2, u=8, v=6)
        # 帽子正面標誌
        mb.box((0, 0.107, hc + 0.075), (0.05, 0.01, 0.035), M["white"], rot=(-0.35, 0, 0))

    ex = c["extras"]
    if "glasses_round" in ex or "glasses_square" in ex:
        sq = "glasses_square" in ex
        gy = fy * 0.98 + 0.012
        for sx in (-1, 1):
            if sq:
                mb.torus((sx * 0.038, gy, hc + 0.008), 0.027, 0.0045, M["frame"], rot=(PI / 2, PI / 4, 0), seg=4, rseg=4)
            else:
                mb.torus((sx * 0.038, gy, hc + 0.008), 0.024, 0.0045, M["frame"], rot=(PI / 2, 0, 0), seg=14, rseg=5)
            mb.cyl((sx * 0.038, gy, hc + 0.008), 0.022, 0.002, M["lens"], rot=(PI / 2, 0, 0), seg=12 if not sq else 4)
            mb.box((sx * 0.087, gy - 0.05, hc + 0.012), (0.005, 0.1, 0.006), M["frame"])
        mb.box((0, gy, hc + 0.014), (0.03, 0.005, 0.005), M["frame"])
    if "headphones" in ex:
        hm, hm2 = mats["accent"], M["black"]
        mb.torus((0, -0.005, hc + 0.01), 0.112, 0.012, hm2, rot=(PI / 2, 0, 0), arc=PI, seg=14, rseg=6, scale=(1, 1, 1.15))
        for sx in (-1, 1):
            mb.cyl((sx * 0.108, -0.005, hc - 0.005), 0.042, 0.04, hm2, rot=(0, PI / 2, 0), seg=14)
            mb.cyl((sx * 0.13, -0.005, hc - 0.005), 0.034, 0.012, hm, rot=(0, PI / 2, 0), seg=14)
    if "beard" in ex:
        mb.sphere((0, 0.035, hc - 0.05), 0.085, hairm, scale=(0.95, 0.75, 0.62), u=12, v=8)
        mb.box((0, fy * 0.97, hc - 0.03), (0.06, 0.02, 0.012), hairm)
        mb.box((0, fy * 0.93, hc - 0.05), (0.03, 0.014, 0.006), M["mouth"])
    if "pencil_ear" in ex:
        mb.seg((0.098, 0.04, hc + 0.03), (0.095, -0.07, hc + 0.0), 0.006, mat("ch_pencil", "#F2C230", 0.6), sides=6)
    return mb


def torso_shell(mb, P, m, z0, z1, wx, wy, cx, cy, seg=10, inflate=0.0):
    """軀幹外殼：從腰 (z0,寬 wx,wy) 收到胸 (z1,cx,cy) 的橢圓台"""
    mb.cyl((0, 0, (z0 + z1) / 2), wx + inflate, z1 - z0, m, r2=cx + inflate, seg=seg,
           scale=(1, (wy + cy) / (wx + cx), 1))


def build_torso(c, P, mats):
    """軀幹：pivot 在腰"""
    mb = MB()
    b = c["build"]
    top, top2, skin = mats["top"], mats["top2"], mats["skin"]
    L = P["torso_len"]
    wx, cx = b["waist"], b["chest"]
    wy, cy = wx * 0.68, cx * 0.68
    sh = b["sh"]
    style = c["top"]
    z0 = -0.06
    hem = -0.06
    if style in ("blazer", "casual_blazer", "cardigan"):
        hem = -0.16
    torso_shell(mb, P, top, hem, L - 0.05, wx * 1.02, wy * 1.05, cx, cy)
    # 肩膀
    mb.sphere((0, 0, L - 0.07), 1.0, top, scale=(sh * 0.92, cy * 0.98, 0.06), u=14, v=8)
    fy = cy * 1.0  # 胸前表面 y

    def front_panel(m, zb, zt, wb, wt, dy=0.0):
        mb.cyl((0, fy * 0.86 + dy, (zb + zt) / 2), wb, zt - zb, m, r2=wt, seg=4,
               rot=(0, 0, PI / 4), scale=(1, 0.18, 1), smooth=False)

    if style == "blazer":  # 西裝外套＋白襯衫＋領帶
        front_panel(M["shirt_white"], L * 0.45, L - 0.02, 0.012, 0.075, 0.012)
        for sx in (-1, 1):
            mb.box((sx * 0.05, fy * 0.95 + 0.006, L * 0.66), (0.035, 0.012, L * 0.45), top2, rot=(0, sx * 0.38, 0))
        mb.seg((0, fy + 0.012, L - 0.035), (0, fy + 0.03, L * 0.3), 0.017, mats["tie"], r2=0.026, sides=4)
        mb.box((0, fy + 0.018, L - 0.04), (0.035, 0.02, 0.03), mats["tie"])
        for z in (L * 0.32, L * 0.18):
            mb.sphere((0, fy + 0.02, z), 0.009, M["black"], u=6, v=4)
        mb.box((0, fy * 0.6, -0.1), (wx * 1.9, wy * 1.0, 0.015), top)  # 下擺
    elif style == "casual_blazer":  # 休閒西裝＋白 T
        front_panel(M["shirt_white"], L * 0.38, L - 0.02, 0.03, 0.08, 0.012)
        for sx in (-1, 1):
            mb.box((sx * 0.058, fy * 0.95 + 0.006, L * 0.66), (0.035, 0.012, L * 0.42), top2, rot=(0, sx * 0.3, 0))
        mb.box((cx * 0.55, fy * 0.85 + 0.02, L * 0.66), (0.05, 0.01, 0.06), M["sticky_y"], rot=(0, 0.1, 0))  # 胸前便利貼
        mb.box((0.0, fy * 0.85, L * 0.28), (0.012, 0.015, 0.012), M["black"])
    elif style == "hoodie":  # 帽 T：兜帽、口袋、抽繩
        mb.torus((0, -cy * 0.55, L + 0.0), 0.085, 0.045, top, rot=(1.1, 0, 0), seg=14, rseg=8, scale=(1.05, 1, 1))
        mb.sphere((0, -cy * 1.05, L - 0.06), 0.1, top, scale=(1.05, 0.55, 0.9), u=12, v=8)
        mb.box((0, wy * 0.95, L * 0.22), (wx * 1.25, 0.03, L * 0.26), top2, bevel=0.01)
        for sx in (-1, 1):
            mb.seg((sx * 0.03, fy * 0.9 + 0.01, L - 0.03), (sx * 0.032, fy * 0.95 + 0.012, L * 0.6), 0.005, M["white"], sides=5)
        mb.cyl((0, 0, hem + 0.01), wx * 1.04, 0.05, top2, r2=wx * 1.05, scale=(1, wy / wx * 1.06, 1), seg=10)
    elif style == "sweater":  # 針織衫：羅紋下擺、胸前條紋、圓領
        mb.cyl((0, 0, hem + 0.025), wx * 1.05, 0.06, top2, scale=(1, wy / wx * 1.07, 1), seg=10)
        z = L * 0.62
        k = (z - hem) / (L - 0.05 - hem)
        rr = wx + (cx - wx) * k
        mb.cyl((0, 0, z), rr * 1.035, 0.05, top2, scale=(1, (wy + cy) / (wx + cx) * 1.04, 1), seg=10)
        mb.cyl((0, 0, z - 0.07), rr * 1.03, 0.02, M["white"], scale=(1, (wy + cy) / (wx + cx) * 1.04, 1), seg=10)
        mb.torus((0, 0.0, L - 0.02), 0.055, 0.014, top2, seg=14)
    elif style == "turtleneck":  # 高領毛衣
        mb.cyl((0, 0, L + 0.02), 0.06, 0.09, top2, r2=0.055, seg=12)
        mb.torus((0, 0, L - 0.015), 0.06, 0.016, top, seg=14)
        for i in range(5):  # 毛衣直紋
            x = -0.06 + i * 0.03
            mb.box((x, fy * 0.93 + 0.004, L * 0.48), (0.008, 0.01, L * 0.7), top2)
    elif style == "jacket":  # 亮色外套：拉鍊、立領、口袋
        mb.box((0, fy * 0.95 + 0.005, L * 0.45), (0.008, 0.012, L * 0.95), M["metal"])
        for sx in (-1, 1):
            mb.box((sx * 0.07, fy * 0.88 + 0.004, L * 0.25), (0.06, 0.012, 0.012), top2, rot=(0, 0, sx * 0.3))
        mb.cyl((0, 0, hem + 0.012), wx * 1.05, 0.03, top2, scale=(1, wy / wx * 1.07, 1), seg=10)
    elif style == "cardigan":  # 開襟針織外套＋白襯衫＋鈕扣
        front_panel(M["shirt_white"], hem + 0.04, L - 0.02, 0.05, 0.06, 0.008)
        for sx in (-1, 1):
            mb.box((sx * 0.038, fy * 0.92 + 0.012, L * 0.42), (0.016, 0.012, L * 0.95), top2, rot=(0, sx * 0.08, 0))
            for k in range(4):
                mb.sphere((sx * 0.038, fy * 0.95 + 0.022, L * 0.12 + k * 0.08), 0.007, M["white"], u=6, v=4)
        mb.torus((0, 0.01, L - 0.02), 0.052, 0.012, M["shirt_white"], seg=14)
        mb.box((-cx * 0.55, fy * 0.85 + 0.02, L * 0.72), (0.05, 0.012, 0.05), top2)  # 胸袋
    elif style == "vest":  # 灰色長袖＋橘色工作背心（反光條、口袋）
        vm = mats["accent"]
        torso_shell(mb, P, vm, -0.02, L - 0.07, wx, wy, cx, cy, inflate=0.012)
        for z in (L * 0.25, L * 0.5):
            k = (z + 0.02) / (L - 0.05)
            rr = wx + (cx - wx) * k
            mb.cyl((0, 0, z), rr * 1.09 + 0.01, 0.035, M["reflect"], scale=(1, (wy + cy) / (wx + cx) * 1.08, 1), seg=10)
        mb.box((0, fy + 0.012, L * 0.4), (0.03, 0.012, L * 0.85), top)  # 前開口露出內衣
        for sx in (-1, 1):
            mb.box((sx * 0.075, fy * 0.95 + 0.016, L * 0.68), (0.055, 0.016, 0.06), vm, bevel=0.004)
            mb.box((sx * 0.08, fy * 0.85 + 0.02, L * 0.12), (0.065, 0.018, 0.07), vm, bevel=0.004)
            mb.box((sx * 0.06, -cy * 0.2, L - 0.04), (0.06, cy * 1.6, 0.02), vm)  # 肩帶
        mb.box((0.075, fy * 0.95 + 0.026, L * 0.7), (0.012, 0.004, 0.06), M["black"])  # 筆
    if "pocket_square" in c["extras"]:
        mb.box((-cx * 0.55, fy * 0.86 + 0.012, L * 0.74), (0.045, 0.014, 0.02), M["white"], rot=(0, 0.1, 0))
    if "scarf" in c["extras"]:
        sm, sm2 = mats["scarf"], mats["scarf2"]
        mb.torus((0, 0.01, L - 0.005), 0.07, 0.03, sm, seg=14, rseg=7, scale=(1, 0.95, 1.1))
        mb.box((0.04, fy + 0.025, L * 0.62), (0.05, 0.02, L * 0.5), sm, rot=(0, -0.1, 0), bevel=0.006)
        for z in (L * 0.52, L * 0.68):
            mb.box((0.042, fy + 0.037, z), (0.052, 0.004, 0.02), sm2, rot=(0, -0.1, 0))
        mb.box((0.0, fy + 0.03, L * 0.75), (0.05, 0.02, L * 0.4), sm, rot=(0, 0.25, 0), bevel=0.006)
    return mb


def build_upper_arm(c, P, mats, side):
    mb = MB()
    La = P["La"]
    r = 0.046 if c["build"]["chest"] < 0.16 else 0.052
    m = mats["sleeve"]
    mb.sphere((0, 0, -0.005), r * 1.08, m, u=10, v=7)
    mb.cyl((0, 0, -La / 2), r * 1.02, La, m, r2=r * 0.92, seg=10)
    if c["top"] == "vest":
        pass
    return mb


def build_forearm(c, P, mats, side):
    mb = MB()
    Lf = P["Lf"]
    r = 0.04 if c["build"]["chest"] < 0.16 else 0.045
    skin = mats["skin"]
    sleeve = mats["sleeve"]
    ex = c["extras"]
    rolled = c["top"] == "casual_blazer"
    arm_len = Lf - 0.085
    if rolled:
        mb.cyl((0, 0, -0.05), r * 1.12, 0.1, sleeve, r2=r * 1.05, seg=10)
        mb.cyl((0, 0, -0.105), r * 1.18, 0.03, mats["top2"], seg=10)
        mb.cyl((0, 0, -0.12 - (arm_len - 0.12) / 2), r * 0.95, arm_len - 0.12, skin, r2=r * 0.8, seg=10)
    else:
        mb.cyl((0, 0, -arm_len / 2), r * 1.0, arm_len, sleeve, r2=r * 0.88, seg=10)
        cuff = mats["top2"] if c["top"] in ("hoodie", "sweater", "jacket", "cardigan") else sleeve
        mb.cyl((0, 0, -arm_len + 0.015), r * 0.95, 0.03, cuff, seg=10)
    mb.sphere((0, 0, -0.01), r * 1.05, sleeve, u=10, v=6)  # 手肘
    hz = -Lf + 0.045
    mb.sphere((0, 0.0, hz), 0.042, skin, scale=(0.75, 0.95, 1.15), u=10, v=7)  # 手
    mb.sphere((side * -0.025, 0.02, hz + 0.012), 0.016, skin, u=6, v=4)  # 拇指
    if side == -1 and "watch" in ex:
        pass
    if side == 1 and "watch" in ex:
        mb.cyl((0, 0, -arm_len + 0.035), r * 0.92, 0.022, M["gold"], seg=10)
    # 手持道具（前臂本地座標）。抱持姿勢下：本地 -Z = 前臂方向（橫過胸前）、+Y = 朝上、
    # -side*X = 朝向胸口。垂手姿勢下：-Z 朝下、+Y 朝前。
    toward = -side  # 本地 X 中朝胸口的方向
    if side == -1 and "sticky_pad" in ex:  # 手掌上一疊便利貼（水平）
        for i, m in enumerate((M["sticky_y"], M["sticky_p"], M["sticky_b"])):
            mb.box((toward * 0.01, 0.035 + i * 0.011, hz + 0.0), (0.08, 0.011, 0.08), m, rot=(0, 0.15 * i, 0))
        mb.seg((toward * 0.03, 0.075, hz + 0.04), (toward * 0.05, 0.075, hz - 0.08), 0.005, M["black"], sides=5)
    if side == -1 and ("notebook" in ex or "clipboard" in ex):  # 直立抱在胸前
        if "notebook" in ex:
            nm = mats["notebook"]
            cz = -Lf * 0.55
            mb.box((toward * 0.035, 0.1, cz), (0.028, 0.24, 0.18), nm, bevel=0.004)
            mb.box((toward * 0.019, 0.1, cz), (0.004, 0.22, 0.16), M["paper"])
            mb.cyl((toward * 0.035, 0.1, cz + 0.092), 0.011, 0.24, M["metal"], rot=(PI / 2, 0, 0), seg=8)
            mb.box((toward * 0.015, 0.15, cz), (0.004, 0.05, 0.1), M["white"])
        else:
            cz = -Lf * 0.55
            mb.box((toward * 0.03, 0.09, cz), (0.014, 0.3, 0.22), mat("ch_clip_board", "#A97A4C", 0.7), bevel=0.004)
            mb.box((toward * 0.03 - toward * 0.009, 0.08, cz), (0.004, 0.24, 0.19), M["paper"])
            mb.box((toward * 0.03 - toward * 0.012, 0.23, cz), (0.02, 0.03, 0.07), M["metal"])
            for k in range(5):
                mb.box((toward * 0.03 - toward * 0.012, 0.17 - k * 0.04, cz), (0.002, 0.008, 0.13),
                       mats["accent"] if k == 0 else M["black"])
    if side == -1 and "tablet" in ex:  # 平板平放在掌上、往臉的方向傾
        rot = (0, 0, -toward * 0.5)
        X = MT((toward * 0.01, 0.035, hz - 0.03), rot)
        mb.box(X @ Vector((0, 0, 0)), (0.17, 0.012, 0.24), M["black"], rot=rot, bevel=0.005)
        mb.box(X @ Vector((0, 0.007, 0)), (0.15, 0.003, 0.215), mats["screen"], rot=rot)
        for i, h in enumerate((0.05, 0.09, 0.07, 0.12)):
            mb.box(X @ Vector((-0.045 + i * 0.03, 0.01, 0.07 - h / 2)), (0.02, 0.004, h), M["white"], rot=rot)
    if side == 1 and "megaphone" in ex:  # 握把在手，喇叭口朝前下方
        mm, mm2 = mats["accent"], M["white"]
        a = P["hold_pitch_R"] + 0.25
        d = Vector((0, math.cos(a), -math.sin(a)))
        base = Vector((0, 0, hz - 0.03)) + Vector((0, 0.0, 0))
        mb.cyl((0, 0.0, hz - 0.0), 0.016, 0.08, M["black"], seg=8)
        p0 = base - d * 0.08
        mb.seg(p0, base + d * 0.22, 0.042, mm2, r2=0.105, sides=14)
        mb.seg(base + d * 0.2, base + d * 0.23, 0.108, mm, r2=0.112, sides=14)
        mb.seg(p0 - d * 0.02, p0 + d * 0.01, 0.044, mm, sides=14)
    return mb


def build_hip(c, P, mats):
    mb = MB()
    b = c["build"]
    pm = mats["pants"]
    mb.cyl((0, 0, 0.0), b["hip"] * 0.95, 0.17, pm, r2=b["waist"] * 0.98, seg=10, scale=(1, 0.7, 1))
    mb.cyl((0, 0, 0.07), b["waist"] * 1.0, 0.03, mats["belt"], seg=10, scale=(1, 0.72, 1))
    if c["top"] not in ("blazer", "casual_blazer", "cardigan", "hoodie", "jacket"):
        mb.box((0, b["waist"] * 0.72, 0.07), (0.04, 0.012, 0.028), M["metal"])
    return mb


def build_thigh(c, P, mats, side):
    mb = MB()
    Lt = P["Lt"]
    r = 0.08 if c["build"]["hip"] >= 0.15 else 0.074
    pm = mats["pants"]
    mb.sphere((0, 0, -0.01), r * 1.05, pm, u=10, v=7)
    mb.cyl((0, 0, -Lt / 2), r, Lt, pm, r2=r * 0.82, seg=10)
    if "cargo" in c["extras"]:
        mb.box((side * r * 0.95, 0.0, -Lt * 0.55), (0.03, 0.1, 0.12), mats["pants2"], bevel=0.006)
    return mb


def build_shin(c, P, mats, side):
    mb = MB()
    Ls = P["Ls"]
    r = 0.064 if c["build"]["hip"] >= 0.15 else 0.06
    pm, sm = mats["pants"], mats["shoe"]
    style = c["shoe_style"]
    leg = Ls - 0.075
    mb.sphere((0, 0, 0), r * 1.02, pm, u=10, v=7)
    mb.cyl((0, 0, -leg / 2), r, leg, pm, r2=r * 0.85, seg=10)
    if style == "boot":
        mb.cyl((0, 0, -Ls + 0.11), r * 0.95, 0.1, sm, seg=10)
    # 鞋：腳底在 -Ls
    sole = M["rubber"] if style == "sneaker" else M["sole_dark"]
    mb.box((0, 0.045, -Ls + 0.045), (0.11, 0.25, 0.075), sm, bevel=0.025)
    mb.box((0, 0.045, -Ls + 0.01), (0.114, 0.255, 0.02), sole, bevel=0.006)
    if style == "sneaker":
        mb.box((0, 0.1, -Ls + 0.075), (0.06, 0.08, 0.012), M["white"], rot=(-0.3, 0, 0))
    return mb


def build_character(c, x0):
    H = c["H"]
    rid = c["id"]
    rh = 0.112
    head_c = H - 1.08 * rh - 0.014
    neck = head_c - rh - 0.025
    hip_h = round(0.5 * H - 0.01, 4)
    Lt = round(hip_h * 0.5, 4)
    Ls = round(hip_h - Lt, 4)
    waist = hip_h + 0.07
    sh = neck - 0.03
    P = dict(rh=rh, hc=head_c - neck, torso_len=sh - waist, La=round(0.165 * H, 4), Lf=round(0.17 * H, 4),
             Lt=Lt, Ls=Ls, hold_pitch_R=0.67)
    col = APPLE[c["color"]]
    mats = dict(
        skin=SKIN[c["skin"]],
        hair=mat(f"ch_hair_{rid}", c["hair_c"], 0.75),
        top=mat(f"ch_top_{rid}", c["top_c"], 0.75),
        top2=mat(f"ch_top2_{rid}", c["top2"], 0.75),
        pants=mat(f"ch_pants_{rid}", c["pants"], 0.85),
        pants2=mat(f"ch_pants2_{rid}", shade(c["pants"], 0.8), 0.85),
        belt=mat(f"ch_belt_{rid}", shade(c["pants"], 0.45), 0.6),
        shoe=mat(f"ch_shoe_{rid}", c["shoes"], 0.5),
        accent=mat(f"ch_accent_{rid}", col, 0.5),
        accent2=mat(f"ch_accent2_{rid}", shade(col, 0.7), 0.5),
        tie=mat(f"ch_tie_{rid}", shade(col, 0.4), 0.4),
        notebook=mat(f"ch_notebook_{rid}", "#7A4A2A", 0.6),
        screen=mat(f"ch_screen_{rid}", shade(col, 0.6), 0.2, emit=col, strength=1.5),
        scarf=mat(f"ch_scarf_{rid}", "#FFF4F7", 0.9),
        scarf2=mat(f"ch_scarf2_{rid}", col, 0.9),
    )
    mats["sleeve"] = mats["top"] if c["top"] != "vest" else mats["top2"]
    if c["top"] == "vest":
        mats["top"] = mats["top2"]  # 背心底下的長袖
        mats["top2"] = mat(f"ch_top3_{rid}", shade(c["top2"], 0.7), 0.75)

    root = node(rid, None, (x0, 0, 0))
    root.empty_display_type = "SINGLE_ARROW"
    root["role_id"] = rid
    root["role_name"] = c["name"]
    root["color_name"] = c["color"]
    root["color_hex"] = col
    root["height"] = H
    root["hip_h"] = hip_h
    root["thigh_len"] = Lt
    root["shin_len"] = Ls
    root["upperarm_len"] = P["La"]
    root["forearm_len"] = P["Lf"]
    root["hold_L"] = bool(c.get("hold_L"))
    root["hold_R"] = bool(c.get("hold_R"))
    root["note"] = "原點在腳底中心，面向 +Y（glTF -Z）。節點繞本地 X 正轉 = 往前擺"

    hip = node(f"{rid}_hip", root, (0, 0, hip_h), build_hip(c, P, mats))
    torso = node(f"{rid}_torso", hip, (0, 0, waist - hip_h), build_torso(c, P, mats))
    node(f"{rid}_head", torso, (0, 0, neck - waist), build_head(c, P, mats))
    sw = c["build"]["sh"] * 0.86 + 0.03
    for side, tag in ((-1, "L"), (1, "R")):
        hold = c.get(f"hold_{tag}")
        if hold and c["id"] != "marketing":  # 前臂橫過胸前抱東西
            ua_rot, fa_rot = (0.45, -side * 0.12, 0.0), (1.57, 0.0, side * 1.15)
            if "tablet" in c["extras"]:
                ua_rot, fa_rot = (0.3, -side * 0.1, 0.0), (1.35, 0.0, side * 0.6)
            if "sticky_pad" in c["extras"]:
                ua_rot, fa_rot = (0.25, -side * 0.1, 0.0), (1.3, 0.0, side * 0.45)
        elif hold:  # 行銷：大聲公自然垂在身側稍微往前
            ua_rot, fa_rot = (0.12, -side * 0.06, 0.0), (0.55, 0.0, 0.0)
        else:
            ua_rot, fa_rot = (0.0, -side * 0.07, 0.0), (0.12, 0.0, 0.0)
        ua = node(f"{rid}_upperarm_{tag}", torso, (side * sw, 0, P["torso_len"] - 0.06),
                  build_upper_arm(c, P, mats, side), rot=ua_rot)
        node(f"{rid}_forearm_{tag}", ua, (0, 0, -P["La"]), build_forearm(c, P, mats, side), rot=fa_rot)
        lx = c["build"]["hip"] * 0.55
        th = node(f"{rid}_thigh_{tag}", hip, (side * lx, 0, 0), build_thigh(c, P, mats, side))
        node(f"{rid}_shin_{tag}", th, (0, 0, -Lt), build_shin(c, P, mats, side))
    return root


roots = []
for i, c in enumerate(CHARS):
    roots.append(build_character(c, -5.25 + i * 1.5))

faces = sum(len(o.data.polygons) for o in bpy.data.objects if o.type == "MESH")
print(f"[chars] objects={len(bpy.data.objects)} faces={faces}")

bpy.context.preferences.filepaths.save_version = 0  # 不產生 .blend1
blend_path = os.path.join(HERE, "characters.blend")
bpy.ops.wm.save_as_mainfile(filepath=blend_path, compress=True)
glb_path = os.path.join(HERE, "characters.glb")
bpy.ops.export_scene.gltf(
    filepath=glb_path, export_format="GLB", export_apply=True, export_extras=True,
    export_cameras=False, export_lights=False, export_yup=True, export_materials="EXPORT",
)
print("[chars] exported", glb_path, os.path.getsize(glb_path) // 1024, "KB")

# ---------------------------------------------------------------------------
# 預覽渲染（不存進 .blend）
# ---------------------------------------------------------------------------
if not NO_RENDER:
    def look(o, target):
        d = Vector(target) - o.location
        o.rotation_euler = d.to_track_quat("-Z", "Y").to_euler()

    floor_me = bpy.data.meshes.new("preview_floor")
    bm = bmesh.new()
    bmesh.ops.create_grid(bm, x_segments=1, y_segments=1, size=20)
    bm.to_mesh(floor_me)
    bm.free()
    floor_me.materials.append(mat("preview_floor", "#E9E4DC", 0.9))
    fl = bpy.data.objects.new("preview_floor", floor_me)
    SC.collection.objects.link(fl)
    sun = bpy.data.lights.new("preview_sun", "SUN")
    sun.energy = 3.0
    so = bpy.data.objects.new("preview_sun", sun)
    SC.collection.objects.link(so)
    so.rotation_euler = Vector((0.3, 0.8, -0.7)).to_track_quat("-Z", "Y").to_euler()
    fill = bpy.data.lights.new("preview_fill", "AREA")
    fill.energy = 600
    fill.size = 8
    fo = bpy.data.objects.new("preview_fill", fill)
    SC.collection.objects.link(fo)
    fo.location = (0, 6, 4)
    look(fo, (0, 0, 1))
    world = bpy.data.worlds.new("preview_world")
    SC.world = world
    world.node_tree.nodes["Background"].inputs["Color"].default_value = (0.75, 0.8, 0.86, 1)
    world.node_tree.nodes["Background"].inputs["Strength"].default_value = 0.7
    cam_d = bpy.data.cameras.new("preview_cam")
    cam = bpy.data.objects.new("preview_cam", cam_d)
    SC.collection.objects.link(cam)
    SC.camera = cam
    R = SC.render
    R.engine = "BLENDER_EEVEE"
    SC.eevee.taa_render_samples = 32
    SC.view_settings.view_transform = "AgX"
    os.makedirs(os.path.join(HERE, "renders"), exist_ok=True)
    import tempfile
    import numpy as np
    tmp = tempfile.mkdtemp(prefix="omixor_chars_")
    SC.eevee.taa_render_samples = 24
    # 每個角色兩格：全身 3/4 正面（上排）＋頭部特寫（下排），最後拼成一張 renders/characters.png
    W, H1, H2 = 360, 600, 360
    tiles = []
    for r, c in zip(roots, CHARS):
        x0 = r.location.x
        row = []
        for key, off, tgt, lens, (w, h) in (
                ("body", (1.1, 3.2, 1.25), (0, 0, 0.92), 42, (W, H1)),
                ("head", (0.35, 0.95, c["H"] - 0.08), (0, 0, c["H"] - 0.2), 55, (W, H2))):
            cam.location = (x0 + off[0], off[1], off[2])
            look(cam, (x0 + tgt[0], tgt[1], tgt[2]))
            cam_d.lens = lens
            R.resolution_x, R.resolution_y = w, h
            R.filepath = os.path.join(tmp, f"{c['id']}_{key}.png")
            bpy.ops.render.render(write_still=True)
            img = bpy.data.images.load(R.filepath)
            a_ = np.array(img.pixels[:], dtype=np.float32).reshape(h, w, 4)
            row.append(a_)
            bpy.data.images.remove(img)
        tiles.append(np.concatenate([row[1], row[0]], axis=0))  # pixels 由下往上
    sheet = np.concatenate(tiles, axis=1)
    out = bpy.data.images.new("characters_sheet", sheet.shape[1], sheet.shape[0], alpha=False)
    out.pixels[:] = sheet.ravel()
    out.filepath_raw = os.path.join(HERE, "renders", "characters.png")
    out.file_format = "PNG"
    out.save()
    print("[chars] rendered", out.filepath_raw)
print("[chars] done")
