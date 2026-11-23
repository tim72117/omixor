package httperr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/service"
	"omixor-core/store"
)

// 偽造的內部錯誤:對外不能出現這些字串
var leakWords = []string{"agent_tasks", "agent_id", "SQLSTATE", "23503", "violates", "INSERT INTO", "deadline exceeded",
	`C:\omixor`, "/var/lib/omixor", "dial tcp", "127.0.0.1", "pq:"}

func fakeInternalErrors() map[string]error {
	pg := &pgconn.PgError{Code: "23503", Severity: "ERROR", TableName: "agent_tasks", ColumnName: "agent_id",
		ConstraintName: "agent_tasks_agent_id_fkey",
		Message:        `insert or update on table "agent_tasks" violates foreign key constraint "agent_tasks_agent_id_fkey"`,
		Detail:         `Key (agent_id)=(a_1) is not present in table "agents".`}
	return map[string]error{
		"pgerror":      pg,
		"pgerror 包一層":  fmt.Errorf("儲存任務:%w", pg),
		"deadline":     fmt.Errorf("query agent_tasks: %w", context.DeadlineExceeded),
		"deadline 純文字": errors.New("context deadline exceeded"),
		"sql 文字":       errors.New(`INSERT INTO agent_tasks (id, agent_id) VALUES ($1,$2): SQLSTATE 23503`),
		"pq 文字":        errors.New(`pq: relation "agent_tasks" does not exist`),
		"windows 路徑":   &fs.PathError{Op: "open", Path: `C:\omixor\data\x.json`, Err: os.ErrNotExist},
		"unix 路徑文字":    errors.New("讀不出設定 /var/lib/omixor/config.json: permission denied"),
		"連線":           &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		"windows 路徑文字": errors.New(`讀不出 C:\omixor\x.json`),
		"連線文字":         errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"),
	}
}

func captureLog(t *testing.T) *strings.Builder {
	var sb strings.Builder
	old := Logf
	Logf = func(f string, a ...any) { fmt.Fprintf(&sb, f, a...) }
	t.Cleanup(func() { Logf = old })
	return &sb
}

func TestInternalErrorsAreMasked(t *testing.T) {
	for name, err := range fakeInternalErrors() {
		t.Run(name, func(t *testing.T) {
			lg := captureLog(t)
			in := Report("POST /api/x", err)
			if !in.Masked || in.Status != 500 {
				t.Fatalf("要遮蔽且是 500:%+v", in)
			}
			for _, w := range leakWords {
				if strings.Contains(in.Msg, w) {
					t.Errorf("對外訊息含內部字串 %q:%s", w, in.Msg)
				}
			}
			if !strings.Contains(lg.String(), "POST /api/x") || !strings.Contains(lg.String(), err.Error()) {
				t.Errorf("log 要有操作名稱與原文:%s", lg.String())
			}
		})
	}
}

func TestPassThrough(t *testing.T) {
	lg := captureLog(t)
	cases := []struct {
		name   string
		err    error
		status int
		msg    string
		data   map[string]any
	}{
		{"service.Error", &service.Error{Code: 403, Msg: "不是這個組織的成員"}, 403, "不是這個組織的成員", nil},
		{"service.Error 帶 data(版本衝突)", &service.Error{Code: 409, Msg: "結構已被別人更新,請重新載入", Data: map[string]any{"currentVersion": "v7"}},
			409, "結構已被別人更新,請重新載入", map[string]any{"currentVersion": "v7"}},
		{"包起來的 service.Error", fmt.Errorf("外層:%w", &service.Error{Code: 404, Msg: "找不到"}), 404, "找不到", nil},
		{"ErrInUse", fmt.Errorf("delete: %w (agent_tasks)", store.ErrInUse), 409, MsgInUse, nil},
		{"ErrBadRef", store.ErrBadRef, 400, MsgBadRef, nil},
		{"ErrNotFound", store.ErrNotFound, 404, MsgNotFound, nil},
		{"ErrVersionConflict", store.ErrVersionConflict, 409, MsgVersionConflict, nil},
		{"VersionConflictError 帶目前版本", fmt.Errorf("存檔:%w", &store.VersionConflictError{Current: 7}), 409, MsgVersionConflict, map[string]any{"currentVersion": int64(7)}},
		{"ErrLocked", store.ErrLocked, 409, MsgLocked, nil},
		{"ErrProjectNoKey", store.ErrProjectNoKey, 400, MsgProjectNoKey, nil},
		{"一般的中文錯誤", errors.New("要伺服器管理員"), 500, "要伺服器管理員", nil},
		{"Public 沒指定狀態碼", fakePublic{"給畫面看的說明", 0, nil}, 0, "給畫面看的說明", nil},
		{"Public 帶狀態碼與 data", fakePublic{"名下還有任務", 409, map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}},
			409, "名下還有任務", map[string]any{"code": "agentHasTasks", "taskCounts": map[string]any{"open": 2}}},
		{"Public 的訊息含路徑也照給(刻意顯示)", fakePublic{`GitHub 回應錯誤:Not Found (/repos/o/r)`, 0, nil}, 0, `GitHub 回應錯誤:Not Found (/repos/o/r)`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Report("op", c.err)
			if in.Status != c.status || in.Msg != c.msg {
				t.Fatalf("得到 %d %q,要 %d %q", in.Status, in.Msg, c.status, c.msg)
			}
			if fmt.Sprint(in.Data) != fmt.Sprint(c.data) && !(len(in.Data) == 0 && len(c.data) == 0) {
				t.Fatalf("data %v,要 %v", in.Data, c.data)
			}
			b := in.Body()
			if b["error"] != c.msg {
				t.Fatalf("body %v", b)
			}
			for k := range c.data {
				if _, ok := b[k]; !ok {
					t.Errorf("body 缺 %s:%v", k, b)
				}
			}
		})
	}
	// 只有外鍵(ErrInUse / ErrBadRef)遮蔽原文;其他的原文本來就是對外訊息
	if !strings.Contains(lg.String(), "agent_tasks") {
		t.Errorf("ErrInUse 的原文要進 log:%s", lg.String())
	}
}

func TestDetailKeepsDiagnostics(t *testing.T) {
	captureLog(t)
	err := errors.New("dial tcp 10.0.0.5:389: connect: connection refused")
	if in := Classify(err); !in.Masked {
		t.Error("Classify 要遮蔽連線位址")
	}
	if in := ClassifyDetail(err); in.Masked || in.Msg != err.Error() {
		t.Errorf("ClassifyDetail(管理員診斷)要照原訊息:%+v", in)
	}
	// 但型別明確的資料庫錯誤診斷也不顯示
	if in := ClassifyDetail(&pgconn.PgError{Code: "42P01", Message: `relation "x" does not exist`}); !in.Masked {
		t.Error("PgError 一律遮蔽")
	}
}

func TestBodyDataDoesNotOverrideError(t *testing.T) {
	b := Info{Msg: "m", Data: map[string]any{"error": "x", "result": 1, "k": 2}}.Body()
	if b["error"] != "m" || b["k"] != 2 || b["result"] != nil {
		t.Fatalf("%v", b)
	}
}

type fakePublic struct {
	msg    string
	status int
	data   map[string]any
}

func (f fakePublic) Error() string                    { return f.msg }
func (f fakePublic) HTTPError() (int, map[string]any) { return f.status, f.data }
