package service_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// commits 的 repo key(保留平台前綴)與濫用限制(ISS-69 審查後續):連結上限、每使用者每小時頻率限制。

func repoReport(t *testing.T, e *rEnv, repo string, c byte, msg string) {
	t.Helper()
	if _, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: repo, Commits: []service.CommitInput{ci(c, msg)}}); err != nil {
		t.Fatal(err)
	}
}

func TestCommitRepoKeepsProviderAndResolves(t *testing.T) {
	e := newREnv(t)
	repoReport(t, e, "GitHub:Acme/App", 'a', "gh one")
	repoReport(t, e, "gitlab:acme/app", 'a', "gl one") // 同 sha、不同平台:兩個 commit
	q, err := e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{SHA: "aaaaaaa"})
	if err != nil || q.Total != 2 {
		t.Fatalf("github 與 gitlab 的同名 repo 不能合併:%+v %v", q, err)
	}
	for _, c := range q.Commits {
		if c.Repo == "github:acme/app" && c.URL != "https://github.com/acme/app/commit/"+sha40('a') {
			t.Fatalf("網址不含前綴:%q", c.URL)
		}
		if c.Repo == "gitlab:acme/app" && c.URL != "" {
			t.Fatalf("gitlab 不推導網址:%q", c.URL)
		}
	}
	// 有前綴 = 精確
	if q, err = e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{Repo: "gitlab:acme/app"}); err != nil || q.Total != 1 || q.Commits[0].Subject != "gl one" {
		t.Fatalf("%+v %v", q, err)
	}
	// 沒前綴、對到兩個平台:400,訊息列出並要求前綴
	_, err = e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{Repo: "acme/app"})
	cWantCode(t, err, 400, "歧義的 repo")
	if !strings.Contains(err.Error(), "github:acme/app") || !strings.Contains(err.Error(), "gitlab:acme/app") {
		t.Fatal(err)
	}
	_, err = e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "acme/app", SHA: sha40('a')})
	cWantCode(t, err, 400, "連結的歧義")
	cWantCode(t, e.svc.UnlinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "acme/app", SHA: sha40('a')}), 400, "取消連結的歧義")
	_, err = e.svc.GetRepoSync(e.boss, "o1", "acme/app")
	cWantCode(t, err, 400, "同步位置的歧義")
	// 之後有沒前綴的 repo:精確的沒前綴 key 優先
	repoReport(t, e, "acme/app", 'b', "plain")
	if q, err = e.svc.QueryCommits(e.boss, "o1", service.CommitQueryInput{Repo: "ACME/app"}); err != nil || q.Total != 1 || q.Commits[0].Repo != "acme/app" {
		t.Fatalf("%+v %v", q, err)
	}
	// 只有一個平台:沒前綴對到它;手動連結用它(不會另建沒前綴的 repo)
	repoReport(t, e, "github:solo/lib", 'c', "solo")
	v, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "solo/lib", SHA: sha40('c')})
	if err != nil || v.Repo != "github:solo/lib" || v.Subject != "solo" {
		t.Fatalf("%+v %v", v, err)
	}
	if sy, err := e.svc.GetRepoSync(e.boss, "o1", "solo/lib"); err != nil || sy.Repo != "github:solo/lib" || sy.LastSHA != sha40('c') {
		t.Fatalf("%+v %v", sy, err)
	}
	// 完全沒有的 repo:沒前綴的新 key;帶 github: 的裸參照推導網址
	v, err = e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "new/repo", SHA: sha40('e')})
	if err != nil || v.Repo != "new/repo" {
		t.Fatalf("%+v %v", v, err)
	}
	v, err = e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "github:Fresh/Repo", SHA: sha40('f')})
	if err != nil || v.Repo != "github:fresh/repo" || v.URL != "https://github.com/fresh/repo/commit/"+sha40('f') {
		t.Fatalf("%+v %v", v, err)
	}
	// 只有前綴、空字串:400
	_, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "github:", Commits: []service.CommitInput{ci('a', "x")}})
	cWantCode(t, err, 400, "只有前綴")
}

