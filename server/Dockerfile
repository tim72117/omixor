# omixor-server:omixor 的線上服務(Cloud Run)。引用 ../core,所以要在 repo 根目錄當建置內容:
#   docker build -f server/Dockerfile -t omixor-server .
FROM golang:1.26 AS build
WORKDIR /src
COPY core ./core
COPY server ./server
WORKDIR /src/server
RUN go mod download
# 版本資訊:docker build --build-arg VERSION=… --build-arg COMMIT=$GITHUB_SHA --build-arg BUILD_TIME=…
ARG VERSION=dev
ARG COMMIT=dev
ARG BUILD_TIME=dev
# 版本(功能集合,見 core/model/edition.go):預設 stable 正式版;開發版 docker build --build-arg EDITION=dev …
ARG EDITION=stable
RUN case "$EDITION" in stable) TAGS="" ;; dev) TAGS="dev" ;; *) echo "EDITION 只能是 stable 或 dev" >&2; exit 1 ;; esac && \
    CGO_ENABLED=0 go build -trimpath -tags "$TAGS" -ldflags="-s -w -X omixor-core/version.Version=${VERSION} -X omixor-core/version.Commit=${COMMIT} -X omixor-core/version.BuildTime=${BUILD_TIME}" -o /omixor-server ./cmd/omixor-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /omixor-server /omixor-server
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/omixor-server"]
