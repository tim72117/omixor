# omixor console(Go + React)的映像:先建 React 畫面,再編 Go,最後放進精簡的執行環境。
# 建置內容是 repo 根目錄(omixor-web 編譯時要引入旁邊的 omixor-core):
#   docker build -f console/Dockerfile .      (docker compose 已經設好)
FROM node:22.11-alpine AS web
WORKDIR /src/web
COPY console/ui/package.json console/ui/package-lock.json ./
RUN npm ci --ignore-scripts
COPY console/ui/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY core ./core
COPY console/go.mod console/go.sum ./console/
WORKDIR /src/console
RUN go mod download
COPY console/ ./
COPY --from=web /src/web/dist ./ui/dist
# 版本資訊:docker build --build-arg VERSION=… --build-arg COMMIT=… --build-arg BUILD_TIME=…
ARG VERSION=dev
ARG COMMIT=dev
ARG BUILD_TIME=dev
RUN LDF="-s -w -X omixor-core/version.Version=${VERSION} -X omixor-core/version.Commit=${COMMIT} -X omixor-core/version.BuildTime=${BUILD_TIME}" \
 && CGO_ENABLED=0 go build -trimpath -ldflags="$LDF" -o /out/omixor-web ./cmd/omixor-web \
 && CGO_ENABLED=0 go build -trimpath -ldflags="$LDF" -o /out/omixor-mcp ./cmd/omixor-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 omixor
COPY --from=go /out/ /usr/local/bin/
# 範本資料(預設不載入;要用就設 OMIXOR_TEMPLATES=/usr/local/share/omixor/templates)
COPY templates /usr/local/share/omixor/templates
USER omixor
ENV OMIXOR_CONSOLE_DATA_DIR=/home/omixor/data OMIXOR_NO_BROWSER=1 TZ=Asia/Taipei
EXPOSE 5191
ENTRYPOINT ["/usr/local/bin/omixor-web"]
