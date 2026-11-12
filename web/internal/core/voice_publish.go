package core

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

// 發佈旁白(只能由人在畫面上觸發),同 Swift 的 VoicePipeline.publish:
// PUT /api/admin/scripts/<路徑>[?course=&lesson=] 寫進 evoleap,再 GET /api/scripts/<路徑> 讀回比對。
// Swift 版發佈前會要 Touch ID;網頁版沒有,改由畫面上的確認對話框把關。

// VoiceTarget 發佈目的地(設定 › 連線 裡的一個連線)
type VoiceTarget struct {
	ID         string
	Name       string
	BaseURL    string
	Production bool
}

// VoiceConnector 發佈時用的連線(對應 Swift 的 ConnectionStore)。
// 預設接到連線功能(ConnFind、ConnHasToken、ConnRequest);測試用假的。
type VoiceConnector interface {
	// Target 找連線;找不到回傳 ok=false(呼叫時拿著 Mu)
	Target(a *App, id string) (VoiceTarget, bool)
	// HasToken 已登入或不需要登入(同 Swift 的 hasToken;會讀鑰匙圈,呼叫時不能拿著 Mu)
	HasToken(a *App, id string) bool
	// HasTokenCached 同 HasToken,但只看快取(畫面用,呼叫時拿著 Mu)
	HasTokenCached(a *App, id string) bool
	// Request 發 API 請求並回傳解析後的 JSON(同 Swift 的 ConnectionStore.request;呼叫時不能拿著 Mu)
	Request(a *App, id, method, path string, body []byte) (any, error)
}

// VoiceConn 預設的發佈連線:設定 › 連線(conn_store.go)
var VoiceConn VoiceConnector = voiceConnStore{}

type voiceConnStore struct{}

func (voiceConnStore) Target(a *App, id string) (VoiceTarget, bool) {
	c, ok := ConnFind(a, id)
	if !ok {
		return VoiceTarget{}, false
	}
	return VoiceTarget{ID: c.ID, Name: c.Name, BaseURL: c.BaseURL, Production: c.IsProduction}, true
}

func (voiceConnStore) HasToken(a *App, id string) bool {
	a.Mu.Lock()
	c, ok := ConnFind(a, id)
	a.Mu.Unlock()
	return ok && ConnHasToken(a, c)
}

func (voiceConnStore) HasTokenCached(a *App, id string) bool {
	c, ok := ConnFind(a, id)
	return ok && connHasTokenCached(a, c)
}

func (voiceConnStore) Request(a *App, id, method, path string, body []byte) (any, error) {
	return ConnRequest(a, id, method, path, body)
}

// voiceNoConn 測試用:沒有任何連線(不碰真正的連線與鑰匙圈)
type voiceNoConn struct{}

func (voiceNoConn) Target(*App, string) (VoiceTarget, bool) { return VoiceTarget{}, false }
func (voiceNoConn) HasToken(*App, string) bool              { return false }
func (voiceNoConn) HasTokenCached(*App, string) bool        { return false }
func (voiceNoConn) Request(*App, string, string, string, []byte) (any, error) {
	return nil, UserError{"測試中沒有連線"}
}

func (p *VoicePipeline) connector() VoiceConnector {
	if p.Conn != nil {
		return p.Conn
	}
	return VoiceConn
}

// Target 目前的發佈目的地(呼叫時拿著 Mu)
func (p *VoicePipeline) Target() (VoiceTarget, bool) {
	c := p.connector()
	if c == nil {
		return VoiceTarget{}, false
	}
	return c.Target(p.app, p.TargetID())
}

// Publish 發佈一個待發佈的批次。呼叫時不要拿著 Mu;會等全部送完才回傳。
func (p *VoicePipeline) Publish(id string) { p.PublishAs(id, Person(p.app.Me)) }

