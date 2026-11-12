package model

import (
	"strings"
	"testing"
)

const sampleDoc = `前言一行

# 標題

## 定位

- 一句話:動態牆

### 細節

內容

## 待決定

- 誰來寫

` + "```md\n## 這不是標題\n```" + `
`

func TestDocOutline(t *testing.T) {
	o := DocOutline(sampleDoc)
	got := []string{}
	for _, s := range o {
		got = append(got, s.ID+":"+s.Title+":"+string(rune('0'+s.Level)))
	}
	want := "s0:(開頭):0 s1:標題:1 s2:定位:2 s3:細節:3 s4:待決定:2"
	if strings.Join(got, " ") != want {
		t.Fatalf("大綱 %v", got)
	}
	if o[2].Start != 5 || o[2].End != 12 {
		t.Fatalf("定位 含子標題到下一個同級標題前:%+v", o[2])
	}
	if o[4].End != DocLineCount(sampleDoc) {
		t.Fatal("最後一段到結尾", o[4])
	}
}

func TestFindAndRead(t *testing.T) {
	s, err := FindDocSection(sampleDoc, "## 待決定")
	if err != nil || s.ID != "s4" {
		t.Fatal(s, err)
	}
	if _, err := FindDocSection(sampleDoc, "沒有"); err == nil {
		t.Fatal("找不到要錯")
	}
	txt, a, b := DocLines(sampleDoc, s.Start, s.End)
	if !strings.HasPrefix(txt, "## 待決定") || a != s.Start || b != s.End {
		t.Fatal(txt, a, b)
	}
	if _, a, b := DocLines(sampleDoc, 0, 999); a != 1 || b != DocLineCount(sampleDoc) {
		t.Fatal("範圍收斂", a, b)
	}
	m := DocGrep(sampleDoc, "動態", 1, 0)
	if len(m) != 1 || m[0].Line != 7 || m[0].Section != "s2" || !strings.Contains(m[0].Text, "7: - 一句話") {
		t.Fatalf("%+v", m)
	}
}

func TestEdits(t *testing.T) {
	if _, err := ApplyTextEdits(sampleDoc, []TextEdit{{Old: "##", New: "#"}}); err == nil || !strings.Contains(err.Error(), "出現") {
		t.Fatal("不唯一要錯", err)
	}
	if _, err := ApplyTextEdits(sampleDoc, []TextEdit{{Old: "一句話", New: "x"}, {Old: "沒有這段", New: "y"}}); err == nil {
		t.Fatal("一個失敗整個失敗")
	}
	out, err := ApplyTextEdits(sampleDoc, []TextEdit{{Old: "動態牆", New: "科技新知動態牆"}})
	if err != nil || !strings.Contains(out, "科技新知動態牆") {
		t.Fatal(out, err)
	}
	if a, b := ChangedLines(sampleDoc, out); a != 7 || b != 7 {
		t.Fatal("改到第 7 行", a, b)
	}
	if DocVersion(out) == DocVersion(sampleDoc) || DocVersion(sampleDoc) != DocVersion(sampleDoc) {
		t.Fatal("版本跟著內文變")
	}

	r, err := ReplaceDocSection(sampleDoc, "定位", "- 新的定位")
	if err != nil || !strings.Contains(r, "## 定位\n\n- 新的定位\n\n## 待決定") || strings.Contains(r, "細節") {
		t.Fatalf("整段替換(含子標題):%q %v", r, err)
	}
	ap, err := AppendToDocSection(sampleDoc, "s2", "- 補充")
	if err != nil || !strings.Contains(ap, "內容\n\n- 補充\n\n## 待決定") {
		t.Fatalf("段尾附加:%q %v", ap, err)
	}
	ins, err := AppendToDocSection(sampleDoc, "s4", "## 新段落\n\n文字")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(ins), "## 新段落\n\n文字") {
		t.Fatalf("最後一段後面插入新段落:%q %v", ins, err)
	}
	if o := DocOutline(ins); o[len(o)-1].Title != "新段落" || o[len(o)-1].Level != 2 {
		t.Fatal("插入後是同級段落", o)
	}
}
