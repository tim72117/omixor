# Ops(Go + React)的映像:先建 React 畫面,再編 Go,最後放進精簡的執行環境
FROM node:22.11-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ops-web ./cmd/ops-web \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ops-mcp ./cmd/ops-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 ops
COPY --from=go /out/ /usr/local/bin/
USER ops
ENV OPS_DATA_DIR=/home/ops/data OPS_NO_BROWSER=1 TZ=Asia/Taipei
EXPOSE 5191 3765
ENTRYPOINT ["/usr/local/bin/ops-web"]
