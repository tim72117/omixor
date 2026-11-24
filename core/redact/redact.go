// Package redact 遮蔽看起來像機密的片段(token、密碼、連線網址、Authorization 標頭…)。
// 給 MCP 的 report_problem 與 service 的 commits 回報共用(service 不能 import mcp,所以放在這個小套件)。
package redact

import (
	"regexp"
	"strings"
	"unicode"
)

// Masked 被遮蔽的片段換成這個字串
const Masked = "[masked]"

var (
	reAuthHeader = regexp.MustCompile(`(?i)\bauthorization\s*:[^\r\n]*`)
	reBearer     = regexp.MustCompile(`(?i)\bbearer\s+[^\s"'<>]+`)
	reDBURL      = regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis|amqp)://[^\s"'<>]+`)
	reOmxToken   = regexp.MustCompile(`\b(?:omx|omo)_[A-Za-z0-9_\-.]+`)
	reCookie     = regexp.MustCompile(`(?i)\b(set-cookie|cookie)\s*:[^\r\n]*`)
	reKeyValue   = regexp.MustCompile(`(?i)(["']?\b(?:password|passwd|pwd|secret|client_secret|token|access_token|refresh_token|id_token|api_key|apikey|api-key|private_key|session|sessionid|auth|signature)\b["']?[ \t]*[:=][ \t]*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s&"',;]+)`)
	rePrefixTok  = regexp.MustCompile(`\b(?:eyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}|(?:gh[pous]_|github_pat_|sk_live_|sk_test_)[A-Za-z0-9_]{8,}|sk-[A-Za-z0-9_\-]{16,}|AKIA[0-9A-Z]{16}|AIza[A-Za-z0-9_\-]{35}|xox[abprs]-[A-Za-z0-9\-]{8,})`)
	reLongBlob   = regexp.MustCompile(`[A-Za-z0-9+/=_\-]{40,}`)
	// reGitOID 獨立的 40 或 64 位小寫十六進位字串(git 物件 id:sha-1、sha-256),前後不是字母數字
	reGitOID = regexp.MustCompile(`(?:^|[^A-Za-z0-9+/=_\-])([0-9a-f]{40}|[0-9a-f]{64})(?:$|[^A-Za-z0-9+/=_\-])`)
)

// Secrets 遮蔽看起來像機密的片段:Authorization 標頭整行、Bearer <...>、資料庫連線網址(postgres:// 等)、omx_ / omo_ 開頭的 token、
// Cookie / Set-Cookie 標頭整行、password / token / secret 等 key=value(只遮值,保留 key)、常見 token 前綴(ghp_、sk-、AKIA、AIza、xox、JWT 等)、
// 40 字元以上的 base64 / 十六進位長字串。oc_ 開頭的 client id 保留
func Secrets(s string) string {
	s = reAuthHeader.ReplaceAllString(s, "Authorization: "+Masked)
	s = reBearer.ReplaceAllString(s, "Bearer "+Masked)
	s = reDBURL.ReplaceAllString(s, Masked)
	s = reOmxToken.ReplaceAllString(s, Masked)
	s = reCookie.ReplaceAllString(s, "${1}: "+Masked)
	s = reKeyValue.ReplaceAllStringFunc(s, func(m string) string {
		sub := reKeyValue.FindStringSubmatch(m)
		q := ""
		if v := sub[2]; v != "" && (v[0] == '"' || v[0] == '\'') {
			q = v[:1]
		}
		return sub[1] + q + Masked + q
	})
	s = rePrefixTok.ReplaceAllString(s, Masked)
	s = reLongBlob.ReplaceAllString(s, Masked)
	return s
}

// SecretsKeepGitIDs 同 Secrets,但獨立出現的 git 物件 id(40 或 64 位小寫十六進位,例如 "This reverts commit <sha>")原樣保留,
// 不會被「40 字元以上長字串」規則遮掉。其餘規則完全一樣(key=value、token 前綴、Authorization 等照遮)
func SecretsKeepGitIDs(s string) string {
	var held []string
	var b strings.Builder
	last := 0
	for _, m := range reGitOID.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[2]])
		b.WriteString("\x00" + string(rune('A'+len(held)%26)) + itoa(len(held)) + "\x00")
		held = append(held, s[m[2]:m[3]])
		last = m[3]
	}
	if len(held) == 0 {
		return Secrets(s)
	}
	b.WriteString(s[last:])
	out := Secrets(b.String())
	for i, h := range held {
		out = strings.Replace(out, "\x00"+string(rune('A'+i%26))+itoa(i)+"\x00", h, 1)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// StripControl 移除控制字元與無效 UTF-8;multiline = true 時保留換行與 tab(\r 去掉),否則所有空白(含換行)壓成單一空格。
// 單一欄位原文超過 100000 位元組先截斷再處理(避免巨大輸入)
func StripControl(s string, multiline bool) string {
	const rawMax = 100000
	if len(s) > rawMax {
		s = s[:rawMax]
	}
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		case unicode.IsControl(r) || r == '\u00a0' || r == '\u200b' || r == '\ufeff':
			// 丟掉
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if !multiline {
		out = strings.Join(strings.Fields(out), " ")
	}
	return strings.TrimSpace(out)
}

// Cap 限制長度(以字元計)
func Cap(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}