// PublishAs 同 Publish,動態牆以 by 署名(畫面有登入時是按發佈的人)
func (p *VoicePipeline) PublishAs(id string, by Actor) {
	a := p.app
	a.Mu.Lock()
	jp := p.Job(id)
	if jp == nil || jp.State != VoiceReady {
		a.Mu.Unlock()
		return
	}
	conn := p.connector()
	target, ok := p.Target()
	if !ok {
		p.update(id, func(j *VoiceJob) { j.Note("找不到發佈目的地的連線,請在設定 › 連線 確認") })
		a.Mu.Unlock()
		return
	}
	a.Mu.Unlock()
	hasToken := conn.HasToken(a, target.ID) // 會讀鑰匙圈,不能拿著鎖
	a.Mu.Lock()
	if jp := p.Job(id); jp == nil || jp.State != VoiceReady {
		a.Mu.Unlock()
		return
	}
	if !hasToken {
		p.update(id, func(j *VoiceJob) { j.Note("發佈前要先登入「" + target.Name + "」") })
		a.Mu.Unlock()
		return
	}
	p.update(id, func(j *VoiceJob) {
		j.State = VoicePublishing
		j.Note(fmt.Sprintf("發佈到「%s」(%s)…", target.Name, target.BaseURL))
	})
	changes := append([]VoiceFileChange{}, p.Job(id).Changes...)
	live := p.liveDir()
	a.Mu.Unlock()

	allOK := true
	for i, c := range changes {
		if c.Publish != nil && *c.Publish == "ok" {
			continue
		}
		result := "ok"
		data, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(c.Path)))
		if err != nil {
			result = "讀不到本機檔案"
		} else if VoiceSha(data) != c.Sha {
			result = "收結果後這個檔又被改過,請重新檢查後再發佈"
		} else {
			path := "/api/admin/scripts/" + VoiceEscapePath(c.Path)
			if c.Course != nil && c.Lesson != nil {
				path += "?" + VoiceFormEncode(map[string]string{"course": *c.Course, "lesson": *c.Lesson})
			}
			if _, err := conn.Request(a, target.ID, "PUT", path, data); err != nil {
				result = err.Error()
			} else if online, err := conn.Request(a, target.ID, "GET", "/api/scripts/"+VoiceEscapePath(c.Path), nil); err != nil {
				// 用 API 讀回來比對,確認線上拿到的就是這份內容
				result = err.Error()
			} else {
				var local any
				if err := jsonUnmarshal(data, &local); err != nil {
					result = err.Error()
				} else if !reflect.DeepEqual(Object(online), Object(local)) {
					result = "寫入成功,但讀回來的內容和本機不同"
				}
			}
		}
		if result != "ok" {
			allOK = false
		}
		a.Mu.Lock()
		p.update(id, func(j *VoiceJob) {
			if i < len(j.Changes) {
				j.Changes[i].Publish = voicePtr(result)
			}
			msg := result
			if result == "ok" {
				msg = "已發佈,讀回比對一致"
			}
			j.Note(c.Path + ":" + msg)
		})
		a.Mu.Unlock()
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	p.update(id, func(j *VoiceJob) {
		if allOK {
			j.State = VoicePublished
			j.PublishedAt = voiceTimePtr(Now().Time)
			j.PublishedTo = voicePtr(target.Name)
			j.Note("全部發佈完成。記得把這些 .voice.json commit 進 shuttle 的 repo")
		} else {
			j.State = VoiceReady
			j.Note("有檔案發佈失敗,修好後可以再按一次發佈(已成功的不會重送)")
		}
	})
	icon, verb := "checkmark.seal", "發佈"
	if !allOK {
		icon, verb = "exclamationmark.triangle", "部分發佈"
	}
	a.Log(by, icon, fmt.Sprintf("%s旁白 %s 到「%s」(%d 份)", verb, id, target.Name, len(changes)), "pipelines")
}

// VoiceEscapePath 同 Swift 的 ConnHTTP.escapePath:每一段用 urlPathAllowed 編碼
func VoiceEscapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, s := range parts {
		parts[i] = voicePercent(s, func(r rune) bool {
			return r < 128 && (isASCIIAlnum(r) || strings.ContainsRune("!$&'()*+,-.:;=@_~", r))
		})
	}
	return strings.Join(parts, "/")
}

// VoiceFormEncode 同 Swift 的 ConnHTTP.formEncode(key 排序;CharacterSet.alphanumerics 含 Unicode 字母數字)
func VoiceFormEncode(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []string{}
	for _, k := range keys {
		out = append(out, k+"="+voicePercent(fields[k], func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsNumber(r) || unicode.IsMark(r) || strings.ContainsRune("-._~", r)
		}))
	}
	return strings.Join(out, "&")
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func voicePercent(s string, allowed func(rune) bool) string {
	var b strings.Builder
	for _, r := range s {
		if allowed(r) {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
