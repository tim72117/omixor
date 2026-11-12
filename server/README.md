# omixor-server

omixor 的線上服務(Cloud Run,服務名稱 `omixor`,網址 https://omixor-340121279179.asia-east1.run.app;舊的服務 `omixor-issues` 用同一個資料庫)。這個模組只負責「建置與啟動」:
HTTP 加健康檢查(`server.go`)與進入點(`cmd/omixor-server`);授權、組織、Issue、附件的邏輯都在 `../core`(`omixor-core`,用 `replace` 引用)。

```
cd server
go build ./cmd/omixor-server
docker build -f server/Dockerfile -t omixor-server .   # 在 repo 根目錄
```

環境變數見 `server.go` 開頭(`OPS_DATABASE_URL`、`OMIXOR_ADMIN_BOOTSTRAP_EMAIL`、`OMIXOR_TOKEN_DAYS`、`EVOLEAP_URL`…)。
部署:`.github/workflows/deploy-issues.yml`(`core/**` 或 `server/**` 有變動就部署)。
