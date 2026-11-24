# components/ — 共用元件

一個元件一個資料夾、樣式用 CSS Modules;非畫面的程式碼在 `src/lib/`,型別在 `src/models/`。
全域的 `styles.css` 只放 token、reset、排版工具、按鈕與表單控制項、App 骨架與少數各頁共用的骨架 class(見最後一節)。

## 資料夾慣例

```
components/<Name>/
  <Name>.tsx          元件本體(同資料夾可以有數個相關元件,例如 Avatar/ 裡的 ActorAvatar、AgentAvatar、AvatarStack)
  <Name>.module.css   這個元件自己的樣式(沒有樣式的元件不用建)
  index.ts            對外匯出;其他地方一律 `import { X } from "../components/<Name>"`,不直接 import 內部檔案
```

- 會用到 recharts(約 400 kB)的元件只放 `components/charts/`;只要顏色或座標軸設定就從 `charts/theme.ts` 拿(不 import recharts)。
- 會用到 `SIcon`(lucide 額外圖示,約 10 kB)的元件和不用的分開放檔(例如 `Card.tsx` / `CardHeader.tsx`),免得只用 Card 的頁面也下載 SIcon。
  Avatar 不 import SIcon:要畫 lucide 額外圖示時由呼叫端傳 `icon={SIcon}`。
- 非畫面的東西不放這裡:格式化在 `lib/format.ts`、hooks 在 `lib/hooks.ts`、名稱目錄在 `lib/directory.tsx`、型別在 `models/`。

### 元件的 props 慣例

- 外觀可調的元件都收 `className`(加在根元素上);版面相關的再收 `style`(只放**動態**的值,例如依資料算的寬度;固定值一律寫進呼叫端的 module CSS)。
- 想要某個元件的外觀、但要自己決定元素或內容時,用匯出的 class:`cardClass`、`cardTitleClass`、`tableClass`、`layoutClass`、
  `rowButtonClass`、`tileClass`、`plainClass`、`fieldClass`、`disclosureClass`、`menuItemClass` / `menuSepClass`、`iconButtonClass`。

### CSS Modules 命名與權重

- 檔名 `<Name>.module.css`,`import s from "./<Name>.module.css"`(函式參數也叫 `s` 時改名成 `css`,避免遮蔽)。
- class 用 camelCase、只描述在元件內的角色:`.root`、`.title`、`.on`、`.ai`;不要加 `bx-` / `ox-` 之類的前綴(Vite 會自動加 hash)。
- 狀態用額外的 class 疊上去:`className={s.switch + (on ? " " + s.on : "")}`。
- `@keyframes` 寫在同一個 module 裡(名稱也會被 hash),不要依賴全域的 keyframes。
- 需要配合全域容器時用 `:global(...)`,例如 `:global(.tabbar) .root`、`:global(.meta) > .fit`。
- **收 className 的元件,根元素的基本樣式包在 `:where()` 裡(權重 0)**:Card、Modal、ProgressBar、SearchField、Layout、Table、
  RowButton、Tile / PlainButton、IconButton(版面屬性)、Dropdown。呼叫端疊上去的 class 一定蓋得過,不受 lazy chunk 的 CSS 載入順序影響,
  不需要 `.x.x`。注意 `color` / `font` 不能放進 `:where`(會輸給全域 `button { color: inherit; font: inherit }`)。
- module class 和**全域** class(`.row`、`.mono`、`.gap8`、`.h3`、`.input`、`.btn`…)疊在同一個元素、又設定同一個屬性時,載入順序不固定,
  要提高權重:`.x.x`、`textarea.x`、`:global(.btn.link).x`。能不疊就不疊(例如卡片要橫排就自己設 `flex-direction`,不要再加 `.row`)。

### 設計 token(`styles.css` 的 `:root`)

一律用變數,不要寫死數值:

