package service

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/redact"
	"omixor-core/store"
)

// commits 與 Issue 的連結(ISS-69,設計見 omixor 文件櫃 DOC-27《docs/design-commits.md》;資料模型 model/commit.go、存取 store 的 *Commit 方法)。規則:
//
//	回報(ReportCommits):組織成員(人);角色憑證 403(phase 1 角色不寫 commits)。一次一個 repo、最多 100 筆;sha 要是 40 位十六進位;
//	  subject、body、branch、author 去控制字元並遮蔽機密(core/redact,同 report_problem;獨立的 git 物件 id 不遮);冪等(重送不重複、verified 只升不降)
//	引用解析:從 commit 訊息取出「像 Issue key」的候選(<代號>-<號碼>),只保留「這個組織真的存在、而且呼叫者看得到」的 Issue;
//	  代號不是 ISS 也不是呼叫者看得到的專案代號的候選一律忽略(UTF-8、SHA-256、CVE-2024-1234 這類不會連結,也不會出現在 unmatched);
//	  受限專案裡呼叫者看不到的 Issue:不連結、也不列在 unmatched(兩者的輸出完全一樣,不洩漏存在)。垃圾桶裡的 Issue 當作不存在。
//	  fixes|fix|fixed|closes|close|closed|resolves|resolve|resolved <key> → kind fixes,其餘提到 → mentions。絕不修改或關閉 Issue
//	手動連結(LinkIssueCommit、UnlinkIssueCommit):權限同 issue_update(組織成員,Issue 要看得到,看不到 404);角色憑證 403;
//	  沒回報過的 sha 允許當裸參照(建一列 subject 空字串、source manual 的 commit)
//	查詢(QueryCommits、ListIssueCommits、GetRepoSync):組織成員;commit 本身整個組織都看得到,連結依 Issue 可見性篩選;角色憑證 403
//	repo key:保留 github: / gitlab: 前綴(github:acme/app 與 gitlab:acme/app 是不同的 repo),沒給前綴的是自己的 key;回報照給的存。
//	  查詢與手動連結沒給前綴時用 resolveCommitRepo 解析:有沒前綴的 key 就用它,否則這個組織裡路徑相同的唯一一個(任何平台),有多個 400 請加前綴,都沒有用沒前綴的
//	濫用限制:每張 Issue 連結最多 2000 條、其中裸參照最多 200 條;每個使用者每小時 commits_report 600 筆 / 120 次、連結與取消連結合計 300 次(429)
//	稽核:commits.report、issue.commit.link、issue.commit.unlink(只記 repo 與筆數,不記 commit 內容)

// CommitInput 回報的一筆 commit
type CommitInput struct {
	SHA         string      `json:"sha"`
	Subject     string      `json:"subject"`
	Body        string      `json:"body,omitempty"`
	AuthorName  string      `json:"authorName,omitempty"`
	AuthoredAt  *model.Time `json:"authoredAt,omitempty"`
	CommittedAt *model.Time `json:"committedAt,omitempty"`
	Branch      string      `json:"branch,omitempty"`
	Parents     []string    `json:"parents,omitempty"`
	URL         string      `json:"url,omitempty"`
}

// CommitReport ReportCommits 的輸入(API 請求本體)
type CommitReport struct {
	Repo    string        `json:"repo"`
	Source  string        `json:"source,omitempty"` // cli、ci、app、agent、webhook、api;預設 api
	Commits []CommitInput `json:"commits"`
}

// CommitLinked 這次回報新建或升級的連結
type CommitLinked struct {
	Issue string `json:"issue"` // Issue 顯示 key
	SHA   string `json:"sha"`   // 前 7 碼
	Kind  string `json:"kind"`
}

// CommitReportResult ReportCommits 的結果。Linked = 這次呼叫新建或升級(mentions → fixes)的連結(重送同一批是空的);
// Unmatched = 看起來像 Issue key、代號是 ISS 或呼叫者看得到的專案代號、但組織裡沒有這張 Issue 的字串(可能打錯)
type CommitReportResult struct {
	Reported  int            `json:"reported"`
	Created   int            `json:"created"`
	Linked    []CommitLinked `json:"linked"`
	Unmatched []string       `json:"unmatched"`
}

// CommitLinkInput 手動連結
type CommitLinkInput struct {
	Repo string `json:"repo"`
	SHA  string `json:"sha"`
	Kind string `json:"kind,omitempty"` // 預設 manual
	Note string `json:"note,omitempty"`
}

