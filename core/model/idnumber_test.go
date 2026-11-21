package model

import "testing"

func TestIDNumberAllKinds(t *testing.T) {
	kinds := []struct {
		prefix string
		num    func(id string) int
	}{
		{"ISS-", func(id string) int { return Issue{ID: id}.Number() }},
		{"T-", func(id string) int { return AgentTask{ID: id}.Number() }},
		{"DOC-", func(id string) int { return CabinetDoc{ID: id}.Number() }},
		{"CH-", func(id string) int { return Channel{ID: id}.Number() }},
		{"M-", func(id string) int { return ChannelMessage{ID: id}.Number() }},
		{"INC-", func(id string) int { return Incident{ID: id}.Number() }},
		{"PRJ-", func(id string) int { return Project{ID: id}.Number() }},
	}
	for _, k := range kinds {
		cases := []struct {
			suffix string
			want   int
		}{
			{"12", 12}, {"0", 0}, {"007", 7}, {"2147483647", 2147483647},
			{"2147483648", 0}, {"99999999999999999999", 0},
			{"", 0}, {"abc", 0}, {"1a", 0}, {"-5", 0}, {"+5", 0}, {"1 2", 0}, {"１２", 0},
		}
		if k.prefix == "PRJ-" { // 專案數字最多 9 位
			cases = append(cases, struct {
				suffix string
				want   int
			}{"999999999", 999999999}, struct {
				suffix string
				want   int
			}{"1000000000", 0})
			for i := range cases {
				if cases[i].suffix == "2147483647" {
					cases[i].want = 0
				}
			}
		}
		for _, c := range cases {
			if got := k.num(k.prefix + c.suffix); got != c.want {
				t.Errorf("%s%s = %d, want %d", k.prefix, c.suffix, got, c.want)
			}
		}
		lower := []byte(k.prefix + "5")
		for i := range lower {
			if lower[i] >= 'A' && lower[i] <= 'Z' {
				lower[i] += 'a' - 'A'
			}
		}
		for _, id := range []string{string(lower), "", k.prefix[:len(k.prefix)-1] + "5", "X" + k.prefix + "5", "5"} {
			want := 5
			if id != string(lower) {
				want = 0
			}
			if got := k.num(id); got != want {
				t.Errorf("id %q = %d, want %d", id, got, want)
			}
		}
		// 前後空白:只有專案與 MessageNumber 容忍
		if got := k.num(" " + k.prefix + "5"); (got != 0) != (k.prefix == "PRJ-") {
			t.Errorf("leading space %s = %d", k.prefix, got)
		}
	}
	if MessageNumber(" M-5 ") != 5 || MessageNumber("M-x") != 0 {
		t.Error("MessageNumber trim")
	}
	if ProjectNumber(" prj-3 ") != 3 {
		t.Error("ProjectNumber trim")
	}
}

func TestDedupExactVsCleanLabels(t *testing.T) {
	in := []string{"a", "A", "a", "", "b"}
	if got := DedupExact(in); len(got) != 4 || got[0] != "a" || got[1] != "A" || got[2] != "" || got[3] != "b" {
		t.Errorf("DedupExact = %q", got)
	}
	if got := DedupExact(nil); got == nil || len(got) != 0 {
		t.Errorf("DedupExact(nil) = %#v", got)
	}
	if got := CleanLabels([]string{" a ", "A", "", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("CleanLabels = %q", got)
	}
}
