// omixor:omixor server 的管理 CLI。程式在 core/cli(用法見那裡);這裡保留是讓既有的 go run ./cmd/omixor 照常可用。
package main

import (
	"os"

	"omixor-core/cli"
)

func main() { cli.Main(os.Args[1:]) }
