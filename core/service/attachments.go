package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 附件(Issue 內文與留言裡的圖片):一個組織一份,只收 PNG、JPEG、GIF、WebP(看檔案內容判斷,不收 SVG)。
// 全部存在 omixor_attachments;server 與 console(畫面上傳、Jira 匯入、REST)都用這裡的規則。
// id = 內容 SHA-256 前 32 碼 + 副檔名,同一張圖在同一組織只存一份;匯入與舊資料可以用 ImportAttachment 指定 id
// (例如 Jira 附件是來源網址的雜湊),讓寫在 Issue 內文裡的舊連結繼續有效。組織成員可以上傳與讀取。

// MaxAttachment 單一附件上限
const MaxAttachment = 10 << 20

var attachmentExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

var attachmentIDRe = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

// AttachmentType 看內容判斷圖片類型;不是允許的圖片回傳空字串
func AttachmentType(data []byte) string {
	t := http.DetectContentType(data)
	if _, ok := attachmentExt[t]; ok {
		return t
	}
	return ""
}

// AttachmentTypeAllowed 這個 MIME 類型收不收(下載之前先看來源宣告的類型用;存的時候仍看內容)
func AttachmentTypeAllowed(mime string) bool {
	_, ok := attachmentExt[strings.ToLower(mime)]
	return ok
}

// ValidAttachmentID 附件 id 的格式對不對
func ValidAttachmentID(id string) bool { return attachmentIDRe.MatchString(id) }

// AttachmentID key 的 SHA-256 前 32 碼 + mime 的副檔名(key 一般是內容;匯入時可以是來源的識別字串)。mime 不收回空字串
func AttachmentID(key []byte, mime string) string {
	ext, ok := attachmentExt[mime]
	if !ok {
		return ""
	}
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])[:32] + ext
}

// checkAttachment 大小與類型的檢查,回傳類型
func checkAttachment(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fail(400, "檔案是空的")
	}
	if len(data) > MaxAttachment {
		return "", fail(400, "圖片太大,上限 10 MB")
	}
	t := AttachmentType(data)
	if t == "" {
		return "", fail(400, "只能放 PNG、JPEG、GIF、WebP 圖片")
	}
	return t, nil
}

// SaveAttachment 存一張圖,回傳 id
func (s *Service) SaveAttachment(p *model.Principal, org string, data []byte) (string, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return "", err
	}
	t, err := checkAttachment(data)
	if err != nil {
		return "", err
	}
	id := AttachmentID(data, t)
	if err := s.Store.PutAttachment(org, id, data); err != nil {
		return "", err
	}
	return id, nil
}

// ImportAttachment 用指定的 id 存一張圖(匯入、搬移舊資料用;同 id 已經有的不覆蓋)。
// 只給同一個程式裡的呼叫者用,不開成 REST 路由或 MCP 工具:讓外部指定 id 可以先佔走別人之後要用的 id。
// 大小與類型的規則同 SaveAttachment,副檔名要和內容相符
func (s *Service) ImportAttachment(p *model.Principal, org, id string, data []byte) error {
	if _, err := s.orgRole(p, org); err != nil {
		return err
	}
	t, err := checkAttachment(data)
	if err != nil {
		return err
	}
	if !ValidAttachmentID(id) || !strings.HasSuffix(id, attachmentExt[t]) {
		return fail(400, "附件 id 不對,或副檔名和內容不符")
	}
	return s.Store.PutAttachment(org, id, data)
}

// GetAttachment 讀一張圖,回傳內容與 MIME 類型
func (s *Service) GetAttachment(p *model.Principal, org, id string) ([]byte, string, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return nil, "", err
	}
	if !ValidAttachmentID(id) {
		return nil, "", fail(404, "找不到附件")
	}
	data, err := s.Store.GetAttachment(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", fail(404, "找不到附件")
	}
	if err != nil {
		return nil, "", err
	}
	t := AttachmentType(data)
	if t == "" {
		return nil, "", fail(404, "找不到附件")
	}
	return data, t, nil
}
