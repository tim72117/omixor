# omixor 虛擬辦公室（3D 場景）

給 omixor 虛擬角色（AI agents）用的開放式辦公室。全部由 `build_office.py` 程序化產生：沒有外部模型或貼圖，螢幕畫面、海報、木地板等貼圖都是腳本用 numpy 畫出來後打包進檔案。

## 檔案

| 檔案 | 說明 |
|---|---|
| `build_office.py` | 場景的唯一來源。每次執行都從空場景重建 |
| `office.blend` | 產生的 Blender 場景（貼圖已打包） |
| `office.glb` | glTF 2.0 匯出（已套用 modifiers、保留物件名稱、Empty、攝影機；不含燈光） |
| `renders/overview.png` | 俯視等角總覽（1920×1080） |
| `renders/<角色id>.png` | 每個角色工作區特寫（1280×720） |
| `build_characters.py` | 8 個角色的程序化低多邊形模型（關節節點，前端擺動做動畫） |
| `characters.glb` / `characters.blend` | 角色模型匯出 / Blender 檔 |
| `renders/characters.png` | 角色預覽（上排全身、下排頭部特寫） |
| `build_nav.py` | 從場景產生導航格與具名地點（`build_office.py` 匯出後會自動呼叫） |
| `navgrid.json` | 0.25 m 導航格（three.js 座標，'0' 可走 / '1' 障礙）。障礙已依角色身體寬度往外膨脹 0.25 m；門口（會議室、CEO 辦公室、錄音間）與座位/椅子/高腳凳站位保留可走 |
| `places.json` | 具名地點：每個角色的座位/工作區、會議室、茶水間、休息區…（中英文別名、站位 spots；每個站位有 `pose`：desk / chair / stand） |
| `viewer/` | three.js 網頁檢視器：角色走動、命令列、`window.office` API（見 `viewer/README.md`） |

## 重建

```sh
cd office3d
blender -b -P build_office.py                         # 重建 + 存 .blend + 匯出 .glb + 渲染全部（M4 約 3–4 分鐘）
blender -b -P build_office.py -- --no-render          # 只重建、存檔、匯出（約 6 秒）
blender -b -P build_office.py -- --only=overview,ceo  # 只渲染指定的圖
blender -b -P build_office.py -- --samples=32         # 改 EEVEE 取樣數（預設 64）
blender -b -P build_office.py -- --no-render --no-nav # 不重新產生 navgrid.json / places.json
blender -b -P build_nav.py                            # 只從現有 office.blend 重算導航格與地點（不存檔，約 2 秒）
blender -b -P build_characters.py                     # 角色模型 + renders/characters.png（--no-render 不渲染）
```

錄音間的門（`narrator_booth_door`）是獨立物件，開著（往西外開），角色才走得進錄音間；`renders/` 裡的舊渲染圖門還是關著的，重渲染後才會更新。

需要 Blender 5.2（EEVEE 引擎識別名 `BLENDER_EEVEE`）。名牌中文字使用 macOS 內建的 `Arial Unicode.ttf`，找不到時會退回 Blender 內建字型（中文會變成方框），文字在產生時就轉成網格，所以 .blend/.glb 不依賴字型檔。

## 座標與單位

- 單位公尺，Blender Z 朝上。地板範圍 x ∈ [-14, 14]、y ∈ [-9, 9]，天花板高 3.2 m。
- 北側（+Y）是整面落地窗；東側（+X）是實牆；南側（-Y）有入口。
- glTF 匯出為 Y 朝上（Blender +Z → glTF +Y，Blender +Y → glTF -Z）。
- 總覽圖會把「剖切」物件隱藏（南牆、西牆、天花板、錄音間屋頂；這些物件帶 custom property `cutaway = true`，glTF 裡在 `extras.cutaway`）。App 做俯視時建議也把它們隱藏。

## 命名規則

