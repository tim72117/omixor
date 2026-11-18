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
- 改了 `site/` 推到 main 就會重新部署(`.github/workflows/deploy-server.yml` 的 paths 含 `site/**`)。
- 新增對外檔案:要同時改 `server/site.go` 的 `sitePublicFiles`、`server/Dockerfile` 的 COPY,以及上表。

## SEO 清單

做好了:
- `<title>`、`description`、`canonical`、`robots`(`max-image-preview:large`)
- Open Graph 與 Twitter Card(`og.png`)
- JSON-LD:`Organization`、`WebSite`、`SoftwareApplication`(不含價格、評分,沒有的資料不寫)
- `sitemap.xml`、`robots.txt`、`llms.txt`、`favicon.svg`
- `<html lang="en">`、單一 `<h1>`、手機版(viewport、無橫向捲軸)

要接上線後才能做:
1. ~~伺服器要真的提供這些檔案~~:已由 server 提供(見「部署」),上線後確認 https://omixor.io/robots.txt、/sitemap.xml 打得開。
2. 網域擁有權已在 Google 驗證過(omixor.io 的 TXT `google-site-verification`),到 Google Search Console 新增資源並**提交 sitemap**。
3. 上線後用「網址審查」、Rich Results Test 檢查 JSON-LD,用 Facebook 分享偵錯工具與 Twitter Card Validator 看預覽圖。
4. 有 www 版本時,`www.omixor.io` 要 301 轉到 `omixor.io`(目前 www 不存在)。

## 改內容時

- 改 `index.html` 的文字,要同步改 `zh.html`(中文對照);`pricing.html` 對 `pricing.zh.html` 同理。
- `pricing.html` 的聯絡按鈕目前是占位(`is-placeholder`、`aria-disabled`),聯絡方式定了再補 `href`。
- 新增對外頁面:加進 `sitemap.xml`;不想被收錄的在 `robots.txt` 擋掉,並加 `noindex`。
- 改了首頁標題或文案:`og.html` 的文字、`index.html` 的 `og:` / `twitter:` 描述也要一起改,再重截 `og.png`。
