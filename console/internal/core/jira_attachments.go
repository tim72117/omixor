package core

import (
	"log"
	"regexp"
	"strconv"
	"strings"

	"omixor-core/backend"
	"omixor-core/service"
)

// Jira 匯入的附件:圖片下載後存進核心的附件(規則同畫面上傳,見 omixor-core 的 service/attachments.go),內文改成 Markdown 圖片。

// jiraAtt Jira 的附件:圖片存成 omixor 附件(url 是 /api/attachments/…),其他檔案(或下載失敗)連回 Jira
type jiraAtt struct {
	name  string
	url   string
	image bool
}

// jiraImageStore 匯入的圖片存到哪個組織的核心。
// id 用來源(Jira 網址 + 附件 id)算,再匯入時已經有就不重下載
type jiraImageStore struct {
	l   *backend.Local // nil = 拿不到核心:圖片一律連回 Jira
	org string
	err error
}

// jiraImageStoreFor 目前組織、以 who 的身分(呼叫時不用拿鎖)
func jiraImageStoreFor(a *App, who *Caller) jiraImageStore {
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	l, err := localCoreFor(who, org)
	if err != nil {
		return jiraImageStore{err: err}
	}
	return jiraImageStore{l: l, org: org}
}

// find 這個來源以前存過的圖;沒有回空字串
func (s jiraImageStore) find(seed string) string {
	l := s.l
	if l == nil {
		return ""
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		id := service.AttachmentID([]byte(seed), mime)
		if _, _, err := l.Svc.GetAttachment(l.P, s.org, id); err == nil {
			return id
		}
	}
	return ""
}

// save 存一張下載好的圖,回傳 id
func (s jiraImageStore) save(seed string, data []byte) (string, error) {
	if s.l == nil {
		return "", s.err
	}
	ensureLocalOrg(s.l, s.org)
	id := service.AttachmentID([]byte(seed), service.AttachmentType(data))
	// 不是圖片時 id 是空的,由核心回它的錯誤訊息
	if err := s.l.Svc.ImportAttachment(s.l.P, s.org, id, data); err != nil {
		return "", err
	}
	return id, nil
}

// jiraAttachments 圖片附件下載存成本機附件(再匯入時已有就不重下載);其他檔案或下載失敗的連回 Jira
func jiraAttachments(a *App, base string, images jiraImageStore, f map[string]any) []jiraAtt {
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
		if service.AttachmentTypeAllowed(mime) && size <= MaxAttachment {
			seed := "jira:" + base + ":" + jiraStr(m["id"])
			if id := images.find(seed); id != "" {
				at.url, at.image = AttachmentURL(id), true
			} else if data, err := jiraDownload(a, base, content); err == nil {
				if id, err := images.save(seed, data); err == nil {
					at.url, at.image = AttachmentURL(id), true
				} else {
					log.Printf("Jira 圖片 %s 存不起來,改連回 Jira:%v", name, err)
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
