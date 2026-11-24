package model

import (
	"regexp"
	"strings"
	"time"
)

// commits(ISS-69,設計見 docs/design-commits.md):repo 的 commit metadata 讀進 omixor,並和 Issue 連結。
// 只存 metadata(sha、訊息、作者、時間、分支),不存 diff 與檔案內容。repo 的識別就是 RepoKey 正規化後的字串(沒有 repos 表)。
// 規則(驗證、遮罩、引用解析、權限)在 service/commits.go,存取在 store 的 *Commit 方法。

// Commit 一筆 commit 的 metadata。(Org, Repo, SHA) 唯一
type Commit struct {
	Org  string `json:"org,omitempty"`
	Repo string `json:"repo"` // CommitRepoKey 正規化後(小寫、保留 github:/gitlab: 前綴;沒給前綴就沒有)
	SHA  string `json:"sha"`  // 40 位小寫十六進位
	// Subject 第一行、Body 其餘;存之前已去控制字元並遮蔽機密(service)。Subject 空 = 只被引用過、還沒有人回報這筆 commit 本身(裸參照)
	Subject     string   `json:"subject"`
	Body        string   `json:"body,omitempty"`
	AuthorName  string   `json:"authorName,omitempty"`
	AuthoredAt  *Time    `json:"authoredAt,omitempty"`
	CommittedAt *Time    `json:"committedAt,omitempty"`
	Branch      string   `json:"branch,omitempty"`  // 回報時所在的分支(可空)
	Parents     []string `json:"parents,omitempty"` // 0..n 個父 commit 的 sha
	URL         string   `json:"url,omitempty"`
	// Source 資料從哪裡來:cli、ci、app、agent、webhook、api(沒給的預設)、manual(手動連結一個沒回報過的 sha 時建的裸參照)
	Source string `json:"source"`
	// Verified 回報來的一律 false;phase 2 的 webhook 簽章驗證或 provider 查證後才會是 true(重送不會把它降回 false)
	Verified   bool   `json:"verified"`
	ReportedBy string `json:"reportedBy,omitempty"` // actor id(輸出時換成相容字串並旁附 reportedByActor)
	ReportedAt Time   `json:"reportedAt"`
	// ReportedByActor 輸出用,不存庫
	ReportedByActor *ActorView `json:"reportedByActor,omitempty"`
}

// Commit 欄位上限
const (
	CommitSubjectMax    = 300
	CommitBodyMax       = 4000
	CommitAuthorMax     = 200
	CommitBranchMax     = 200
	CommitURLMax        = 500
	CommitNoteMax       = 500
	CommitMaxParents    = 16
	CommitMaxBatch      = 100 // 一次回報最多幾筆
	CommitQueryMaxLimit = 200
	// 一張 Issue 的連結上限(防灌爆):全部連結、其中裸參照(commit 還沒有人回報內容)
	CommitMaxLinksPerIssue    = 2000
	CommitMaxBareRefsPerIssue = 200
	// CommitShortSHA 顯示用的 sha 長度
	CommitShortSHA = 7
)

// 每個使用者(acting user id,不分 IP)的頻率限制,記憶體內、每個 service 實例各自計算,固定一小時視窗
const (
	CommitRateWindow       = time.Hour
	CommitReportMaxCommits = 600 // commits_report:每小時最多回報幾筆 commit
	CommitReportMaxCalls   = 120 // commits_report:每小時最多呼叫幾次
	CommitLinkMaxPerHour   = 300 // issue_commit_link 與 issue_commit_unlink 合計,每小時
)

// Commit 來源
const (
	CommitSourceCLI     = "cli"
	CommitSourceCI      = "ci"
	CommitSourceApp     = "app"
	CommitSourceAgent   = "agent"
	CommitSourceWebhook = "webhook"
	CommitSourceAPI     = "api"    // 回報時沒給 source 的預設
	CommitSourceManual  = "manual" // 內部:手動連結沒回報過的 sha 時建的裸參照;不接受回報時指定
)

// CommitReportSources 回報時可以指定的來源
var CommitReportSources = []string{CommitSourceCLI, CommitSourceCI, CommitSourceApp, CommitSourceAgent, CommitSourceWebhook, CommitSourceAPI}

// Issue 與 commit 連結的種類
const (
	CommitLinkMentions = "mentions"
	CommitLinkFixes    = "fixes"
	CommitLinkFoundIn  = "found_in" // 這張 Issue 是在這個 commit(版本)發現的
	CommitLinkManual   = "manual"   // 其他手動連結(例如目標 commit),用 note 說明
)

// CommitLinkKinds 全部連結種類
var CommitLinkKinds = []string{CommitLinkMentions, CommitLinkFixes, CommitLinkFoundIn, CommitLinkManual}

