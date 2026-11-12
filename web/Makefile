# 建置:先建 React 畫面,再把它包進 Go 執行檔
.PHONY: build web run test

build: web
	go build -o bin/omixor-web ./cmd/omixor-web
	go build -o bin/omixor-mcp ./cmd/omixor-mcp

web:
	cd ui && npm run build

run: build
	./bin/omixor-web

# 測試用暫存資料夾,不碰真正的資料
test:
	OPS_DATA_DIR=/tmp/omixor-web-test-$$PPID go test ./...
	cd ui && npm run typecheck
