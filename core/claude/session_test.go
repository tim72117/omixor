package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// 測試時把測試執行檔本身當成假的 claude：讀 stdin 的每一行，回 init、assistant、result。
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CLAUDE") == "1" {
		fakeClaude()
		return
	}
	os.Exit(m.Run())
}

func fakeClaude() {
	sc := bufio.NewScanner(os.Stdin)
	turn := 0
	for sc.Scan() {
		var in userInput
		json.Unmarshal(sc.Bytes(), &in)
		turn++
		if in.Message.Content == "slow" {
			time.Sleep(10 * time.Second)
		}
		text, _ := json.Marshal(fmt.Sprintf("%d:%s", turn, in.Message.Content))
		fmt.Printf(`{"type":"system","subtype":"init","session_id":"s","model":"fake"}` + "\n")
		fmt.Printf(`{"type":"assistant","message":{"content":[{"type":"text","text":%s}]}}`+"\n", text)
		fmt.Printf(`{"type":"result","subtype":"success","result":%s,"session_id":"s","num_turns":%d}`+"\n", text, turn)
	}
}

func fakeOptions(t *testing.T) Options {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE", "1")
	return Options{Bin: exe}
}

func TestSessionArgs(t *testing.T) {
	got := Options{Lean: true}.SessionArgs()
	if !slices.Contains(got, "--input-format") || !slices.Contains(got, "--strict-mcp-config") || slices.Contains(got, "--") {
		t.Fatalf("%q", got)
	}
}

func TestSessionTurns(t *testing.T) {
	ctx := context.Background()
	s, err := Start(ctx, fakeOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, q := range []string{"一", "二 \"引號\"\n換行"} {
		var seen []string
		r, err := s.Send(ctx, q, func(ev Event) {
			if a, ok := ev.(*Assistant); ok {
				seen = append(seen, a.Text())
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%d:%s", i+1, q)
		if r.Text != want || r.NumTurns != i+1 || !slices.Equal(seen, []string{want}) {
			t.Fatalf("第 %d 輪：%+v %q", i+1, r, seen)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, "x", nil); err == nil || !strings.Contains(err.Error(), "已關閉") {
		t.Fatalf("關閉後應該報錯：%v", err)
	}
}

func TestSessionCancel(t *testing.T) {
	s, err := Start(context.Background(), fakeOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Send(ctx, "slow", nil); err != context.DeadlineExceeded {
		t.Fatalf("應該逾時：%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("取消後沒有馬上結束程序")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
