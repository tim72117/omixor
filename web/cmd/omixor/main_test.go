package main

import (
	"bytes"
	"strings"
	"testing"

	"omixor-core/cli"
)

// 指令轉給核心的 CLI(完整測試在 core/cli)
func TestForwardsToCoreCLI(t *testing.T) {
	t.Setenv("OMIXOR_CREDENTIALS", t.TempDir()+"/cred.json")
	err := cli.Run([]string{"whoami"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "還沒登入") {
		t.Fatal(err)
	}
}
