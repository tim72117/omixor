package core

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"omixor-core/model"
)

// web 畫面的保守預設(console/ui/src/features.ts 的 EXPERIMENTAL_FALLBACK)要和核心的實驗功能清單一致,
// 不然還沒讀到功能開關時,web 會把已實作的功能當成實驗(或反過來)
func TestExperimentalFallbackMatchesCore(t *testing.T) {
	src, err := os.ReadFile("../../ui/src/features.ts")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)EXPERIMENTAL_FALLBACK\s*=\s*new Set<string>\(\[(.*?)\]\)`).FindSubmatch(src)
	if body == nil {
		t.Fatal("features.ts 找不到 EXPERIMENTAL_FALLBACK = new Set<string>([...])")
	}
	web := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(body[1], -1) {
		web[string(m[1])] = true
	}
	core := map[string]bool{}
	for _, f := range model.Features {
		if f.Experimental {
			core[f.Key] = true
		}
	}
	var extra, missing []string
	for k := range web {
		if !core[k] {
			extra = append(extra, k)
		}
	}
	for k := range core {
		if !web[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	if len(extra) > 0 || len(missing) > 0 {
		t.Fatalf("features.ts 的 EXPERIMENTAL_FALLBACK 和 core/model/features.go 不一致:多了 [%s]、少了 [%s]",
			strings.Join(extra, ", "), strings.Join(missing, ", "))
	}
}
