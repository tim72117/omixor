package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// 附件(Issue 內文與留言裡的圖片)現在存在核心(omixor-core,見 SaveAttachment / ReadAttachment);下面的本機存放只剩 Jira 匯入(SaveAttachmentIn)與舊資料的讀取。
// 舊的說明:分組織存放,只收 PNG、JPEG、GIF、WebP(看檔案內容判斷,不收 SVG)。
//   - 檔案模式:<組織資料夾>/attachments/<id>
//   - 資料庫模式:ops_files 資料表(path = orgs/<組織>/attachments/<id>,data bytea)
// id = 內容(或來源)的 SHA-256 前 32 碼 + 副檔名,同一張圖只存一份。
// 網址 /api/attachments/<id>,讀的是目前組織的附件。

// MaxAttachment 單一附件上限
const MaxAttachment = 10 << 20

var attachmentTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

var attachmentIDRe = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

// AttachmentURL 附件在畫面上的網址
func AttachmentURL(id string) string { return "/api/attachments/" + id }

// AttachmentType 看內容判斷圖片類型;不是允許的圖片回傳空字串
func AttachmentType(data []byte) string {
	t := http.DetectContentType(data)
	if _, ok := attachmentTypes[t]; ok {
		return t
	}
	return ""
}

// attachmentKey 存放處的路徑(相對 DataRoot)
func attachmentKey(orgDir, id string) (string, bool) {
	k, ok := keyOf(filepath.Join(orgDir, "attachments", id))
	return k, ok
}

// SaveAttachmentIn 存到這個組織資料夾;seed 不是空的時用它算 id(例如 Jira 附件的來源網址,再匯入不用重下載)
func SaveAttachmentIn(orgDir string, data []byte, seed string) (string, error) {
	if len(data) == 0 {
		return "", UserError{"檔案是空的"}
	}
	if len(data) > MaxAttachment {
		return "", UserError{"圖片太大,上限 10 MB"}
	}
	t := AttachmentType(data)
	if t == "" {
		return "", UserError{"只能放 PNG、JPEG、GIF、WebP 圖片"}
	}
	h := sha256.Sum256(data)
	if seed != "" {
		h = sha256.Sum256([]byte(seed))
	}
	id := hex.EncodeToString(h[:])[:32] + attachmentTypes[t]
	if err := attachmentWrite(orgDir, id, data); err != nil {
		return "", err
	}
	return id, nil
}

// AttachmentIDFor 用 seed 算出的 id(不知道副檔名時逐一檢查是否已存在);沒有回傳空字串
func AttachmentIDFor(orgDir, seed string) string {
	h := sha256.Sum256([]byte(seed))
	base := hex.EncodeToString(h[:])[:32]
	for _, ext := range []string{".png", ".jpg", ".gif", ".webp"} {
		if attachmentExists(orgDir, base+ext) {
			return base + ext
		}
	}
	return ""
}

// attachmentCore 目前組織的附件交給核心:遠端模式是 omixor server,本機模式是同一個程式裡的核心(PostgreSQL)
type attachmentCore interface {
	save(org string, data []byte) (string, error)
	get(org, id string) ([]byte, string, error)
}

func (l *localBackend) save(org string, data []byte) (string, error) {
	l.ensureOrg(org)
	return l.svc.SaveAttachment(localPrincipal, org, data)
}
func (l *localBackend) get(org, id string) ([]byte, string, error) {
	return l.svc.GetAttachment(localPrincipal, org, id)
}
func (r *remoteBackend) save(org string, data []byte) (string, error) {
	return r.client("").SaveAttachment(serverOrgID(org), data)
}
func (r *remoteBackend) get(org, id string) ([]byte, string, error) {
	return r.client("").GetAttachment(serverOrgID(org), id)
}
func (noDBBackend) save(string, []byte) (string, error)        { return "", errNoDB }
func (noDBBackend) get(string, string) ([]byte, string, error) { return nil, "", errNoDB }

// SaveAttachment 存到目前組織的核心(呼叫時不用拿鎖)
func SaveAttachment(a *App, data []byte) (string, error) {
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	return currentIssueBackend().(attachmentCore).save(org, data)
}

// ReadAttachment 讀目前組織的附件,回傳內容與 MIME 類型。核心沒有的,再找舊的本機存放(Jira 匯入的圖還在那裡)
func ReadAttachment(a *App, id string) ([]byte, string, error) {
	if !attachmentIDRe.MatchString(id) {
		return nil, "", fs.ErrNotExist
	}
	a.Mu.Lock()
	org, dir := a.CurrentOrgID(), a.DataDir()
	a.Mu.Unlock()
	if data, t, err := currentIssueBackend().(attachmentCore).get(org, id); err == nil {
		return data, t, nil
	}
	data, err := attachmentRead(dir, id)
	if err != nil {
		return nil, "", err
	}
	t := AttachmentType(data)
	if t == "" {
		return nil, "", fs.ErrNotExist
	}
	return data, t, nil
}

func attachmentWrite(orgDir, id string, data []byte) error {
	if pgPool == nil {
		return writeFileAtomic(filepath.Join(orgDir, "attachments", id), data)
	}
	k, ok := attachmentKey(orgDir, id)
	if !ok {
		return errors.New("附件路徑不在資料夾裡")
	}
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgPool.Exec(ctx, `insert into ops_files (path, data) values ($1, $2) on conflict (path) do nothing`, k, data)
	return err
}

func attachmentRead(orgDir, id string) ([]byte, error) {
	if pgPool == nil {
		return os.ReadFile(filepath.Join(orgDir, "attachments", id))
	}
	k, ok := attachmentKey(orgDir, id)
	if !ok {
		return nil, fs.ErrNotExist
	}
	ctx, cancel := pgCtx()
	defer cancel()
	var b []byte
	err := pgPool.QueryRow(ctx, `select data from ops_files where path = $1`, k).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fs.ErrNotExist
	}
	return b, err
}

func attachmentExists(orgDir, id string) bool {
	if pgPool == nil {
		_, err := os.Stat(filepath.Join(orgDir, "attachments", id))
		return err == nil
	}
	k, ok := attachmentKey(orgDir, id)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	_ = pgPool.QueryRow(ctx, `select count(*) from ops_files where path = $1`, k).Scan(&n)
	return n > 0
}
