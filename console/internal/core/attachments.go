package core

import (
	"io/fs"

	"omixor-core/service"
)

// 附件(Issue 內文與留言裡的圖片):存放與規則都在核心(omixor-core 的 service/attachments.go,表 omixor_attachments),
// 這裡只負責挑組織與身分。網址 /api/attachments/<id>,讀的是目前組織的附件(登入者是他能讀的組織)。
// 以前 console 自己存的一份(omixor_files、資料夾裡的檔案)見 attachments_legacy.go:只搬進核心與讀取,不再寫。

// MaxAttachment 單一附件上限
const MaxAttachment = service.MaxAttachment

// AttachmentURL 附件在畫面上的網址
func AttachmentURL(id string) string { return "/api/attachments/" + id }

// SaveAttachment 存到目前組織(呼叫時不用拿鎖);who 是網頁的呼叫者(沒給 = 系統身分)
func SaveAttachment(a *App, data []byte, who ...*Caller) (string, error) {
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	b, err := coreBackendFor(firstCaller(who), org)
	if err != nil {
		return "", err
	}
	return b.SaveAttachment(org, data)
}

// ReadAttachment 讀目前組織的附件,回傳內容與 MIME 類型
func ReadAttachment(a *App, id string, who ...*Caller) ([]byte, string, error) {
	if !service.ValidAttachmentID(id) {
		return nil, "", fs.ErrNotExist
	}
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	return readOrgAttachment(firstCaller(who), org, id)
}

// ReadAttachmentAmong 依序在這些組織找附件(登入者的圖片請求帶不了組織;id 是雜湊,只找呼叫者能讀的組織)
func ReadAttachmentAmong(id string, orgs []string, who ...*Caller) ([]byte, string, error) {
	if !service.ValidAttachmentID(id) {
		return nil, "", fs.ErrNotExist
	}
	for _, org := range orgs {
		if data, t, err := readOrgAttachment(firstCaller(who), org, id); err == nil {
			return data, t, nil
		}
	}
	return nil, "", fs.ErrNotExist
}

// readOrgAttachment 以呼叫者的身分向核心讀;核心沒有的才找舊的本機存放。
// 有呼叫者而且對不到他在這個組織的身分時,舊存放也不讀
func readOrgAttachment(c *Caller, org, id string) ([]byte, string, error) {
	b, err := coreBackendFor(c, org)
	switch {
	case err == nil:
		if data, t, err := b.GetAttachment(org, id); err == nil {
			return data, t, nil
		}
	case c != nil:
		return nil, "", fs.ErrNotExist
	}
	return readLegacyAttachment(org, id)
}
