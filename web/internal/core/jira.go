package core

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Jira:把 Jira 專案的 Issue(含留言)匯入目前組織的 Issue(只讀 Jira,不會改 Jira 上的東西)。
//
// 連線:目前組織的連線 id "jira"。
//   - Jira Cloud(*.atlassian.net):email + API token,認證方式 basic(鑰匙圈存「email:token」)
//   - Jira Server / Data Center:個人存取權杖,認證方式 bearer
//
// 搜尋:先用 /rest/api/2/search/jql(Cloud 新的端點,nextPageToken 分頁),
// 沒有這個端點(404/405/410)時改用 /rest/api/2/search(Server/DC,startAt 分頁)。
//
// 再匯入一次會更新同一筆(用 Jira 的網址比對),不會重複;Jira 來的留言 id 是 "jira-<留言 id>",本機加的留言保留。

const JiraConnID = "jira"

const jiraFields = "summary,description,status,priority,labels,created,updated,resolutiondate,reporter,creator,comment,issuetype,attachment"

// JiraIsCloud 網址是 Jira Cloud(要用 email + API token)
func JiraIsCloud(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return strings.HasSuffix(h, ".atlassian.net") || strings.HasSuffix(h, ".jira.com")
}

// JiraNormalizeBaseURL 只留 scheme://host[/context](去掉 /browse/…、/jira/… 這類畫面網址與結尾的 /)
func JiraNormalizeBaseURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", UserError{"要給 Jira 網址"}
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", UserError{"Jira 網址格式錯誤,例如 https://your-team.atlassian.net"}
	}
	p := u.Path
	for _, cut := range []string{"/browse/", "/jira/", "/projects/", "/secure/", "/issues/"} {
		if i := strings.Index(p, cut); i >= 0 {
			p = p[:i]
		}
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(p, "/"), nil
}

func jiraConn(a *App) (Connection, bool) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return ConnFind(a, JiraConnID)
}

// JiraSetup 設定 Jira 網址與登入。email 有給用 basic(Cloud),沒給用 bearer(Server/DC 的個人存取權杖)。
// token 空白時只改網址等設定,沿用已存的登入(網址或認證方式變了會清掉)。
func JiraSetup(a *App, base, email, token string) (Connection, error) {
	b, err := JiraNormalizeBaseURL(base)
	if err != nil {
		return Connection{}, err
	}
	email, token = strings.TrimSpace(email), strings.TrimSpace(token)
	auth := AuthBearer
	if email != "" {
		auth = AuthBasic
	} else if JiraIsCloud(b) && token != "" {
		return Connection{}, UserError{"Jira Cloud 要填登入用的 email(搭配 API token)"}
	}
	c, ok := jiraConn(a)
	if !ok {
		c = Connection{ID: JiraConnID, Name: "Jira", Note: "Issue › 從 Jira 匯入(只讀)", Approval: DefaultApprovalSettings(),
			OAuth: DefaultOAuthSettings()}
	}
	if token == "" && (!ok || c.BaseURL != b || c.Auth != auth) {
		return Connection{}, UserError{"要給 API token(或個人存取權杖)"}
	}
	c.BaseURL, c.Auth, c.TestPath = b, auth, "/rest/api/2/myself"
	ConnUpsert(a, c)
	if token != "" {
		secret := token
		if auth == AuthBasic {
			secret = email + ":" + token
		}
		ConnSetBearer(a, JiraConnID, secret)
	}
	ConnRefreshAccount(a, JiraConnID)
	a.Changed("jira")
	return c, nil
}

