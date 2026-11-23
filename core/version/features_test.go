package version

import "testing"

// Info().Features 是複本:呼叫端改動元素或 append 都不能影響全域 Features 與下一次的 Info
func TestInfoFeaturesIsCopy(t *testing.T) {
	want := append([]string(nil), Features...)
	f := Info().Features
	f[0] = "changed"
	_ = append(f[:1], "extra")
	if g := Info().Features; len(g) != len(want) || g[0] != want[0] {
		t.Errorf("下一次 Info().Features = %v,要 %v", g, want)
	}
	if len(Features) != len(want) || Features[0] != want[0] {
		t.Errorf("全域 Features = %v,要 %v", Features, want)
	}
}
