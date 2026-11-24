package service_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// commits 與 Issue 的連結(ISS-69):冪等、100 筆上限、sha 驗證、引用解析(含專案代號、大小寫、不存在的 key、誤判字串)、
// fixes 與 mentions、遮罩、權限、角色拒絕、跨組織隔離、受限專案的連結隱藏。沿用 newREnv(受限專案 SEC、公開專案 APP、組織層級 ISS)。

func sha40(c byte) string { return strings.Repeat(string(c), 40) }

func ci(c byte, msg string) service.CommitInput {
	return service.CommitInput{SHA: sha40(c), Subject: msg}
}

func report(t *testing.T, e *rEnv, p *model.Principal, org string, list ...service.CommitInput) *service.CommitReportResult {
	t.Helper()
	return reportRepo(t, e, "acme/app", p, org, list...)
}

// reportRepo 指定 repo key 的回報(repo 保留平台前綴:github:acme/app 與 acme/app 是不同的 repo)
func reportRepo(t *testing.T, e *rEnv, repo string, p *model.Principal, org string, list ...service.CommitInput) *service.CommitReportResult {
	t.Helper()
	res, err := e.svc.ReportCommits(p, org, service.CommitReport{Repo: repo, Source: "cli", Commits: list})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func cWantCode(t *testing.T, err error, code int, what string) {
	t.Helper()
	if err == nil || service.Code(err) != code {
		t.Fatalf("%s:要 %d,得到 %v", what, code, err)
	}
}

func TestReportCommitsIdempotentAndLinks(t *testing.T) {
	e := newREnv(t)
	res := report(t, e, e.boss, "o1", ci('a', "Fixes ISS-1: crash"), ci('b', "mention APP-1 and iss-1"))
	if res.Reported != 2 || res.Created != 2 || len(res.Unmatched) != 0 {
		t.Fatalf("%+v", res)
	}
	got := map[string]bool{}
	for _, l := range res.Linked {
		got[l.Issue+"/"+l.SHA+"/"+l.Kind] = true
	}
	for _, w := range []string{"ISS-1/aaaaaaa/fixes", "APP-1/bbbbbbb/mentions", "ISS-1/bbbbbbb/mentions"} {
		if !got[w] {
			t.Fatalf("缺 %s:%+v", w, res.Linked)
		}
	}
	// 重送:不新增、不重複連結
	res = report(t, e, e.boss, "o1", ci('a', "Fixes ISS-1: crash"), ci('b', "mention APP-1 and iss-1"))
	if res.Reported != 2 || res.Created != 0 || len(res.Linked) != 0 {
		t.Fatalf("重送要冪等:%+v", res)
	}
	l, err := e.svc.ListIssueCommits(e.boss, "o1", "ISS-1", 10)
	if err != nil || l.Total != 2 || l.Issue != "ISS-1" {
		t.Fatal(l, err)
	}
	// 沒有修改 Issue:狀態還是 open
	iss, err := e.svc.GetIssue(e.boss, "o1", "ISS-1")
	if err != nil || iss.Status != "open" || iss.Version != e.orgIss.Version {
		t.Fatalf("不能修改或關閉 Issue:%+v %v", iss, err)
	}
	// mentions 之後再 fixes → 升級(同一個 sha 再回報一次,新的訊息出現 fixes 動詞);不降級
	res = report(t, e, e.boss, "o1", ci('b', "fixed ISS-1 for real"))
	if res.Created != 0 || len(res.Linked) != 1 || res.Linked[0].Issue != "ISS-1" || res.Linked[0].Kind != "fixes" {
		t.Fatalf("要升級成 fixes:%+v", res)
	}
	res = report(t, e, e.boss, "o1", ci('b', "just mentions ISS-1 again"))
	if len(res.Linked) != 0 {
		t.Fatalf("不降級:%+v", res)
	}
	l, _ = e.svc.ListIssueCommits(e.boss, "o1", "ISS-1", 10)
	for _, c := range l.Commits {
		if c.Kind != "fixes" {
			t.Fatalf("%+v", c)
		}
	}
}

func mtime(s string) model.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return model.Time{Time: t}
}