| 名稱 | 類型 | 用途 |
|---|---|---|
| `zone_<角色id>` | Empty（立方體） | 角色工作區的父物件。extras 帶 `role_id`、`role_name`、`job`、`color_name`、`color_hex`、`sf_symbol` |
| `seat_<角色id>` | Empty（箭頭） | 角色站位，在地面（z=0）、椅子位置。**本地 +Y = 角色面向**（glTF 中為本地 -Z）。請用世界座標（它可能是 `<id>_workstation` 的子物件） |
| `nameplate_<角色id>` | Empty（球） | 名牌位置（吊牌中心），可放 App 的名稱標籤 |
| `cam_<角色id>` | Camera | 角色工作區特寫 |
| `cam_overview` | Camera（正交） | 俯視等角總覽 |
| `zone_meeting` / `zone_lounge` / `zone_reception` / `zone_commons` | Empty | 共用區：會議室、茶水間/休息區、入口櫃台、西側公共角落 |
| `cam_meeting` / `cam_lounge` / `cam_reception` | Camera | 共用區特寫（沒有另外渲染） |
| `<角色id>_<道具>` | Mesh | 工作區道具，例如 `engineer_server_rack`、`product-manager_kanban_wall` |

Blender collection：每個角色一個 `role_<角色id>`；共用區在 `shared_areas` 底下（`shared_meeting`、`shared_lounge`、`shared_reception`、`shared_commons`）；另有 `architecture`、`lighting`、`cameras`。
名稱裡不會有 `.`（three.js 會把它去掉），重複名稱用 `_02` 這類後綴。

## 座位 / 工作區對照

| 角色 id | 名稱 | 代表色 | 工作區位置（世界 x, y） | 座位面向 | 主要道具 |
|---|---|---|---|---|---|
| `ceo` | CEO | purple | (11.25, 6.25) 東北角玻璃辦公室 | 西（-X） | 胡桃木大辦公桌、皮革主管椅、OKR 螢幕、目標白板＋OKR 卡（東牆）、沙發＋單椅＋茶几會客區、矮櫃、立燈、皇冠擺飾 |
| `product-manager` | 產品經理 | indigo | (0, 5.4) | 北（+Y） | 看板牆（4 欄便利貼＋roadmap 時間軸）、雙螢幕（roadmap / 規格）、便利貼、平板 |
| `engineer` | 工程師 | green | (5, 5.4) | 北 | 三螢幕（程式碼 / 終端機 / CI）、機械鍵盤、伺服器小機櫃（LED）、兩片開發板、橡皮鴨 |
| `course-dev` | 課程開發 | indigo | (-5, 5.4) | 北 | 高書架＋矮書架、成堆文件、筆記本、單螢幕（腳本）、繪圖板＋筆、檯燈、紙箱 |
| `narrator` | 旁白製作 | purple | (12, 0) 錄音間 | 東（+X） | 錄音間（吸音棉三面牆、深色玻璃窗、門、ON AIR 燈）、懸臂麥克風架＋防震架＋防噴罩、耳機、混音器、監聽喇叭、DAW 螢幕 |
| `marketing` | 行銷 | pink | (-5, -0.6) | 北 | 海報牆（3 張）、大聲公、型錄展示架、易拉展、筆電＋社群螢幕 |
| `visitor-analyst` | 訪客分析師 | teal | (5, -0.6) | 北 | 大型儀表板牆（發光圖表）、雙螢幕、印出的圖表 |
| `triager` | Issue 整理員 | orange | (0, -0.6) | 北 | 三層收件匣托盤、16 格分類櫃（彩色資料夾＋標籤）、便利貼（隔板與螢幕邊）、Issue 列表螢幕 |

每個角色工作區的代表色識別元素：代表色地毯、隔板上的色塊與 LED 燈條（雙面）、天花板吊下的發光名牌（中文名稱）。CEO 是玻璃隔間上的紫色色帶＋紫色地毯；旁白是錄音間外牆紫色燈條、色塊與外牆名牌。

## 接到 App 的注意事項

- 程序化 shader 不會進 glTF；本場景的顏色都放在 Principled 的 Base Color 或打包的圖片貼圖，所以 glTF 看得到正確顏色。螢幕用 emissive 貼圖（`KHR_materials_emissive_strength`）。
- 玻璃使用 alpha blend（alpha ≈ 0.16）。
- 共用網格（椅子、植物、桌子等）在 glTF 中是同一個 mesh 被多個 node 參照。
- 燈光沒有匯出（three.js / SceneKit 的光照單位和 Blender 不同，建議在 App 端自行打光：一盞暖色平行光從北方窗外 + 環境光）。
