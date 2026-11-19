package office

// 命令解析:照 office3d/viewer/js/command.js 逐段搬(規則、詞表、錯誤訊息相同),
// 讓後端收到 text 時就能先驗證角色與地點,不合法直接回 400,不用等檢視器。
// 差異:沒有「目前選取的角色」(後端沒有選取),省略角色一律是錯誤;
// NFKC 只做全形 ASCII → 半形(command.js 用 String.normalize('NFKC'),這裡不引進 x/text)。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	allWords   = []string{"全部", "全部人", "所有人", "大家", "每個人", "全員", "all", "everyone", "everybody"}
	separators = []string{"、", ",", "\uFF0C", "和", "與", "跟", "and", "&", "+"}
	verbTable  = []struct {
		typ   string
		words []string
	}{
		{"return", []string{"回座位", "回到座位", "回自己座位", "回自己的座位", "回位子", "回到位子", "回座", "回位", "回去", "回來", "歸位", "坐下", "坐回去",
			"returntoseat", "returntoseats", "return", "goback", "back", "gohome", "sitdown", "sit"}},
		{"find", []string{"去找", "找到", "找", "去見", "見", "跟上", "跟著", "走向", "find", "meet", "visit", "follow"}},
		{"goto", []string{"走到", "走去", "前往", "去到", "移動到", "移到", "過去", "到", "去", "goto", "walkto", "moveto", "go", "walk", "move", "head"}},
		{"stop", []string{"停下來", "停下", "停止", "停", "別動", "stop", "halt", "wait"}},
	}
	seatSuffix     = []string{"的座位", "座位", "的位子", "位子", "的位置", "seat", "的椅子", "chair"}
	zoneSuffix     = []string{"的工作區", "工作區", "的桌子", "桌子", "辦公桌", "desk", "workspace", "workstation", "zone"}
	targetTrimHead = []string{"to", "the", "到"}
	targetTrimTail = []string{"那裡", "那邊", "那兒", "旁邊", "身邊", "去", "一下", "吧", "!", "\uFF01", "。", "."}

	possessiveRe = regexp.MustCompile(`['’]s\b`)
	stripChars   = "\t\n\v\f\r -_'\"’「」『』()()【】 　"
	splitRe      = regexp.MustCompile(`[;\x{FF1B}\n]+`) // ; 、全形;、換行
)

// foldWidth 全形 ASCII(U+FF01–FF5E)轉半形、全形空白轉空白(NFKC 的子集)
func foldWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			return r - 0xFEE0
		case r == 0x3000:
			return ' '
		}
		return r
	}, s)
}

// Normalize 同 command.js 的 normalize
func Normalize(s string) string {
	s = strings.ToLower(foldWidth(s))
	s = possessiveRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(stripChars, r) || r == ' ' {
			return -1
		}
		return r
	}, s)
}

// Target 動作的目標
type Target struct {
	Kind string `json:"kind"` // place | agent
	ID   string `json:"id"`
}

// Action 一個動作(格式同 command.js 的 action,檢視器的 World.runAction 直接吃)
type Action struct {
	Type   string   `json:"type"` // goto | find | return | stop
	Agents []string `json:"agents"`
	Target *Target  `json:"target,omitempty"`
}

type parser struct {
	agents     []AgentInfo
	places     []PlaceInfo
	agentAlias map[string]string
	placeAlias map[string]string
	agentKeys  []string
	placeKeys  []string
	allKeys    []string
	sepKeys    []string
	verbs      []struct{ w, typ string }
	seatSfx    []string
	zoneSfx    []string
}

func byLenDesc(keys []string) []string {
	sort.SliceStable(keys, func(i, j int) bool { return utf8.RuneCountInString(keys[i]) > utf8.RuneCountInString(keys[j]) })
	return keys
}

func normAll(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, Normalize(s))
	}
	return out
}