func hexSHA(i int) string { return fmt.Sprintf("%040x", i+1) }

func TestCommitBareReferenceCapPerIssue(t *testing.T) {
	e := newREnv(t)
	e.svc.Now = func() time.Time { return time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) } // 固定時鐘:不跨小時,並確認 200 次連結沒碰頻率上限
	for i := 0; i < model.CommitMaxBareRefsPerIssue; i++ {
		if _, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(i)}); err != nil {
			t.Fatalf("第 %d 條裸參照:%v", i, err)
		}
	}
	_, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(5000)})
	cWantCode(t, err, 400, "超過裸參照上限")
	if !strings.Contains(err.Error(), "commits_report") {
		t.Fatal(err)
	}
	// 已有的連結改種類不受上限影響
	if v, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(0), Kind: "fixes"}); err != nil || v.Kind != "fixes" {
		t.Fatalf("%+v %v", v, err)
	}
	// 連到「已回報」的 commit 不算裸參照
	res, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: hexSHA(6000), Subject: "real"}}})
	if err != nil || res.Created != 1 {
		t.Fatal(res, err)
	}
	if _, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(6000)}); err != nil {
		t.Fatal(err)
	}
	// 別張 Issue 不受影響
	if _, err := e.svc.LinkIssueCommit(e.boss, "o1", "APP-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(5000)}); err != nil {
		t.Fatal(err)
	}
	// 回報後(補上內容)裸參照變成一般連結,名額釋出
	if _, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: hexSHA(1), Subject: "now real"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(5000)}); err != nil {
		t.Fatalf("名額釋出後:%v", err)
	}
}

