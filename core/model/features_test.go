package model

import (
	"strings"
	"testing"
)

// 功能清單:key 不重複;分頁的所屬頁面有登記且排在前面;實驗功能預設關、有說明;必要的預設開
func TestFeatureCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Features {
		if f.Key == "" || f.Name == "" || f.Description == "" {
			t.Fatalf("欄位不完整:%+v", f)
		}
		if seen[f.Key] {
			t.Fatalf("key 重複:%s", f.Key)
		}
		seen[f.Key] = true
		if f.Parent != "" {
			if !seen[f.Parent] {
				t.Fatalf("%s 的所屬頁面 %s 要先登記", f.Key, f.Parent)
			}
			if !strings.HasPrefix(f.Key, f.Parent+".") {
				t.Fatalf("分頁的 key 要是「頁面.分頁」:%s", f.Key)
			}
			if p, _ := FeatureByKey(f.Parent); p.Parent != "" {
				t.Fatalf("只有兩層:%s", f.Key)
			}
			if f.Required {
				t.Fatalf("分頁不能是必要的:%s", f.Key)
			}
		}
		if f.Experimental && (f.Default || f.ExperimentalNote == "") {
			t.Fatalf("實驗功能預設關、要有說明:%s", f.Key)
		}
		if !f.Experimental && !f.Default {
			t.Fatalf("已實作的預設開:%s", f.Key)
		}
		if f.Required && (!f.Default || f.Experimental) {
			t.Fatalf("必要的預設開:%s", f.Key)
		}
		if f.Edition != EditionStable && f.Edition != EditionDev {
			t.Fatalf("版本要是 stable 或 dev:%s %q", f.Key, f.Edition)
		}
		if f.Experimental && f.Edition != EditionDev {
			t.Fatalf("實驗功能只能在開發版:%s", f.Key)
		}
		if f.Required && f.Edition != EditionStable {
			t.Fatalf("必要的功能要在正式版:%s", f.Key)
		}
		if f.Parent != "" {
			if p, _ := FeatureByKey(f.Parent); p.Edition == EditionDev && f.Edition != EditionDev {
				t.Fatalf("分頁的版本不能比所屬頁面低(頁面是開發版,分頁也要是):%s", f.Key)
			}
		}
	}
	// 正式版的頁面:有分頁的話,至少一個分頁也在正式版(不然正式版的頁面是空的)
	for _, p := range Features {
		if p.Parent != "" || p.Edition != EditionStable {
			continue
		}
		hasChild, stableChild := false, false
		for _, c := range Features {
			if c.Parent == p.Key {
				hasChild = true
				stableChild = stableChild || c.Edition == EditionStable
			}
		}
		if hasChild && !stableChild {
			t.Fatalf("正式版的頁面 %s 要有正式版的分頁", p.Key)
		}
	}
	if f, _ := FeatureByKey("familyAssistant"); f.Edition != EditionDev {
		t.Fatal("家庭助理還在框架階段,屬於開發版")
	}
	for _, k := range []string{"overview", "settings", "system"} {
		if f, ok := FeatureByKey(k); !ok || !f.Required {
			t.Fatalf("%s 要是必要的", k)
		}
	}
}

// allOn 系統層全部開放(測試用)
func allOn() ServerFeatures {
	m := map[string]bool{}
	for _, f := range Features {
		m[f.Key] = true
	}
	return ServerFeatures{Features: m}
}

