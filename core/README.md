# omixor-core

omixor 的核心後端模組(Go)。目前有:

| 套件 | 內容 |
|---|---|
| `model` | 共用的資料型別(Issue、組織、成員、使用者、token、操作紀錄),JSON 格式和 app、omixor-web 相同 |
| `store` | 存放介面,實作有 PostgreSQL(`store.NewPG`)與記憶體(`store.NewMem`,測試用) |
| `service` | 商業邏輯與權限:授權(用 evoleap 證明身分,發 omixor 自己的 token)、組織與成員、Issue |
| `api` | HTTP 路由(`api.New(svc).Handler()`) |
| `client` | 連線上 omixor server 的用戶端,提供和 `service` 一樣的 Issue/組織操作 |
| `cmd/omixor-server` | 線上服務(Cloud Run) |
| `cmd/omixor` | 管理 CLI |

## 架構

- **核心**(本模組):授權、組織、Issue 等後端模組。
- **web**(`omixor-web`):自己做網頁與介面,編譯時引入核心。啟動時二選一:
  - 內建:`OPS_DATABASE_URL` 連 PostgreSQL,核心在同一個程式裡跑
  - 遠端:`OMIXOR_SERVER` 指向線上 omixor server,授權、組織、Issue 都交給它
- **app**(`ops`,Swift):介面殼層與資料快取,重要資料從 omixor server 取得。

## 權限

- 伺服器管理員(`users.role = admin`):管使用者、token、操作紀錄,建立組織,看得到所有組織
- 組織管理員(`org_members.role = admin`):管自己組織的成員
- 組織成員:讀寫自己組織的 Issue;作者一律是自己(不能冒名)

## 測試

```bash
go test ./...
```
