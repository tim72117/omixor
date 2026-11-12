package core

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// fakePNG 最小的 PNG 檔頭(看內容判斷類型用)
var fakePNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01")

var jiraDownloads int

// 假的 Jira:cloud 為 true 時有 /search/jql(nextPageToken),否則只有舊的 /search(startAt)
func fakeJira(t *testing.T, cloud bool, wantAuth string, issues func() []map[string]any) *FakeNet {
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.Header.Get("Authorization") != wantAuth {
			return 401, map[string]any{"errorMessages": []string{"bad auth"}}
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/rest/api/2/myself":
			return 200, map[string]any{"displayName": "Tim", "emailAddress": "tim@example.com"}
		case r.URL.Path == "/rest/api/2/project":
			return 200, []any{map[string]any{"key": "EDU", "name": "教評"}, map[string]any{"key": "ABC", "name": "其他"}}
		case r.URL.Path == "/rest/api/2/search/jql" && cloud:
			if !strings.Contains(q.Get("jql"), `project = "EDU"`) {
				return 400, map[string]any{"errorMessages": []string{"bad jql"}}
			}
			all := filterJira(issues(), q.Get("jql"))
			// 每頁 1 筆,測分頁
			i := 0
			if tok := q.Get("nextPageToken"); tok != "" {
				i = int(tok[0] - '0')
			}
			out := map[string]any{"issues": all[i : i+1], "isLast": i+1 >= len(all)}
			if i+1 < len(all) {
				out["nextPageToken"] = string(rune('0' + i + 1))
			}
			return 200, out
		case r.URL.Path == "/rest/api/2/search/jql":
			return 404, map[string]any{"errorMessages": []string{"not found"}}
		case r.URL.Path == "/rest/api/2/search" && !cloud:
			all := filterJira(issues(), q.Get("jql"))
			return 200, map[string]any{"issues": all, "startAt": 0, "total": len(all)}
		case r.URL.Path == "/rest/api/2/attachment/content/900":
			jiraDownloads++
			if r.Header.Get("Accept") != "*/*" {
				return 406, map[string]any{"errorMessages": []string{"accept"}}
			}
			return 200, RawBody(fakePNG)
		case r.URL.Path == "/rest/api/2/issue/EDU-1/comment":
			start := q.Get("startAt")
			if start == "0" {
				return 200, map[string]any{"comments": []any{jc("11", "第一則"), jc("12", "第二則")}, "total": 3}
			}
			return 200, map[string]any{"comments": []any{jc("13", "第三則")}, "total": 3}
		}
		return 404, map[string]any{"errorMessages": []string{"no route " + r.URL.Path}}
	})
	t.Cleanup(f.Close)
	return f
}

func jc(id, text string) map[string]any {
	return map[string]any{"id": id, "body": text, "created": "2024-03-0" + id[1:] + "T10:00:00.000+0800",
		"author": map[string]any{"displayName": "Mei"}}
}

func filterJira(all []map[string]any, jql string) []map[string]any {
	if !strings.Contains(jql, "statusCategory != Done") {
		return all
	}
	out := []map[string]any{}
	for _, it := range all {
		f := it["fields"].(map[string]any)
		if f["status"].(map[string]any)["statusCategory"].(map[string]any)["key"] != "done" {
			out = append(out, it)
		}
	}
	return out
}

func jiraFixture(summary1 string) []map[string]any {
	return []map[string]any{
		{"key": "EDU-1", "fields": map[string]any{
			"summary": summary1, "description": "說明 !shot.png|thumbnail! 結束\n", "labels": []any{"ui"},
			"attachment": []any{
				map[string]any{"id": "900", "filename": "shot.png", "mimeType": "image/png", "size": 20, "content": "https://your-team.atlassian.net/rest/api/2/attachment/content/900"},
				map[string]any{"id": "901", "filename": "spec.pdf", "mimeType": "application/pdf", "size": 20, "content": "https://your-team.atlassian.net/rest/api/2/attachment/content/901"},
			},
			"status":   map[string]any{"name": "進行中", "statusCategory": map[string]any{"key": "indeterminate"}},
			"priority": map[string]any{"name": "High"}, "issuetype": map[string]any{"name": "Bug"},
			"created": "2024-03-01T09:00:00.000+0800", "updated": "2024-03-05T09:00:00.000+0800",
			"reporter": map[string]any{"displayName": "Yuting"},
			// 搜尋結果只帶 1 則,total 3:要另外讀完
			"comment": map[string]any{"comments": []any{jc("11", "第一則")}, "total": 3, "maxResults": 1},
		}},
		{"key": "EDU-2", "fields": map[string]any{
			"summary": "已完成的", "description": nil,
			"status":   map[string]any{"name": "Done", "statusCategory": map[string]any{"key": "done"}},
			"priority": map[string]any{"name": "Lowest"}, "issuetype": map[string]any{"name": "Task"},
			"created": "2024-02-01T09:00:00.000+0800", "updated": "2024-02-03T09:00:00.000+0800",
			"resolutiondate": "2024-02-02T09:00:00.000+0800",
			"comment":        map[string]any{"comments": []any{}, "total": 0},
		}},
	}
}