// ManualCommitLinkKinds 手動連結可以指定的種類(自動解析出來的 mentions、fixes 也允許手動指定,表示人工確認)
var ManualCommitLinkKinds = CommitLinkKinds

// 連結的來源
const (
	CommitOriginAuto   = "auto"   // 由 commit 訊息解析
	CommitOriginManual = "manual" // 人或工具建立
)

// IssueCommit 一張 Issue 與一個 commit 的連結。(Org, Issue, Repo, SHA) 唯一
type IssueCommit struct {
	Org      string `json:"org,omitempty"`
	Issue    string `json:"issue"` // Issue 內部 id(輸出時 service 另附顯示 key)
	Repo     string `json:"repo"`
	SHA      string `json:"sha"`
	Kind     string `json:"kind"`
	Origin   string `json:"origin"`
	LinkedBy string `json:"linkedBy,omitempty"` // actor id(auto 時是回報者;輸出時換成相容字串)
	LinkedAt Time   `json:"linkedAt"`
	Note     string `json:"note,omitempty"`
}

// RepoSync 一個 repo 最近一次回報的位置,讓 CLI / App 只送新的
type RepoSync struct {
	Org        string `json:"org,omitempty"`
	Repo       string `json:"repo"`
	LastSHA    string `json:"lastSha"`
	LastAt     Time   `json:"lastAt"`
	LastSource string `json:"lastSource"`
}

// IssueCommitView 一張 Issue 看到的連結:連結加上 commit 摘要。Issue 的 Key 是顯示編號
type IssueCommitView struct {
	Issue      string `json:"issue"`             // Issue 顯示 key
	IssueID    string `json:"issueId,omitempty"` // Issue 內部 id
	Repo       string `json:"repo"`
	SHA        string `json:"sha"`
	SHA7       string `json:"sha7"`
	Subject    string `json:"subject"`
	Kind       string `json:"kind"`
	Origin     string `json:"origin"`
	URL        string `json:"url,omitempty"`
	AuthorName string `json:"authorName,omitempty"`
	AuthoredAt *Time  `json:"authoredAt,omitempty"`
	LinkedBy   string `json:"linkedBy,omitempty"`
	LinkedAt   Time   `json:"linkedAt"`
	Note       string `json:"note,omitempty"`
	Verified   bool   `json:"verified"`
	// LinkedByActor 輸出用,不存庫
	LinkedByActor *ActorView `json:"linkedByActor,omitempty"`
}

var reFullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CleanSHA 完整 sha:去頭尾空白、轉小寫;要剛好 40 位十六進位。不合格 ok = false
func CleanSHA(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	return s, reFullSHA.MatchString(s)
}

// CommitSHAPrefix 查詢用的 sha 前綴:至少 7 位、最多 40 位十六進位(小寫)。不合格 ok = false
func CommitSHAPrefix(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < CommitShortSHA || len(s) > 40 {
		return "", false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", false
		}
	}
	return s, true
}

// ShortSHA 前 7 碼
func ShortSHA(sha string) string {
	if len(sha) > CommitShortSHA {
		return sha[:CommitShortSHA]
	}
	return sha
}

// 平台前綴:commit 的 repo key 保留呼叫者給的 github: / gitlab: 前綴(github:acme/app 與 gitlab:acme/app 是不同的 repo);
// 沒給前綴的是自己的 key(泛用命名空間)
var commitProviders = []string{"github:", "gitlab:"}

// CommitRepoKey commit 的 repo 識別:先用 CleanRepo 檢查字元,轉小寫;有 github: 或 gitlab: 前綴就保留,沒有就維持沒有前綴。
// 空字串、只有前綴或不合格 ok = false。(Issue.repo 的 RepoKey 另有規則,會去掉前綴,不受影響)
func CommitRepoKey(s string) (string, bool) {
	c, ok := CleanRepo(s)
	if !ok {
		return "", false
	}
	k := strings.ToLower(c)
	_, path := CommitRepoSplit(k)
	if path == "" || strings.Trim(path, "/:") == "" {
		return "", false
	}
	return k, true
}

// CommitRepoSplit 把正規化的 repo key 拆成 (平台前綴含冒號 "github:" / "gitlab:" 或空字串, 路徑)
func CommitRepoSplit(key string) (provider, path string) {
	for _, pf := range commitProviders {
		if r, ok := strings.CutPrefix(key, pf); ok {
			return pf, r
		}
	}
	return "", key
}

// CommitRepoCandidates 一個沒有前綴的路徑可能對到的所有儲存 key(沒前綴、github:、gitlab:)
func CommitRepoCandidates(path string) []string {
	out := []string{path}
	for _, pf := range commitProviders {
		out = append(out, pf+path)
	}
	return out
}
