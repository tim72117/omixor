# Desk

原生 macOS app(SwiftUI),瀏覽這台電腦上的 Claude Code 對話,並繼續對話或開新對話。

介面分三欄:

- **左側**:依專案資料夾分組的對話,來源是 `~/.claude/projects`(CLI、VS Code、桌面版的 Code 對話都會出現)。可搜尋標題。
- **中間**:對話內容。連續的工具呼叫併成「Ran 3 commands」,點開看每一步。下方輸入框送出訊息,可選模型與權限模式。
- **右側**:環境(資料夾、git 分支)、context 使用率、未 commit 的變更行數。

## 需求

- macOS 14 以上,Xcode 或 Swift 6 工具鏈
- 已安裝並登入 Claude Code(`claude` 指令在 `/opt/homebrew/bin`、`/usr/local/bin` 或 `~/.local/bin`)

## 執行

```bash
cd mac
swift run                  # 開發時直接執行
./scripts/build-app.sh     # 包成 build/Desk.app(ad-hoc 簽章,只供本機)
open build/Desk.app
```

快捷鍵:⌘N 開新對話、⌘R 重新載入對話列表、⌥⌘I 顯示或隱藏右側面板、Enter 送出、Option+Enter 換行。

## 運作方式

- 每送出一則訊息,啟動一次 `claude -p <訊息> --output-format stream-json --verbose`,接續既有對話時加 `--resume <session id>`,在對話的資料夾執行。
- 權限模式對應 `--permission-mode`。print 模式不能互動詢問,所以 **Ask** 模式下需要許可的工具會被拒絕,Claude 會在回覆中說明;要讓它改檔案請選 **Accept edits**。
- 對話紀錄的 jsonl 格式不是公開規格,`TranscriptParser` 只讀需要的欄位,遇到不認得的紀錄就略過。
- 很長的對話只讀檔尾 24MB 顯示;列表只讀檔頭與標題,所以幾 GB 的紀錄也能很快列出。

## 測試

```bash
swift test                                   # 解析器單元測試
DESK_REAL_DATA=1 swift test --filter RealDataTests/testLoadProjectsFromThisMachine   # 讀本機真實紀錄、量載入時間
DESK_REAL_CLI=1 DESK_CLI_DIR=/tmp/desk-cli swift test --filter testRunnerNewThenResume  # 實際呼叫 claude(會產生對話紀錄)
```

## 尚未支援

- 工具執行前逐次詢問允許/拒絕(需要改用 Agent SDK 或 stream-json 輸入的權限回呼)
- 串流逐字顯示(目前一則回覆完成才出現)
- 附加檔案、圖片
- 顯示 diff、建立 PR