// Enabled:必要的一律開;頁面關了分頁不顯示;分頁全關時頁面也不顯示;沒登記的 key 當作開
func TestFeatureEnabled(t *testing.T) {
	srv := allOn()
	s := OrgSystemSettings{Features: map[string]bool{"settings": false, "overview": false}}
	if !s.Enabled(srv, "settings") || !s.Enabled(srv, "overview") || !s.Enabled(srv, "system") {
		t.Fatal("必要的一律開")
	}
	if !s.Enabled(srv, "development") || !s.Enabled(srv, "development.issues") || s.Enabled(srv, "development.overview") {
		t.Fatal("預設:已實作的開、實驗的關")
	}
	if !s.Enabled(srv, "nope.unknown") {
		t.Fatal("沒登記的 key 當作開")
	}
	s.Features["development"] = false
	if s.Enabled(srv, "development") || s.Enabled(srv, "development.issues") || !s.Own(srv, "development.issues") {
		t.Fatal("頁面關了分頁不顯示(分頁自己的開關不變)")
	}
	s.Features["development"] = true
	s.Features["pipelines.voice"] = false
	if s.Enabled(srv, "pipelines") {
		t.Fatal("分頁全關時頁面也不顯示")
	}
	s.Features["pipelines.runs"] = true
	if !s.Enabled(srv, "pipelines") || !s.Enabled(srv, "pipelines.runs") {
		t.Fatal("系統層開放時:打開實驗分頁")
	}
	// 系統層用預設(實驗分頁不開放):組織存著開也不生效,也不算頁面的分頁
	def := ServerFeatures{}
	if s.Enabled(def, "pipelines") || s.Enabled(def, "pipelines.runs") || s.Own(def, "pipelines.runs") {
		t.Fatal("系統層沒開放的分頁存著開也不生效")
	}
	if !s.Features["pipelines.runs"] {
		t.Fatal("存著的值保留")
	}
	if !s.Enabled(srv, "channels") {
		t.Fatal("沒有分頁的頁面")
	}
	// 頁面的分頁系統層全部沒開放:頁面自己開著就顯示(就像以前沒編進去的分頁不算)
	off := ServerFeatures{Features: map[string]bool{"pipelines.voice": false}}
	s2 := OrgSystemSettings{Features: map[string]bool{}}
	if !s2.Enabled(off, "pipelines") || s2.Enabled(off, "pipelines.voice") {
		t.Fatal("系統層沒開放的分頁不算頁面的分頁")
	}
	// 系統層關掉頁面:頁面與分頁都不可用
	off = ServerFeatures{Features: map[string]bool{"incidents": false}}
	if s2.Enabled(off, "incidents") || s2.Enabled(off, "incidents.list") || off.Has("incidents.list") || !off.Own("incidents.list") {
		t.Fatal("系統層關掉頁面,分頁也沒開放(分頁自己的開關不變)")
	}
}

// 系統層:預設照 Edition(原本正式版的開放、原本只有開發版的不開放);必要的永遠開放;沒登記的 key 當作開
func TestServerFeatures(t *testing.T) {
	def := ServerFeatures{}
	for _, f := range Features {
		want := f.Edition == EditionStable
		if f.SystemDefault() != want || def.Own(f.Key) != want {
			t.Fatalf("系統層預設:%s %v", f.Key, def.Own(f.Key))
		}
	}
	for _, k := range []string{"familyAssistant", "mcpGateway", "development.overview"} {
		if def.Has(k) {
			t.Fatalf("%s 預設不開放", k)
		}
	}
	if len(def.Available()) >= len(Features) || def.LegacyEdition() != EditionStable {
		t.Fatal("預設不是全部開放;相容的 edition 是 stable")
	}
	s := ServerFeatures{Features: map[string]bool{"settings": false, "familyAssistant": true, "channels": false}}
	if !s.Own("settings") || !s.Has("system") || !s.Has("familyAssistant") || s.Has("channels") || !s.Has("nope") {
		t.Fatal("必要的永遠開放;改過的照改過的;沒登記的當作開")
	}
	if s.LegacyEdition() != EditionDev {
		t.Fatal("開放了原本開發版的功能,相容的 edition 是 dev")
	}
	if len(allOn().Available()) != len(Features) {
		t.Fatal("全部開放時列全部")
	}
	c := s.Clone()
	c.Features["channels"] = true
	if s.Features["channels"] {
		t.Fatal("Clone 要深拷貝")
	}
}

// 退場的 key:存著的舊值當作新 key 的值(新 key 有值時以新 key 為準);搬移後只剩新 key
func TestLegacyFeatureKeys(t *testing.T) {
	if CanonicalFeatureKey("development.github") != "development.repos" || CanonicalFeatureKey("cabinet") != "cabinet" {
		t.Fatal("CanonicalFeatureKey")
	}
	if v, ok := StoredFeature(map[string]bool{"development.github": false}, "development.repos"); !ok || v {
		t.Fatal("只有舊 key 時用舊值")
	}
	if v, ok := StoredFeature(map[string]bool{"development.github": false, "development.repos": true}, "development.repos"); !ok || !v {
		t.Fatal("新 key 有值時以新 key 為準")
	}
	if _, ok := StoredFeature(map[string]bool{}, "development.repos"); ok {
		t.Fatal("都沒有時 ok = false")
	}
	m := map[string]bool{"development.github": false}
	MigrateLegacyFeatures(m)
	if v, ok := m["development.repos"]; !ok || v || len(m) != 1 {
		t.Fatal(m)
	}
	m = map[string]bool{"development.github": false, "development.repos": true}
	MigrateLegacyFeatures(m)
	if !m["development.repos"] || len(m) != 1 {
		t.Fatal(m)
	}
	for old := range LegacyFeatureKeys {
		if _, ok := FeatureByKey(old); ok {
			t.Fatal("退場的 key 不該再登記:", old)
		}
	}
}
