// Package errmask 判斷一個錯誤的原文是不是內部細節(資料庫、逾時、檔案路徑、連線位址…)、該不該對外換成通用訊息。
// 不依賴 service、store,所以 httperr(對外的狀態碼與訊息)與 service(稽核紀錄)都能用,不會循環 import。
package errmask

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/url"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 被換掉原文時對外的通用訊息
const (
	MsgDB       = "資料庫操作失敗,請稍後再試;若持續發生請聯絡管理員"
	MsgTimeout  = "操作逾時,請稍後再試;若持續發生請聯絡管理員"
	MsgInternal = "伺服器內部錯誤,請稍後再試;若持續發生請聯絡管理員"
)

// 訊息看起來含內部細節:SQL、SQLSTATE、檔案路徑、連線位址、逾時
var leakPatterns = regexp.MustCompile(`(?i)` + `(` + joinAlt(
	`SQLSTATE`,
	`\bpq: `,
	`\bpgx\b`,
	`violates (foreign key|unique|check|not-null)`,
	`duplicate key`,
	`relation "[^"]*"`,
	`column "[^"]*"`,
	`\b(select|insert into|update|delete from)\b[^\n]*\b(from|set|values|where|into)\b`,
	// 磁碟路徑:前面不是英數字,接反斜線,或 /目錄/ 至少兩層(不誤殺「時間格式要像 a:/b」)
	`(^|[^a-z0-9_])[a-z]:(\\|/[^\s/]+/)`,
	`password authentication`,
	`\bFATAL:`,
	`no rows in result set`,
	`conn closed`,
	`unexpected EOF`,
	`\b(tls|x509): `,
	`\b(get|post|put|patch|delete|head) "https?://`,
	`[?&](token|access_token|api_key|apikey|key|secret|password|passwd)=`,
	`(^|[\s"'(:=])/(home|var|tmp|usr|etc|opt|root|srv|mnt|app|data|proc|users)/\S`,
	`\.go:\d+`,
	`no such file or directory`,
	`cannot find the (path|file)`,
	`permission denied`,
	`access is denied`,
	`dial tcp`,
	`no such host`,
	`\bbearer\s+\S+`,
	`_(secret|token)=`,
	// UNC 路徑:\\主機\共用名稱(前面不是反斜線或英數字)
	`(^|[\s"'(:=])\\\\[^\\\s]+\\\S`,
	`connection refused`,
	`connection reset`,
	`broken pipe`,
	`i/o timeout`,
	`context deadline exceeded`,
	`\bpanic:`,
) + `)`)

func joinAlt(s ...string) string {
	out := ""
	for i, p := range s {
		if i > 0 {
			out += "|"
		}
		out += p
	}
	return out
}

// Mask err 的原文該不該換掉:該的話回傳通用訊息與 true。
// 型別明確的資料庫錯誤、逾時、檔案系統與網路錯誤一定換;heuristic 為 true 時再用文字比對猜(未分類的錯誤)。
// 呼叫端先處理「刻意給人看的」錯誤(httperr.Public、service.Error),再呼叫這裡。
func Mask(err error, heuristic bool) (string, bool) {
	if err == nil {
		return "", false
	}
	var pg *pgconn.PgError
	var conn *pgconn.ConnectError
	var cfg *pgconn.ParseConfigError
	if errors.As(err, &pg) || errors.As(err, &conn) || errors.As(err, &cfg) || errors.Is(err, pgx.ErrNoRows) {
		return MsgDB, true
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return MsgTimeout, true
	}
	var pathErr *fs.PathError
	var opErr *net.OpError
	var urlErr *url.Error
	if errors.As(err, &pathErr) || errors.As(err, &opErr) || errors.As(err, &urlErr) ||
		errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return MsgInternal, true
	}
	if heuristic && leakPatterns.MatchString(hintExample.ReplaceAllString(err.Error(), "")) {
		return MsgInternal, true
	}
	return "", false
}

// Text err 對外 / 寫進稽核紀錄用的文字:該換掉的換成通用訊息,否則照 err.Error()
func Text(err error) string {
	if err == nil {
		return ""
	}
	if m, ok := Mask(err, true); ok {
		return m
	}
	return err.Error()
}

// 提示用的範例路徑(「…,例如 /etc/gcp/key.json」)是給人看的說明,不算外洩;比對前先拿掉
var hintExample = regexp.MustCompile(`(例如|比如|像是|像)[::]?\s*[A-Za-z]?:?[\/]\S*`)
