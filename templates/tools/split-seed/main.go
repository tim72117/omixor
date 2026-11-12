// split-seed 把 Swift 版匯出的單一 seed.json 依資料集拆成目錄(templates/seed/<資料集>.json 與 _meta.json),
// 單檔太大不好看也不好比對。用法:
//
//	cd ../app && OPS_EXPORT_SEED=/tmp/seed.json swift test --filter SpecExportTests
//	go run templates/tools/split-seed/main.go /tmp/seed.json templates/seed
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("用法:split-seed <seed.json> <輸出資料夾>")
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	var seed struct {
		ExportedAt  string                     `json:"exportedAt"`
		Collections map[string]json.RawMessage `json:"collections"`
	}
	if err := json.Unmarshal(b, &seed); err != nil {
		log.Fatal(err)
	}
	dir := os.Args[2]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	write := func(name string, raw []byte) {
		var out bytes.Buffer
		if err := json.Indent(&out, raw, "", "  "); err != nil {
			log.Fatal(err)
		}
		out.WriteByte('\n')
		if err := os.WriteFile(filepath.Join(dir, name), out.Bytes(), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	meta, _ := json.Marshal(map[string]string{"exportedAt": seed.ExportedAt})
	write("_meta.json", meta)
	for name, raw := range seed.Collections {
		write(name+".json", raw)
	}
	fmt.Printf("拆成 %d 個資料集 → %s\n", len(seed.Collections), dir)
}
