# templates

預設的範本資料(示範用的假資料),Go 版(`web`)與 Swift 版(`app`)共用。

**預設不載入。** 沒有開的時候,預設組織和新建的組織一樣是空的。要用時才開:

```bash
OMIXOR_TEMPLATES=1 ./web/bin/omixor-web          # 自己找 templates/(目前資料夾、執行檔所在資料夾,往上層找)
OMIXOR_TEMPLATES=/path/to/templates ./web/bin/omixor-web   # 指定位置
```

範本是執行時從磁碟讀的,**不編進執行檔**。Docker 映像把它放在 `/usr/local/share/omixor/templates`,要用就設 `OMIXOR_TEMPLATES` 指向那裡。

## 內容

| 位置 | 內容 |
|---|---|
| `org.json` | 第一次啟動建立的預設組織(名稱、圖示、顏色);沒開範本時是 omixor |
| `cabinet/` | 預設組織的文件櫃結構,一個區塊一個檔 |
| `seed/` | 預設組織的示範畫面資料(總覽、服務、事件、告警…),一個資料集一個檔;`_meta.json` 記匯出時間 |
| `doc/` | 文件櫃的文件轉換器 `docrender.js` 與範例網頁 `docpage.html`(Swift 版有同內容的副本) |
| `tools/split-seed/` | 把 Swift 版匯出的單一 seed.json 拆進 `seed/` 的小工具 |

目錄裡的檔案照檔名排序讀入,檔名前面的數字就是順序。單檔太大就拆成目錄。

只影響**還沒有資料**的組織:已經存過的 `cabinet-layout.json` 等不會被範本覆蓋。虛擬角色不屬於範本:新組織沒有任何預設角色,角色只能在畫面或 MCP 新增(資料在核心的資料庫)。

## 更新示範資料

`seed/` 是 Swift 版匯出的,不要手改:

```bash
cd app && OPS_EXPORT_SEED=/tmp/seed.json swift test --filter SpecExportTests
cd .. && go run templates/tools/split-seed/main.go /tmp/seed.json templates/seed
```

## 不在這裡的東西

- 連線預設值(evoleap 正式站與本機)留在程式碼:遠端登入要用,不是示範資料。
- MCP 規格(`web/spec/tools.json`、`initialize.json`)是協定標準答案,不是範本。
