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
		if _, ok := editionRank[f.Edition]; !ok {
			t.Fatalf("版本要是 stable 或 dev:%s %q", f.Key, f.Edition)
		}
		if f.Experimental && f.Edition != EditionDev {
			t.Fatalf("實驗功能只能在開發版:%s", f.Key)
		}
		if f.Required && f.Edition != EditionStable {
			t.Fatalf("必要的功能要在正式版:%s", f.Key)
		}
		if f.Parent != "" {
			if p, _ := FeatureByKey(f.Parent); !EditionIncludes(f.Edition, p.Edition) {
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

// Enabled:必要的一律開;頁面關了分頁不顯示;分頁全關時頁面也不顯示;沒登記的 key 當作開
func TestFeatureEnabled(t *testing.T) {
	s := OrgSystemSettings{Features: map[string]bool{"settings": false, "overview": false}}
	if !s.Enabled("settings") || !s.Enabled("overview") || !s.Enabled("system") {
		t.Fatal("必要的一律開")
	}
	if !s.Enabled("development") || !s.Enabled("development.issues") || s.Enabled("development.board") {
		t.Fatal("預設:已實作的開、實驗的關")
	}
	if !s.Enabled("nope.unknown") {
		t.Fatal("沒登記的 key 當作開")
	}
	s.Features["development"] = false
	if s.Enabled("development") || s.Enabled("development.issues") || !s.Own("development.issues") {
		t.Fatal("頁面關了分頁不顯示(分頁自己的開關不變)")
	}
	s.Features["development"] = true
	s.Features["pipelines.voice"] = false
	if s.Enabled("pipelines") {
		t.Fatal("分頁全關時頁面也不顯示")
	}
	s.Features["pipelines.runs"] = true
	if BuildEdition == EditionDev {
		if !s.Enabled("pipelines") || !s.Enabled("pipelines.runs") {
			t.Fatal("開發版:打開實驗分頁")
		}
	} else {
		if s.Enabled("pipelines") || s.Enabled("pipelines.runs") || s.Own("pipelines.runs") {
			t.Fatal("正式版:沒編進來的分頁存著開也不生效,也不算頁面的分頁")
		}
		if !s.Features["pipelines.runs"] {
			t.Fatal("存著的值保留")
		}
	}
	if !s.Enabled("channels") {
		t.Fatal("沒有分頁的頁面")
	}
}

// 版本:大的版本包含小的;不認得的一律不包含;這次編譯可用的清單照 BuildEdition 過濾
func TestEdition(t *testing.T) {
	if !EditionIncludes(EditionDev, EditionStable) || !EditionIncludes(EditionDev, EditionDev) || !EditionIncludes(EditionStable, EditionStable) {
		t.Fatal("包含自己與較小的版本")
	}
	if EditionIncludes(EditionStable, EditionDev) || EditionIncludes(EditionDev, "beta") || EditionIncludes("beta", EditionStable) {
		t.Fatal("正式版不含開發版;不認得的不包含")
	}
	avail := AvailableFeatures()
	for _, f := range avail {
		if !f.Available() {
			t.Fatal(f.Key)
		}
	}
	fa, _ := FeatureByKey("familyAssistant")
	switch BuildEdition {
	case EditionDev:
		if len(avail) != len(Features) || !fa.Available() {
			t.Fatal("開發版有全部功能")
		}
	case EditionStable:
		if len(avail) >= len(Features) || fa.Available() {
			t.Fatal("正式版沒有開發版的功能")
		}
		for _, f := range avail {
			if f.Edition != EditionStable || f.Experimental {
				t.Fatal("正式版只有正式版的功能:", f.Key)
			}
		}
		s := OrgSystemSettings{Features: map[string]bool{"familyAssistant": true}}
		if s.Enabled("familyAssistant") || s.Own("familyAssistant") {
			t.Fatal("正式版:存著開也不生效")
		}
	default:
		t.Fatal("BuildEdition 不認得:", BuildEdition)
	}
}