func issueByLink(a *App, link string) *OpsIssue {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	for _, i := range a.Issues() {
		if i.Link == link {
			c := cloneIssue(i)
			return &c
		}
	}
	return nil
}

func TestJiraCloudImport(t *testing.T) {
	a := connTestApp(t)
	jiraDownloads = 0
	summary := "登入頁跑版"
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("tim@example.com:api-token"))
	fakeJira(t, true, auth, func() []map[string]any { return jiraFixture(summary) })

	if _, err := JiraSetup(a, "your-team.atlassian.net/browse/EDU-1", "", "api-token"); err == nil {
		t.Fatal("Cloud 沒給 email 要擋")
	}
	c, err := JiraSetup(a, "https://your-team.atlassian.net/browse/EDU-1", "tim@example.com", "api-token")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://your-team.atlassian.net" || c.Auth != AuthBasic {
		t.Fatalf("連線設定不對:%s %s", c.BaseURL, c.Auth)
	}
	ps, err := JiraProjects(a)
	if err != nil || len(ps) != 2 || ps[0].Key != "ABC" {
		t.Fatalf("專案:%v %v", ps, err)
	}

	// 預設只匯入未完成的
	res, err := JiraImport(a, []string{"EDU"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Updated != 0 {
		t.Fatalf("結果:%+v", res)
	}
	link := "https://your-team.atlassian.net/browse/EDU-1"
	is := issueByLink(a, link)
	if is == nil {
		t.Fatal("沒有匯入 EDU-1")
	}
	// 圖片:內文引用的換成本機附件,PDF 列在最後連回 Jira
	if res.Images != 1 || jiraDownloads != 1 {
		t.Fatalf("圖片數 %d,下載 %d 次", res.Images, jiraDownloads)
	}
	img := regexp.MustCompile(`!\[shot\.png\]\((/api/attachments/[0-9a-f]{32}\.png)\)`).FindStringSubmatch(is.Body)
	if img == nil || !strings.HasPrefix(is.Body, "說明 ![shot.png](") || !strings.Contains(is.Body, "- [spec.pdf](https://your-team.atlassian.net/rest/api/2/attachment/content/901)") ||
		strings.Contains(is.Body, "#### Jira 附件\n\n![shot") {
		t.Fatalf("內文圖片不對:%q", is.Body)
	}
	if data, mime, err := ReadAttachment(a, strings.TrimPrefix(img[1], "/api/attachments/")); err != nil || mime != "image/png" || string(data) != string(fakePNG) {
		t.Fatalf("讀附件:%v %s", err, mime)
	}
	is.Body = "說明"
	if is.Title != summary || is.Priority != "high" || is.Status != "open" || is.Source != "Jira EDU-1" ||
		is.CreatedBy != "Yuting" || len(is.Comments) != 3 || is.Comments[2].Text != "第三則" {
		t.Fatalf("內容不對:%+v", is)
	}
	if strings.Join(is.Labels, ",") != "jira,EDU,Bug,ui" {
		t.Fatalf("標籤:%v", is.Labels)
	}

	// 本機加一則留言,再匯入一次(含已關閉):更新、不重複、本機留言保留
	a.Mu.Lock()
	a.UpdateIssue(is.ID, "tim", func(i *OpsIssue) {
		i.Comments = append(i.Comments, IssueComment{ID: "local-1", Author: "tim", At: nowPrecise(), Text: "本機留言"})
	})
	a.Mu.Unlock()
	summary = "登入頁跑版(已改標題)"
	res, err = JiraImport(a, []string{"EDU"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Updated != 1 {
		t.Fatalf("第二次結果:%+v", res)
	}
	if jiraDownloads != 1 {
		t.Fatalf("再匯入不該重下載圖片:%d 次", jiraDownloads)
	}
	is2 := issueByLink(a, link)
	if is2.ID != is.ID || is2.Title != summary || len(is2.Comments) != 4 || is2.Comments[3].ID != "local-1" {
		t.Fatalf("更新不對:%+v", is2)
	}
	done := issueByLink(a, "https://your-team.atlassian.net/browse/EDU-2")
	if done == nil || done.Status != "closed" || done.ClosedAt == nil || done.Priority != "low" || done.CreatedBy != "Jira" {
		t.Fatalf("已完成的不對:%+v", done)
	}
	a.Mu.Lock()
	n := len(a.Issues())
	a.Mu.Unlock()
	if n != 2 {
		t.Fatalf("應該只有 2 筆,有 %d", n)
	}
}

func TestJiraServerImport(t *testing.T) {
	a := connTestApp(t)
	fakeJira(t, false, "Bearer pat-123", func() []map[string]any { return jiraFixture("x") })
	c, err := JiraSetup(a, "https://jira.corp.test/", "", "pat-123")
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth != AuthBearer {
		t.Fatalf("Server 要用 bearer:%s", c.Auth)
	}
	res, err := JiraImport(a, []string{"EDU"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 2 {
		t.Fatalf("結果:%+v", res)
	}
	// 錯的 token:連不上
	if _, err := JiraSetup(a, "https://jira.corp.test", "", "wrong"); err != nil {
		t.Fatal(err)
	}
	if _, err := JiraImport(a, []string{"EDU"}, true); err == nil {
		t.Fatal("token 錯應該失敗")
	}
}
