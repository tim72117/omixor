package service_test

import (
	"encoding/json"
	"slices"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 子項完成證據的查核(ISS-44)。sha40、ci、reportRepo 見 commits_test.go

// doneItem 新增一個子項並回報完成
func doneItem(t *testing.T, e *rEnv, issue, evidence string) *model.IssueItem {
	t.Helper()
	it := addItem(t, e, e.boss, issue, service.ItemInput{Title: "做事"})
	out, err := e.svc.ReportItem(e.boss, "o1", issue, it.Key, service.ItemReport{Status: "done", Evidence: json.RawMessage(evidence)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantCheck(t *testing.T, it *model.IssueItem, commit string, flagged bool) {
	t.Helper()
	c := it.EvidenceCheck
	if it.Status != model.ItemDone || c == nil || c.Commit != commit || c.NeedsVerification != flagged {
		t.Fatalf("%s 證據 %s:要 commit=%q needsVerification=%v,實際 %+v(狀態 %s)", it.Key, it.Evidence, commit, flagged, c, it.Status)
	}
}

func setIssueRepo(t *testing.T, e *rEnv, issue, repo string) {
	t.Helper()
	if _, err := e.svc.UpdateIssue(e.boss, "o1", issue, service.IssuePatch{Repo: &repo}); err != nil {
		t.Fatal(err)
	}
}

func TestIssueItemEvidenceCheck(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	setIssueRepo(t, e, is, "github:acme/app")
	full := `{"commit":"` + sha40('a') + `","tests":"ok"}`

	early := doneItem(t, e, is, full)
	wantCheck(t, early, model.EvidenceUnverifiable, true)

	// 別的 repo(含同路徑的另一個平台)回報過不算
	reportRepo(t, e, "gitlab:acme/app", e.boss, "o1", ci('a', "x"))
	reportRepo(t, e, "acme/other", e.boss, "o1", ci('a', "x"))
	wantCheck(t, doneItem(t, e, is, full), model.EvidenceUnverifiable, true)

	// 沒前綴的 key 算同一個 repo
	reportRepo(t, e, "acme/app", e.boss, "o1", ci('b', "x"))
	wantCheck(t, doneItem(t, e, is, full), model.EvidenceNotFound, true)
	wantCheck(t, doneItem(t, e, is, `{"commit":"BBBBBBB"}`), model.EvidenceConfirmed, false)
	reportRepo(t, e, "github:acme/app", e.boss, "o1", ci('a', "x"))
	// 讀取時重算:先前回報的子項現在是 confirmed
	got, err := e.svc.GetItem(e.alice, "o1", is, early.Key)
	if err != nil {
		t.Fatal(err)
	}
	wantCheck(t, got, model.EvidenceConfirmed, false)
	if !slices.Equal(got.EvidenceCheck.SelfReported, []string{"tests"}) {
		t.Fatalf("自述欄位:%+v", got.EvidenceCheck)
	}

	nf := doneItem(t, e, is, `{"commit":"ccccccc","unverified":["沒跑 Swift"]}`)
	wantCheck(t, nf, model.EvidenceNotFound, false)
	if !slices.Equal(nf.EvidenceCheck.SelfReported, []string{"commit", "unverified"}) {
		t.Fatalf("沒確認的 commit 算自述:%+v", nf.EvidenceCheck)
	}
	wantCheck(t, doneItem(t, e, is, `{"commit":"aaaaaa"}`), model.EvidenceInvalidSHA, true)
	wantCheck(t, doneItem(t, e, is, `{"tests":"ok","unverified":[]}`), "", true)
	wantCheck(t, doneItem(t, e, is, ``), "", true)
	wantCheck(t, doneItem(t, e, is, `{"unverified":"沒測"}`), "", false)

	// 列表整批查,結果和單筆一致;沒完成、沒證據的沒有 evidenceCheck
	open := addItem(t, e, e.boss, is, service.ItemInput{Title: "還沒做"})
	if open.EvidenceCheck != nil {
		t.Fatalf("%+v", open.EvidenceCheck)
	}
	l, err := e.svc.ListItems(e.boss, "o1", is, service.ItemListQuery{IncludeDone: true})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, it := range l {
		if it.EvidenceCheck != nil {
			counts[it.EvidenceCheck.Commit]++
		}
	}
	if counts[model.EvidenceConfirmed] != 4 || counts[model.EvidenceNotFound] != 1 || counts[model.EvidenceInvalidSHA] != 1 || counts[""] != 3 {
		t.Fatalf("%v", counts)
	}
}

func TestIssueItemEvidenceCheckScopeAndVisibility(t *testing.T) {
	e := newREnv(t)
	// 裸參照(手動連結沒回報過的 sha)不算回報過
	if _, err := e.svc.LinkIssueCommit(e.boss, "o1", e.orgIss.ID, service.CommitLinkInput{Repo: "acme/app", SHA: sha40('d')}); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, doneItem(t, e, e.orgIss.ID, `{"commit":"ddddddd"}`), model.EvidenceUnverifiable, true)
	// Issue 沒標 repo:組織裡任何 repo 都算
	reportRepo(t, e, "acme/other", e.boss, "o1", ci('a', "x"))
	wantCheck(t, doneItem(t, e, e.orgIss.ID, `{"commit":"aaaaaaa"}`), model.EvidenceConfirmed, false)

	// 受限專案:非成員連子項都看不到
	sec := doneItem(t, e, e.secIssue.ID, `{"commit":"aaaaaaa"}`)
	_, err := e.svc.GetItem(e.alice, "o1", e.secIssue.ID, sec.Key)
	wantCode(t, "非成員看不到", err, 404)

	// 角色憑證不能讀 commits:別人的子項,存在與否的結果一樣
	for _, sha := range []string{"aaaaaaa", "eeeeeee"} {
		it := doneItem(t, e, e.appIssue.ID, `{"commit":"`+sha+`"}`)
		got, err := e.svc.GetItem(e.cto, "o1", e.appIssue.ID, it.Key)
		if err != nil {
			t.Fatal(err)
		}
		wantCheck(t, got, model.EvidenceNotVisible, false)
	}
}

// 角色只查得到自己任務綁定的子項:回報的回應帶真正的結果與 needsVerification
func TestIssueItemEvidenceCheckOwnAgentItem(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	reportRepo(t, e, "acme/app", e.boss, "o1", ci('a', "x"))
	for _, c := range []struct {
		sha, commit string
		flagged     bool
	}{{"aaaaaaa", model.EvidenceConfirmed, false}, {"eeeeeee", model.EvidenceNotFound, true}} {
		other := doneItem(t, e, is, `{"commit":"`+c.sha+`"}`)
		it := addItem(t, e, e.boss, is, service.ItemInput{Title: "角色做 " + c.sha, Assignee: "agent:cto"})
		res, err := e.svc.ClaimItem(e.boss, "o1", is, it.Key, service.ItemClaimInput{})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := e.svc.AuthenticateAgent("cto", res.Credential.Secret, "ip")
		if err != nil {
			t.Fatal(err)
		}
		out, err := e.svc.ReportItem(agent, "o1", is, it.Key, service.ItemReport{Status: "done", Evidence: json.RawMessage(`{"commit":"` + c.sha + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		wantCheck(t, out, c.commit, c.flagged)
		// 同一批列表:自己的有結果,別人的(同一個 sha)仍是 not_visible
		list, err := e.svc.ListItems(agent, "o1", is, service.ItemListQuery{IncludeDone: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range list {
			switch x.ID {
			case it.ID:
				wantCheck(t, &x, c.commit, c.flagged)
			case other.ID:
				wantCheck(t, &x, model.EvidenceNotVisible, false)
			}
		}
	}
}
