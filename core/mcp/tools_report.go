package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"omixor-core/model"
	"omixor-core/redact"
	"omixor-core/service"
)

// report_problem:AI 用戶端回報 omixor 系統本身的問題。走和 issue_create 相同的 Backend.CreateIssue(成員資格、專案規則一樣),
// 自動附上非機密的診斷資訊;相同 tool + error 的未結案回報只加留言(去重)。

const (
	reportLabel       = "mcp-feedback"
	reportSource      = "mcp report_problem"
	reportTitleMax    = 200
	reportTextMax     = 4000
	reportUAMax       = 200
	reportFPErrChars  = 200
	reportRawMax      = 100000 // 單一欄位原文超過這個長度先截斷再處理(避免巨大輸入)
	reportMasked      = redact.Masked
	reportNotProvided = "_not provided_"
)

// reportSeverities severity 參數的值與對應的 Issue 優先度
var reportSeverities = []string{"blocker", "annoying", "idea"}

var reportPriority = map[string]string{"blocker": "high", "annoying": "normal", "idea": "low"}

// ConnDiag 一條 MCP 連線的非機密診斷事實(端點種類、認證方式、伺服器識別、User-Agent);絕不放 token、憑證、cookie、資料庫網址、請求標頭
type ConnDiag struct {
	Endpoint  string         // unified、admin、agent、worker
	Auth      string         // omx-user-token、oauth-read-only、oauth-read-write、agent-credential
	Server    map[string]any // serverIdentity 的 {name, origin, version, env};沒有就 nil
	UserAgent string         // 已清理、限長
}

// newConnDiag 建立連線診斷。bk 判斷認證方式(唯讀 / 讀寫 OAuth,其餘視為 omx_ 使用者 token);agent 端點的 bk 傳 nil,認證方式是角色憑證
func newConnDiag(endpoint string, bk any, server map[string]any, r *http.Request) *ConnDiag {
	d := &ConnDiag{Endpoint: endpoint, Server: server}
	switch {
	case endpoint == "agent" || endpoint == "worker":
		d.Auth = "agent-credential"
	case isReadOnlyBackend(bk):
		d.Auth = "oauth-read-only"
	case isOAuthWriteBackend(bk):
		d.Auth = "oauth-read-write"
	default:
		d.Auth = "omx-user-token"
	}
	if r != nil {
		d.UserAgent = capText(stripControl(r.UserAgent(), false), reportUAMax)
	}
	return d
}

// connDiagnostics 工具從 Call.Ctx 取診斷:Session 有就用,沒有回 nil
type connDiagnoser interface{ ConnDiagnostics() *ConnDiag }

// ConnDiagnostics 實作 connDiagnoser
func (s *Session) ConnDiagnostics() *ConnDiag { return s.Diag }

var reDigits = regexp.MustCompile(`[0-9]+`)

// maskSecrets 遮蔽看起來像機密的片段(規則在 core/redact,和 commits 回報共用)
func maskSecrets(s string) string { return redact.Secrets(s) }

// stripControl 移除控制字元;multiline = true 時保留換行與 tab(\r 去掉),否則所有空白(含換行)壓成單一空格
func stripControl(s string, multiline bool) string {
	if len(s) > reportRawMax {
		s = s[:reportRawMax]
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
		case unicode.IsControl(r) || r == ' ' || r == ' ':
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

// capText 限制長度(以字元計)
func capText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n]))
	}
	return s
}

// cleanReportText 使用者提供欄位的處理順序:去控制字元 → 遮蔽機密 → 限長(先遮蔽再截斷,機密不會被截成半截留下來)
func cleanReportText(s string, multiline bool, max int) string {
	return capText(maskSecrets(stripControl(s, multiline)), max)
}

// reportFingerprint 去重指紋:sha256(小寫 tool + "|" + 正規化 error 的前 200 字元);tool 與 error 都空 = 沒有指紋(回空字串)。
// 正規化:小寫、空白壓成單一空格、連續數字(id、時間戳)換成 "#"。回傳前 12 個十六進位字元
func reportFingerprint(tool, errText string) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	norm := strings.ToLower(strings.Join(strings.Fields(errText), " "))
	norm = reDigits.ReplaceAllString(norm, "#")
	norm = capText(norm, reportFPErrChars)
	if tool == "" && norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tool + "|" + norm))
	return hex.EncodeToString(sum[:])[:12]
}

// diagLines 診斷資訊(Markdown 清單):只有非機密事實
func diagLines(d *ConnDiag, at time.Time, severity string) []string {
	lines := []string{"- Reported at (UTC): " + at.UTC().Format("2006-01-02T15:04:05Z"), "- Severity: " + severity}
	if d == nil {
		return append(lines, "- Connection details: unavailable")
	}
	if d.Server != nil {
		for _, k := range []string{"name", "origin", "version", "env"} {
			if v, _ := d.Server[k].(string); v != "" {
				lines = append(lines, "- Server "+k+": "+capText(stripControl(v, false), 255))
			}
		}
	} else {
		lines = append(lines, "- Server: unavailable on this endpoint")
	}
	lines = append(lines, "- Endpoint: "+d.Endpoint, "- Auth: "+d.Auth)
	if d.UserAgent != "" {
		lines = append(lines, "- Client user-agent: "+d.UserAgent)
	}
	return lines
}