func newParser(agents []AgentInfo, places []PlaceInfo) *parser {
	p := &parser{agents: agents, places: places, agentAlias: map[string]string{}, placeAlias: map[string]string{}}
	for _, a := range agents {
		for _, al := range append([]string{a.ID, a.Name}, a.Aliases...) {
			k := Normalize(al)
			if _, ok := p.agentAlias[k]; !ok {
				p.agentKeys = append(p.agentKeys, k)
			}
			p.agentAlias[k] = a.ID // 同 JS 的 Map.set:後面的蓋前面的
		}
	}
	for _, pl := range places {
		for _, al := range append([]string{pl.ID, pl.Name}, pl.Aliases...) {
			k := Normalize(al)
			if _, ok := p.placeAlias[k]; !ok {
				p.placeAlias[k] = pl.ID
				p.placeKeys = append(p.placeKeys, k)
			}
		}
	}
	byLenDesc(p.agentKeys)
	byLenDesc(p.placeKeys)
	p.allKeys = byLenDesc(normAll(allWords))
	p.sepKeys = byLenDesc(normAll(separators))
	for _, v := range verbTable {
		for _, w := range v.words {
			p.verbs = append(p.verbs, struct{ w, typ string }{Normalize(w), v.typ})
		}
	}
	sort.SliceStable(p.verbs, func(i, j int) bool {
		return utf8.RuneCountInString(p.verbs[i].w) > utf8.RuneCountInString(p.verbs[j].w)
	})
	p.seatSfx = byLenDesc(normAll(seatSuffix))
	p.zoneSfx = byLenDesc(normAll(zoneSuffix))
	return p
}