| 類別 | 變數 |
|---|---|
| 顏色 | `--bg` `--bg2` `--bg3` `--side` `--line` `--fg` `--fg2`(次要文字)`--fg3`(只給裝飾/停用)`--accent` `--accent-fill` `--on-accent` `--danger` `--danger-fill` `--avatar-fill`;狀態色 `--red` `--orange` `--yellow` `--green` `--teal` `--blue` `--indigo` `--purple` `--pink` `--gray` |
| 清單底色 | `--hover`(fg 5%,RowButton 的 hover)`--selected`(accent 18%,`.list-row.on`、`.list-item.on`、`tableClass.sel`、派發的選取角色) |
| 字級 | `--fs-xs`(11)`--fs-sm`(12)`--fs-base`(13)`--fs-md`(14)`--fs-lg`(16)`--fs-xl`(18)`--fs-2xl`(20)`--fs-3xl`(24) |
| 控制項高度 | `--h-sm`(22)`--h-md`(28)`--h-lg`(34) |
| 圓角 | `--r-sm`(4)`--r-md`(6)`--r-lg`(8)`--r-xl`(10)`--r-pill` |
| 其他 | `--focus-ring`、`--dot-size`(StatusDot 預設直徑)、`--tint-text`(彩色文字混色比例) |

`--hover` / `--selected` 由 `--fg` / `--accent` 混出來,深色模式自動跟著變。
在 TS 裡用顏色名稱時用 `color("purple")`(`lib/color.ts`),它把 Swift 的顏色名轉成 `var(--purple)`,不認得的原樣回傳(所以也可以直接傳 `var(--accent)` 或任何 CSS 顏色)。

## 元件清單

