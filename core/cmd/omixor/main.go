// omixor:omixor server 的管理 CLI(用法見 core/cli)。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/cli"
	"omixor-core/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		printVersion()
		return
	}
	cli.Main(os.Args[1:])
}

// printVersion 印本地版本,以及目前登入的 server 的版本(沒登入或查不到就說明)
func printVersion() {
	fmt.Println("omixor CLI:", version.Info())
	srv := loginServer()
	if srv == "" {
		fmt.Println("server:還沒登入(omixor login)")
		return
	}
	d, err := version.Fetch(srv, 3*time.Second)
	if err != nil {
		fmt.Printf("server %s:查不到版本(%v)\n", srv, err)
		return
	}
	fmt.Printf("server %s:%s\n", srv, d)
}

// loginServer 讀 omixor login 存的登入資料,回傳發 token 的 server 網址(沒登入回傳 "")
func loginServer() string {
	p := os.Getenv("OMIXOR_CREDENTIALS")
	if p == "" {
		dir, _ := os.UserConfigDir()
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".config")
		}
		p = filepath.Join(dir, "omixor", "credentials.json")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var c struct {
		Server string `json:"server"`
		Token  string `json:"token"`
	}
	if json.Unmarshal(b, &c) != nil || c.Token == "" {
		return ""
	}
	if s := strings.TrimRight(c.Server, "/"); s != "" {
		return s
	}
	return cli.DefaultServer
}
