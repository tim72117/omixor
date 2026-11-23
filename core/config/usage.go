package config

import (
	"fmt"
	"math"
	"strings"
)

// meta 一個設定的簡短屬性文字:型別、必填、機密、預設
func (v *Var) meta(prog Program) string {
	parts := []string{v.Kind.String()}
	if v.Kind == Enum {
		parts[0] = "選項 " + strings.Join(v.Choices, "/")
	}
	if prog != "" && v.requiredFor(prog) || prog == "" && len(v.RequiredFor) > 0 {
		parts = append(parts, "必填")
	}
	if v.Secret {
		parts = append(parts, "機密")
	}
	return strings.Join(parts, "、")
}

func (v *Var) defaultText() string {
	switch {
	case v.Default != "":
		return v.Default
	case v.DefaultDoc != "":
		return v.DefaultDoc
	}
	return ""
}

func (v *Var) legacyText() string { return strings.Join(v.Legacy, "、") }

// Usage 給 prog 的 -h 用的「環境變數」說明(由設定定義產生)
func Usage(prog Program) string {
	var b strings.Builder
	b.WriteString("環境變數:\n")
	n := 0
	for _, g := range Groups {
		for _, v := range All {
			if v.Group != g || !v.usedBy(prog) {
				continue
			}
			n++
			fmt.Fprintf(&b, "  %s  (%s)\n      %s", v.Name, v.meta(prog), v.Doc)
			if d := v.defaultText(); d != "" {
				fmt.Fprintf(&b, "。預設:%s", d)
			}
			if l := v.legacyText(); l != "" {
				fmt.Fprintf(&b, "。舊名稱:%s", l)
			}
			b.WriteString("\n")
		}
	}
	if n == 0 {
		return ""
	}
	b.WriteString("  完整說明:docs/configuration.md\n")
	return b.String()
}

