package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"

	"omixor-core/model"
	"omixor-core/store"
)

// 附件(Issue 內文與留言裡的圖片):一個組織一份,只收 PNG、JPEG、GIF、WebP(看檔案內容判斷,不收 SVG)。
// id = 內容 SHA-256 前 32 碼 + 副檔名,同一張圖在同一組織只存一份。組織成員可以上傳與讀取。

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

// SaveAttachment 存一張圖,回傳 id
func (s *Service) SaveAttachment(p *model.Principal, org string, data []byte) (string, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return "", err
	}
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
	h := sha256.Sum256(data)
	id := hex.EncodeToString(h[:])[:32] + attachmentExt[t]
	if err := s.Store.PutAttachment(org, id, data); err != nil {
		return "", err
	}
	return id, nil
}

// GetAttachment 讀一張圖,回傳內容與 MIME 類型
func (s *Service) GetAttachment(p *model.Principal, org, id string) ([]byte, string, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return nil, "", err
	}
	if !attachmentIDRe.MatchString(id) {
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
