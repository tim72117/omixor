package httperr

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"omixor-core/store"
)

// 哨兵錯誤:不論被怎麼包,對外一律固定句子;包在後面的原文(位置、鍵值、舊資料)只進 log
func TestSentinelsAlwaysFixedMessage(t *testing.T) {
	lg := captureLog(t)
	cases := []struct {
		name   string
		err    error
		status int
		msg    string
		secret string
	}{
		{"NotFound 夾帶資料庫文字", fmt.Errorf("讀 /var/lib/omixor/data.db 失敗:%w", store.ErrNotFound), 404, MsgNotFound, "/var/lib/omixor"},
		{"Locked 夾帶", fmt.Errorf("task 7f3a org=secret-org:%w", store.ErrLocked), 409, MsgLocked, "secret-org"},
		{"Unmigrated 夾帶舊資料", fmt.Errorf("%w:文件櫃結構 org=%q project=%q(%v)", store.ErrUnmigratedRow, "o1", "p1", "invalid character 'x' at {\"pw\":1}"), 503, MsgUnmigratedRow, "invalid character"},
		{"ProjectNoKey 夾帶", fmt.Errorf("pg: %w (project=abc123)", store.ErrProjectNoKey), 400, MsgProjectNoKey, "abc123"},
		{"VersionConflict 夾帶", fmt.Errorf("update issues set ...:%w", &store.VersionConflictError{Current: 9}), 409, MsgVersionConflict, "update issues"},
	}
	for _, c := range cases {
		in := Report("PUT /x", c.err)
		if in.Status != c.status || in.Msg != c.msg || !in.Masked {
			t.Errorf("%s:%+v", c.name, in)
		}
		if strings.Contains(in.Msg, c.secret) {
			t.Errorf("%s:對外訊息含原文", c.name)
		}
	}
	for _, secret := range []string{"/var/lib/omixor", "secret-org", "invalid character", "abc123", "update issues"} {
		if !strings.Contains(lg.String(), secret) {
			t.Errorf("原文 %q 要進 log", secret)
		}
	}
	// 版本衝突的 data 要保留
	in := Classify(fmt.Errorf("x:%w", &store.VersionConflictError{Current: 9}))
	if in.Data["currentVersion"] != int64(9) || in.Body()["currentVersion"] != int64(9) {
		t.Errorf("currentVersion 要保留:%+v", in)
	}
	// 沒夾帶東西的哨兵本身:固定句子、不算被遮蔽(沒有原文可記)
	if in := Classify(store.ErrNotFound); in.Masked || in.Msg != MsgNotFound {
		t.Errorf("%+v", in)
	}
	if in := Classify(&store.VersionConflictError{Current: 3}); in.Masked || in.Data["currentVersion"] != int64(3) {
		t.Errorf("%+v", in)
	}
}

// 刻意給人看的文字用 Human:照給、狀態碼沿用,errors.Is 仍認得
func TestHumanKeepsDeliberateMessage(t *testing.T) {
	captureLog(t)
	err := Human(store.ErrNotFound, "找不到這個專案(代號 SHU)")
	in := Classify(err)
	if in.Status != 404 || in.Msg != "找不到這個專案(代號 SHU)" || in.Masked {
		t.Fatalf("%+v", in)
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatal("errors.Is 要認得")
	}
	in = Classify(fmt.Errorf("外層:%w", Human(&store.VersionConflictError{Current: 4}, "已被別人改過")))
	if in.Status != 409 || in.Msg != "已被別人改過" || in.Data["currentVersion"] != int64(4) {
		t.Fatalf("%+v", in)
	}
}
