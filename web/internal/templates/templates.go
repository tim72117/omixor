// Package templates 讀範本資料(示範用的假資料)。範本放在 repo 最外層的 templates/(JSON 與文件轉換器),
// 預設不載入:沒有開的時候,預設組織和新組織一樣是空的。要用時設環境變數 OMIXOR_TEMPLATES 才會讀;
// 讀的是磁碟上的檔案,不編進執行檔。單檔太大的用目錄分檔,目錄裡的檔案照檔名排序讀入(前面的數字是順序):
//
//	org.json            第一次啟動建立的預設組織(名稱、圖示、顏色)
//	agents/             預設組織的 AI 角色與排程,一個角色一個檔
//	cabinet/            預設組織的文件櫃結構,一個區塊一個檔
//	seed/               預設組織的示範畫面資料,一個資料集一個檔(_meta.json 記匯出時間)
//	doc/                文件櫃的文件範本:通用轉換器 docrender.js 與範例網頁 docpage.html(Swift 版共用)
//
// 開啟方式:OMIXOR_TEMPLATES=<資料夾>(指定位置),或 OMIXOR_TEMPLATES=1(自己找:
// 目前資料夾和上層的 templates/,再找執行檔所在資料夾和上層的 templates/)。
// 開了卻找不到,就當沒有範本,並在紀錄裡說一次。
// seed/ 是 Swift 版匯出的,不要手改:重新匯出後用 templates/tools/split-seed 拆進來。
package templates

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var warnOnce sync.Once

// Enabled 有沒有開範本(預設沒有)
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OMIXOR_TEMPLATES"))) {
	case "", "0", "off", "false", "no":
		return false
	}
	return true
}

// Dir 範本資料夾;沒開或找不到回傳 ""
func Dir() string {
	if !Enabled() {
		return ""
	}
	switch v := strings.TrimSpace(os.Getenv("OMIXOR_TEMPLATES")); strings.ToLower(v) {
	case "1", "on", "true", "yes", "auto":
	default:
		return v
	}
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, s := range starts {
		for d := s; ; d = filepath.Dir(d) {
			c := filepath.Join(d, "templates")
			if st, err := os.Stat(filepath.Join(c, "org.json")); err == nil && !st.IsDir() {
				return c
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	return ""
}

func read(name string) []byte {
	d := Dir()
	if d == "" {
		if Enabled() {
			warnOnce.Do(func() {
				log.Printf("找不到範本資料夾 templates/(OMIXOR_TEMPLATES 可以指定位置):先當沒有範本")
			})
		}
		return nil
	}
	b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(name)))
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("範本 %s 讀不出來:%v", name, err)
		}
		return nil
	}
	return b
}

// Org 預設組織;範本不在時是 omixor / building.2 / blue
func Org() (name, symbol, color string) {
	name, symbol, color = "omixor", "building.2", "blue"
	var o struct{ Name, Symbol, Color string }
	if json.Unmarshal(read("org.json"), &o) == nil {
		if o.Name != "" {
			name = o.Name
		}
		if o.Symbol != "" {
			symbol = o.Symbol
		}
		if o.Color != "" {
			color = o.Color
		}
	}
	return
}

// DocRenderJS 文件櫃的通用轉換器(Swift 版的 DocRenderJS.swift 是它的副本)
func DocRenderJS() string { return string(read("doc/docrender.js")) }

func list(dir string) [][]byte {
	d := Dir()
	if d == "" {
		read("") // 只為了說一次找不到
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(d, dir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), "_") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([][]byte, 0, len(names))
	for _, n := range names {
		out = append(out, read(dir+"/"+n))
	}
	return out
}

// Array 目錄裡的每個檔各是一個物件,依序組成 JSON 陣列;目錄不在就是 []
func Array(dir string) []byte {
	out := []byte("[")
	for i, p := range list(dir) {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, p...)
	}
	return append(out, ']')
}

// Seed 示範畫面資料:匯出時間與各資料集(檔名去掉 .json 就是資料集名稱);範本不在時 collections 是空的
func Seed() (exportedAt string, collections map[string]json.RawMessage) {
	collections = map[string]json.RawMessage{}
	d := Dir()
	if d == "" {
		read("")
		return "", collections
	}
	var meta struct {
		ExportedAt string `json:"exportedAt"`
	}
	_ = json.Unmarshal(read("seed/_meta.json"), &meta)
	entries, err := os.ReadDir(filepath.Join(d, "seed"))
	if err != nil {
		return meta.ExportedAt, collections
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") || strings.HasPrefix(n, "_") {
			continue
		}
		collections[strings.TrimSuffix(n, ".json")] = read("seed/" + n)
	}
	return meta.ExportedAt, collections
}
