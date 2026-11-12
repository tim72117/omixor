// omixor-issues:舊名稱,實際就是 omixor-core 的 omixor-server(核心 + HTTP API)。
// 程式在 omixor-core(server、service、store、api);這裡保留是讓既有的建置與部署照常可用。
package main

import "omixor-core/server"

func main() { server.Run() }
