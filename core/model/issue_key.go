package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Issue 的內部 id(UUIDv7)與顯示編號 key(ISS-<n>、<專案代號>-<n>);規則見 Issue 的說明(規格:雲端 omixor DOC-10)

// IssueKeyPrefix 組織層級 Issue 的 key 前綴(ISS-<n>)
const IssueKeyPrefix = "ISS"

// IssueScopeOrg 組織層級 Issue 的序號 scope;專案的是 IssueScope(專案 id)
const IssueScopeOrg = "issue"

// IssueScope Issue 序號的 scope:組織層級 "issue",專案 "issue:<專案 id>"
func IssueScope(project string) string {
	if project == "" {
		return IssueScopeOrg
	}
	return IssueScopeOrg + ":" + project
}

// IssueKey 組出 key:前綴 + "-" + 號碼
func IssueKey(prefix string, n int) string { return fmt.Sprintf("%s-%d", prefix, n) }

// IssueKeyNumber key 的號碼(最後一個「-」後面的數字;格式不對回 0)
func IssueKeyNumber(key string) int {
	key = strings.TrimSpace(key)
	k := strings.LastIndexByte(key, '-')
	if k <= 0 {
		return 0
	}
	return idNumber(key[k+1:], "")
}

// IssueKeyPrefixOf key 的前綴(APP-12 → APP;格式不對回 "")
func IssueKeyPrefixOf(key string) string {
	key = strings.TrimSpace(key)
	k := strings.LastIndexByte(key, '-')
	if k <= 0 || IssueKeyNumber(key) == 0 {
		return ""
	}
	return strings.ToUpper(key[:k])
}

// NewIssueID 產生內部 id:UUIDv7(RFC 9562),時間用 t(毫秒),其餘隨機;小寫、有連字號
func NewIssueID(t time.Time) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	ms := uint64(t.UnixMilli())
	if t.IsZero() || t.UnixMilli() < 0 {
		ms = 0
	}
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// IsUUID 是不是 8-4-4-4-12 的十六進位 UUID 格式(不分大小寫;不檢查版本)
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}

// ReservedProjectKeys 不能當專案代號的字(會和其他編號混淆)
var ReservedProjectKeys = []string{"ISS", "PRJ", "T", "DOC", "CH", "INC"}

// ErrProjectKey 專案代號格式不對(訊息給使用者看)
var ErrProjectKey = errors.New("專案代號要是 2 到 10 個英文大寫字母或數字,第一個字是英文字母")

// CleanProjectKey 整理專案代號:去頭尾空白、轉大寫;空字串 = 沒有代號(回 "", nil)。
// 規則:2 到 10 個大寫英數字,第一個字是英文字母;保留字(ReservedProjectKeys)不能用。組織內不重複由 service 檢查
func CleanProjectKey(s string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(s))
	if k == "" {
		return "", nil
	}
	if len(k) < 2 || len(k) > 10 || !(k[0] >= 'A' && k[0] <= 'Z') {
		return "", ErrProjectKey
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "", ErrProjectKey
		}
	}
	if OneOf(k, ReservedProjectKeys) {
		return "", fmt.Errorf("專案代號 %s 是保留字(%s 都不能用)", k, strings.Join(ReservedProjectKeys, "、"))
	}
	return k, nil
}
