package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// 機密遮蔽:設定值(Display、String、%v、%#v、slog)與錯誤訊息(Redact)都不會洩漏機密。

const masked = "***"

// 連線設定裡會被遮蔽的參數名(網址的 query、key=value 形式都適用;不分大小寫)
var secretParams = map[string]bool{"password": true, "sslpassword": true, "sslkey": true}

// connPasswords 從資料庫連線設定(網址或 key=value 形式)取出所有機密片段(原文與解碼後各一份)
func connPasswords(s string) []string {
	var out []string
	add := func(raw string) {
		if raw == "" {
			return
		}
		out = append(out, raw)
		if d, err := url.QueryUnescape(raw); err == nil && d != raw {
			out = append(out, d)
		}
		if d, err := url.PathUnescape(raw); err == nil && d != raw {
			out = append(out, d)
		}
	}
	for _, sp := range connSecretSpans(s) {
		add(s[sp[0]:sp[1]])
	}
	return out
}

// RedactURL 把資料庫連線設定(網址或 key=value)裡的密碼換成 ***:
// 網址遮 userinfo 的密碼與 query 的 password/sslpassword/sslkey;key=value 遮同名的值(支援單引號與反斜線跳脫)。
func RedactURL(s string) string {
	spans := connSecretSpans(s)
	if len(spans) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(s[last:sp[0]])
		b.WriteString(masked)
		last = sp[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// connSecretSpans 機密片段在 s 裡的位置([開始, 結束),由前到後、不重疊)
func connSecretSpans(s string) [][2]int {
	if i := strings.Index(s, "://"); i > 0 && !strings.ContainsAny(s[:i], " \t=") {
		return urlSecretSpans(s, i+3)
	}
	return kvSecretSpans(s)
}

func urlSecretSpans(s string, start int) [][2]int {
	var spans [][2]int
	end := len(s)
	if k := strings.IndexAny(s[start:], "/?#"); k >= 0 {
		end = start + k
	}
	if at := strings.LastIndex(s[start:end], "@"); at >= 0 {
		ui := s[start : start+at]
		if c := strings.Index(ui, ":"); c >= 0 && c+1 < len(ui) {
			spans = append(spans, [2]int{start + c + 1, start + at})
		}
	}
	q := strings.Index(s[end:], "?")
	if q < 0 {
		return spans
	}
	pos := end + q + 1
	stop := len(s)
	if h := strings.Index(s[pos:], "#"); h >= 0 {
		stop = pos + h
	}
	for pos <= stop {
		amp := strings.IndexAny(s[pos:stop], "&;")
		pe := stop
		if amp >= 0 {
			pe = pos + amp
		}
		if eq := strings.Index(s[pos:pe], "="); eq >= 0 {
			key, _ := url.QueryUnescape(s[pos : pos+eq])
			if secretParams[strings.ToLower(key)] && pos+eq+1 < pe {
				spans = append(spans, [2]int{pos + eq + 1, pe})
			}
		}
		if amp < 0 {
			break
		}
		pos = pe + 1
	}
	return spans
}

// kvSecretSpans libpq 的 key=value 形式:鍵 = 值,以空白分隔;值可以用單引號包起來(裡面 \' 與 \ 是跳脫)
func kvSecretSpans(s string) [][2]int {
	var spans [][2]int
	i, n := 0, len(s)
	const bslash = byte(92) // 反斜線
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
	for i < n {
		for i < n && isSpace(s[i]) {
			i++
		}
		ks := i
		for i < n && !isSpace(s[i]) && s[i] != '=' {
			i++
		}
		key := strings.ToLower(s[ks:i])
		for i < n && isSpace(s[i]) {
			i++
		}
		if i >= n || s[i] != '=' {
			continue
		}
		i++
		for i < n && isSpace(s[i]) {
			i++
		}
		vs, ve := i, i
		if i < n && s[i] == '\'' {
			i++
			vs = i
			for i < n && s[i] != '\'' {
				if s[i] == bslash && i+1 < n {
					i++
				}
				i++
			}
			ve = i
			if i < n {
				i++
			}
		} else {
			for i < n && !isSpace(s[i]) {
				if s[i] == bslash && i+1 < n {
					i++
				}
				i++
			}
			ve = i
		}
		if secretParams[key] && ve > vs {
			spans = append(spans, [2]int{vs, ve})
		}
	}
	return spans
}

// Display 值的顯示用字串:機密遮蔽(連線網址/key=value 只遮密碼),空值回 ""
func (v *Var) Display(val string) string {
	if val == "" {
		return ""
	}
	if !v.Secret {
		return val
	}
	if v.Kind == URL || v.Kind == Conn {
		return RedactURL(val)
	}
	return masked
}

// String 所有有設的值(機密已遮蔽),給日誌用。值接收者:fmt.Printf("%v", *c) 也不會列出內部的 raw
func (c Config) String() string {
	var names []string
	for n := range c.raw {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s=%s\n", n, byName[n].Display(c.raw[n]))
	}
	return b.String()
}

// GoString 讓 %#v 也不洩漏機密
func (c Config) GoString() string {
	return "config.Config{" + strings.ReplaceAll(strings.TrimSpace(c.String()), "\n", " ") + "}"
}

// Format 所有 fmt 動詞(%v %+v %#v %s %q %d…)都只輸出遮蔽後的內容,不會用反射列出 raw
func (c Config) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('#') {
		_, _ = fmt.Fprint(f, c.GoString())
		return
	}
	_, _ = fmt.Fprint(f, strings.TrimSpace(c.String()))
}

// LogValue 給 slog 用(機密已遮蔽)
func (c Config) LogValue() slog.Value { return slog.StringValue(strings.TrimSpace(c.String())) }

// secretValues 設定 v 的值裡要被遮蔽的片段:整個值,加上資料庫連線裡的密碼、OMIXOR_SERVERS 這類 JSON 裡的
// token/password/secret 欄位(各自拆出來:錯誤訊息常常只含其中一個)
func secretValues(v *Var, val string) []string {
	out := []string{val}
	switch {
	case v.Kind == URL || v.Kind == Conn:
		out = append(out, connPasswords(val)...)
	case v.Kind == String:
		var j any
		if t := strings.TrimSpace(val); (strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{")) && json.Unmarshal([]byte(t), &j) == nil {
			collectJSONSecrets(j, &out)
		}
	}
	return out
}

func collectJSONSecrets(x any, out *[]string) {
	switch t := x.(type) {
	case []any:
		for _, e := range t {
			collectJSONSecrets(e, out)
		}
	case map[string]any:
		for k, e := range t {
			lk := strings.ToLower(k)
			if str, ok := e.(string); ok && (strings.Contains(lk, "token") || strings.Contains(lk, "password") || strings.Contains(lk, "secret") ||
				strings.Contains(lk, "apikey") || strings.Contains(lk, "api_key") || strings.Contains(lk, "credential")) {
				if str != "" {
					*out = append(*out, str)
				}
				continue
			}
			collectJSONSecrets(e, out)
		}
	}
}

// Redact 把字串裡出現的機密值換成 ***(連線設定裡的密碼、OMIXOR_SERVERS 裡的 token 也會被單獨找出來換);
// 錯誤訊息、日誌要印可能含機密的文字時先過它。
//
// 取捨:長度 >= 3 的機密直接字串取代;長度 1–2 的機密只在「整字」(前後不是字母、數字、底線)時才換,
// 避免把訊息裡常見的字元(例如密碼是 a)全部誤殺;空字串不處理。
func (c *Config) Redact(s string) string {
	var secrets []string
	for _, v := range All {
		if val, ok := c.raw[v.Name]; ok && v.Secret {
			secrets = append(secrets, secretValues(v, val)...)
		}
	}
	// 長的先換,免得短的把長的拆碎
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, sec := range secrets {
		switch {
		case sec == "":
		case len(sec) >= 3:
			s = strings.ReplaceAll(s, sec, masked)
		default:
			s = replaceWord(s, sec)
		}
	}
	return s
}

// replaceWord 把 s 裡整字吻合 sec 的地方換成 ***(前後不能是字母、數字、底線)
func replaceWord(s, sec string) string {
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], sec) {
			var prev, next rune = ' ', ' '
			if i > 0 {
				prev = []rune(s[:i])[len([]rune(s[:i]))-1]
			}
			if rest := s[i+len(sec):]; rest != "" {
				next = []rune(rest)[0]
			}
			if !isWord(prev) && !isWord(next) {
				b.WriteString(masked)
				i += len(sec)
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
