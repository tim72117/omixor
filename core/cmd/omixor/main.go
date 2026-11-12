// omixor:omixor server 的管理 CLI(用法見 core/cli)。
package main

import (
	"os"

	"omixor-core/cli"
)

func main() { cli.Main(os.Args[1:]) }
