package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Session 是常駐的 CLI 程序：只啟動一次，之後每次 Send 是同一段對話的下一輪。
// 省掉每次啟動程序的 1.5–2 秒，記憶體也只占一份。同一時間只能有一個 Send。
type Session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event
	stderr *lockedBuffer
	tools  *toolServer

	mu      sync.Mutex // 一次只跑一輪
	readErr error      // events 關閉後才可讀
	closed  bool
	killed  bool
}

// Start 啟動常駐程序。ctx 結束時程序會被終止；用完要 Close。
func Start(ctx context.Context, o Options) (s *Session, err error) {
	o, tools, err := o.withTools()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tools.close()
		}
	}()
	cmd, _ := o.command(ctx, o.SessionArgs())
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("啟動 %s：%w", cmd.Path, err)
	}
	s = &Session{cmd: cmd, stdin: stdin, events: make(chan Event, 64), stderr: stderr, tools: tools}
	go s.read(out)
	return s, nil
}

func (s *Session) read(out io.Reader) {
	defer close(s.events)
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		ev, err := decode(sc.Bytes())
		if err != nil {
			s.readErr = err
			return
		}
		s.events <- ev
	}
	s.readErr = sc.Err()
}

// Send 送出下一輪的問題，回傳這一輪的 result 事件。onEvent 可為 nil。
func (s *Session) Send(ctx context.Context, prompt string, onEvent func(Event)) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("session 已關閉")
	}
	line, err := json.Marshal(userInput{Type: "user", Message: userMessage{Role: "user", Content: prompt}})
	if err != nil {
		return nil, err
	}
	if _, err := s.stdin.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("送出問題：%w：%s", err, s.stderrText())
	}
	for {
		select {
		case <-ctx.Done():
			// 這一輪的輸出還在路上，程序的狀態已經對不上，只能結束它
			s.killed = true
			s.cmd.Process.Kill()
			s.closeLocked()
			return nil, ctx.Err()
		case ev, ok := <-s.events:
			if !ok {
				s.closed = true
				s.cmd.Wait()
				s.tools.close()
				if s.readErr != nil {
					return nil, s.readErr
				}
				return nil, fmt.Errorf("claude 程序已結束：%s", s.stderrText())
			}
			if onEvent != nil {
				onEvent(ev)
			}
			if r, ok := ev.(*Result); ok {
				if r.IsError {
					return r, fmt.Errorf("claude 回報錯誤（%s）：%s", r.Subtype, r.Text)
				}
				return r, nil
			}
		}
	}
}

// Close 關閉 stdin 讓程序自己結束，並等它退出。
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *Session) closeLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.stdin.Close()
	for range s.events { // 讀完剩下的輸出，程序才不會卡在寫入
	}
	err := s.cmd.Wait()
	s.tools.close()
	if s.killed {
		return nil
	}
	return err
}

func (s *Session) stderrText() string { return strings.TrimSpace(s.stderr.String()) }

type userInput struct {
	Type    string      `json:"type"`
	Message userMessage `json:"message"`
}

type userMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// lockedBuffer 讓 exec 寫 stderr 的 goroutine 和讀取的一方不互相干擾。
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
