#!/bin/bash
# 用 Docker 開一個暫時的 PostgreSQL(127.0.0.1:55432,資料放記憶體),跑資料庫相關的測試,結束後刪掉
set -e
cd "$(dirname "$0")/.."
NAME=ops-web-test-pg
docker rm -f $NAME >/dev/null 2>&1 || true
docker run -d --name $NAME --tmpfs /var/lib/postgresql/data -e POSTGRES_PASSWORD=test -e POSTGRES_DB=ops_test \
  -p 127.0.0.1:55432:5432 postgres:17.2-alpine >/dev/null
trap 'docker rm -f $NAME >/dev/null 2>&1' EXIT
for i in $(seq 1 60); do docker exec $NAME pg_isready -U postgres -d ops_test >/dev/null 2>&1 && break; sleep 0.5; done
sleep 1
export OPS_TEST_DATABASE_URL="postgres://postgres:test@127.0.0.1:55432/ops_test?sslmode=disable"
OPS_DATA_DIR=$(mktemp -d) OPS_NO_TIMERS=1 go test -count=1 "${@:-./internal/...}"