func TestCommitTotalLinkCapPerIssue(t *testing.T) {
	e := newREnv(t)
	clock := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }
	total := model.CommitMaxLinksPerIssue
	for b := 0; b*100 < total; b++ {
		if b%5 == 0 { // 每小時 600 筆:每 5 批換一個小時
			clock = clock.Add(61 * time.Minute)
		}
		var batch []service.CommitInput
		for i := 0; i < 100; i++ {
			batch = append(batch, service.CommitInput{SHA: hexSHA(b*100 + i), Subject: "mentions ISS-1"})
		}
		res, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: batch})
		if err != nil || len(res.Linked) != 100 {
			t.Fatalf("批 %d:%v %+v", b, err, res)
		}
	}
	// 到頂:新 commit 仍會存,但不再連到這張 Issue(其他 Issue 照常)
	clock = clock.Add(61 * time.Minute)
	res, err := e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: hexSHA(9999), Subject: "ISS-1 and APP-1"}}})
	if err != nil || res.Created != 1 || len(res.Linked) != 1 || res.Linked[0].Issue != "APP-1" {
		t.Fatalf("%+v %v", res, err)
	}
	l, err := e.svc.ListIssueCommits(e.boss, "o1", "ISS-1", 1)
	if err != nil || l.Total != total {
		t.Fatalf("%+v %v", l, err)
	}
	// 手動連結新的:400;改已有的種類:可以
	_, err = e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(9999)})
	cWantCode(t, err, 400, "總數上限")
	if _, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: hexSHA(0), Kind: "fixes"}); err != nil {
		t.Fatal(err)
	}
	// 重送已連結的 commit(升成 fixes)在上限時仍可
	res, err = e.svc.ReportCommits(e.boss, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: hexSHA(1), Subject: "fixes ISS-1"}}})
	if err != nil || len(res.Linked) != 1 || res.Linked[0].Kind != "fixes" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCommitReportRateLimits(t *testing.T) {
	e := newREnv(t)
	clock := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }
	rep := func(p *model.Principal, n, base int) error {
		var batch []service.CommitInput
		for i := 0; i < n; i++ {
			batch = append(batch, service.CommitInput{SHA: hexSHA(base + i), Subject: "x"})
		}
		_, err := e.svc.ReportCommits(p, "o1", service.CommitReport{Repo: "a/b", Commits: batch})
		return err
	}
	// 筆數上限:6 批 × 100 = 600 可以,再 1 筆 429
	for b := 0; b < 6; b++ {
		if err := rep(e.boss, 100, b*100); err != nil {
			t.Fatal(b, err)
		}
	}
	err := rep(e.boss, 1, 9000)
	cWantCode(t, err, 429, "筆數上限")
	if !strings.Contains(err.Error(), "60 分鐘") {
		t.Fatalf("要提示等多久:%v", err)
	}
	// 其他使用者不受影響;同一個人換成別的組織或工具也共用額度(以使用者計)
	if err := rep(e.alice, 1, 9001); err != nil {
		t.Fatal(err)
	}
	// 被拒的呼叫不佔次數;視窗過後恢復
	clock = clock.Add(59 * time.Minute)
	cWantCode(t, rep(e.boss, 1, 9002), 429, "還沒過一小時")
	clock = clock.Add(2 * time.Minute)
	if err := rep(e.boss, 1, 9002); err != nil {
		t.Fatal("一小時後要恢復:", err)
	}
	// 次數上限:120 次(每次 1 筆)
	clock = clock.Add(2 * time.Hour)
	for i := 0; i < model.CommitReportMaxCalls; i++ {
		if err := rep(e.carol, 1, 20000+i); err != nil {
			t.Fatalf("第 %d 次:%v", i, err)
		}
	}
	cWantCode(t, rep(e.carol, 1, 29999), 429, "次數上限")
	// 驗證失敗(400)的呼叫不扣額度:bob 先送一個空批與一個壞 repo,仍有完整額度
	_, err = e.svc.ReportCommits(e.bob, "o1", service.CommitReport{Repo: "a b", Commits: []service.CommitInput{ci('a', "x")}})
	cWantCode(t, err, 400, "壞 repo")
	if err := rep(e.bob, 100, 30000); err != nil {
		t.Fatal(err)
	}
}

func TestCommitLinkRateLimitCombined(t *testing.T) {
	e := newREnv(t)
	clock := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }
	repoReport(t, e, "a/b", 'a', "real")
	for i := 0; i < model.CommitLinkMaxPerHour; i++ { // 連結與取消連結合計(取消不存在的連結回 404,但仍算一次)
		var err error
		if i%2 == 0 {
			_, err = e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')})
		} else {
			err = e.svc.UnlinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "a/b", SHA: sha40('b')})
			cWantCode(t, err, 404, "不存在的連結")
			err = nil
		}
		if err != nil {
			t.Fatalf("第 %d 次:%v", i, err)
		}
	}
	_, err := e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')})
	cWantCode(t, err, 429, "link 超過")
	cWantCode(t, e.svc.UnlinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitUnlinkInput{Repo: "a/b", SHA: sha40('a')}), 429, "unlink 超過")
	// 回報有自己的額度;別的使用者不受影響;一小時後恢復
	if _, err := e.svc.ReportCommits(e.alice, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{ci('c', "x")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LinkIssueCommit(e.boss, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	if _, err := e.svc.LinkIssueCommit(e.alice, "o1", "ISS-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')}); err != nil {
		t.Fatal(err)
	}
}

// 不是成員或角色憑證:先被權限擋下,不佔額度,也不會因額度回 429
func TestCommitRateLimitAfterPermission(t *testing.T) {
	e := newREnv(t)
	for i := 0; i < model.CommitReportMaxCalls+5; i++ {
		_, err := e.svc.ReportCommits(e.eve, "o1", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{ci('a', "x")}})
		cWantCode(t, err, 403, "非成員")
	}
}