func TestReportCommitsValidation(t *testing.T) {
	e := newREnv(t)
	rep := func(in service.CommitReport) error {
		_, err := e.svc.ReportCommits(e.boss, "o1", in)
		return err
	}
	ok := []service.CommitInput{ci('a', "x")}
	cWantCode(t, rep(service.CommitReport{Repo: "", Commits: ok}), 400, "repo 空")
	cWantCode(t, rep(service.CommitReport{Repo: "a b", Commits: ok}), 400, "repo 不合法")
	cWantCode(t, rep(service.CommitReport{Repo: "a/b", Source: "evil", Commits: ok}), 400, "source")
	cWantCode(t, rep(service.CommitReport{Repo: "a/b"}), 400, "沒有 commits")
	for _, bad := range []string{"", "abc", strings.Repeat("g", 40), strings.Repeat("a", 39), strings.Repeat("a", 41)} {
		cWantCode(t, rep(service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: bad, Subject: "x"}}}), 400, "sha "+bad)
	}
	cWantCode(t, rep(service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('a'), Subject: "x", Parents: []string{"zz"}}}}), 400, "parents")
	cWantCode(t, rep(service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('a'), Subject: "x", URL: "javascript:alert(1)"}}}), 400, "url")
	// 100 筆可以、101 筆不行
	var l []service.CommitInput
	for i := range 101 {
		l = append(l, service.CommitInput{SHA: fmt.Sprintf("%040x", i+1), Subject: "c"})
	}
	if err := rep(service.CommitReport{Repo: "a/b", Commits: l[:100]}); err != nil {
		t.Fatal(err)
	}
	cWantCode(t, rep(service.CommitReport{Repo: "a/b", Commits: l}), 400, "101 筆")
	// 大寫 sha 與前後空白會整理;repo 小寫、保留平台前綴;查詢沒給前綴時對到唯一的 github:acme/app
	res, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "GitHub:Acme/App", Commits: []service.CommitInput{{SHA: " " + strings.Repeat("A", 40) + " ", Subject: "up"}}})
	if err != nil || res.Created != 1 {
		t.Fatal(res, err)
	}
	q, err := e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{Repo: "acme/app", SHA: "aaaaaaa"})
	if err != nil || q.Total != 1 || q.Commits[0].SHA != sha40('a') || q.Commits[0].Repo != "github:acme/app" || q.Commits[0].Verified || q.Commits[0].URL == "" {
		t.Fatalf("%+v %v", q, err)
	}
	// 同一批重複的 sha 只算一筆
	res, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "dup/r", Commits: []service.CommitInput{ci('d', "one"), ci('d', "two")}})
	if err != nil || res.Reported != 1 || res.Created != 1 {
		t.Fatal(res, err)
	}
	// 多行 subject:第一行是 subject,其餘進 body
	_, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "ml/r", Commits: []service.CommitInput{{SHA: sha40('e'), Subject: "first line\n\nrest of message"}}})
	chkErr(t, err)
	q, _ = e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{Repo: "ml/r"})
	if q.Commits[0].Subject != "first line" || !strings.Contains(q.Commits[0].Body, "rest of message") {
		t.Fatalf("%+v", q.Commits[0])
	}
}

func chkErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReportCommitsKeyExtraction(t *testing.T) {
	e := newREnv(t)
	msgs := map[byte]string{
		'1': "upgrade to UTF-8 and SHA-256, see CVE-2024-1234 and ISO-8601 plus RFC-2119",
		'2': "closes app-1, resolves ISS-1.", // 專案代號與大小寫;結尾句點
		'3': "xISS-1 and a-ISS-1 and ISS-1x and ISS-1-2",
		'4': "Fix ISS-1: and also see ISS-999 (typo?) and ISS-0",
		'5': "not a verb bugfix ISS-1",
		'6': "(ISS-1) [APP-1] ISS-007",
	}
	var list []service.CommitInput
	for c, m := range msgs {
		list = append(list, ci(c, m))
	}
	res := report(t, e, e.boss, "o1", list...)
	byCommit := map[string][]string{}
	for _, l := range res.Linked {
		byCommit[l.SHA] = append(byCommit[l.SHA], l.Issue+":"+l.Kind)
	}
	has := func(c byte, want ...string) {
		t.Helper()
		got := strings.Join(sortedCopy(byCommit[strings.Repeat(string(c), 7)]), ",")
		if got != strings.Join(sortedCopy(want), ",") {
			t.Errorf("commit %c:得到 [%s],要 %v", c, got, want)
		}
	}
	has('1')                                     // 誤判字串一律不連結
	has('2', "APP-1:fixes", "ISS-1:fixes")       // 大小寫不敏感、輸出統一成顯示 key
	has('3')                                     // 邊界:前後有字元的不算
	has('4', "ISS-1:fixes")                      // ISS-999 不存在 → unmatched
	has('5', "ISS-1:mentions")                   // bugfix 不是動詞
	has('6', "ISS-1:mentions", "APP-1:mentions") // ISS-007 → ISS-7(不存在)
	if strings.Join(res.Unmatched, ",") != "ISS-7,ISS-999" {
		t.Fatalf("unmatched 只列「已知代號但不存在」的:%v", res.Unmatched)
	}
}

