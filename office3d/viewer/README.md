# omixor 虛擬辦公室檢視器（viewer/）

純靜態網頁：three.js 0.170.0（import map 從 jsDelivr 載入固定版本），不需要 npm install。
載入 `../office.glb`、`../characters.glb`、`../navgrid.json`、`../places.json`。

## 啟動

一定要從 `office3d/` 用靜態伺服器開（直接開 file:// 會被瀏覽器擋 fetch / module）：

```sh
cd office3d
python3 -m http.server 8000
# 瀏覽器開 http://localhost:8000/viewer/
```

需要能連到 `cdn.jsdelivr.net`（只有 three.js 本身從 CDN 載入）。

### 由 omixor console 提供（後端控制角色）

console（`console/`，畫面連接埠預設 5191）會提供這個資料夾：開 http://127.0.0.1:5191/office/ 。
檢視器載入完成後會偵測同源有沒有 `/api/office/state`（回 JSON 才算），有就進入 console 模式（`js/bridge.js`）：

- 連 SSE `GET /api/office/events`，收到 `command` 就呼叫 `World.runAction` 執行，把結果（ack）、`office:arrived`（抵達）、角色狀態 `office.state()` POST 到 `/api/office/report`。
- 斷線自動重連（1、2、4… 秒，最多 15 秒；45 秒沒收到心跳也重連）；重連時從 `snapshot` 補跑還在進行、這個頁面還沒跑過的命令。
- 右上角顯示連線狀態：`console：已連線（主檢視器）`／`已連線（副檢視器，只顯示不回報）`／`重連中…`／`獨立模式`。
- 多個分頁同時開：命令每個分頁都會執行，只有主檢視器（最早連上的）回報；它關掉時下一個自動接手。
- 在檢視器上手動操作（命令列、點地板、`window.office` 的 goto/find/returnToSeat/stop）會把那個角色還沒抵達的後端命令標成「被取代」（superseded）。
- 背景分頁不會跑 requestAnimationFrame：console 模式下改用計時器繼續模擬，角色照樣走到、照樣回報。
- 用 `python3 -m http.server` 開（或網址加 `?standalone`）時是**獨立模式**，行為和以前一樣。
- `office.connection()` 回傳 `{ mode:'console'|'standalone'|'detecting', status, primary, viewerId, pending }`。

API 與 MCP 工具（`office_move`、`office_state`）見 `console/README.md` 的「3D 虛擬辦公室」。

## 操作

- 滑鼠左鍵拖曳旋轉、右鍵平移、滾輪縮放（OrbitControls）。上方按鈕切換「等角視角／俯視」、顯示導航格（紅色＝障礙格）、全部回座位。
- 點角色或名牌：選取（腳下出現代表色圓圈，狀態表同步反白）。選取後點地板：角色走過去。
- 走路中的角色會畫出代表色虛線路徑。名牌右側小圓點：灰＝閒置、橘＝走路中、綠＝抵達。
- 下方左邊是命令輸入框與命令紀錄，右邊是每個角色的目前狀態（閒置／走路中／抵達）與位置或目標。

## 命令語法

```
<角色> <動詞> <目標>
```

| 動作 | 中文 | 英文 | 範例 |
|---|---|---|---|
| 走到地點 | 去、到、走到、前往、移動到 | go、go to、walk to、move to | `工程師 去 會議室`、`engineer go meeting` |
| 找角色 | 找、去找、去見、跟著 | find、meet、follow、visit | `ceo 找 旁白`、`ceo find narrator` |
| 回座位 | 回座位、回去、回位子、坐下、歸位 | return、back、go back、sit | `全部 回座位`、`all return` |
| 停下 | 停、停下、停止 | stop、halt | `工程師 停` |

- **角色**：id（`ceo`、`product-manager`…）、中文名（`產品經理`、`Issue 整理員`…）或別名（`pm`、`旁白`、`分析師`、`整理員`…）。大小寫、空白、連字號都不影響（`visitor analyst` = `visitor-analyst`）。
  多個角色用「、 , 和 與 跟 and &」分隔：`產品經理、工程師 去 會議室`、`pm and engineer go meeting`。
  `全部`／`所有人`／`大家`／`all`／`everyone` 代表所有角色。省略角色時用目前選取的角色（`去 茶水間`）。
- **地點**：places.json 的 id、名稱或別名：會議室（meeting）、茶水間（pantry / coffee）、休息區（lounge / sofa）、入口櫃台（reception / front desk）、入口（entrance / door）、西側公共角落（commons）、印表機（printer）、窗邊（window）、中央走道（center）、CEO 辦公室、錄音間（booth）…
  每個角色還有 `<角色>的座位`（`ceo 的座位`、`ceo seat`、`ceo's seat`）和 `<角色>的工作區`（`工程師的工作區`、`engineer desk`）。
- 目標是角色時（`行銷 去 ceo`）等同「找」。
- 多條命令用「；」或「;」分開：`工程師 去 會議室；ceo 找 工程師`。
- 無法解析時，輸入框下方會用紅字說明原因（找不到角色、缺少動作、找不到地點並列出可用地點）。輸入框支援 ↑↓ 叫出歷史。

