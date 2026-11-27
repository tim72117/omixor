package core

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// console 以前自己存的附件(不再寫入新的):
//   - 資料庫:omixor_files(path = orgs/<組織>/attachments/<id>)
//   - 資料夾:<DataRoot>/orgs/<組織>/attachments/<id>
//
// 本機核心建立時把它們複製進核心的 omixor_attachments(id 不變,Issue 內文裡的連結繼續有效)。舊的列與檔案不刪:
// 搬移失敗時仍由這裡讀。刪除組織時一起刪。
// 所有安裝都升級過之後,這個檔案可以移除。

const legacyAttachmentLike = "orgs/%/attachments/%"

// legacyAttachmentDir 舊的資料夾(不建立)
func legacyAttachmentDir(org string) string {
	return filepath.Join(DataRoot(), "orgs", org, "attachments")
}

// readLegacyAttachment 讀舊存放(有資料庫讀 omixor_files,沒有讀資料夾),大小與類型照核心的規則
func readLegacyAttachment(org, id string) ([]byte, string, error) {
	var data []byte
	var err error
	if pgPool() == nil {
		data, err = os.ReadFile(filepath.Join(legacyAttachmentDir(org), id))
	} else {
		data, err = legacyAttachmentRow(org, id)
	}
	if err != nil {
		return nil, "", err
	}
	t := service.AttachmentType(data)
	if t == "" || len(data) > MaxAttachment {
		return nil, "", fs.ErrNotExist
	}
	return data, t, nil
}

func legacyAttachmentRow(org, id string) ([]byte, error) {
	k, ok := keyOf(filepath.Join(legacyAttachmentDir(org), id))
	if !ok {
		return nil, fs.ErrNotExist
	}
	ctx, cancel := pgCtx()
	defer cancel()
	var b []byte
	err := pgQueryRow(ctx, `select data from omixor_files where path = $1`, k).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fs.ErrNotExist
	}
	return b, err
}

type legacyAttachment struct{ org, id string }

// legacyAttachmentList 舊存放裡的附件:true = 從 omixor_files 讀,false = 從資料夾讀(兩邊都有的用資料庫的)
func legacyAttachmentList() (map[legacyAttachment]bool, error) {
	out := map[legacyAttachment]bool{}
	dirs, err := os.ReadDir(filepath.Join(DataRoot(), "orgs"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, d := range dirs {
		files, err := os.ReadDir(legacyAttachmentDir(d.Name()))
		if err != nil {
			continue // 沒有 attachments 資料夾
		}
		for _, f := range files {
			if f.Type().IsRegular() && service.ValidAttachmentID(f.Name()) {
				out[legacyAttachment{d.Name(), f.Name()}] = false
			}
		}
	}
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgQuery(ctx, `select path from omixor_files where path like $1`, legacyAttachmentLike)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		if s := strings.Split(path, "/"); len(s) == 4 && s[2] == "attachments" && service.ValidAttachmentID(s[3]) {
			out[legacyAttachment{s[1], s[3]}] = true
		}
	}
	return out, rows.Err()
}

// migrateLegacyAttachments 把舊存放的附件複製進核心。冪等(核心已經有的略過),每次建本機核心時跑;只在有資料庫時呼叫。
// 核心沒有那個組織、內容不合核心規則的留在原處;任何失敗只記 log,不擋啟動。回傳這次複製了幾張
func migrateLegacyAttachments(svc *service.Service, p *model.Principal) (copied int) {
	list, err := legacyAttachmentList()
	if err != nil {
		log.Printf("附件搬進核心:讀舊附件清單失敗,下次啟動再試:%v", err)
		return 0
	}
	failed := 0
	for a, inDB := range list {
		if _, err := svc.Store.GetAttachment(a.org, a.id); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			failed++
			log.Printf("附件搬進核心:%s/%s:%v", a.org, a.id, err)
			continue
		}
		var data []byte
		if inDB {
			data, err = legacyAttachmentRow(a.org, a.id)
		} else {
			data, err = os.ReadFile(filepath.Join(legacyAttachmentDir(a.org), a.id))
		}
		if err == nil {
			err = svc.ImportAttachment(p, a.org, a.id, data)
		}
		var se *service.Error
		switch {
		case err == nil:
			copied++
		case errors.As(err, &se) && se.Code == http.StatusNotFound: // 組織不在核心(已刪除)
		default:
			failed++
			log.Printf("附件搬進核心:%s/%s:%v", a.org, a.id, err)
		}
	}
	if copied > 0 || failed > 0 {
		log.Printf("附件搬進核心:複製 %d 張、失敗 %d 張(舊資料保留)", copied, failed)
	}
	return copied
}

// removeLegacyAttachments 刪除組織時清掉舊存放(核心那一份由核心刪);不清的話同一個 id 的新組織下次啟動會搬到舊圖
func removeLegacyAttachments(org string) {
	if !ValidOrgID(org) {
		return
	}
	if pgPool() != nil {
		ctx, cancel := pgCtx()
		defer cancel()
		if _, err := pgExec(ctx, `delete from omixor_files where starts_with(path, $1)`, "orgs/"+org+"/attachments/"); err != nil {
			log.Printf("刪除組織 %s 的舊附件:%v", org, err)
		}
	}
	if err := os.RemoveAll(legacyAttachmentDir(org)); err != nil {
		log.Printf("刪除組織 %s 的舊附件資料夾:%v", org, err)
	}
}

func init() {
	OnOrgDelete(func(_ *App, id, _ string) { removeLegacyAttachments(id) })
}