func sortedCopy(l []string) []string {
	out := append([]string{}, l...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestReportCommitsMasksSecrets(t *testing.T) {
	e := newREnv(t)
	secret := "omx_abcDEF123456"
	res, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{
		SHA: sha40('a'), Subject: "use token omx_abcDEF123456 now\x00\x07", Body: "password=hunter2\nAuthorization: Bearer abc.def\nreverts " + sha40('9') + "\nok",
		Branch: "feat/" + secret, AuthorName: "A\x1bB"}}})
	chkErr(t, err)
	if res.Created != 1 {
		t.Fatal(res)
	}
	q, _ := e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{SHA: "aaaaaaa"})
	c := q.Commits[0]
	all := c.Subject + c.Body + c.Branch + c.AuthorName
	for _, bad := range []string{secret, "hunter2", "Bearer abc.def", "\x00", "\x07", "\x1b"} {
		if strings.Contains(all, bad) {
			t.Fatalf("%q 沒有被遮蔽或清掉:%+v", bad, c)
		}
	}
	if !strings.Contains(c.Subject, "[masked]") || !strings.Contains(c.Body, "password=[masked]") || !strings.Contains(c.Body, sha40('9')) || c.AuthorName != "AB" {
		t.Fatalf("%+v", c)
	}
}

func TestReportCommitsPermissions(t *testing.T) {
	e := newREnv(t)
	in := service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{ci('a', "x")}}
	// 角色憑證(同組織)與外部人、外部角色都是 403
	for name, p := range map[string]*model.Principal{"角色 cto": e.cto, "角色 pm": e.pm, "外部成員": e.eve, "外部角色": e.xa} {
		_, err := e.svc.ReportCommits(p, "o1", in)
		cWantCode(t, err, 403, "回報 "+name)
		_, err = e.svc.QueryCommits(p, "o1", service.CommitQueryInput{})
		cWantCode(t, err, 403, "查詢 "+name)
		_, err = e.svc.GetRepoSync(p, "o1", "a/b")
		cWantCode(t, err, 403, "sync "+name)
		_, err = e.svc.ListIssueCommits(p, "o1", "ISS-1", 10)
		cWantCode(t, err, 403, "列連結 "+name)
		_, err = e.svc.LinkIssueCommit(p, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')})
		cWantCode(t, err, 403, "連結 "+name)
		cWantCode(t, e.svc.UnlinkIssueCommit(p, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "a/b", SHA: sha40('a')}), 403, "取消連結 "+name)
	}
	// 一般成員(不是專案成員)可以回報
	if _, err := e.svc.ReportCommits(e.alice, "o1", in); err != nil {
		t.Fatal(err)
	}
	// 稽核只記筆數、不記內容
	// (角色拒絕時沒有寫入)
	q, _ := e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{})
	if q.Total != 1 {
		t.Fatal("被拒的呼叫不該寫入", q.Total)
	}
}

func TestCommitsCrossOrgIsolation(t *testing.T) {
	e := newREnv(t)
	report(t, e, e.boss, "o1", ci('a', "Fixes ISS-1"))
	// o2 的成員 eve 看不到 o1 的 commits,在 o2 回報同一個 sha 是另一筆
	q, err := e.svc.QueryCommits(e.eve, "o2", service.CommitQueryInput{})
	if err != nil || q.Total != 0 {
		t.Fatal(q, err)
	}
	res, err := e.svc.ReportCommits(e.eve, "o2", service.CommitReport{Repo: "acme/app", Commits: []service.CommitInput{ci('a', "Fixes ISS-1 here")}})
	if err != nil || res.Created != 1 || len(res.Linked) != 0 || strings.Join(res.Unmatched, ",") != "ISS-1" {
		t.Fatalf("o2 沒有 ISS-1:%+v %v", res, err)
	}
	// o1 的 Issue 在 o2 查不到(404)
	_, err = e.svc.ListIssueCommits(e.eve, "o2", "ISS-1", 10)
	cWantCode(t, err, 404, "o2 沒有 ISS-1")
	_, err = e.svc.QueryCommits(e.eve, "o1", service.CommitQueryInput{})
	cWantCode(t, err, 403, "外部組織")
	l, _ := e.svc.ListIssueCommits(e.boss, "o1", "ISS-1", 10)
	if l.Total != 1 {
		t.Fatal(l)
	}
}

