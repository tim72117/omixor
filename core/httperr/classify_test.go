package httperr

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/service"
	"omixor-core/store"
)

// 使用者 / agent 需要看到的訊息:不能被換成通用訊息(誤殺)
func TestHeuristicDoesNotOverMask(t *testing.T) {
	for _, msg := range []string{
		"時間格式要像 a:/b",
		"請設定 GCP 金鑰檔路徑,例如 /etc/gcp/key.json",
		"想用 c:/ 表示根目錄",
		"%s 底下沒有 orgs/<組織>/issues.json",
		"操作被取消(context canceled)之前沒有完成",
		"GitHub 回應 403:Resource not accessible by integration",
		"比例 a:/b 不合法",
	} {
		if in := Classify(errors.New(msg)); in.Masked || in.Msg != msg {
			t.Errorf("不該遮蔽 %q:%+v", msg, in)
		}
	}
}

// 內部細節:漏網的要遮蔽(含包一層、字串化)
func TestHeuristicMasksLeaks(t *testing.T) {
	urlErr := &url.Error{Op: "Get", URL: "http://10.0.0.5:8080/internal?token=abc", Err: io.EOF}
	cases := map[string]error{
		"password authentication": errors.New("FATAL: password authentication failed for user omixor"),
		"FATAL":                   errors.New("FATAL: terminating connection"),
		"no rows":                 errors.New("no rows in result set"),
		"conn closed":             errors.New("conn closed"),
		"unexpected EOF":          errors.New("read: unexpected EOF"),
		"url 帶 token(文字)":         errors.New(`Get "http://10.0.0.5:8080/internal?token=abc": EOF`),
		"url 帶 token(型別)":         urlErr,
		"url 帶 token(包一層)":        fmt.Errorf("同步:%w", urlErr),
		"url 帶 key(字串化)":          errors.New(fmt.Sprint("失敗 http://x/y?a=1&key=zzz")),
		"tls":                     errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"),
		"x509":                    errors.New("x509: certificate has expired"),
		"windows 路徑(中文後面)":        errors.New(`讀不出C:\omixor\x.json`),
		"正斜線磁碟路徑":                 errors.New("讀不出 D:/omixor/data/x.json"),
		"io.EOF":                  fmt.Errorf("讀回應:%w", io.EOF),
		"io.ErrUnexpectedEOF":     fmt.Errorf("讀回應:%w", io.ErrUnexpectedEOF),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			captureLog(t)
			ResetThrottle()
			in := Report("op", err)
			if !in.Masked || in.Msg == err.Error() {
				t.Fatalf("要遮蔽:%+v", in)
			}
			for _, w := range []string{"10.0.0.5", "token=abc", "omixor", "password", "x509", "C:\\", "D:/", "unexpected EOF"} {
				if strings.Contains(in.Msg, w) {
					t.Errorf("對外訊息含 %q:%s", w, in.Msg)
				}
			}
		})
	}
}

// 外部整合的錯誤(Public)不靠文字猜:訊息含 permission denied、dial tcp、i/o timeout 也照給
func TestPublicIntegrationErrorsKeepDetail(t *testing.T) {
	for _, msg := range []string{
		"GitHub 回應 403:… permission denied for token",
		"連不上 gitlab.com:dial tcp: lookup gitlab.com: no such host",
		"Facebook 回應逾時(i/o timeout)",
		"請求被取消(context canceled)",
	} {
		in := Classify(fakePublic{msg, 0, nil})
		if in.Masked || in.Msg != msg {
			t.Errorf("Public 要照給 %q:%+v", msg, in)
		}
	}
}

type causePublic struct {
	fakePublic
	cause error
}

func (c causePublic) Cause() error { return c.cause }

// Public 帶 Cause(連不上後端):對外固定句子、原文只進 log
func TestPublicWithCauseIsMasked(t *testing.T) {
	lg := captureLog(t)
	ResetThrottle()
	e := causePublic{fakePublic{"連不到 omixor server", 0, nil}, errors.New("dial tcp 10.1.2.3:8080: connect: connection refused")}
	in := Report("GET /x", e)
	if !in.Masked || in.Msg != "連不到 omixor server" || strings.Contains(in.Msg, "10.1.2.3") {
		t.Fatalf("%+v", in)
	}
	if !strings.Contains(lg.String(), "10.1.2.3") {
		t.Errorf("原文要進 log:%s", lg.String())
	}
	if in2 := Classify(fakePublic{"x", 0, nil}); in2.Masked {
		t.Error("沒有 Cause 的 Public 不遮蔽")
	}
}

func TestStatusOr(t *testing.T) {
	if Classify(fakePublic{"x", 0, nil}).StatusOr(500) != 500 || Classify(fakePublic{"x", 404, nil}).StatusOr(500) != 404 {
		t.Error("StatusOr")
	}
}

// service.Code 與 httperr 的狀態碼對照一致(service 不能 import httperr,所以用測試守住)
func TestCodeMatchesService(t *testing.T) {
	for _, err := range []error{
		&service.Error{Code: 403, Msg: "x"}, &service.Error{Code: 409, Msg: "x", Data: map[string]any{"a": 1}},
		fmt.Errorf("外層:%w", &service.Error{Code: 404, Msg: "x"}),
		store.ErrInUse, fmt.Errorf("x: %w", store.ErrInUse), store.ErrBadRef,
		store.ErrNotFound, store.ErrLocked, store.ErrProjectNoKey, store.ErrVersionConflict,
		&store.VersionConflictError{Current: 2}, errors.New("一般錯誤"), errors.New("SQLSTATE 23505"),
	} {
		if a, b := service.Code(err), Classify(err).Status; a != b {
			t.Errorf("%v:service.Code=%d,httperr=%d", err, a, b)
		}
	}
}

// log 注入:原文的換行、控制字元不會讓 log 多出一行
func TestLogNeutralizesNewlines(t *testing.T) {
	var lines []string
	old := Logf
	Logf = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	t.Cleanup(func() { Logf = old })
	ResetThrottle()
	Report("POST /x\nFAKE", errors.New("SQLSTATE 23505\n2026/01/01 admin 登入成功\r\x1b[31m"))
	if len(lines) != 1 || strings.ContainsAny(lines[0], "\n\r\x1b") {
		t.Fatalf("log 要是一行、沒有控制字元:%q", lines)
	}
}

// 節流:同一種錯誤每個視窗最多 throttleBurst 行,下一個視窗補報次數
func TestLogThrottle(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	old, oldNow := Logf, now
	cur := time.Unix(1000, 0)
	Logf = func(f string, a ...any) { mu.Lock(); lines = append(lines, fmt.Sprintf(f, a...)); mu.Unlock() }
	now = func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
	t.Cleanup(func() { Logf, now = old, oldNow; ResetThrottle() })
	ResetThrottle()
	err := errors.New("SQLSTATE 23505 重複")
	for i := 0; i < 100; i++ {
		Report("op", err)
	}
	if len(lines) != throttleBurst {
		t.Fatalf("第一個視窗要 %d 行,得到 %d", throttleBurst, len(lines))
	}
	Report("別的操作", err) // 不同的 key 不受影響
	if len(lines) != throttleBurst+1 {
		t.Fatalf("不同操作要各自計:%d", len(lines))
	}
	mu.Lock()
	cur = cur.Add(throttleWindow + time.Second)
	mu.Unlock()
	Report("op", err)
	last := lines[len(lines)-1]
	if len(lines) != throttleBurst+2 || !strings.Contains(last, fmt.Sprint(100-throttleBurst)) {
		t.Fatalf("下一個視窗要補報被省略的次數:%d %s", len(lines), last)
	}
}
