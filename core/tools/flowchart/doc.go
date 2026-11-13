// Package flowchart 把流程描述(JSON 或 Mermaid flowchart 子集)自動排版,畫成灰階、乾淨的 SVG 流程圖。
//
// 排版與渲染是純函式(Render、Parse、RenderGraph),沒有 I/O;雲端 server、本機 omixor-web、
// MCP 工具(core/mcp 的 flowchart_render)與文件櫃顯示都直接呼叫它。存進文件櫃時,文件內文放一段
// ```flowchart 程式碼區塊(原始描述:JSON 或 Mermaid 原文,渲染時自動判斷),畫面顯示文件時再畫成圖;
// flowchart / graph 語法的 ```mermaid 區塊也當成流程圖區塊(見 Blocks、Upsert、Fence、Source)。
//
// # 格式一:Mermaid flowchart(子集)
//
// 字串不是以 { 開頭就當成 Mermaid。第一行是 flowchart 或 graph,後面接方向 TD/TB(由上而下,預設)、LR、RL、BT;
// 前面可以有 --- title: 標題 --- 前置區塊。一行一個敘述,也可以用 ; 分隔;%% 開頭是註解。
//
//	節點    A            矩形,標籤 = id(沒宣告過的節點第一次出現時自動建立)
//	        A[文字]      矩形(process)            A(文字)      圓角矩形(round)
//	        A([文字])    膠囊(start / end)       A((文字))    圓(circle)
//	        A{文字}      菱形(decision)          A{{文字}}    六角形(hexagon)
//	        A[(文字)]    圓柱(database)          A[[文字]]    雙邊矩形(subprocess)
//	        A[/文字/]    平行四邊形(io;[\文字\]、[/文字\] 也算)    A>文字]  當成矩形
//	        文字可以用 "…" 包起來(裡面可以有括號);<br> 換行;A:::highlight / A:::muted 設樣式
//	連線    A --> B      箭頭            A --- B      無箭頭
//	        A -.-> B     虛線箭頭        A -.- B      虛線無箭頭
//	        A ==> B      粗線箭頭        A === B      粗線無箭頭
//	        A <--> B     雙向箭頭        A ~~~ B      不畫,只影響排版
//	        A -->|是| B、A -- 是 --> B、A -. 是 .-> B、A == 是 ==> B   帶標籤
//	串接    A --> B --> C(同一行連續)、A & B --> C & D(多對多)
//	群組    subgraph id [標題] … end;subgraph 標題 … end;可巢狀。節點在哪個 subgraph 裡被提到就屬於那個(最內層)
//	樣式    class A,B highlight(只認得 highlight、muted)
//
// 忽略並放進 Warnings:classDef、style、linkStyle、click(不會輸出任何連結)、subgraph 裡的 direction、accTitle、accDescr。
// 不支援的語法回 *Error,訊息帶行號與附近的文字,例如「第 3 行:節點「C」的形狀括號沒有結束(缺少「]」)(在「C[沒結束」附近)」;
// 其他圖型(sequenceDiagram、classDiagram…)、@{ shape: … } 新語法、把邊連到 subgraph 的 id 也回錯誤。
//
// # 格式二:JSON
//
//	{
//	  "title":     "登入流程",                    // 選填,畫在圖的左上角
//	  "direction": "TB" | "LR" | "BT" | "RL",      // 預設 TB(也接受 TD)
//	  "nodes": [
//	    {"id": "a", "label": "開始", "shape": "start", "group": "g1", "style": "highlight"}
//	  ],
//	  "edges": [
//	    {"from": "a", "to": "b", "label": "是", "style": "solid" | "dashed" | "thick", "arrow": "end" | "none" | "both"}
//	  ],
//	  "groups": [{"id": "g1", "label": "前端", "parent": "外層群組 id"}]   // 選填;節點的 group 沒宣告會自動建立
//	}
//
// shape:process(預設,矩形)、decision(菱形)、start / end(膠囊)、io(平行四邊形)、subprocess(雙邊矩形)、
// database(圓柱)、note(便條)、round(圓角矩形)、circle(圓)、hexagon(六角形);常見的別名(diamond、cylinder…)也認得,
// 不認得的畫成矩形並放進 Warnings。style:highlight(強調色)、muted(淡灰虛線)。節點也可以直接寫字串 "a"(= {"id": "a"})。
// id 重複、邊的 from / to 不是已知節點回 *Error。
//
// 上限:節點 300、邊 600(Mermaid 的 A & B --> C & D 展開後計算)、群組 100、描述 256KB、標籤 400 字。
//
// # 範例(含決策與回邊)
//
//	flowchart TD
//	    A([開始]) --> B[/輸入帳號密碼/]
//	    B --> C{驗證通過?}
//	    C -->|是| D([進入首頁])
//	    C -->|否| E[失敗次數 +1]
//	    E --> F{超過 5 次?}
//	    F -->|是| G[鎖定帳號]:::highlight
//	    F -->|否,重新輸入| B
//	    C -.->|查詢| H[(使用者資料庫)]
//
// 同一張圖的 JSON 寫法:
//
//	{"direction": "TB",
//	 "nodes": [{"id": "A", "label": "開始", "shape": "start"}, {"id": "B", "label": "輸入帳號密碼", "shape": "io"},
//	           {"id": "C", "label": "驗證通過?", "shape": "decision"}, {"id": "D", "label": "進入首頁", "shape": "end"},
//	           {"id": "E", "label": "失敗次數 +1"}, {"id": "F", "label": "超過 5 次?", "shape": "decision"},
//	           {"id": "G", "label": "鎖定帳號", "style": "highlight"}, {"id": "H", "label": "使用者資料庫", "shape": "database"}],
//	 "edges": [{"from": "A", "to": "B"}, {"from": "B", "to": "C"}, {"from": "C", "to": "D", "label": "是"},
//	           {"from": "C", "to": "E", "label": "否"}, {"from": "E", "to": "F"}, {"from": "F", "to": "G", "label": "是"},
//	           {"from": "F", "to": "B", "label": "否,重新輸入"}, {"from": "C", "to": "H", "label": "查詢", "style": "dashed"}]}
//
// F → B 是往回的邊:排版時暫時反轉,畫的時候從 F 的右側出去、沿著右邊往上繞,從右側進入 B,不會穿過中間的節點。
//
// # 排版(分層排版,Sugiyama 簡化版)
//
//  1. 去環:從沒有入邊的節點開始 DFS,指回 DFS 堆疊上節點的邊是回邊,排版時暫時反轉。
//  2. 分層:最長路徑法;沒有前驅的節點往下拉到緊貼它的後繼。
//  3. 跨好幾層的邊每層插一個虛擬點;回邊在兩端所在的層也各插一個掛在節點右側的虛擬點,讓線從側邊繞。
//  4. 排序:以 DFS 順序起始,上下來回用重心法(barycenter)重排 24 輪,保留交叉最少的一次;同群組的節點在每層保持相鄰。
//  5. 座標:每層依前驅/後繼的平均位置算理想位置,在「順序不變、保持最小間距」的限制下用保序迴歸(PAVA)取最近解,
//     上下來回 16 輪;最後把長邊的虛擬點拉直、一對一相連的節點對齊,並把不屬於群組的節點推出群組框的範圍。
//  6. 邊:層與層之間是正交折線(轉角圓角),水平段分配到不同軌道避免重疊(同一個起點或同一個入口的線共用軌道);
//     多條線進入同一個平邊節點時入口沿邊分散。標籤放在邊的中段、白底;決策節點出去的標籤放在各分支往下的那一段。
//  7. LR / RL 是把座標轉置(層沿 x 排),BT / RL 再把層的方向反過來。
//
// 節點大小依標籤估算:CJK 約 1em、ASCII 約 0.55em(字級 14),超過寬度斷行,節點最寬約 220px;菱形依文字放大到能包住文字。
//
// # 輸出與安全
//
// SVG 只會有 svg、title、style(固定字型)、defs、marker、rect、path、polygon、ellipse、line、polyline、text;
// 描述裡的字串只出現在 <text> 與 <title> 的內容(XML escape,去掉控制字元與無效的 UTF-8),不會進到任何屬性;
// 沒有 script、foreignObject、href、外部資源。配色為灰階,決策節點與 highlight 用同一種藍色強調。
// 畫面請用 <img src="data:image/svg+xml,…"> 顯示,不要直接塞進 DOM。
package flowchart