| 元件 | 路徑 | 說明 |
|---|---|---|
| `ActorAvatar` `ActorLabel` `AgentAvatar` `AIAvatar` `AvatarStack` `AuthorIcon` | `Avatar` | 名稱一律從 `useDirectory()` 取。`plainAI`:AI 一律畫紫色 sparkles;`ActorLabel inline` 用 inline-flex。`AgentAvatar` 沒給顏色是藍、沒給圖示是 `person.crop.circle`;`icon={SIcon}` 改用 lucide 額外圖示(`AuthorIcon` 同,另有 `mcpScale` / `personScale`)。`AIAvatar` 是沒有 actor 的 AI 頭像(助理)。`AvatarStack actors max` 疊放並顯示「+N」 |
| `AIBadge` `HealthBadge` `RunStatusBadge` `EnvBadge` | `Badge` | |
| `Callout` `AICallout` `Banner` | `Callout` | |
| `Card` `CardTitle` `CardHeader` `cardClass` `cardTitleClass` | `Card` | `Card` 收 `className` / `style`;`CardHeader`(數量徽章 + 「全部 >」)在另一個檔(用 SIcon) |
| `Check` | `Check` | |
| `Chip` | `Chip` | 小方角標籤(預設等寬字)。伺服器頁的膠囊標籤(可點、圖示可上色)外觀不同,留在 `sections/infrastructure/infraShared.tsx` |
| `CodeBlock` | `CodeBlock` | 有複製按鈕的程式碼區塊 |
| `useConfirm` `ConfirmSpec` | `Confirm` | |
| `DiagramBlock` `diagramKindOf` `isMermaidFlowchart` | `Diagram` | 文件裡的線框圖 / 流程圖 |
| `Disclosure` `disclosureClass` | `Disclosure` | `<details>`:`variant="chevron"`(預設,自訂「›」)或 `"native"`(原生三角形、小字次要色) |
| `EmptyState` `InlineEmpty` | `EmptyState` | |
| `ErrorText` | `ErrorText` | |
| `Field` `fieldClass` | `Field` | 表單欄位(小字標籤 + 撐滿寬度的控制項) |
| `Icon` | `Icon` | SF Symbols 名稱 → lucide |
| `IconButton` `iconButtonClass` | `IconButton` | `.normal`(有內距、hover 底色)、`.bare`(無內距、hover 變淡) |
| `IconTile` | `IconTile` | |
| `KpiGrid` | `KpiGrid` | KPI 卡片列,依卡片數(`n`,預設 children 數)與容器寬度選欄數 |
| `Page` `Fill` `Scroll` `layoutClass` `PageScroll` `Flow` | `Layout` | 頁面骨架:直向撐滿 / 橫向撐滿 / 撐滿並捲動;`PageScroll` 是 `.page-scroll > .page-inner` |
| `Markdown` `MarkdownText` | `Markdown` | 兩套輸出不同,之後再決定合併 |
| `AutoTextarea` | `AutoTextarea` | 全站唯一的 textarea:隨內容長高(`minRows`~`maxRows`,超過才捲動)、無拖曳把手;`monospace` 給程式碼類、可 `ref`、標準 textarea 屬性透傳、`onChange(v, e)` |
| `MdTextarea` | `MdTextarea` | 可貼上 / 拖曳圖片的 Markdown 輸入框;輸入框是 `AutoTextarea`(`minRows` / `maxRows` / `monospace` 轉給它)、`wrapClassName` 加在外層 |
| `MenuList` `MenuButton` `useContextMenu` `Dropdown` `menuItemClass` `menuSepClass` | `Menu` | `useContextMenu("simple")`:沒有勾選欄、按任何鍵關閉。`Dropdown`:絕對定位的下拉容器(位置由 `className` 給),`items` 時內容是 `MenuList`,自訂內容用 `menuItemClass` |
| `MetaFit` `FitItem` | `MetaFit` | 放在 `.meta` 資訊行裡,放不下的項目整個拿掉 |
| `MetricTile` | `MetricTile` | 有 delta / sparkline 的版本是 `charts/Metric` |
| `Modal` `ModalTitle` | `Modal` | `role="dialog"` + `aria-modal`;`title` 或 `<ModalTitle>`(自己排版標題時)當 `aria-labelledby`,沒有看得到的標題時給 `ariaLabel`。Escape 與點背景關閉、開啟時聚焦第一個可聚焦元素(內容有 `autoFocus` 時不動)、Tab 在對話框內循環、關閉後焦點回原處。另收 `className` `style` `height` `onKeyDown`(先執行,`preventDefault()` 可擋掉 Escape 關閉) |
| `PageHeader` | `PageHeader` | |
| `Pill` | `Pill` | |
| `Bar` `ProgressBar` `UsageBar` | `ProgressBar` | 都收 `className` |
| `RowButton` `rowButtonClass` | `RowButton` | 整列可點的按鈕(滿寬、靠左、`--hover`);內距預設 6px,用 className 改 |
| `SearchField` | `SearchField` | `variant="outlined"`(白底加框)/ `"filled"`(淡灰底);`className`、`inputClassName` |
| `Segmented` `Seg` `useTabScroll` | `Segmented` | 放在 `.tabbar` / `.tabscroll` 裡時放不下會橫向捲動 |
| `SIcon` | `SIcon` | lucide 額外圖示 |
| `Spinner` | `Spinner` | |
| `StatusDot` | `StatusDot` | `c` 顏色、`size` 直徑(預設 `--dot-size` 8px;圖例與表格常用 6、7)、`pulsing` + `pulse="ping"\|"glow"`、`title` |
| `Switch` | `Switch` | |
| `Table` `tableClass` | `Table` | `variant="data"`(預設:黏表頭、整列 hover、`tableClass.sel` 選取列)/ `"simple"`(不黏表頭、`tableClass.click` 的列才 hover);`clearHead` 表頭不鋪底色 |
| `Tile` `PlainButton` `tileClass` `plainClass` | `Tile` | 可點的磚塊(hover 稍亮、按下變淡;外觀另給,例如 `cardClass`)與不帶外觀的按鈕 |
| `useToast` `useAct` | `Toast` | |
| `ViewState` `ViewLoading` | `ViewState` | view 的標準載入 / 錯誤畫面,見下方「資料層」 |
| `ToggleChip` | `ToggleChip` | |
| `Sparkline` `SeriesChart` `Metric` `SERIES_COLORS` `axisStyle` `axisProps` | `charts` | 只有這裡 import recharts;`<XAxis {...axisProps} />` = 刻度字樣 + 軸線顏色 |

