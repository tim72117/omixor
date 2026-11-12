#!/bin/bash
# 停掉佔用這些連接埠的比對用實例:conformance/stop.sh 3801 3802 …
for p in "$@"; do lsof -ti tcp:$p -sTCP:LISTEN | xargs kill 2>/dev/null; done
true
