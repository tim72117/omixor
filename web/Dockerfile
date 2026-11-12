# Ops(Go + React)的映像:先建 React 畫面,再編 Go,最後放進精簡的執行環境。
# 建置內容是 repo 根目錄(omixor-web 編譯時要引入旁邊的 omixor-core):
#   docker build -f web/Dockerfile .      (docker compose 已經設好)
FROM node:22.11-alpine AS web
WORKDIR /src/web
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ui/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY core ./core
COPY web/go.mod web/go.sum ./web/
WORKDIR /src/web
RUN go mod download
COPY web/ ./
COPY --from=web /src/web/dist ./ui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/omixor-web ./cmd/omixor-web \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/omixor-mcp ./cmd/omixor-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 ops
COPY --from=go /out/ /usr/local/bin/
# 範本資料(預設不載入;要用就設 OMIXOR_TEMPLATES=/usr/local/share/omixor/templates)
COPY templates /usr/local/share/omixor/templates
USER ops
ENV OPS_DATA_DIR=/home/ops/data OPS_NO_BROWSER=1 TZ=Asia/Taipei
EXPOSE 5191 3765
ENTRYPOINT ["/usr/local/bin/omixor-web"]