func TestRestrictedProjectHidesLinksNotCommits(t *testing.T) {
	e := newREnv(t)
	// alice 不是 SEC 成員:SEC-1 的引用不連結,也不在 unmatched;不存在的 SEC-99 同樣不出現(兩者輸出一樣,不洩漏存在)
	res := report(t, e, e.alice, "o1", ci('a', "Fixes SEC-1 and SEC-99 and APP-1"))
	if len(res.Linked) != 1 || res.Linked[0].Issue != "APP-1" || len(res.Unmatched) != 0 {
		t.Fatalf("%+v", res)
	}
	// 舊 key(搬進受限專案前的 APP-2)也不能被拿來連結
	res = report(t, e, e.alice, "o1", ci('b', "mentions APP-2"))
	if len(res.Linked) != 0 {
		t.Fatalf("搬進受限專案的 Issue 用舊 key 也不能連:%+v", res)
	}
	// bob 是 SEC 成員:連得到
	res = report(t, e, e.bob, "o1", ci('c', "Fixes SEC-1"))
	if len(res.Linked) != 1 || res.Linked[0].Issue != "SEC-1" || res.Linked[0].Kind != "fixes" {
		t.Fatalf("%+v", res)
	}
	// commit 本身 alice 看得到(組織成員),但 commit 上 SEC-1 的連結被隱藏;bob 看得到
	q, err := e.svc.QueryCommits(e.alice, "o1", service.CommitQueryInput{SHA: "ccccccc"})
	if err != nil || q.Total != 1 || len(q.Commits[0].Issues) != 0 {
		t.Fatalf("%+v %v", q, err)
	}
	q, _ = e.svc.QueryCommits(e.bob, "o1", service.CommitQueryInput{SHA: "ccccccc"})
	if len(q.Commits[0].Issues) != 1 || q.Commits[0].Issues[0].Issue != "SEC-1" {
		t.Fatalf("%+v", q)
	}
	// 依受限 Issue 查 / 列連結 / 手動連結:alice 一律 404(和不存在同一句話)
	_, err = e.svc.QueryCommits(e.alice, "o1", service.CommitQueryInput{Issue: "SEC-1"})
	cWantCode(t, err, 404, "查 SEC-1")
	_, err = e.svc.ListIssueCommits(e.alice, "o1", "SEC-1", 10)
	cWantCode(t, err, 404, "列 SEC-1")
	_, err = e.svc.LinkIssueCommit(e.alice, "o1", "SEC-1", service.CommitLinkInput{Repo: "acme/app", SHA: sha40('a')})
	cWantCode(t, err, 404, "連 SEC-1")
	cWantCode(t, e.svc.UnlinkIssueCommit(e.alice, "o1", "SEC-1", service.CommitUnlinkInput{Repo: "acme/app", SHA: sha40('c')}), 404, "取消 SEC-1")
	_, err = e.svc.ListIssueCommits(e.alice, "o1", "SEC-99", 10)
	if service.Code(err) != 404 {
		t.Fatal(err)
	}
	// 組織管理員看得到全部
	l, err := e.svc.ListIssueCommits(e.boss, "o1", "SEC-1", 10)
	if err != nil || l.Total != 1 {
		t.Fatal(l, err)
	}
}

func TestManualLinkUnlinkAndBareReference(t *testing.T) {
	e := newREnv(t)
	// 沒回報過的 sha 當裸參照
	v, err := e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "GitHub:Acme/App", SHA: sha40('f'), Kind: "found_in", Note: "seen in v1 token=abc123"})
	chkErr(t, err)
	if v.Issue != "ISS-1" || v.Kind != "found_in" || v.Origin != "manual" || v.Subject != "" || v.SHA7 != "fffffff" || strings.Contains(v.Note, "abc123") {
		t.Fatalf("%+v", v)
	}
	q, _ := e.svc.QueryCommits(e.alice, "o1", service.CommitQueryInput{SHA: "fffffff"})
	if q.Total != 1 || q.Commits[0].Source != "manual" || q.Commits[0].Subject != "" || len(q.Commits[0].Issues) != 1 {
		t.Fatalf("%+v", q)
	}
	// 之後真的回報:補齊內容(不算新建)、連結維持手動
	res := reportRepo(t, e, "github:acme/app", e.boss, "o1", service.CommitInput{SHA: sha40('f'), Subject: "real subject, mentions ISS-1"})
	if res.Created != 0 || len(res.Linked) != 0 {
		t.Fatalf("%+v", res)
	}
	q, _ = e.svc.QueryCommits(e.alice, "o1", service.CommitQueryInput{SHA: "fffffff"})
	if q.Commits[0].Subject != "real subject, mentions ISS-1" || q.Commits[0].Source != "cli" || q.Commits[0].Issues[0].Kind != "found_in" {
		t.Fatalf("%+v", q.Commits[0])
	}
	// 參數驗證
	_, err = e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: "abc"})
	cWantCode(t, err, 400, "sha")
	_, err = e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a'), Kind: "bogus"})
	cWantCode(t, err, 400, "kind")
	_, err = e.svc.LinkIssueCommit(e.alice, "o1", "ISS-404", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')})
	cWantCode(t, err, 404, "Issue 不存在")
	// 預設種類 manual;再連一次改種類(冪等)
	v, err = e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "acme/app", SHA: sha40('f')})
	chkErr(t, err)
	if v.Kind != "manual" {
		t.Fatal(v)
	}
	// 取消連結:前綴也行;不存在 404
	cWantCode(t, e.svc.UnlinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "acme/app", SHA: "abc"}), 400, "太短的前綴")
	chkErr(t, e.svc.UnlinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "acme/app", SHA: "fffffff"}))
	cWantCode(t, e.svc.UnlinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "acme/app", SHA: sha40('f')}), 404, "已取消")
	l, _ := e.svc.ListIssueCommits(e.alice, "o1", "ISS-1", 10)
	if l.Total != 0 {
		t.Fatal(l)
	}
	// commit 還在
	if q, _ := e.svc.QueryCommits(e.alice, "o1", service.CommitQueryInput{SHA: "fffffff"}); q.Total != 1 {
		t.Fatal("取消連結不刪 commit")
	}
}