行為：
- 去地點：會分配該地點的站位（spots），多人去同一處不會擠在同一點；會議室、公共角落、座位的站位會坐下，其他地點會面向該區中心（例如櫃台、茶几、窗戶）。
- 去別人的座位：座位主人不在時可以坐他的位子；主人坐著時改站在座位旁。
- 找人：走到對方旁邊約 0.8 m，目標移動時每 0.45 秒檢查一次、移動超過 0.4 m 就重新規劃路徑；抵達後面向對方。
- 避讓：前方近距離有人時，編號較大的角色會等一下（最多約 1.4 秒）讓對方先走，之後或遇到站著不動的人就往側邊閃。
- 終點被擋（有人站在目標站位上或擋在最後一小段）：停下等待，約 1.2 秒後改選同地點的其他空站位（或附近空格），重試 3 次仍不行就在原地停下；只有真的走到座位（< 5 cm）且座位上沒人才會坐下，不會瞬移、不會兩人坐同一個位子。
- 找人：站位必須跟對方之間有視線（不隔著桌子 / 隔板），0.8／1.05 m 找不到時放寬到 1.3／1.6 m。
- 坐姿：places.json 站位的 `pose`（`desk` 坐在辦公桌前、`chair` 一般椅子、`stand` 站著）決定抵達後的姿勢。
- 停下（`stop`）會釋放還沒走到的站位保留；已經坐在 / 站在保留的站位上則保留不放。
- 模擬用固定步長（1/60 秒）子步驟，低 FPS 時一幀補跑多步，速度跟真實時間一致；一幀最多補 1 秒，分頁切回來不會暴衝。
- `characters.glb` 缺少某個角色（或節點不完整）時，該角色會被略過，命令與 API 只認得載入成功的角色，對缺少的角色回傳錯誤。

## 對外 API（給 App / WebView 用）

載入完成後會發出 `office:ready` 事件，之後可以呼叫 `window.office`：

```js
office.goto('engineer', 'meeting')          // 地點 id 或別名；也可以是角色 id（= find）
office.goto('engineer', '會議室')
office.goto('marketing', 'seat_ceo')
office.goto('engineer', { x: 2.5, z: -1 })  // three.js 地面座標
office.find('ceo', 'narrator')
office.returnToSeat('engineer')             // 不給 id = 全部
office.stop('engineer')
office.command('全部 回座位')               // 回傳 { ok, actions:[{type, agents, target, description, results}], errors:[] }
office.state()                              // [{ id, name, color, state:'idle'|'walking'|'arrived', stateLabel, seated, position:[x,0,z], yaw, task, where }]
office.places()                             // [{ id, name, kind, aliases, position }]
office.agents()                             // [{ id, name, color }]
office.select('engineer')                   // null 取消選取
office.setView('iso' | 'top')
const off = office.on('arrived', d => console.log(d))   // 等同 window.addEventListener('office:arrived', …)；回傳取消函式
```

goto / find 回傳 `{ ok: true }` 或 `{ ok: false, error: '…' }`。

事件（`window` 上的 `CustomEvent`，資料在 `event.detail`）：

| 事件 | detail |
|---|---|
| `office:ready` | `{ agents:[id], places:[id] }` |
| `office:arrived` | `{ agent, target:{kind:'place',id} \| {kind:'agent',id} \| {kind:'point',x,z}, position:[x,0,z], seated, where }` |
| `office:state` | `{ agent, state }`（開始走、抵達時） |
| `office:command` | `office.command()` 的回傳值（輸入框送出的也會發） |

## 檔案

| 檔案 | 說明 |
|---|---|
| `index.html` | 版面、樣式、import map |
| `js/office.js` | three.js 場景（打光、陰影、隱藏 `cutaway`）、載入、點選、UI、`window.office` API |
| `js/agents.js` | 角色控制：路徑跟隨、轉向、避讓、找人、站位分配；走路／坐下／呼吸動畫（擺動 `<id>_thigh_L` 等關節節點） |
| `js/nav.js` | 導航格、A*（8 方向、不切角、貼牆加成本）、路徑拉直、連通區塊（純 JS） |
| `js/command.js` | 中英文命令解析（純 JS；console 的 `internal/office/parse.go` 是同一套規則的 Go 版，測試會比對兩邊結果） |
| `js/bridge.js` | 連 omixor console：偵測、SSE、執行後端命令、回報（純 JS） |
| `test/run_tests.mjs` | Node 單元測試：A*、全部地點互通、命令解析（`node viewer/test/run_tests.mjs`） |
| `test/agents_test.mjs` | agents.js 單元 / 小型模擬測試（固定種子、stop 釋放站位、終點被擋、缺角色、找人視線、姿勢）；`run_tests.mjs` 會一併執行 |
| `test/bridge_test.mjs` | bridge.js 單元測試（假的 EventSource / fetch / 計時器：snapshot、ack、抵達、被取代、副檢視器、重連、偵測）；`run_tests.mjs` 會一併執行 |
| `test/sim_test.mjs` | Node 無頭模擬：用假的 three.js 跑角色移動（`node viewer/test/sim_test.mjs`；固定種子，`SEED=… ` 可換） |

## 資料來源

- `../characters.glb`：`build_characters.py` 產生。每個角色根節點 `<id>`（腳底中心、面向 -Z），底下 `<id>_hip / _torso / _head / _upperarm_L|R / _forearm_L|R / _thigh_L|R / _shin_L|R`，pivot 在關節，繞本地 X 正轉＝往前擺。根節點 extras 有身高、骨段長度、`hold_L/hold_R`（手上拿東西，走路時擺動較小）。
- `../navgrid.json`、`../places.json`：`build_nav.py` 產生（`build_office.py` 匯出後也會自動跑）。格式說明寫在兩個檔案的 `note` 欄位。
