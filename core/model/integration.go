package model

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// 組織的外部整合設定(GitHub、GitLab):一個組織各一份,屬於組織的 core 資料(成員讀、組織管理員改,規則在 service/integration.go)。
//
// 只存「非機密」的設定:追蹤哪些 repo/專案、GitLab 主機網址。
// token、OAuth client secret、個人存取權杖一律留在各裝置(App 的鑰匙圈、web 的加密 vault),核心不收也不存。
// 也留在各裝置的(和裝置有關、或屬於安全性設定):用哪一個本機連線(connection id)、GitLab 的 CA 憑證檔路徑、
// 不驗證憑證(insecure)、OAuth 的 client id 與端點。
// 讀 PR、Actions(GitLab 的 MR、Pipeline)仍由 App 與 web 用各自裝置上的 token 直接打 GitHub/GitLab,不經過核心。

// MaxIntegrationItems 一份設定最多追蹤幾個 repo / 專案
const MaxIntegrationItems = 200

// GitHubSettings 組織的 GitHub 設定
type GitHubSettings struct {
	// Repos 追蹤的 repo(owner/name,依加入順序;不分大小寫不重複)
	Repos []string `json:"repos"`
	// Saved 核心存過這份設定(false = 還沒設定過,回傳的是空白預設;裝置第一次連上時看這個決定要不要匯入本機舊設定)
	Saved     bool   `json:"saved"`
	UpdatedAt *Time  `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"` // email(或名稱)
}

// Clone 深拷貝
func (s GitHubSettings) Clone() GitHubSettings {
	s.Repos = append([]string{}, s.Repos...)
	if s.UpdatedAt != nil {
		t := *s.UpdatedAt
		s.UpdatedAt = &t
	}
	return s
}

// GitLabSettings 組織的 GitLab 設定(公司自架)
type GitLabSettings struct {
	// BaseURL GitLab 主機網址(https://gitlab.example.com,不含 /api/v4;"" = 還沒設定)
	BaseURL string `json:"baseUrl"`
	// Projects 追蹤的專案(group/subgroup/project 或數字 id,依加入順序;不分大小寫不重複)
	Projects  []string `json:"projects"`
	Saved     bool     `json:"saved"`
	UpdatedAt *Time    `json:"updatedAt,omitempty"`
	UpdatedBy string   `json:"updatedBy,omitempty"`
}

// Clone 深拷貝
func (s GitLabSettings) Clone() GitLabSettings {
	s.Projects = append([]string{}, s.Projects...)
	if s.UpdatedAt != nil {
		t := *s.UpdatedAt
		s.UpdatedAt = &t
	}
	return s
}

// OrgIntegrations 一個組織的整合設定(記憶體與檔案型 store 用;沒存過的是 nil)
type OrgIntegrations struct {
	GitHub *GitHubSettings `json:"github,omitempty"`
	GitLab *GitLabSettings `json:"gitlab,omitempty"`
}

// NormalizeGitHubRepo owner/name;也接受 https://github.com/owner/name(.git)、git@github.com:owner/name.git
// (同 App 的 GitHub.normalizeRepo、web 的 GHNormalizeRepo)
func NormalizeGitHubRepo(s string) (string, bool) {
	t := strings.TrimSpace(s)
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/", "git@github.com:"} {
		if strings.HasPrefix(t, p) {
			t = t[len(p):]
		}
	}
	t = strings.TrimSuffix(t, ".git")
	t = strings.TrimSuffix(t, "/")
	parts := []string{}
	for _, p := range strings.Split(t, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) != 2 {
		return "", false
	}
	for _, p := range parts {
		for _, r := range p {
			if !(unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune("-_.", r)) {
				return "", false
			}
		}
	}
	return strings.Join(parts, "/"), true
}

// NormalizeGitLabBaseURL https://gitlab.example.com(去掉結尾的 / 與 /api/v4);"" 視為不對
func NormalizeGitLabBaseURL(s string) (string, bool) {
	t := strings.TrimSpace(s)
	t = strings.TrimSuffix(t, "/")
	t = strings.TrimSuffix(t, "/api/v4")
	t = strings.TrimSuffix(t, "/")
	u, err := url.Parse(t)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	return t, true
}

var glPathSeg = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

// NormalizeGitLabProject 專案:數字 id、group/subgroup/project,或專案網址(https://gitlab…/group/project、git@…:group/project.git)
// (同 web 的 GLNormalizeProject)
func NormalizeGitLabProject(s, baseURL string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", false
	}
	if n, err := strconv.Atoi(t); err == nil {
		if n <= 0 {
			return "", false
		}
		return strconv.Itoa(n), true
	}
	if baseURL != "" && strings.HasPrefix(strings.ToLower(t), strings.ToLower(baseURL)+"/") {
		t = t[len(baseURL)+1:]
	} else if u, err := url.Parse(t); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		t = strings.TrimPrefix(u.Path, "/")
	} else if strings.HasPrefix(t, "git@") {
		if _, rest, ok := strings.Cut(t, ":"); ok {
			t = rest
		}
	}
	if i := strings.Index(t, "/-/"); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSuffix(strings.Trim(t, "/"), ".git")
	parts := strings.Split(t, "/")
	if len(parts) < 2 {
		return "", false
	}
	for _, p := range parts {
		if !glPathSeg.MatchString(p) {
			return "", false
		}
	}
	return t, true
}