### lib/ 與 models/

| 匯出 | 路徑 |
|---|---|
| `COLORS` `color` `hexColor` `personColor` | `lib/color.ts` |
| `t`(ISO → 毫秒,空值 NaN)`tOrNow`(空值當作現在,用在還沒結束的區間)`now` `hm` `hhmm` `dur`(=`shortDuration`)`pct`(=`percent`)`num` `monthDay` `monthDayTime` `startOfDay` `isToday` | `lib/format.ts` |
| `copyText` `useCopied` | `lib/clipboard.ts` |
| `useNow` `useStored` `useWidth` `useList` | `lib/hooks.ts` |
| `isAI` | `lib/actor.ts` |
| `go` | `lib/nav.ts` |
| `DirectoryProvider` `useDirectory` `OmixorProvider` `useOmixor` | `lib/directory.tsx` |
| 所有型別、列舉 | `models/`(`domain.ts` + `ops.ts`,由 `models/index.ts` 匯出;contract 測試直接讀 `src/models/domain.ts`) |

### 名稱目錄(DirectoryProvider + OmixorProvider)

- `DirectoryProvider`(App 最外層)是唯一讀 `people`、`automations`、`agents`、`me` 的地方。
- `OmixorProvider`(總覽、開發、CI/CD、伺服器頁)只另外讀 `services`、`repos`、`nav`,人名等沿用目錄,不再重複讀。
- `name()` 只有一套後備規則:沒有 actor →「未指派」、人查不到 → id(沒有 id 是「?」)、AI → 自動化名稱/角色名稱/「MCP 用戶端」/「AI」、其他 →「系統」。
- `me` 讀到前是 `""`(`ready` 為 false);`DevPullRequestsTab` 在 `me` 未知時不顯示「加入審查」。
- 頭像(`components/Avatar`)一律從 `useDirectory()` 取資料,不需要 `OmixorProvider`。

## 資料層(`api.ts` + `lib/viewStore.ts` + `lib/http.ts`)

- `useView(名稱, 主題?, {throttleMs?})` 與 `useActionQuery(名稱, 參數, 主題?)` 共用 `lib/viewStore.ts` 的快取:
  同一個 view(或同一組 action + 參數)不論幾個元件在看都只發一個請求,SSE 通知也只重讀一次;沒人看的 view 收到通知只標成 stale,下次有人看才讀。
  所以**同一個 view 在多個元件各自 `useView` 沒有額外成本**,不用為了省請求把資料一路往下傳。
- 回傳值照舊是 `[data, error, reload]`,另外有欄位 `.data` `.error` `.loading`(請求進行中)`.status`(`"loading" | "error" | "ready"`)`.reload`。
  重讀失敗時 `data` 保留上一次的結果、`error` 有值。
- 版本:`/api/view/*` 與 `/api/action/*` 回應帶 `X-Omixor-Version`(後端在算資料前讀的版本),SSE 通知的版本不比它新就不重讀。
  伺服器每次 SSE 連上(包括重連)都會先送 `"*"`;畫面不自己補送。斷線時手上的版本作廢,重連的 `"*"` 一律重讀(伺服器可能重啟過)。
- `action<T>(…)` / `tool<T>(…)` 沒給型別參數時回傳 `unknown`:要用回傳值就給型別(`models/` 裡的型別),或賦值給有型別的變數讓 TS 推斷;
  不用回傳值(fire-and-forget)的就不用給。
- `refreshTopics(主題)` 和收到 SSE 通知一樣(一律重讀,不比版本),版本衝突後用。
- 單元測試在 `contract/viewStore.test.ts`(`npm test`)。

### 載入與錯誤的標準寫法

```tsx
const v = useView<Incident[]>("incidents");
return <ViewState view={v} what="事件">{(list) => <IncidentList list={list} />}</ViewState>;
```

