# omixor-server:omixor 的線上服務(Cloud Run)。引用 ../core,所以要在 repo 根目錄當建置內容:
#   docker build -f server/Dockerfile -t omixor-server .
FROM golang:1.26 AS build
WORKDIR /src
COPY core ./core
COPY server ./server
# site/ 只在最後一層複製對外檔案(見下方)
WORKDIR /src/server
RUN go mod download
# 版本資訊:docker build --build-arg VERSION=… --build-arg COMMIT=$GITHUB_SHA --build-arg BUILD_TIME=…
ARG VERSION=dev
ARG COMMIT=dev
ARG BUILD_TIME=dev
# 所有功能都編進去;這台伺服器開放哪些功能由伺服器管理員在「系統」頁決定(存在資料庫,見 core/model/features.go)。
# 以前的 --build-arg EDITION 已經拿掉(給了也沒有作用)。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X omixor-core/version.Version=${VERSION} -X omixor-core/version.Commit=${COMMIT} -X omixor-core/version.BuildTime=${BUILD_TIME}" -o /omixor-server ./cmd/omixor-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /omixor-server /omixor-server
# 對外的形象網站(英文版):只放對外的檔案(zh.html、pricing.zh.html、og.html、README.md 不放;清單同 server/site.go 的 sitePublicFiles,另加 404 頁 site404File)
COPY site/index.html site/pricing.html site/404.html site/favicon.svg site/og.png site/robots.txt site/sitemap.xml site/llms.txt /site/
ENV OMIXOR_SITE_DIR=/site
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/omixor-server"]