// JiraProject 可以匯入的專案
type JiraProject struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// JiraProjects 登入者看得到的專案
func JiraProjects(a *App) ([]JiraProject, error) {
	if _, ok := jiraConn(a); !ok {
		return nil, UserError{"還沒設定 Jira"}
	}
	v, err := ConnRequest(a, JiraConnID, "GET", "/rest/api/2/project", nil)
	if err != nil {
		return nil, err
	}
	list, _ := v.([]any)
	out := []JiraProject{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		k, _ := m["key"].(string)
		n, _ := m["name"].(string)
		if k != "" {
			out = append(out, JiraProject{Key: k, Name: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// JiraImportResult 匯入結果
type JiraImportResult struct {
	Created  int               `json:"created"`
	Updated  int               `json:"updated"`
	Comments int               `json:"comments"`
	Images   int               `json:"images"`
	Projects map[string]int    `json:"projects"`
	Errors   map[string]string `json:"errors"`
}

// jiraIssue 從 Jira 讀到的一筆(還沒對應到本機)
type jiraIssue struct {
	key    string
	fields map[string]any
	notes  []IssueComment
	atts   []jiraAtt
}

// jiraAtt Jira 的附件:圖片下載存成本機附件(url 是 /api/attachments/…),其他檔案(或下載失敗)連回 Jira
type jiraAtt struct {
	name  string
	url   string
	image bool
}

// JiraImport 匯入這些專案的 Issue;includeClosed 為 false 時只匯入還沒完成的(statusCategory != Done)
func JiraImport(a *App, projects []string, includeClosed bool) (JiraImportResult, error) {
	res := JiraImportResult{Projects: map[string]int{}, Errors: map[string]string{}}
	c, ok := jiraConn(a)
	if !ok {
		return res, UserError{"還沒設定 Jira"}
	}
	keys := []string{}
	for _, p := range projects {
		if p = strings.TrimSpace(p); p != "" {
			keys = append(keys, p)
		}
	}
	if len(keys) == 0 {
		return res, UserError{"要選至少一個專案"}
	}
	// 附件存到開始匯入時的組織
	a.Mu.Lock()
	orgDir := a.DataDir()
	a.Mu.Unlock()
	// 網路請求在鎖外做,全部讀完再一次寫進 Issue
	var all []jiraIssue
	for _, k := range keys {
		items, err := jiraFetchProject(a, k, includeClosed, c.TrimmedBase(), orgDir)
		if err != nil {
			res.Errors[k] = err.Error()
			continue
		}
		res.Projects[k] = len(items)
		all = append(all, items...)
	}
	if len(all) == 0 {
		if len(res.Errors) > 0 {
			for k, e := range res.Errors {
				return res, UserError{k + ":" + e}
			}
		}
		return res, nil
	}

	a.Mu.Lock()
	s := a.issueSt()
	byLink := map[string]int{}
	num := 0
	for i, is := range s.Issues {
		if is.Link != "" {
			byLink[is.Link] = i
		}
		num = max(num, is.Number())
	}
	var created []OpsIssue
	for _, ji := range all {
		n := jiraToIssue(c.TrimmedBase(), ji)
		res.Comments += len(ji.notes)
		for _, at := range ji.atts {
			if at.image {
				res.Images++
			}
		}
		if idx, ok := byLink[n.Link]; ok {
			old := s.Issues[idx]
			n.ID = old.ID
			// 本機加的留言保留,Jira 的留言換成最新的
			for _, cm := range old.Comments {
				if !strings.HasPrefix(cm.ID, "jira-") {
					n.Comments = append(n.Comments, cm)
				}
			}
			sort.SliceStable(n.Comments, func(i, j int) bool { return n.Comments[i].At.Before(n.Comments[j].At.Time) })
			s.Issues[idx] = n
			res.Updated++
			continue
		}
		num++
		n.ID = "ISS-" + strconv.Itoa(num)
		created = append(created, n)
		res.Created++
	}
	// 新的放前面(和 CreateIssue 一樣新的在前),同一批裡較新建立的在前
	for i, j := 0, len(created)-1; i < j; i, j = i+1, j-1 {
		created[i], created[j] = created[j], created[i]
	}
	s.Issues = append(created, s.Issues...)
	a.saveIssues()
	a.Log(a.Actor(), "square.and.arrow.down", "從 Jira 匯入 "+strings.Join(keys, "、")+":新增 "+strconv.Itoa(res.Created)+
		" 筆、更新 "+strconv.Itoa(res.Updated)+" 筆", "development")
	a.Mu.Unlock()
	a.Changed("issues")
	return res, nil
}

// jiraFetchProject 讀一個專案的全部 Issue(含留言;圖片附件下載存到 orgDir)
func jiraFetchProject(a *App, key string, includeClosed bool, base, orgDir string) ([]jiraIssue, error) {
	jql := `project = "` + strings.ReplaceAll(key, `"`, `\"`) + `"`
	if !includeClosed {
		jql += " AND statusCategory != Done"
	}
	jql += " ORDER BY created ASC"
	raw, err := jiraSearch(a, jql)
	if err != nil {
		return nil, err
	}
	out := make([]jiraIssue, 0, len(raw))
	for _, it := range raw {
		k, _ := it["key"].(string)
		f, _ := it["fields"].(map[string]any)
		if k == "" || f == nil {
			continue
		}
		notes, err := jiraComments(a, k, f)
		if err != nil {
			return nil, err
		}
		out = append(out, jiraIssue{key: k, fields: f, notes: notes, atts: jiraAttachments(a, base, orgDir, f)})
	}
	return out, nil
}

// jiraAttachments 圖片附件下載存成本機附件(再匯入時已有就不重下載);其他檔案或下載失敗的連回 Jira
func jiraAttachments(a *App, base, orgDir string, f map[string]any) []jiraAtt {
	list, _ := f["attachment"].([]any)
	out := []jiraAtt{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		name, content := jiraStr(m["filename"]), jiraStr(m["content"])
		if name == "" || content == "" {
			continue
		}
		at := jiraAtt{name: name, url: content}
		mime := strings.ToLower(jiraStr(m["mimeType"]))
		size := jiraInt(m["size"])
		if _, ok := attachmentTypes[mime]; ok && size <= MaxAttachment {
			seed := "jira:" + base + ":" + jiraStr(m["id"])
			if id := AttachmentIDFor(orgDir, seed); id != "" {
				at.url, at.image = AttachmentURL(id), true
			} else if data, err := jiraDownload(a, base, content); err == nil {
				if id, err := SaveAttachmentIn(orgDir, data, seed); err == nil {
					at.url, at.image = AttachmentURL(id), true
				}
			}
		}
		out = append(out, at)
	}
	return out
}

// jiraDownload 下載附件內容(同網域的網址才帶 token)
func jiraDownload(a *App, base, content string) ([]byte, error) {
	path := content
	if strings.HasPrefix(content, base+"/") {
		path = strings.TrimPrefix(content, base)
	}
	r, err := ConnRequestWith(a, JiraConnID, "GET", path, nil, map[string]string{"Accept": "*/*"})
	if err != nil {
		return nil, err
	}
	if !r.OK() {
		return nil, connErr("下載附件失敗(HTTP " + strconv.Itoa(r.Status) + ")")
	}
	return r.Data, nil
}

// jiraImageRe Jira wiki 語法的圖片:!檔名! 或 !檔名|thumbnail! / !檔名|width=300!
var jiraImageRe = regexp.MustCompile(`!([^!|\n]+?)(\|[^!\n]*)?!`)

// jiraRenderImages 把內文裡引用的附件圖片換成 Markdown 圖片;回傳換過的文字與被引用到的檔名
func jiraRenderImages(text string, atts []jiraAtt, used map[string]bool) string {
	byName := map[string]jiraAtt{}
	for _, at := range atts {
		byName[at.name] = at
	}
	return jiraImageRe.ReplaceAllStringFunc(text, func(s string) string {
		m := jiraImageRe.FindStringSubmatch(s)
		name := strings.TrimSpace(m[1])
		if at, ok := byName[name]; ok {
			used[name] = true
			if at.image {
				return "![" + mdAlt(name) + "](" + at.url + ")"
			}
			return "[" + mdAlt(name) + "](" + at.url + ")"
		}
		if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
			return "[圖片](" + name + ")"
		}
		return s
	})
}

// mdAlt 檔名放進 Markdown 的 [] 裡:拿掉會破壞語法的字元
func mdAlt(s string) string { return strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(s) }

// jiraMaxIssues 一個專案最多讀幾筆(避免設定錯誤時無限翻頁)
const jiraMaxIssues = 20000

func jiraSearch(a *App, jql string) ([]map[string]any, error) {
	var out []map[string]any
	// Cloud 新端點:nextPageToken 分頁
	token := ""
	for {
		q := url.Values{"jql": {jql}, "fields": {jiraFields}, "maxResults": {"100"}}
		if token != "" {
			q.Set("nextPageToken", token)
		}
		r, err := ConnRequestRaw(a, JiraConnID, "GET", "/rest/api/2/search/jql?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if len(out) == 0 && token == "" && (r.Status == 404 || r.Status == 405 || r.Status == 410) {
			return jiraSearchLegacy(a, jql)
		}
		if _, err := connCheck(r, nil); err != nil {
			return nil, jiraErr(err)
		}
		d, _ := r.JSON().(map[string]any)
		out = append(out, jiraIssuesOf(d)...)
		next, _ := d["nextPageToken"].(string)
		last, _ := d["isLast"].(bool)
		if next == "" || last || len(out) >= jiraMaxIssues {
			return out, nil
		}
		token = next
	}
}

// jiraSearchLegacy Server/DC:startAt 分頁
func jiraSearchLegacy(a *App, jql string) ([]map[string]any, error) {
	var out []map[string]any
	for start := 0; ; {
		q := url.Values{"jql": {jql}, "fields": {jiraFields}, "maxResults": {"100"}, "startAt": {strconv.Itoa(start)}}
		v, err := ConnRequest(a, JiraConnID, "GET", "/rest/api/2/search?"+q.Encode(), nil)
		if err != nil {
			return nil, jiraErr(err)
		}
		d, _ := v.(map[string]any)
		page := jiraIssuesOf(d)
		out = append(out, page...)
		total := jiraInt(d["total"])
		start += len(page)
		if len(page) == 0 || start >= total || len(out) >= jiraMaxIssues {
			return out, nil
		}
	}
}

func jiraIssuesOf(d map[string]any) []map[string]any {
	list, _ := d["issues"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// jiraErr Jira 的錯誤訊息在 errorMessages 裡
func jiraErr(err error) error {
	if e, ok := AsConnError(err); ok && e.Status == 400 {
		return ConnError{Status: 400, Message: "Jira 查詢失敗:" + e.Message}
	}
	return err
}

// jiraComments 搜尋結果帶的留言不完整時,另外分頁讀完
func jiraComments(a *App, key string, f map[string]any) ([]IssueComment, error) {
	cm, _ := f["comment"].(map[string]any)
	list, _ := cm["comments"].([]any)
	total := jiraInt(cm["total"])
	if total > len(list) {
		list = nil
		for start := 0; ; {
			q := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"100"}}
			v, err := ConnRequest(a, JiraConnID, "GET", "/rest/api/2/issue/"+url.PathEscape(key)+"/comment?"+q.Encode(), nil)
			if err != nil {
				return nil, err
			}
			d, _ := v.(map[string]any)
			page, _ := d["comments"].([]any)
			list = append(list, page...)
			start += len(page)
			if len(page) == 0 || start >= jiraInt(d["total"]) {
				break
			}
		}
	}
	out := []IssueComment{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		id := jiraStr(m["id"])
		if id == "" {
			continue
		}
		at := jiraTime(m["created"])
		out = append(out, IssueComment{ID: "jira-" + id, Author: jiraUser(m["author"]), At: Time{at}, Text: jiraStr(m["body"])})
	}
	return out, nil
}

// jiraToIssue 對應成本機的 Issue(ID 由呼叫的人決定)
func jiraToIssue(base string, ji jiraIssue) OpsIssue {
	f := ji.fields
	created := jiraTime(f["created"])
	updated := jiraTime(f["updated"])
	if updated.IsZero() {
		updated = created
	}
	status, _ := f["status"].(map[string]any)
	cat, _ := status["statusCategory"].(map[string]any)
	is := OpsIssue{
		Title: strings.TrimSpace(jiraStr(f["summary"])), Body: jiraStr(f["description"]), Status: "open",
		Priority: jiraPriority(f["priority"]), Source: "Jira " + ji.key, Link: base + "/browse/" + ji.key,
		CreatedBy: jiraUser(f["reporter"]), CreatedAt: Time{created}, UpdatedAt: Time{updated},
		Comments: append([]IssueComment{}, ji.notes...),
	}
	if is.Title == "" {
		is.Title = ji.key
	}
	if is.CreatedBy == "" {
		is.CreatedBy = jiraUser(f["creator"])
	}
	if is.CreatedBy == "" {
		is.CreatedBy = "Jira"
	}
	labels := []string{"jira", strings.SplitN(ji.key, "-", 2)[0]}
	if t, _ := f["issuetype"].(map[string]any); t != nil {
		if n := jiraStr(t["name"]); n != "" {
			labels = append(labels, n)
		}
	}
	if ls, ok := f["labels"].([]any); ok {
		for _, l := range ls {
			labels = append(labels, jiraStr(l))
		}
	}
	is.Labels = CleanLabels(labels)
	if jiraStr(cat["key"]) == "done" {
		is.Status = "closed"
		closed := jiraTime(f["resolutiondate"])
		if closed.IsZero() {
			closed = updated
		}
		is.ClosedAt = &Time{closed}
	}
	// 圖片:內文與留言裡的 !檔名! 換成本機附件;內文沒引用到的附件列在最後
	used := map[string]bool{}
	is.Body = strings.TrimRight(jiraRenderImages(is.Body, ji.atts, used), "\n")
	for i := range is.Comments {
		is.Comments[i].Text = jiraRenderImages(is.Comments[i].Text, ji.atts, used)
	}
	var rest []string
	for _, at := range ji.atts {
		if used[at.name] {
			continue
		}
		if at.image {
			rest = append(rest, "!["+mdAlt(at.name)+"]("+at.url+")")
		} else {
			rest = append(rest, "- ["+mdAlt(at.name)+"]("+at.url+")")
		}
	}
	if len(rest) > 0 {
		if is.Body != "" {
			is.Body += "\n\n"
		}
		is.Body += "#### Jira 附件\n\n" + strings.Join(rest, "\n\n")
	}
	return is
}

// jiraPriority Jira 的優先度名稱 → urgent / high / normal / low
func jiraPriority(v any) string {
	m, _ := v.(map[string]any)
	switch strings.ToLower(jiraStr(m["name"])) {
	case "highest", "blocker", "critical", "urgent", "最高", "緊急":
		return "urgent"
	case "high", "major", "高":
		return "high"
	case "low", "lowest", "minor", "trivial", "低", "最低":
		return "low"
	default:
		return "normal"
	}
}

func jiraUser(v any) string {
	m, _ := v.(map[string]any)
	for _, k := range []string{"displayName", "name", "emailAddress"} {
		if s := jiraStr(m[k]); s != "" {
			return s
		}
	}
	return ""
}

func jiraStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

func jiraInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// jiraTime Jira 的時間格式 2024-01-02T03:04:05.000+0800
func jiraTime(v any) time.Time {
	s := jiraStr(v)
	if s == "" {
		return time.Time{}
	}
	for _, l := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func init() {
	RegisterView("jira", func(a *App) any {
		c, ok := ConnFind(a, JiraConnID)
		if !ok {
			return map[string]any{"configured": false}
		}
		var account, errMsg *string
		if st := a.conns().States[JiraConnID]; st != nil {
			account, errMsg = st.Account, st.Error
		}
		return map[string]any{
			"configured": true, "baseURL": c.BaseURL, "auth": c.Auth, "cloud": JiraIsCloud(c.BaseURL),
			"loggedIn": connHasTokenCached(a, c), "account": account, "error": errMsg,
		}
	})
	RegisterAction("jira_setup", func(a *App, args map[string]any) (any, error) {
		base, _ := args["baseURL"].(string)
		email, _ := args["email"].(string)
		token, _ := args["token"].(string)
		if _, err := JiraSetup(a, base, email, token); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		st := connStateCopy(a, JiraConnID)
		a.Mu.Unlock()
		if st != nil && st.Error != nil {
			return nil, UserError{"連不上 Jira:" + *st.Error}
		}
		return map[string]any{"ok": true}, nil
	})
	RegisterAction("jira_projects", func(a *App, args map[string]any) (any, error) {
		return JiraProjects(a)
	})
	RegisterAction("jira_import", func(a *App, args map[string]any) (any, error) {
		var projects []string
		if err := ReadInto(args["projects"], &projects); err != nil {
			return nil, UserError{"projects 要是專案代碼的陣列"}
		}
		closed, _ := args["includeClosed"].(bool)
		return JiraImport(a, projects, closed)
	})
}