func (p *parser) agentName(id string) string {
	for _, a := range p.agents {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}

func (p *parser) placeName(id string) string {
	for _, pl := range p.places {
		if pl.ID == id {
			return pl.Name
		}
	}
	return id
}

type agentMatch struct {
	all bool
	id  string
	n   int
}

func (p *parser) matchAgentAt(s string, pos int) *agentMatch {
	for _, k := range p.allKeys {
		if k != "" && strings.HasPrefix(s[pos:], k) {
			return &agentMatch{all: true, n: len(k)}
		}
	}
	for _, k := range p.agentKeys {
		if k != "" && strings.HasPrefix(s[pos:], k) {
			return &agentMatch{id: p.agentAlias[k], n: len(k)}
		}
	}
	return nil
}

// resolveAgent 角色 id / 名稱 / 別名 → id(找不到回 "")
func (p *parser) resolveAgent(t string) string { return p.agentAlias[Normalize(t)] }

// resolvePlace 地點 id / 名稱 / 別名 → id(找不到回 "")
func (p *parser) resolvePlace(t string) string { return p.placeAlias[Normalize(t)] }

// resolveTarget 同 command.js 的 resolveTarget
func (p *parser) resolveTarget(raw string) *Target {
	t := Normalize(raw)
	for changed := true; changed; {
		changed = false
		for _, h := range targetTrimHead {
			if _, isPlace := p.placeAlias[t]; strings.HasPrefix(t, h) && len(t) > len(h) && !isPlace {
				rest := t[len(h):]
				_, restPlace := p.placeAlias[rest]
				if restPlace || p.agentAlias[rest] != "" || utf8.RuneCountInString(rest) >= 2 {
					t, changed = rest, true
				}
			}
		}
		for _, tl := range targetTrimTail {
			if strings.HasSuffix(t, tl) && len(t) > len(tl) {
				t, changed = t[:len(t)-len(tl)], true
			}
		}
	}
	if t == "" {
		return nil
	}
	if id, ok := p.placeAlias[t]; ok {
		return &Target{Kind: "place", ID: id}
	}
	for _, x := range []struct {
		sfx    []string
		prefix string
	}{{p.seatSfx, "seat_"}, {p.zoneSfx, "zone_"}} {
		for _, sfx := range x.sfx {
			if strings.HasSuffix(t, sfx) && len(t) > len(sfx) {
				who := strings.TrimSuffix(t[:len(t)-len(sfx)], "的")
				if id := p.agentAlias[who]; id != "" {
					return &Target{Kind: "place", ID: x.prefix + id}
				}
			}
		}
	}
	if id := p.agentAlias[t]; id != "" {
		return &Target{Kind: "agent", ID: id}
	}
	if utf8.RuneCountInString(t) >= 2 {
		for _, k := range p.placeKeys {
			if utf8.RuneCountInString(k) >= 2 && (strings.Contains(k, t) || strings.Contains(t, k)) {
				return &Target{Kind: "place", ID: p.placeAlias[k]}
			}
		}
	}
	return nil
}

func (p *parser) agentNames() string {
	n := make([]string, 0, len(p.agents))
	for _, a := range p.agents {
		n = append(n, a.Name)
	}
	return strings.Join(n, "、")
}

func (p *parser) publicPlaceNames() string {
	n := []string{}
	for _, pl := range p.places {
		if pl.Kind != "seat" && pl.Kind != "workspace" {
			n = append(n, pl.Name)
		}
	}
	return strings.Join(n, "、")
}

func (p *parser) parseOne(text string) (*Action, error) {
	s := Normalize(text)
	if s == "" {
		return nil, fmt.Errorf("請輸入命令,例如「工程師 去 會議室」")
	}
	pos := 0
	subj := []string{}
	all := false
	for {
		m := p.matchAgentAt(s, pos)
		if m == nil {
			break
		}
		if m.all {
			all = true
		} else if !contains(subj, m.id) {
			subj = append(subj, m.id)
		}
		pos += m.n
		sep := false
		for _, k := range p.sepKeys {
			if strings.HasPrefix(s[pos:], k) && p.matchAgentAt(s, pos+len(k)) != nil {
				pos += len(k)
				sep = true
				break
			}
		}
		if !sep {
			break
		}
	}
	if all {
		subj = subj[:0]
		for _, a := range p.agents {
			subj = append(subj, a.ID)
		}
	}
	verb := ""
	for _, v := range p.verbs {
		if strings.HasPrefix(s[pos:], v.w) {
			verb = v.typ
			pos += len(v.w)
			break
		}
	}
	rest := s[pos:]
	if verb == "" {
		switch {
		case len(subj) > 0 && rest != "":
			verb = "goto"
		case len(subj) > 0 && rest == "":
			n := p.agentName(subj[0])
			return nil, fmt.Errorf("「%s」缺少動作。用法:%s 去 會議室/%s 找 CEO/%s 回座位", text, n, n, n)
		default:
			return nil, fmt.Errorf("看不懂「%s」:找不到角色或動作。角色可用:%s", text, p.agentNames())
		}
	}
	if len(subj) == 0 {
		return nil, fmt.Errorf("「%s」沒有指定角色,請在命令開頭寫角色名稱(例如「工程師 去 會議室」)", text)
	}
	if verb == "return" || verb == "stop" {
		if verb == "return" && rest != "" {
			if tg := p.resolveTarget(rest); tg != nil {
				typ := "goto"
				if tg.Kind == "agent" {
					typ = "find"
				}
				return &Action{Type: typ, Agents: subj, Target: tg}, nil
			}
		}
		return &Action{Type: verb, Agents: subj}, nil
	}
	if rest == "" {
		return nil, fmt.Errorf("「%s」缺少目的地", text)
	}
	tg := p.resolveTarget(rest)
	if tg == nil {
		return nil, fmt.Errorf("找不到地點或角色「%s」。可用地點:%s;或「<角色>的座位」「<角色>的工作區」", rest, p.publicPlaceNames())
	}
	if tg.Kind == "agent" {
		others := []string{}
		for _, id := range subj {
			if id != tg.ID {
				others = append(others, id)
			}
		}
		if len(others) == 0 {
			return nil, fmt.Errorf("%s 不能去找自己", p.agentName(tg.ID))
		}
		return &Action{Type: "find", Agents: others, Target: tg}, nil
	}
	return &Action{Type: "goto", Agents: subj, Target: tg}, nil
}

// countParts 用「;」「;」換行拆開後,非空的命令條數
func countParts(text string) int {
	n := 0
	for _, part := range splitRe.Split(text, -1) {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}

// parse 多條命令用「;」「;」或換行分開;回傳成功的動作與每條失敗的原因
func (p *parser) parse(text string) ([]Action, []string) {
	var actions []Action
	var errs []string
	for _, part := range splitRe.Split(text, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, err := p.parseOne(part)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		actions = append(actions, *a)
	}
	if len(actions) == 0 && len(errs) == 0 {
		errs = append(errs, "請輸入命令")
	}
	return actions, errs
}

// describe 同 command.js 的 describe
func (p *parser) describe(a Action) string {
	who := "全部"
	if len(a.Agents) != len(p.agents) {
		n := make([]string, 0, len(a.Agents))
		for _, id := range a.Agents {
			n = append(n, p.agentName(id))
		}
		who = strings.Join(n, "、")
	}
	switch a.Type {
	case "return":
		return who + " 回座位"
	case "stop":
		return who + " 停下"
	case "find":
		return who + " 去找 " + p.agentName(a.Target.ID)
	}
	return who + " 去 " + p.placeName(a.Target.ID)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
