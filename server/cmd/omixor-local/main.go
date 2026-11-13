// omixor-local:本機守護服務(只聽 127.0.0.1,資料存在資料夾的 JSON)。macOS App 本機模式在背景啟動它,也可以獨立執行(Windows、命令列,-h 看說明)。設定見 server/local.go、server/standalone.go。
package main

import "omixor-server"

func main() { server.RunLocal() }