// CommitUnlinkInput 解除連結
type CommitUnlinkInput struct {
	Repo string `json:"repo"`
	SHA  string `json:"sha"`
}

// CommitQueryInput 查詢條件
type CommitQueryInput struct {
	Repo  string `json:"repo,omitempty"`
	SHA   string `json:"sha,omitempty"`   // 前綴,至少 7 碼
	Issue string `json:"issue,omitempty"` // Issue 編號
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"` // 預設 20、最多 200
}

// CommitIssueRef 一個 commit 連到的 Issue(呼叫者看得到的)
type CommitIssueRef struct {
	Issue string `json:"issue"` // 顯示 key
	Kind  string `json:"kind"`
}

// CommitView 查詢結果的一筆:commit 加上它連到的(呼叫者看得到的)Issue
type CommitView struct {
	model.Commit
	SHA7   string           `json:"sha7"`
	Issues []CommitIssueRef `json:"issues"`
}

// CommitQueryResult QueryCommits 的結果;Total 是符合條件的總數
type CommitQueryResult struct {
	Total   int          `json:"total"`
	Commits []CommitView `json:"commits"`
}

// IssueCommits 一張 Issue 的連結清單(最近連結的在前);Total 是連結總數
type IssueCommits struct {
	Issue   string                  `json:"issue"` // 顯示 key
	Total   int                     `json:"total"`
	Commits []model.IssueCommitView `json:"commits"`
}

// CommitSyncView GetRepoSync 的結果:LastSHA 空字串 = 這個 repo 還沒回報過
type CommitSyncView struct {
	Repo       string      `json:"repo"`
	LastSHA    string      `json:"lastSha"`
	LastAt     *model.Time `json:"lastAt,omitempty"`
	LastSource string      `json:"lastSource,omitempty"`
}

func commitRepoErr() error {
	return fail(400, "repo 只能用英數與 - _ . / :,最長 %d 字(例如 owner/name、gitlab:group/sub/project)", model.RepoMaxLen)
}

// commitText 使用者提供文字的處理順序:去控制字元 → 遮蔽機密(獨立的 git 物件 id 保留)→ 限長(先遮蔽再截斷,機密不會被截成半截留下來)
func commitText(s string, multiline bool, max int) string {
	return redact.Cap(redact.SecretsKeepGitIDs(redact.StripControl(s, multiline)), max)
}

// commitReader 回報、手動連結、查詢共同的前置:成員(外部人 403)、角色憑證 403
func (s *Service) commitCaller(p *model.Principal, org string) error {
	if err := s.viewer(p, org); err != nil {
		return err
	}
	if p.Agent != nil {
		return fail(403, "角色身分不能讀寫 commits(phase 1 只開放給人)")
	}
	return nil
}

// ---- 引用解析 ----

