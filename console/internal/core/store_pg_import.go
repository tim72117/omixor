package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
)

// 把資料夾裡的 JSON 檔匯入資料庫(omixor-web import)

// ImportDir 把資料夾裡的 JSON 檔匯入資料庫(已存在的會覆蓋);回傳匯入的路徑
func ImportDir(dir string) ([]string, error) {
	if pgPool() == nil {
		return nil, errors.New("沒有連資料庫")
	}
	var done []string
	err := walkJSON(dir, func(rel string, data []byte) error {
		if rel == "mode.json" { // 來源機器的連線模式,不是這台的資料(見 remote_removed.go)
			return nil
		}
		if !json.Valid(data) {
			log.Printf("略過 %s:不是有效的 JSON", rel)
			return nil
		}
		// 組織的 Issue 交給本機核心匯入(已存在的同樣覆蓋)
		if parts := strings.Split(rel, "/"); len(parts) == 3 && parts[0] == "orgs" && parts[2] == "issues.json" {
			list, err := decodeIssueFile(data)
			if err != nil {
				log.Printf("略過 %s:不是 Issue 清單", rel)
				return nil
			}
			if _, err := ImportIssues(parts[1], list, true); err != nil {
				return fmt.Errorf("%s:%w", rel, err)
			}
			done = append(done, rel)
			return nil
		}
		if err := (pgBackend{}).Write(rel, data); err != nil {
			return err
		}
		done = append(done, rel)
		return nil
	})
	return done, err
}

func walkJSON(dir string, f func(rel string, data []byte) error) error {
	root := os.DirFS(dir)
	return fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "deleted-orgs" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".json") || strings.Contains(p, ".unreadable-") {
			return nil
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		return f(p, b)
	})
}
