package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// 旁白批次要跑的外部程式(shuttle 專案的 npm run voice),同 Swift 版 Voice/Shell.swift。
// 做成介面,測試換成假的(不會真的呼叫 Gemini)。

// VoiceShellResult 同 Swift 的 Shell.Result
type VoiceShellResult struct {
	Code int
	Out  string
	Err  string
}

func (r VoiceShellResult) OK() bool { return r.Code == 0 }

// Message 錯誤訊息優先,沒有就用一般輸出
func (r VoiceShellResult) Message() string {
	if e := strings.TrimSpace(r.Err); e != "" {
		return e
	}
	return strings.TrimSpace(r.Out)
}

// VoiceRunner 執行外部指令並等它結束
type VoiceRunner interface {
	Run(args []string, cwd string) VoiceShellResult
}

// VoiceExecRunner 真的執行指令(/usr/bin/env + 登入 shell 的 PATH),最多等 30 分鐘
type VoiceExecRunner struct{ Timeout time.Duration }

var (
	voicePathOnce sync.Once
	voicePath     string
)

// voiceLoginPath 向登入 shell 要一份完整的 PATH(npm 才找得到)
func voiceLoginPath() string {
	voicePathOnce.Do(func() {
		home, _ := os.UserHomeDir()
		fallback := strings.Join([]string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin", home + "/.local/bin"}, ":")
		voicePath = fallback
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, sh, "-lc", `printf %s "$PATH"`).Output()
		if s := strings.TrimSpace(string(out)); err == nil && s != "" {
			voicePath = s + ":" + fallback
		}
	})
	return voicePath
}

func (r VoiceExecRunner) Run(args []string, cwd string) VoiceShellResult {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 1800 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/env", args...)
	cmd.Dir = cwd
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") || strings.HasPrefix(kv, "NO_COLOR=") || strings.HasPrefix(kv, "FORCE_COLOR=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "PATH="+voiceLoginPath(), "NO_COLOR=1", "FORCE_COLOR=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		first := ""
		if len(args) > 0 {
			first = args[0]
		}
		return VoiceShellResult{Code: -1, Err: "無法執行 " + first + ":" + err.Error()}
	}
	err := cmd.Wait()
	code := 0
	if err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() >= 0 {
			code = ee.ExitCode()
		} else if ok {
			code = 15 // 被終止(逾時),Swift 的 terminate 會回傳 SIGTERM 的 15
		}
	}
	return VoiceShellResult{Code: code, Out: out.String(), Err: errb.String()}
}
