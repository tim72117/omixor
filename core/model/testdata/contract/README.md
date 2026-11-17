# 跨語言契約範例檔

這裡的 `*.json` 是 core API 實際回傳的 JSON 形狀,由 `core/model/contract_test.go` 用代表性樣本(欄位盡量填滿,含選填欄位、時間、巢狀;列表另放一筆只有必填欄位的)產生。**不要手改**。

- 每個檔案只放回應 `{"result": …}` 裡的 `result` 內容(外層的 `{"result": …}` 由各平台的用戶端拆掉)。
- 時間是到秒的 RFC 3339(`model.Time`);沒值的選填欄位依 Go 的 `omitempty` 省略,`ChannelSummary.lastMessageAt`、`ClaimResult.task` 沒值時是 `null`。
- 每個檔案對應的路由寫在 `contract_test.go` 的 `contractFixtures()`。

讀這些檔案的測試:

| 平台 | 測試 | 解碼路徑 |
| --- | --- | --- |
| Go | `core/model/contract_test.go` | 產生/比對範例檔,並確認 Go 讀得回來 |
| macOS App | `mac/Tests/OmixorTests/ContractTests.swift` | 假伺服器回範例檔,走 `IssueRemote` 的 list/getChannel/listIncidents/claimTask… 等 App 實際的方法 |
| iOS | `ios/OmixorTests/ContractTests.swift` | `APIClient.decoder` 解 `ios/Omixor/API/Models.swift` 的型別 |
| console/ui | `console/ui/contract/`(`npm test`) | `tsc` 檢查範例檔符合前端型別(兩個方向的欄位名稱),`node --test` 執行時檢查關鍵欄位與前端的轉換函式 |

改了 model 欄位之後:

```sh
cd core && UPDATE_CONTRACT=1 go test ./model/ -run TestContract   # 重生範例檔
cd mac && swift test --filter ContractTests                        # macOS App
cd console/ui && npm test                                              # console/ui
# iOS:cd ios && xcodegen generate && xcodebuild test -scheme Omixor -destination 'platform=iOS Simulator,name=iPhone 17 Pro'
```

新增範例檔:在 `contractFixtures()` 加一筆、重生,再到上面三個平台的測試加對應的解碼與斷言。