// esc 表格裡的字:| 要跳脫
func esc(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// Markdown 產生 docs/configuration.md 的內容(測試會比對檔案,不一樣就失敗)
func Markdown() string {
	var b strings.Builder
	b.WriteString("# 設定(環境變數)\n\n")
	b.WriteString("<!-- 這份文件由 core/config 的設定定義產生,不要手改。重新產生:UPDATE_CONFIG_DOC=1 go test ./config -run TestDocFresh(在 core/ 底下) -->\n\n")
	b.WriteString("omixor 的設定都是環境變數。每個程式啟動時一次讀完並驗證(型別、範圍、必填、互斥),有問題會用中文一次列出;")
	b.WriteString("機密(密碼、token、金鑰、資料庫網址裡的密碼)不會出現在日誌或錯誤訊息裡。\n\n")
	b.WriteString("- 舊名稱(`OPS_*`):新名稱沒設才讀舊名稱,讀到時記一次 log 提醒改名;新名稱優先。舊名稱之後會拿掉。\n")
	b.WriteString("- 布林值:`1`、`true`、`yes`、`on` 為開;`0`、`false`、`no`、`off` 或空白為關;其他值啟動時報錯。\n")
	b.WriteString("- 程式欄:`server` = omixor-server、`local` = omixor-local、`web` = omixor-web(console)、`cli` = omixor、`mcp` = omixor-mcp。\n")
	b.WriteString("- 每個程式的 `-h`(或不給參數時的說明)也列出它用到的變數,內容同這份文件。\n\n")
	for _, g := range Groups {
		var vs []*Var
		for _, v := range All {
			if v.Group == g {
				vs = append(vs, v)
			}
		}
		if len(vs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |\n|---|---|---|---|---|---|---|---|\n", g)
		for _, v := range vs {
			kind := v.Kind.String()
			if v.Kind == Enum {
				kind = "選項:" + strings.Join(v.Choices, "/")
			}
			if v.Range != nil {
				if v.Range[1] == math.MaxInt {
					kind += fmt.Sprintf("(≥%d)", v.Range[0])
				} else {
					kind += fmt.Sprintf("(%d–%d)", v.Range[0], v.Range[1])
				}
			}
			if len(v.Schemes) > 0 {
				kind += "(" + strings.Join(v.Schemes, "/") + ")"
			}
			req, sec := "", ""
			if len(v.RequiredFor) > 0 {
				ps := make([]string, len(v.RequiredFor))
				for i, p := range v.RequiredFor {
					ps[i] = string(p)
				}
				req = strings.Join(ps, "、")
			}
			if v.Secret {
				sec = "是"
			}
			ps := make([]string, len(v.Programs))
			for i, p := range v.Programs {
				ps[i] = string(p)
			}
			def := v.defaultText()
			if def != "" {
				def = "`" + esc(def) + "`"
			}
			if v.Default == "" && v.DefaultDoc != "" {
				def = esc(v.DefaultDoc)
			}
			leg := ""
			if len(v.Legacy) > 0 {
				leg = "`" + strings.Join(v.Legacy, "`、`") + "`"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s | %s |\n", v.Name, leg, esc(kind), def, req, sec, strings.Join(ps, "、"), esc(v.Doc))
		}
		b.WriteString("\n")
	}
	b.WriteString("## 與舊版行為不同的啟動驗證\n\n")
	b.WriteString("設定集中之後,下列情形在程式啟動時就報錯(舊版是靜默忽略,或到用到時才失敗)。升級前先確認部署的環境變數沒有這些值;其他的值(包含 `OMIXOR_AUTH` 的任何值、`OMIXOR_NO_BROWSER` 的任何值)維持舊版行為,不會讓啟動失敗。\n\n")
	b.WriteString("| 設定 | 以前 | 現在 |\n|---|---|---|\n")
	for _, c := range behaviorChanges {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", c[0], esc(c[1]), esc(c[2]))
	}
	b.WriteString("\n")
	b.WriteString("## 測試/開發用變數\n\n這些只給測試與開發流程用,不是正式設定:不進上面的表、不經過 `core/config`,測試程式碼直接讀。\n\n| 名稱 | 說明 |\n|---|---|\n")
	for _, d := range DevVars {
		fmt.Fprintf(&b, "| `%s` | %s |\n", d.Name, esc(d.Doc))
	}
	b.WriteString("\n## 系統變數\n\n")
	b.WriteString("`" + strings.Join(SystemVars, "`、`") + "` 是作業系統的變數,不屬於 omixor 設定,程式直接讀(找 claude 執行檔、取得登入 shell 的 PATH)。\n")
	return b.String()
}

// behaviorChanges 與舊版行為不同的啟動驗證(名稱、以前、現在);產生 docs/configuration.md 的「與舊版行為不同」一節,給 release note 用
var behaviorChanges = [][3]string{
	{"`PORT`(server)", "不檢查,原樣當成監聽位址(0 = 隨機埠、不合法的值到監聽時才失敗)", "要是 1–65535 的整數,否則啟動失敗"},
	{"`OMIXOR_MCP_PORT`(web、mcp)", "不合法的值靜默改用預設 3765", "要是 1–65535 的整數(0 與超出範圍都拒絕),否則啟動失敗"},
	{"`OMIXOR_WEB_PORT`(web)", "不合法的值靜默改用預設 5191", "要是 1–65535 的整數,否則啟動失敗"},
	{"`OMIXOR_TOKEN_DAYS`(server)", "非整數當 0(token 不到期),負數照用", "要是 0–36500 的整數(0 仍是不到期),負數、非整數啟動失敗"},
	{"`OMIXOR_AGENT_CRED_HOURS`(server)", "不是數字就忽略、用預設", "不是數字啟動失敗(0 或負數仍是「用預設」)"},
	{"`OMIXOR_SERVER`(cli、web)、`OMIXOR_PUBLIC_URL`、`EVOLEAP_URL`、`OMIXOR_MCP_URL`、`OMIXOR_LDAP_URL`", "不檢查,沒有 scheme 的值到連線時才失敗(或被當成別的意思)", "一定要是含 scheme 的網址(`http`/`https`;LDAP 是 `ldap`/`ldaps`),否則啟動失敗"},
	{"`OMIXOR_DATABASE_URL`", "不檢查,連線時才由資料庫驅動程式報錯", "網址形式(含 `://`)的 scheme 要是 `postgres`/`postgresql`;key=value 形式要有 `=`;否則啟動失敗。兩種形式都接受"},
	{"LDAP 部分設定(server)", "沒設 `OMIXOR_LDAP_URL` 時,其他 LDAP 設定被靜默忽略、LDAP 不啟用", "設了其他 LDAP 項目卻沒設 `OMIXOR_LDAP_URL`(`OMIXOR_LDAP_BIND_PASSWORD` 除外)啟動失敗,避免漏設"},
	{"`OMIXOR_LDAP_START_TLS`、`OMIXOR_LDAP_INSECURE_SKIP_VERIFY`、`OMIXOR_LDAP_TRUST_MAIL`", "不認得的值當成「關」", "要是 1/true/yes/on 或 0/false/no/off,否則啟動失敗"},
	{"`OMIXOR_WEB_ADDR` 與 `OMIXOR_WEB_PORT`(web)", "兩個都設而連接埠不同時,靜默以 `OMIXOR_WEB_ADDR` 為準", "連接埠不同啟動失敗(只設其中一個,或讓兩者一致)"},
	{"`OMIXOR_NO_TIMERS` 等布林設定", "不認得的值當成「關」(各程式略有不同)", "要是 1/true/yes/on 或 0/false/no/off,否則啟動失敗"},
	{"整數型設定(`PGCTLTIMEOUT` 等)", "不檢查,由使用它的程式或 pg_ctl 處理", "非整數或超出範圍啟動失敗"},
}
