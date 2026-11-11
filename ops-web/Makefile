# 建置:先建 React 畫面,再把它包進 Go 執行檔
.PHONY: build web run test

build: web
	go build -o bin/ops-web ./cmd/ops-web
	go build -o bin/ops-mcp ./cmd/ops-mcp

web:
	cd web && npm run build

run: build
	./bin/ops-web

# 測試用暫存資料夾,不碰真正的資料
test:
	OPS_DATA_DIR=/tmp/ops-web-test-$$PPID go test ./...
	cd web && npm run typecheck