- 還沒有資料:`ViewLoading`(轉圈 +「載入中…」;`variant="inline"` 給卡片內用)。
- 沒有資料而且讀取失敗:「讀不到{what}」+ 錯誤訊息 +「重試」。
- 有資料但最後一次重讀失敗:照常畫資料,上方一行橘色錯誤。
- 一頁讀很多 view(例如總覽)時:主要的 view 用 `ViewState` 包;其他的至少把錯誤列出來(`ErrorText`),不要讓「讀不到」看起來像「沒有資料」。
- 舊寫法(自己判斷 `data === undefined` 顯示「載入中…」、`ErrorText`)不用一次改完;新程式碼與改到的地方用 `ViewState`。

## 全域 class(`styles.css`)

只有下面這些;其他一律寫在元件或頁面自己的 `*.module.css`。新增全域 class 前先想想能不能做成元件。

| 類別 | class |
|---|---|
| reset / 元素 | `*` `html` `body` `#root` `button` `input` `select` `textarea`、焦點框(`:where(button, [role="button"], a, summary, [tabindex])`;`.side-item` `.list-item` `.list-row` 的焦點框往內縮) |
| 排版工具 | `.row` `.col` `.grow` `.wrap` `.gap4/6/8/12` `.grid` `.g2/.g3/.g4` `.caption` `.muted` `.faint` `.mono` `.num` `.ellipsis` `.h2` `.h3` `.divider` `.hscroll` |
| 按鈕與表單 | `.btn`(`.primary` `.danger` `.link` `.small` `.red` `.purple` `.indigo` `.green`)、`.input` `.select` |
| App 骨架 | `.app` `.sidebar` `.side-group` `.side-label` `.side-item` `.side-badge` `.org-switcher` `.org-current` `.mcp-status` `.main` `.toolbar` `.content` `.feature-notice` |
| 頁面骨架 | `.page-scroll` `.page-inner` `.page-title`、`.split` `.split-list` `.split-detail` |
| 各頁共用的清單與列 | `.tabbar` `.tabscroll`(useTabScroll 依這兩個 class 找捲動容器,`data-fade-l/r` 邊緣淡出)、`.list` `.list-row` `.list-foot` `.list-item` `.list-head` `.section-label`、`.meta`(清單列的資訊行,配 MetaFit)、`.actions`(頁首動作群) |

## 變更紀錄

- **Phase 1**:把三套重疊的元件庫(`ui.tsx`、`bits.tsx` + `bits.css`、`omixorKit.tsx` + `omixorKit.css`)拆成一個元件一個資料夾 + CSS Modules,
  工具搬到 `lib/`、型別搬到 `models/`,舊檔案暫時當相容轉出口。
- **Phase 2**:各 sections 改成直接匯入新模組,頁面樣式搬進各自的 `*.module.css`。
- **Phase 3**:刪除轉出口(`ui.tsx` `bits.tsx` `omixorKit.tsx` `components/models.ts`)與 `bits.css` `omixorKit.css`;`styles.css` 刪掉沒人用的 class
  (`.card*` `.metric*` `.pill` `.modal*` `.menu*` `.empty` `.error-text` `.dot` `.table` `.tabbar`(舊)`.page-header` `.g6`、登入 / 帳號 / 連接 / 系統頁 / 文件櫃的頁面 class),
  `ox-tabbar` `ox-tabscroll` `ox-list*` `ox-section-label` `ox-meta` `ox-actions` 改名為不帶前綴的全域 class;新增 `--hover` `--selected` token;
  補上 Layout(Page/Fill/Scroll)、Table、Disclosure、Field、RowButton、Dropdown、KpiGrid、Tile/PlainButton、CardTitle、ModalTitle、AIAvatar、AvatarStack,
  取代各頁複製的版本;`Icon`、`Diagram`、`MdTextarea` 也搬進自己的資料夾;incSupport 的 `t` 改成 `lib/format` 的 `tOrNow`。