func orNone(s string) string {
	if s == "" {
		return reportNotProvided
	}
	return s
}

func registerReportProblem(s *Server) {
	s.Register("report_problem", func(c *Call) (any, error) {
		e := env(c)
		e.Lock()
		defer e.Unlock()
		author, err := e.Author("")
		if err != nil {
			return nil, err
		}
		rawTitle, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		title := cleanReportText(rawTitle, false, reportTitleMax)
		if title == "" {
			return nil, ToolError{Message: "title must not be empty", Status: 400}
		}
		severity, err := c.Args.OptionalEnum("severity", reportSeverities)
		if err != nil {
			return nil, err
		}
		if severity == "" {
			severity = "annoying"
		}
		full, err := returnFull(c)
		if err != nil {
			return nil, err
		}
		what, _ := c.Args.RawString("what_happened")
		what = cleanReportText(what, true, reportTextMax)
		expected, _ := c.Args.RawString("expected")
		expected = cleanReportText(expected, true, reportTextMax)
		tool, _ := c.Args.RawString("tool")
		tool = cleanReportText(tool, false, 100)
		errText, _ := c.Args.RawString("error")
		errText = cleanReportText(errText, true, reportTextMax)

		var diag *ConnDiag
		if d, ok := c.Ctx.(connDiagnoser); ok {
			diag = d.ConnDiagnostics()
		}
		now := time.Now().UTC()
		dl := strings.Join(diagLines(diag, now, severity), "\n")
		fp := reportFingerprint(tool, errText)
		fpLabel := ""
		if fp != "" {
			fpLabel = "fp:" + fp
		}
		bk, org := e.Backend(), e.Org()

		// 去重:同一個指紋、未結案、標了 mcp-feedback 的 Issue 只加留言
		if fpLabel != "" {
			found, err := bk.QueryIssues(org, model.IssueQuery{Label: fpLabel, Status: "open"})
			if err != nil {
				return nil, prefixErr("The issue was not saved: ", err)
			}
			for _, it := range found {
				if it.Status == "closed" || !hasLabel(it.Labels, reportLabel) || !hasLabel(it.Labels, fpLabel) {
					continue
				}
				// 嚴重度對應的優先度比現有的高才升級(只升不降),走和 issue_update 同一條 UpdateIssue
				changed := []string{"comment"}
				if np := reportPriority[severity]; PriorityRank(np) < PriorityRank(it.Priority) {
					if _, err := bk.UpdateIssue(org, it.ID, service.IssuePatch{Priority: &np, Author: author}); err != nil {
						return nil, prefixErr("The issue was not saved: ", err)
					}
					changed = []string{"priority", "comment"}
				}
				text := "**Reported again**\n\n" + dl
				if what != "" {
					text += "\n\n**What happened**\n\n" + what
				}
				updated, err := addIssueComment(bk, org, it.ID, text, author)
				if err != nil {
					return nil, prefixErr("The issue was not saved: ", err)
				}
				commentID := ""
				if n := len(updated.Comments); n > 0 {
					commentID = updated.Comments[n-1].ID
				}
				return reportResult(e, updated, changed, commentID, true, full), nil
			}
		}

		var body strings.Builder
		body.WriteString("## What happened\n\n" + orNone(what) + "\n\n")
		body.WriteString("## Expected\n\n" + orNone(expected) + "\n\n")
		body.WriteString("## Tool\n\n")
		if tool != "" {
			body.WriteString("`" + strings.ReplaceAll(tool, "`", "'") + "`\n\n")
		} else {
			body.WriteString(reportNotProvided + "\n\n")
		}
		body.WriteString("## Error\n\n")
		if errText != "" {
			body.WriteString("~~~text\n" + strings.ReplaceAll(errText, "~~~", "~ ~ ~") + "\n~~~\n\n")
		} else {
			body.WriteString(reportNotProvided + "\n\n")
		}
		body.WriteString("## Diagnostics\n\n" + dl + "\n")
		labels := []string{reportLabel}
		if fpLabel != "" {
			labels = append(labels, fpLabel)
		}
		issue, err := bk.CreateIssue(org, service.IssueInput{Title: title, Body: body.String(), Priority: reportPriority[severity],
			Labels: labels, Source: reportSource, Author: author})
		if err != nil {
			return nil, prefixErr("The issue was not saved: ", err)
		}
		return reportResult(e, issue, []string{"created"}, "", false, full), nil
	})
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}

// reportResult 回執:ack = issueAck + deduplicated;full = 完整 Issue + deduplicated
func reportResult(e Env, i *model.Issue, changed []string, commentID string, dedup, full bool) any {
	if !full {
		out := issueAck(*i, changed, commentID)
		out["deduplicated"] = dedup
		return out
	}
	out, _ := issueJSON(e, *i).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	out["deduplicated"] = dedup
	return out
}
