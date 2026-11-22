# site/ — omixor.io 形象網站

對外的英文首頁。單一靜態頁,沒有建置步驟。本機預覽:`.claude/launch.json` 的 `omixor-site`(http://localhost:5300)。

## 檔案

| 檔案 | 對外 | 說明 |
|---|---|---|
| `index.html` | ✅ | 英文首頁(含 SEO meta、Open Graph、JSON-LD) |
| `pricing.html` | ✅ | 英文方案頁 `/pricing`(只有自建伺服器方案,不標價格;JSON-LD 只有 `WebPage`、`BreadcrumbList`) |
| `robots.txt` | ✅ | 允許收錄,擋掉 `/zh.html`、`/pricing.zh.html`、`/og.html`、`/api/`、`/mcp/`,並指向 sitemap |
| `sitemap.xml` | ✅ | 只列要被收錄的頁面(目前有 `/`、`/pricing`) |
| `llms.txt` | ✅ | 給 AI 爬蟲讀的簡短介紹與連線方式 |
| `404.html` | ✅ | 找不到頁面時的頁面(`noindex`,不設 canonical);server 對未知路徑以狀態碼 404 回它,`/404.html` 本身不開放 |
| `favicon.svg` | ✅ | 網站圖示 |
| `og.png` | ✅ | 社群分享預覽圖(由 `og.html` 截圖) |
| `zh.html` | ❌ | 中文對照版,內部看的,`noindex`,不部署 |
| `pricing.zh.html` | ❌ | `pricing.html` 的中文對照版,`noindex`,不部署 |
| `og.html` | ❌ | `og.png` 的來源頁,不部署 |
| `README.md` | ❌ | 這份說明 |

部署時只放「對外」的檔案。

## 部署

由 omixor server(Cloud Run 服務 `omixor`,網址 https://omixor.io)直接提供,不另外架站:

- `server/Dockerfile` 把上表「對外」的檔案複製到映像的 `/site`,並設 `OMIXOR_SITE_DIR=/site`。
- `server/site.go` 只提供白名單路徑:`/`(index.html)、`/pricing`(pricing.html)、`/favicon.svg`、`/og.png`、`/robots.txt`、`/sitemap.xml`、`/llms.txt`;其他路徑(例如 `/zh.html`)一律 404,也不會蓋到 `/api/`、`/mcp/`、`/health`。
- 檔案在啟動時讀進記憶體一次;文字檔(html、xml、txt、svg)預先壓好 gzip,請求接受 gzip 就回壓縮版(`Content-Encoding: gzip`、`Vary: Accept-Encoding`),png 不壓。
- 未知路徑的 GET/HEAD 回 `404.html`(狀態碼 404);`/api`、`/mcp` 底下與 `/health` 維持原本的純文字錯誤,其他方法與 405 不變(`siteNotFound`)。
- 改了 `site/` 要推 `v*` tag 才會重新部署(`.github/workflows/deploy-server.yml` 只在 tag 觸發;推 main 只跑測試)。
- 新增對外檔案:要同時改 `server/site.go` 的 `sitePublicFiles`、`server/Dockerfile` 的 COPY,以及上表(`404.html` 不在 `sitePublicFiles`,由 `site404File` 指定)。

## SEO 清單

做好了:
- `<title>`、`description`、`canonical`、`robots`(`max-image-preview:large`)
- Open Graph 與 Twitter Card(`og.png`)
- JSON-LD:`Organization`、`WebSite`、`SoftwareApplication`(不含價格、評分,沒有的資料不寫)
- `sitemap.xml`、`robots.txt`、`llms.txt`、`favicon.svg`
- `<html lang="en">`、單一 `<h1>`、手機版(viewport、無橫向捲軸)
- 文字檔 gzip 壓縮(server 端)
- 自訂 404 頁(狀態碼 404、`noindex`)
- `sitemap.xml` 的 `lastmod` 對上實際修改日;不寫 `changefreq`、`priority`(Google 不採用)

要接上線後才能做:
1. ~~伺服器要真的提供這些檔案~~:已由 server 提供(見「部署」),上線後確認 https://omixor.io/robots.txt、/sitemap.xml 打得開。
2. 網域擁有權已在 Google 驗證過(omixor.io 的 TXT `google-site-verification`),到 Google Search Console 新增資源並**提交 sitemap**。
3. 上線後用「網址審查」、Rich Results Test 檢查 JSON-LD,用 Facebook 分享偵錯工具與 Twitter Card Validator 看預覽圖。
4. 有 www 版本時,`www.omixor.io` 要 301 轉到 `omixor.io`(目前 www 不存在)。
5. `http://` → `https://` 目前是平台給的 302,能改成 301 較好(在平台/負載平衡設定,不在 server)。
6. `og.png` 目前 800×420,建議重截成 1200×630(並更新 `og:image:width`/`height`)。

## 改內容時

- 改 `index.html` 的文字,要同步改 `zh.html`(中文對照);`pricing.html` 對 `pricing.zh.html` 同理。
- `pricing.html` 的聯絡按鈕目前是占位(`is-placeholder`、`aria-disabled`),聯絡方式定了再補 `href`。
- 新增對外頁面:加進 `sitemap.xml`;不想被收錄的在 `robots.txt` 擋掉,並加 `noindex`。
- 改了 `index.html` 或 `pricing.html` 的內容,commit 後把 `sitemap.xml` 對應的 `<lastmod>` 改成該檔的最後修改日:`git log -1 --format=%cs -- site/index.html`(只改 `zh.html` 等不對外的檔案不用改)。
- 改了首頁標題或文案:`og.html` 的文字、`index.html` 的 `og:` / `twitter:` 描述也要一起改,再重截 `og.png`。