func TestRepoSyncAdvancesToNewest(t *testing.T) {
	e := newREnv(t)
	s, err := e.svc.GetRepoSync(e.boss, "o1", "a/b")
	if err != nil || s.LastSHA != "" || s.Repo != "a/b" {
		t.Fatal(s, err)
	}
	at := func(ts string) *model.Time { x := mtime(ts); return &x }
	_, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Source: "ci", Commits: []service.CommitInput{
		{SHA: sha40('a'), Subject: "old", AuthoredAt: at("2026-01-01T00:00:00Z")},
		{SHA: sha40('b'), Subject: "new", AuthoredAt: at("2026-01-03T00:00:00Z")},
		{SHA: sha40('c'), Subject: "mid", AuthoredAt: at("2026-01-02T00:00:00Z")},
	}})
	chkErr(t, err)
	s, _ = e.svc.GetRepoSync(e.boss, "o1", "a/b")
	if s.LastSHA != sha40('b') || s.LastSource != "ci" || s.LastAt == nil {
		t.Fatalf("%+v", s)
	}
	// 補更舊的歷史:位置不倒退
	_, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Source: "cli", Commits: []service.CommitInput{
		{SHA: sha40('d'), Subject: "older", AuthoredAt: at("2025-12-01T00:00:00Z")}}})
	chkErr(t, err)
	s, _ = e.svc.GetRepoSync(e.boss, "o1", "a/b")
	if s.LastSHA != sha40('b') || s.LastSource != "cli" {
		t.Fatalf("%+v", s)
	}
	// 往前進
	_, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{
		{SHA: sha40('e'), Subject: "newest", AuthoredAt: at("2026-02-01T00:00:00Z")}}})
	chkErr(t, err)
	s, _ = e.svc.GetRepoSync(e.boss, "o1", "A/B")
	if s.LastSHA != sha40('e') {
		t.Fatalf("%+v", s)
	}
}

func TestQueryCommitsFiltersAndLimit(t *testing.T) {
	e := newREnv(t)
	report(t, e, e.boss, "o1", ci('a', "Add login page ISS-1"), ci('b', "Fix logout bug"))
	chkQ := func(in service.CommitQueryInput, want int) *service.CommitQueryResult {
		t.Helper()
		q, err := e.svc.QueryCommits(e.boss, "o1", in)
		chkErr(t, err)
		if q.Total != want {
			t.Fatalf("%+v:total %d,要 %d", in, q.Total, want)
		}
		return q
	}
	chkQ(service.CommitQueryInput{}, 2)
	chkQ(service.CommitQueryInput{Query: "LOGIN"}, 1)
	chkQ(service.CommitQueryInput{Query: "log bug"}, 1)
	chkQ(service.CommitQueryInput{Issue: "iss-1"}, 1)
	q := chkQ(service.CommitQueryInput{Limit: 1}, 2)
	if len(q.Commits) != 1 {
		t.Fatal(len(q.Commits))
	}
	_, err := e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{SHA: "abc"})
	cWantCode(t, err, 400, "sha 前綴太短")
	_, err = e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{Repo: "bad repo"})
	cWantCode(t, err, 400, "repo")
}