var (
	reKeyCandidate = regexp.MustCompile(`(?i)[a-z][a-z0-9]{0,15}-[0-9]{1,9}`)
	reFixVerb      = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_])(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)[ \t]*:?[ \t]*$`)
)

func isWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

// keyCandidate 從訊息取出的候選
type keyCandidate struct {
	prefix string // 大寫代號
	num    int
	fixes  bool
}

func (k keyCandidate) key() string { return k.prefix + "-" + strconv.Itoa(k.num) }

// extractKeyCandidates 找出訊息裡「像 Issue key」的字串。邊界規則:前一個字元不能是字母、數字、底線、連字號(免得從 xISS-1 或 a-ISS-1 中間切出來);
// 後一個字元不能是字母、數字、底線,也不能是「-數字」(CVE-2024-1234 整串不是 key);「.」後面接數字(子項編號 ISS-37.2)照樣算 ISS-37。
// 同一個 key 重複出現,只要有一次是 fixes 動詞就是 fixes
func extractKeyCandidates(text string) []keyCandidate {
	var out []keyCandidate
	idx := map[string]int{}
	for _, m := range reKeyCandidate.FindAllStringIndex(text, -1) {
		a, b := m[0], m[1]
		if a > 0 && (isWordByte(text[a-1]) || text[a-1] == '-') {
			continue
		}
		if b < len(text) {
			n := text[b]
			if isWordByte(n) {
				continue
			}
			if n == '-' && b+1 < len(text) && text[b+1] >= '0' && text[b+1] <= '9' {
				continue
			}
		}
		raw := text[a:b]
		dash := strings.LastIndexByte(raw, '-')
		num, err := strconv.Atoi(raw[dash+1:])
		if err != nil || num <= 0 {
			continue
		}
		c := keyCandidate{prefix: strings.ToUpper(raw[:dash]), num: num}
		start := max(0, a-24)
		c.fixes = reFixVerb.MatchString(text[start:a])
		if i, ok := idx[c.key()]; ok {
			out[i].fixes = out[i].fixes || c.fixes
			continue
		}
		idx[c.key()] = len(out)
		out = append(out, c)
	}
	return out
}

// keyResolver 一次回報裡把候選 key 解析成「存在且看得到」的 Issue(結果在這次呼叫內快取)
type keyResolver struct {
	s        *Service
	org      string
	acc      *projectAccess
	prefixes map[string]bool // 可比對的代號:ISS 與呼叫者看得到的專案代號
	cache    map[string]*model.Issue
	asked    int
}

// maxKeyLookups 一次回報最多查幾個不同的 key(防止巨大訊息拖慢資料庫)
const maxKeyLookups = 500

func (s *Service) newKeyResolver(p *model.Principal, org string) (*keyResolver, error) {
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	ps, err := s.Store.ListProjects(org)
	if err != nil {
		return nil, err
	}
	r := &keyResolver{s: s, org: org, acc: acc, prefixes: map[string]bool{model.IssueKeyPrefix: true}, cache: map[string]*model.Issue{}}
	for _, x := range ps {
		if x.Key != "" && acc.Sees(x.ID) { // 看不到的專案代號不比對:裡面的 key 一律當作沒提到(也不出現在 unmatched)
			r.prefixes[strings.ToUpper(x.Key)] = true
		}
	}
	return r, nil
}

// resolve 回傳 (Issue, 比對的代號有效)。Issue = nil 且有效 → 沒有這張 Issue(unmatched);無效 → 忽略
func (r *keyResolver) resolve(c keyCandidate) (*model.Issue, bool, error) {
	if !r.prefixes[c.prefix] {
		return nil, false, nil
	}
	k := c.key()
	if i, ok := r.cache[k]; ok {
		return i, true, nil
	}
	if r.asked >= maxKeyLookups {
		return nil, false, nil
	}
	r.asked++
	i, err := r.s.Store.GetIssue(r.org, k)
	if errors.Is(err, store.ErrNotFound) {
		r.cache[k] = nil
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !r.acc.Sees(i.Project) { // 看不到的專案(舊 key 別名指到受限專案):當作沒提到
		r.cache[k] = nil
		return nil, false, nil
	}
	r.cache[k] = i
	return i, true, nil
}

// ---- 回報 ----

// splitCommitMessage subject 含換行時,第一行當 subject、其餘接在 body 前面
func splitCommitMessage(subject, body string) (string, string) {
	subject = strings.TrimSpace(subject)
	if i := strings.IndexByte(subject, '\n'); i >= 0 {
		rest := strings.TrimSpace(subject[i+1:])
		subject = strings.TrimSpace(subject[:i])
		if rest != "" {
			if strings.TrimSpace(body) != "" {
				body = rest + "\n\n" + strings.TrimSpace(body)
			} else {
				body = rest
			}
		}
	}
	return subject, body
}

func commitTime(t *model.Time) *model.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	x := model.Time{Time: t.Time.UTC().Truncate(time.Second)}
	return &x
}

// effectiveTime commit 的排序時間:authored_at,沒有用 committed_at,再沒有用回報時間
func effectiveTime(c model.Commit) time.Time {
	switch {
	case c.AuthoredAt != nil:
		return c.AuthoredAt.Time
	case c.CommittedAt != nil:
		return c.CommittedAt.Time
	}
	return c.ReportedAt.Time
}

// githubURL repo 是儲存用的 key(可帶 github: 前綴);網址只用路徑
func githubURL(repo, sha string) string {
	_, path := model.CommitRepoSplit(repo)
	return "https://github.com/" + path + "/commit/" + sha
}

// commits 錯誤的機器碼(Error.Data["code"]):MCP 等用它分辨情況、自己組訊息,不去比對中文訊息文字
const (
	CommitRepoAmbiguousCode = "commitRepoAmbiguous" // Data.candidates:對到的 repo key 清單
	CommitBareRefLimitCode  = "commitBareRefLimit"  // Data.limit
	CommitLinkLimitCode     = "commitLinkLimit"     // Data.limit
	CommitRateLimitedCode   = "commitRateLimited"   // Data.retryMinutes
)

// commitFail 帶機器碼與資料的服務錯誤(HTTP 的狀態碼與中文訊息不變,回應多帶 code 與資料欄位)
func commitFail(status int, code string, data map[string]any, format string, a ...any) error {
	d := map[string]any{"code": code}
	for k, v := range data {
		d[k] = v
	}
	return &Error{Code: status, Msg: fmt.Sprintf(format, a...), Data: d}
}

// commitRate 扣這個使用者這一小時的配額;超過回 429 並說明多久後可以再試
func (s *Service) commitRate(p *model.Principal, reqs ...quotaReq) error {
	for i := range reqs {
		reqs[i].key += ":" + p.User.ID
	}
	ok, wait := s.commitQuota.take(s.now(), model.CommitRateWindow, reqs...)
	if ok {
		return nil
	}
	mins := int((wait + time.Minute - 1) / time.Minute)
	return commitFail(429, CommitRateLimitedCode, map[string]any{"retryMinutes": mins}, "commits 的使用次數太多(每小時上限),請 %d 分鐘後再試", mins)
}

// resolveCommitRepo 查詢與手動連結用:repo 有平台前綴就是精確的;沒前綴時,先看有沒有沒前綴的 key,
// 再看這個組織裡路徑相同的 repo(任何平台):剛好一個就用它,多個 400 請加前綴,都沒有就用沒前綴的(新的參照)
func (s *Service) resolveCommitRepo(org, in string) (string, error) {
	key, ok := model.CommitRepoKey(in)
	if !ok {
		return "", commitRepoErr()
	}
	if prov, _ := model.CommitRepoSplit(key); prov != "" {
		return key, nil
	}
	found, err := s.Store.FindCommitRepos(org, key)
	if err != nil {
		return "", err
	}
	if len(found) == 0 || model.OneOf(key, found) {
		return key, nil
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", commitFail(400, CommitRepoAmbiguousCode, map[string]any{"candidates": append([]string(nil), found...)}, "repo %s 在這個組織對到多個平台:%s;請加上 github: 或 gitlab: 前綴", key, strings.Join(found, "、"))
}

// ReportCommits 回報一批 commits(一個 repo、一個組織)。規則見檔頭
func (s *Service) ReportCommits(p *model.Principal, org string, in CommitReport) (*CommitReportResult, error) {
	if err := s.commitCaller(p, org); err != nil {
		return nil, err
	}
	repo, ok := model.CommitRepoKey(in.Repo)
	if !ok {
		return nil, commitRepoErr()
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))
	if source == "" {
		source = model.CommitSourceAPI
	}
	if !model.OneOf(source, model.CommitReportSources) {
		return nil, fail(400, "source 必須是 %s 之一", strings.Join(model.CommitReportSources, "、"))
	}
	if len(in.Commits) == 0 {
		return nil, fail(400, "commits 不能是空的")
	}
	if len(in.Commits) > model.CommitMaxBatch {
		return nil, fail(400, "一次最多回報 %d 筆 commits(收到 %d 筆)", model.CommitMaxBatch, len(in.Commits))
	}
	if err := s.commitRate(p, quotaReq{"commits.report.calls", 1, model.CommitReportMaxCalls}, quotaReq{"commits.report.commits", len(in.Commits), model.CommitReportMaxCommits}); err != nil {
		return nil, err
	}
	by := s.whoActor(p)
	now := s.mt()
	githubRepo := strings.HasPrefix(repo, "github:")

	var list []model.Commit
	seen := map[string]bool{}
	for n, x := range in.Commits {
		sha, ok := model.CleanSHA(x.SHA)
		if !ok {
			return nil, fail(400, "commits[%d].sha 要是 40 位十六進位的完整 sha", n)
		}
		if len(x.Parents) > model.CommitMaxParents {
			return nil, fail(400, "commits[%d].parents 最多 %d 個", n, model.CommitMaxParents)
		}
		parents := make([]string, 0, len(x.Parents))
		for _, ps := range x.Parents {
			pc, ok := model.CleanSHA(ps)
			if !ok {
				return nil, fail(400, "commits[%d].parents 每一個都要是 40 位十六進位的完整 sha", n)
			}
			parents = append(parents, pc)
		}
		u := strings.TrimSpace(x.URL)
		if u != "" {
			pu, err := url.Parse(u)
			if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" || len(u) > model.CommitURLMax || strings.ContainsAny(u, " \t\r\n") {
				return nil, fail(400, "commits[%d].url 要是 http(s) 網址(最長 %d 字)", n, model.CommitURLMax)
			}
		} else if githubRepo {
			u = githubURL(repo, sha)
		}
		if seen[sha] { // 同一批重複的 sha:只取第一筆
			continue
		}
		seen[sha] = true
		subject, body := splitCommitMessage(redact.StripControl(x.Subject, true), redact.StripControl(x.Body, true))
		subject = commitText(subject, false, model.CommitSubjectMax)
		if subject == "" {
			subject = "(no message)"
		}
		c := model.Commit{
			Repo: repo, SHA: sha, Subject: subject, Body: commitText(body, true, model.CommitBodyMax),
			AuthorName: commitText(x.AuthorName, false, model.CommitAuthorMax),
			AuthoredAt: commitTime(x.AuthoredAt), CommittedAt: commitTime(x.CommittedAt),
			Branch: commitText(x.Branch, false, model.CommitBranchMax), Parents: parents, URL: u,
			Source: source, ReportedBy: by, ReportedAt: now,
		}
		list = append(list, c)
	}

	// 先解析引用(讀),再寫入:解析失敗時不留下半批資料
	res, err := s.newKeyResolver(p, org)
	if err != nil {
		return nil, err
	}
	type pending struct {
		issue *model.Issue
		sha   string
		kind  string
	}
	var links []pending
	unmatched := map[string]bool{}
	for _, c := range list {
		for _, cand := range extractKeyCandidates(c.Subject + "\n" + c.Body) {
			iss, valid, err := res.resolve(cand)
			if err != nil {
				return nil, err
			}
			switch {
			case !valid:
			case iss == nil:
				unmatched[cand.key()] = true
			default:
				kind := model.CommitLinkMentions
				if cand.fixes {
					kind = model.CommitLinkFixes
				}
				links = append(links, pending{iss, c.SHA, kind})
			}
		}
	}

	created, err := s.Store.UpsertCommits(org, list)
	if err != nil {
		return nil, err
	}
	out := &CommitReportResult{Reported: len(list), Created: created, Linked: []CommitLinked{}, Unmatched: []string{}}
	totals, added := map[string]int{}, map[string]int{} // 每張 Issue 的連結總數上限:到頂的 Issue 不再新增連結(已有的照常升級)
	for _, l := range links {
		if _, ok := totals[l.issue.ID]; !ok {
			st, err := s.Store.IssueCommitStats(org, l.issue.ID, repo, l.sha)
			if err != nil {
				return nil, err
			}
			totals[l.issue.ID] = st.Total
		}
		if totals[l.issue.ID]+added[l.issue.ID] >= model.CommitMaxLinksPerIssue {
			st, err := s.Store.IssueCommitStats(org, l.issue.ID, repo, l.sha)
			if err != nil {
				return nil, err
			}
			if !st.Linked {
				continue
			}
		}
		changed, err := s.Store.LinkIssueCommit(org, model.IssueCommit{Issue: l.issue.ID, Repo: repo, SHA: l.sha, Kind: l.kind,
			Origin: model.CommitOriginAuto, LinkedBy: by, LinkedAt: now})
		if errors.Is(err, store.ErrBadRef) { // Issue 在解析之後被永久刪除:略過這一條
			continue
		}
		if err != nil {
			return nil, err
		}
		if changed {
			added[l.issue.ID]++
			out.Linked = append(out.Linked, CommitLinked{Issue: l.issue.Display(), SHA: model.ShortSHA(l.sha), Kind: l.kind})
		}
	}
	for k := range unmatched {
		out.Unmatched = append(out.Unmatched, k)
	}
	sort.Strings(out.Unmatched)
	if len(out.Unmatched) > 50 {
		out.Unmatched = out.Unmatched[:50]
	}
	if err := s.advanceRepoSync(org, repo, source, list, now); err != nil {
		return nil, err
	}
	s.Audit(p, "commits.report", org, map[string]any{"repo": repo, "source": source, "reported": out.Reported, "created": out.Created, "linked": len(out.Linked)})
	return out, nil
}

// advanceRepoSync 更新 repo_sync:last_sha = 這批裡最新的(authored_at,沒有用 committed_at、回報時間;同時間取 sha 較大的,結果穩定);
// 這批比目前記錄的位置還舊(補歷史)時不動 last_sha,只更新 last_at、last_source
func (s *Service) advanceRepoSync(org, repo, source string, list []model.Commit, now model.Time) error {
	if len(list) == 0 {
		return nil
	}
	newest := list[0]
	for _, c := range list[1:] {
		a, b := effectiveTime(c), effectiveTime(newest)
		if a.After(b) || a.Equal(b) && c.SHA > newest.SHA {
			newest = c
		}
	}
	sync := model.RepoSync{Repo: repo, LastSHA: newest.SHA, LastAt: now, LastSource: source}
	cur, err := s.Store.GetRepoSync(org, repo)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err == nil && cur.LastSHA != "" && cur.LastSHA != newest.SHA {
		if prev, err := s.Store.GetCommit(org, repo, cur.LastSHA); err == nil && effectiveTime(*prev).After(effectiveTime(newest)) {
			sync.LastSHA = cur.LastSHA
		}
	}
	return s.Store.PutRepoSync(org, sync)
}

// ---- 手動連結 ----

// commitIssue 找 Issue 並確認呼叫者看得到(看不到、不存在、在垃圾桶一律同一句 404)。呼叫端先過 commitCaller
func (s *Service) commitIssue(p *model.Principal, org, ref string) (*model.Issue, error) {
	id, err := s.resolveIssueID(org, ref)
	if err != nil {
		return nil, err
	}
	i, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, issueNotFound(ref)
	}
	if err != nil {
		return nil, err
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	if err := a.issueErr(i, ref); err != nil {
		return nil, err
	}
	return i, nil
}

func commitRef(repoIn, shaIn string) (repo, sha string, err error) {
	repo, ok := model.CommitRepoKey(repoIn)
	if !ok {
		return "", "", commitRepoErr()
	}
	sha, ok = model.CleanSHA(shaIn)
	if !ok {
		return "", "", fail(400, "sha 要是 40 位十六進位的完整 sha")
	}
	return repo, sha, nil
}

// 一張 Issue 的連結上限(裸參照另有較小的上限)
func commitLinkCapErr(st store.IssueCommitStats, bare bool) error {
	if st.Total >= model.CommitMaxLinksPerIssue {
		return commitFail(400, CommitLinkLimitCode, map[string]any{"limit": model.CommitMaxLinksPerIssue}, "這張 Issue 的 commit 連結已達上限(%d 條);請先移除不需要的連結", model.CommitMaxLinksPerIssue)
	}
	if bare && st.Bare >= model.CommitMaxBareRefsPerIssue {
		return commitFail(400, CommitBareRefLimitCode, map[string]any{"limit": model.CommitMaxBareRefsPerIssue}, "這張 Issue 上未回報的 commit 參照太多(上限 %d 條);請先用 commits_report 回報這些 commit", model.CommitMaxBareRefsPerIssue)
	}
	return nil
}

// LinkIssueCommit 手動把一個 commit 連到 Issue(權限同 issue_update)。種類預設 manual;sha 沒回報過就建裸參照
func (s *Service) LinkIssueCommit(p *model.Principal, org, issueRef string, in CommitLinkInput) (*model.IssueCommitView, error) {
	if err := s.commitCaller(p, org); err != nil {
		return nil, err
	}
	repo, sha, err := commitRef(in.Repo, in.SHA)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = model.CommitLinkManual
	}
	if !model.OneOf(kind, model.ManualCommitLinkKinds) {
		return nil, fail(400, "kind 必須是 %s 之一", strings.Join(model.ManualCommitLinkKinds, "、"))
	}
	note := commitText(in.Note, false, model.CommitNoteMax)
	if err := s.commitRate(p, quotaReq{"commits.link", 1, model.CommitLinkMaxPerHour}); err != nil {
		return nil, err
	}
	iss, err := s.commitIssue(p, org, issueRef)
	if err != nil {
		return nil, err
	}
	if repo, err = s.resolveCommitRepo(org, in.Repo); err != nil {
		return nil, err
	}
	by := s.whoActor(p)
	now := s.mt()
	existing, err := s.Store.GetCommit(org, repo, sha)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	bare := err != nil || existing.Subject == ""
	st, err := s.Store.IssueCommitStats(org, iss.ID, repo, sha)
	if err != nil {
		return nil, err
	}
	if !st.Linked { // 已有的連結只是改種類或說明,不受上限影響
		if err := commitLinkCapErr(st, bare); err != nil {
			return nil, err
		}
	}
	if existing == nil {
		ref := model.Commit{Repo: repo, SHA: sha, Source: model.CommitSourceManual, ReportedBy: by, ReportedAt: now}
		if strings.HasPrefix(repo, "github:") {
			ref.URL = githubURL(repo, sha)
		}
		if err := s.Store.EnsureCommitRef(org, ref); err != nil {
			return nil, err
		}
	}
	if _, err := s.Store.LinkIssueCommit(org, model.IssueCommit{Issue: iss.ID, Repo: repo, SHA: sha, Kind: kind, Origin: model.CommitOriginManual,
		LinkedBy: by, LinkedAt: now, Note: note}); err != nil {
		return nil, issueStoreErr(err)
	}
	s.Audit(p, "issue.commit.link", org, map[string]any{"issue": iss.Display(), "repo": repo, "sha": model.ShortSHA(sha), "kind": kind})
	views, _, err := s.Store.ListIssueCommits(org, iss.ID, 0)
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		if v.Repo == repo && v.SHA == sha {
			return s.presentIssueCommits(iss, []model.IssueCommitView{v})[0], nil
		}
	}
	return nil, fail(500, "連結已建立但讀不回來")
}

// UnlinkIssueCommit 移除 Issue 與 commit 的連結(權限同 issue_update);沒有這個連結 404
func (s *Service) UnlinkIssueCommit(p *model.Principal, org, issueRef string, in CommitUnlinkInput) error {
	if err := s.commitCaller(p, org); err != nil {
		return err
	}
	if _, ok := model.CommitRepoKey(in.Repo); !ok {
		return commitRepoErr()
	}
	if err := s.commitRate(p, quotaReq{"commits.link", 1, model.CommitLinkMaxPerHour}); err != nil {
		return err
	}
	iss, err := s.commitIssue(p, org, issueRef)
	if err != nil {
		return err
	}
	repo, err := s.resolveCommitRepo(org, in.Repo)
	if err != nil {
		return err
	}
	sha, ok := model.CleanSHA(in.SHA)
	if !ok { // 取消連結也接受 7 碼以上的前綴(issues 只回 sha7):在這張 Issue 的連結裡找唯一符合的
		pre, ok := model.CommitSHAPrefix(in.SHA)
		if !ok {
			return fail(400, "sha 要是完整的 40 位,或至少 %d 位的十六進位前綴", model.CommitShortSHA)
		}
		views, _, err := s.Store.ListIssueCommits(org, iss.ID, 0)
		if err != nil {
			return err
		}
		var found []string
		for _, v := range views {
			if v.Repo == repo && strings.HasPrefix(v.SHA, pre) {
				found = append(found, v.SHA)
			}
		}
		switch len(found) {
		case 0:
			return fail(404, "Issue %s 沒有連結 %s 的 %s", iss.Display(), repo, pre)
		case 1:
			sha = found[0]
		default:
			return fail(400, "前綴 %s 對到 %d 個 commit,請給更長的 sha", pre, len(found))
		}
	}
	if err := s.Store.UnlinkIssueCommit(org, iss.ID, repo, sha); errors.Is(err, store.ErrNotFound) {
		return fail(404, "Issue %s 沒有連結 %s 的 %s", iss.Display(), repo, model.ShortSHA(sha))
	} else if err != nil {
		return err
	}
	s.Audit(p, "issue.commit.unlink", org, map[string]any{"issue": iss.Display(), "repo": repo, "sha": model.ShortSHA(sha)})
	return nil
}

// presentIssueCommits 連結清單的輸出:填 Issue 顯示 key、署名換成相容字串並附 actor 物件
func (s *Service) presentIssueCommits(iss *model.Issue, views []model.IssueCommitView) []*model.IssueCommitView {
	ids := make([]string, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.LinkedBy)
	}
	names, _ := s.LoadActors(ids...)
	out := make([]*model.IssueCommitView, 0, len(views))
	for _, v := range views {
		v := v
		v.Issue = iss.Display()
		v.LinkedByActor = names.View(v.LinkedBy)
		v.LinkedBy = names.Legacy(v.LinkedBy, model.LegacyFree)
		out = append(out, &v)
	}
	return out
}

// ListIssueCommits 一張 Issue 連結的 commits(最近連結的在前,最多 limit 筆;limit <= 0 用 10)。Issue 看不到 404
func (s *Service) ListIssueCommits(p *model.Principal, org, issueRef string, limit int) (*IssueCommits, error) {
	if err := s.commitCaller(p, org); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, model.CommitQueryMaxLimit)
	iss, err := s.commitIssue(p, org, issueRef)
	if err != nil {
		return nil, err
	}
	views, total, err := s.Store.ListIssueCommits(org, iss.ID, limit)
	if err != nil {
		return nil, err
	}
	out := &IssueCommits{Issue: iss.Display(), Total: total, Commits: []model.IssueCommitView{}}
	for _, v := range s.presentIssueCommits(iss, views) {
		out.Commits = append(out.Commits, *v)
	}
	return out, nil
}

// ---- 查詢 ----

// QueryCommits 依 repo、sha 前綴、關鍵字、Issue 查 commits(新的在前)。每筆附上它連到的、呼叫者看得到的 Issue
func (s *Service) QueryCommits(p *model.Principal, org string, in CommitQueryInput) (*CommitQueryResult, error) {
	if err := s.commitCaller(p, org); err != nil {
		return nil, err
	}
	q := store.CommitQuery{Query: commitText(in.Query, false, 200), Limit: in.Limit}
	if strings.TrimSpace(in.Repo) != "" {
		repo, err := s.resolveCommitRepo(org, in.Repo)
		if err != nil {
			return nil, err
		}
		q.Repo = repo
	}
	if strings.TrimSpace(in.SHA) != "" {
		pre, ok := model.CommitSHAPrefix(in.SHA)
		if !ok {
			return nil, fail(400, "sha 要是至少 %d 位的十六進位前綴", model.CommitShortSHA)
		}
		q.SHAPrefix = pre
	}
	if strings.TrimSpace(in.Issue) != "" {
		iss, err := s.commitIssue(p, org, in.Issue)
		if err != nil {
			return nil, err
		}
		q.Issue = iss.ID
	}
	list, total, err := s.Store.QueryCommits(org, q)
	if err != nil {
		return nil, err
	}
	out := &CommitQueryResult{Total: total, Commits: make([]CommitView, 0, len(list))}
	if len(list) == 0 {
		return out, nil
	}
	// 連結:依 repo 分組批次查,再依 Issue 可見性篩選(看不到的 Issue 的連結整條省略)
	byRepo := map[string][]string{}
	for _, c := range list {
		byRepo[c.Repo] = append(byRepo[c.Repo], c.SHA)
	}
	links := map[string]map[string][]model.IssueCommit{}
	for repo, shas := range byRepo {
		if links[repo], err = s.Store.ListCommitLinks(org, repo, shas); err != nil {
			return nil, err
		}
	}
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	issues := map[string]*model.Issue{} // 內部 id → Issue(看不到或不存在是 nil)
	lookup := func(id string) *model.Issue {
		if i, ok := issues[id]; ok {
			return i
		}
		i, err := s.Store.GetIssue(org, id)
		if err != nil || !acc.Sees(i.Project) {
			i = nil
		}
		issues[id] = i
		return i
	}
	actorIDs := make([]string, 0, len(list))
	for _, c := range list {
		actorIDs = append(actorIDs, c.ReportedBy)
	}
	names, _ := s.LoadActors(actorIDs...)
	for _, c := range list {
		v := CommitView{Commit: c, SHA7: model.ShortSHA(c.SHA), Issues: []CommitIssueRef{}}
		for _, l := range links[c.Repo][c.SHA] {
			if i := lookup(l.Issue); i != nil {
				v.Issues = append(v.Issues, CommitIssueRef{Issue: i.Display(), Kind: l.Kind})
			}
		}
		sort.Slice(v.Issues, func(a, b int) bool { return v.Issues[a].Issue < v.Issues[b].Issue })
		v.Org = ""
		v.ReportedByActor = names.View(c.ReportedBy)
		v.ReportedBy = names.Legacy(c.ReportedBy, model.LegacyFree)
		out.Commits = append(out.Commits, v)
	}
	return out, nil
}

// GetRepoSync 這個 repo 最近一次回報的位置(給 CLI / App 做只送新的增量同步);還沒回報過 LastSHA 是空字串
func (s *Service) GetRepoSync(p *model.Principal, org, repoIn string) (*CommitSyncView, error) {
	if err := s.commitCaller(p, org); err != nil {
		return nil, err
	}
	repo, err := s.resolveCommitRepo(org, repoIn)
	if err != nil {
		return nil, err
	}
	cur, err := s.Store.GetRepoSync(org, repo)
	if errors.Is(err, store.ErrNotFound) {
		return &CommitSyncView{Repo: repo}, nil
	}
	if err != nil {
		return nil, err
	}
	at := cur.LastAt
	return &CommitSyncView{Repo: repo, LastSHA: cur.LastSHA, LastAt: &at, LastSource: cur.LastSource}, nil
}
