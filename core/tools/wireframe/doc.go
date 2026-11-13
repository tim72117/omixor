// Package wireframe 是 omixor 自建工具外掛的第一個試作:把一份 JSON 描述(元件與版面)畫成灰階的 SVG 線框圖。
//
// 渲染是純函式(Render),沒有 I/O,雲端 server、本機 omixor-web、MCP 工具(core/mcp 的 wireframe_render)
// 與文件櫃顯示都直接呼叫它。存進文件櫃時,文件內文放一段 ```wireframe 程式碼區塊(JSON 描述,真正的資料來源),
// 畫面顯示文件時再把區塊渲染成圖(見 Blocks、Upsert、Fence)。
//
// # 描述格式
//
//	{
//	  "title":  "登入頁",                         // 選填,只放進 SVG 的 <title>
//	  "device": "desktop" | "tablet" | "mobile" | {"w": 1024, "h": 768},
//	  "root":   <node>
//	}
//
// device 預設 desktop(1280×800);tablet 是 768×1024,mobile 是 390×844;自訂尺寸 w、h 介於 200~4000。
// 畫面內容超過裝置高度時畫布自動加高(最高 20000),並在原本的高度畫一條「第一屏」虛線。
//
// 每個 node 是 {"type": "...", "children": [...], 其他屬性}。children 裡可以直接放字串,等同 {"type": "text", "text": 字串}。
//
// 版面節點(都可以有 children):
//
//	col      直排(預設 gap 12)            row      橫排(預設 gap 12)
//	stack    疊放(children 疊在同一塊)    grid     格狀,columns 欄(預設 2),子節點可用 span 跨欄
//	card     有框的卡片(預設 padding 16,可有 title)
//	section  區段(可有 title;border: true 畫框)
//
// 版面屬性(所有節點都可以用 width / height):
//
//	gap      子節點間距(數字)
//	padding  數字,或 [上下, 左右],或 [上, 右, 下, 左]
//	width    數字(px)、"fill"(平分剩餘空間)、"50%"(父層寬度的比例)
//	height   同上;"fill" 在直排裡平分剩餘高度
//	align    子節點在交叉軸的對齊:start | center | end | stretch
//	         (col 預設 start;row 預設 center,容器類子節點在 row 裡預設撐滿高度)
//	justify  主軸剩餘空間的分配:start | center | end | between(沒有 fill 子節點時才有作用)
//	border   true 時畫外框;fill: true 時塗淺灰底
//
// 自動版面(簡化的 flex):row 裡按鈕、徽章、頭像、圖示、勾選框、開關、文字依內容決定寬度,側欄預設 240,
// 其他元件與容器沒有給 width 時平分剩餘寬度;col 裡容器與輸入類元件撐滿寬度,按鈕這類依內容(align: stretch 撐滿)。
// 沒有給 size 的 spacer 會吃掉主軸剩餘空間(把後面的東西推到最右/最下)。
//
// 元件節點:
//
//	header / navbar  title、items(右側選單)、logo、back(返回箭頭)、menu(漢堡選單)、avatar;高 56
//	footer           text;高 48
//	sidebar          title、items、active(第幾個,0 起算);預設寬 240
//	text             text(沒給畫灰色佔位線,lines 行)、size(字級,預設 14)、bold、muted、align(left|center|right)
//	heading          text、level(1~3)或 size
//	link             text(加底線)
//	button           text、variant(primary | secondary | ghost)、size(sm | md | lg)
//	input            label、placeholder、value、kind: "password";textarea 另有 rows
//	select           label、placeholder / value
//	checkbox / radio label、checked
//	toggle           label、on
//	image            label、ratio(寬/高);畫成打叉的框
//	avatar           size(預設 40)、label(右側文字)
//	icon             size(預設 24)
//	list             items(字串陣列)或 count(預設 3);avatar: true 每列加頭像;chevron: true 加右箭頭
//	table            columns(欄名陣列)或 cols(欄數);rows(列數,預設 5)或 [[儲存格…]…]
//	tabs             items、active
//	chart            kind(bar | line | pie)、title;示意圖,資料是固定的
//	badge            text
//	divider          分隔線(row 裡是直線)
//	spacer           size;沒給就撐開
//	modal            疊層:畫在整個畫面上(半透明遮罩 + 置中的對話框),title、width、children
//
// 不認得的 type 畫成標了名稱的虛線框(有 children 時照直排畫在框裡),並在 warnings 提醒,不會報錯。
//
// # 限制與安全
//
// 描述最大 512KB、巢狀最多 30 層、節點最多 2000 個,超過回 *Error。
// 輸出的 SVG 只用 rect、line、circle、path、polyline、text 與固定的 <style>;所有文字都做 XML escape、
// 顏色與字型都是固定值(描述裡沒有能進到屬性的字串),不會有 script、foreignObject、外部連結或圖片。
// 顯示時建議用 <img src="data:image/svg+xml,…"> 放進畫面:就算 SVG 被竄改,瀏覽器也不會執行 img 裡的腳本。
//
// # 範例(手機登入頁)
//
//	{
//	  "title": "登入",
//	  "device": "mobile",
//	  "root": {"type": "col", "children": [
//	    {"type": "header", "title": "omixor", "back": true},
//	    {"type": "col", "padding": [32, 24], "gap": 16, "children": [
//	      {"type": "heading", "text": "歡迎回來", "level": 1},
//	      {"type": "text", "text": "用公司帳號登入", "muted": true},
//	      {"type": "input", "label": "Email", "placeholder": "you@example.com"},
//	      {"type": "input", "label": "密碼", "kind": "password", "value": "secret"},
//	      {"type": "row", "children": [
//	        {"type": "checkbox", "label": "記住我", "checked": true},
//	        {"type": "spacer"},
//	        {"type": "link", "text": "忘記密碼?"}
//	      ]},
//	      {"type": "button", "text": "登入", "variant": "primary", "width": "fill"},
//	      {"type": "divider"},
//	      {"type": "button", "text": "用 Google 登入", "variant": "secondary", "width": "fill"}
//	    ]}
//	  ]}
//	}
package wireframe
