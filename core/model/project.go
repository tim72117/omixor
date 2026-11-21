package model

import "strings"

// 專案:組織底下的一層,目前只有文件櫃(結構與文件)分專案;Issue、頻道、角色、成員、整合、功能開關都還是屬於組織。
//   - 專案可不填:Project 是空字串 = 「未分專案」,就是原本的組織文件櫃(行為不變)
//   - 權限(第 3 階段):預設(Restricted = false)組織成員看得到專案與裡面的 Issue、文件;Restricted = true 的受限專案只有
//     專案成員(omixor_project_members)、組織管理員與伺服器管理員看得到,其他人看到的和「不存在」完全一樣(404、列表裡沒有)
//   - 每個專案各自一份文件櫃結構;文件仍存在組織底下(DOC-<n> 在組織內唯一),用 CabinetDoc.Project 區分,可以在專案間移動
//   - 角色的 docAccess:沒有前綴的 key 只適用「未分專案」;"<專案 id>:<區塊 id 或頁籤網址>" 適用那個專案(見 DocAccessKey)

// ProjectPrefix 專案 id 的前綴:PRJ-<n>(一個組織內流水號)
const ProjectPrefix = "PRJ-"

// Project 組織底下的專案
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Key 專案代號(例如 APP),專案裡 Issue 的 key 是 <代號>-<n>;空字串 = 還沒設定(不能建 Issue)。規則見 CleanProjectKey
	Key         string `json:"key"`
	Description string `json:"description"`
	// Repos 連到這個專案的程式碼倉庫(依連結順序;沒連 = 空陣列)。只用於預設值與篩選,不是權限來源(見 ProjectRepo)
	Repos []string `json:"repos"`
	// Restricted 受限專案:只有專案成員、組織管理員、伺服器管理員看得到(Issue、文件櫃、專案本身);false = 組織成員都看得到(預設)
	Restricted bool `json:"restricted"`
	// MyRole 呼叫者在這個專案的角色(服務層出口填):admin = 組織管理員或專案管理員、member = 專案成員、空 = 不是成員(非受限專案的一般組織成員)
	MyRole    string `json:"myRole,omitempty"`
	CreatedAt Time   `json:"createdAt"`
	UpdatedAt Time   `json:"updatedAt"`
}

// 專案成員的角色
const (
	ProjectRoleAdmin  = "admin"  // 專案管理員:管理這個專案的成員(加入、移除、改角色);也看得到受限專案
	ProjectRoleMember = "member" // 專案成員:看得到受限專案,能讀寫專案裡的 Issue 與文件
)

// ValidProjectRole 是不是認得的專案角色
func ValidProjectRole(r string) bool { return r == ProjectRoleAdmin || r == ProjectRoleMember }

// ProjectMember 專案成員:一個 actor(使用者或角色)在一個專案的角色。
// 受限專案只有成員(與組織管理員)看得到;非受限專案的成員只是名單(不影響可見性),但仍可記錄專案管理員
type ProjectMember struct {
	Project string `json:"project"`
	// Actor 成員的 actor 物件;Node 是相容的節點寫法(user:<email>、agent:<角色 id>)
	Actor     *ActorView `json:"actor,omitempty"`
	Node      string     `json:"node"`
	Role      string     `json:"role"`
	CreatedAt Time       `json:"createdAt"`
	// ActorID 存放處用的 actor id(不輸出)
	ActorID string `json:"-"`
}

// ProjectRepo 專案與程式碼倉庫的連結(多對多):一個 repo 可以連多個專案,一個專案可以連多個 repo。
//   - 沒連任何專案的 repo = 組織共用
//   - 只用來「預設」與「篩選」:建立 Issue 時沒指定專案、repo 只連到一個專案就預設放進那個專案;Issue 依 repo 篩選時,
//     沒標 repo、但所屬專案連到該 repo 的 Issue 也算。連結不是權限來源,也不會自動搬動既有 Issue
type ProjectRepo struct {
	Project   string `json:"project"`
	Repo      string `json:"repo"` // 連結時寫的樣子(CleanRepo 整理後);比對一律用 RepoKey
	CreatedAt Time   `json:"createdAt"`
}

// ProjectsLinkedToRepo repo 連到哪些專案的 id(依 ps 的順序;比對規則同 RepoMatch)。ps 的 Repos 要有填(service 的 ListProjects 會填)
func ProjectsLinkedToRepo(ps []Project, repo string) []string {
	out := []string{}
	for _, p := range ps {
		for _, r := range p.Repos {
			if RepoMatch(r, repo) {
				out = append(out, p.ID)
				break
			}
		}
	}
	return out
}

// RepoKey repo 比對用的標準寫法:去頭尾空白、轉小寫、去掉 github:、gitlab: 前綴。空字串 = 沒有
func RepoKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, pf := range []string{"github:", "gitlab:"} {
		if r, ok := strings.CutPrefix(s, pf); ok {
			return r
		}
	}
	return s
}

// Number PRJ-3 → 3(格式不對回 0)
func (p Project) Number() int { return ProjectNumber(p.ID) }

// ProjectNumber PRJ-3 → 3(不分大小寫;格式不對回 0)
func ProjectNumber(id string) int {
	id = strings.TrimSpace(id)
	if len(id) > len(ProjectPrefix)+9 { // 數字最多 9 位
		return 0
	}
	return idNumber(id, ProjectPrefix)
}

// IsProjectID 是不是 PRJ-<n> 的格式(不分大小寫)
func IsProjectID(id string) bool { return ProjectNumber(id) > 0 }

// NormalizeProjectID 專案 id 的標準寫法:去掉前後空白;PRJ-<n> 格式的換成大寫(其他原樣回傳,由呼叫端回 404)
func NormalizeProjectID(id string) string {
	id = strings.TrimSpace(id)
	if IsProjectID(id) {
		return strings.ToUpper(id)
	}
	return id
}

// DocAccessKey 文件櫃位置(區塊 id 或頁籤網址)在角色 docAccess 裡的 key:未分專案就是位置本身,否則 "<專案 id>:<位置>"
func DocAccessKey(project, key string) string {
	if project == "" {
		return key
	}
	return project + ":" + key
}

// SplitDocAccessKey 拆開 docAccess 的 key:"PRJ-1:/a" → ("PRJ-1", "/a");沒有專案前綴回 ("", k)。
// 只有冒號前面是 PRJ-<n> 格式才算前綴(專案 id 換成大寫)
func SplitDocAccessKey(k string) (project, key string) {
	if p, rest, ok := strings.Cut(k, ":"); ok && IsProjectID(p) {
		return strings.ToUpper(strings.TrimSpace(p)), strings.TrimSpace(rest)
	}
	return "", k
}

// NormalizeDocAccessKey docAccess 的 key 的標準寫法(專案前綴換成大寫,其他不動)
func NormalizeDocAccessKey(k string) string {
	p, rest := SplitDocAccessKey(k)
	return DocAccessKey(p, rest)
}
