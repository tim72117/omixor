# 建置:先建 React 畫面,再把它包進 Go 執行檔
.PHONY: build web run test

# 版本資訊(core/version):git commit 與建置時間
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo dev)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X omixor-core/version.Commit=$(COMMIT) -X omixor-core/version.BuildTime=$(BUILD_TIME)

build: web
	go build -ldflags "$(LDFLAGS)" -o bin/omixor-web ./cmd/omixor-web
	go build -ldflags "$(LDFLAGS)" -o bin/omixor-mcp ./cmd/omixor-mcp

web:
	cd ui && npm run build

run: build
	./bin/omixor-web

# 測試用暫存資料夾,不碰真正的資料
test:
	OPS_DATA_DIR=/tmp/omixor-web-test-$$PPID go test ./...
	cd ui && npm run typecheck
