package mcp

import (
	"fmt"
	"strings"

	"omixor-core/tools/flowchart"
	"omixor-core/tools/wireframe"
)

// 兩種圖互相提醒用:文件裡另一種圖的區塊數
func wireframeBlocks(md string) (int, string) { return len(wireframe.Blocks(md)), wireframe.Lang }
func flowchartBlocks(md string) (int, string) { return len(flowchart.Blocks(md)), flowchart.Lang }

// 圖表工具(wireframe_render、flowchart_render)共用的流程:描述 → 渲染 → 回傳 SVG,可順便存進文件櫃。
// 文件內文的格式是「說明 + 一段 ```<lang> 程式碼區塊放原始描述」,畫面顯示時再渲染成圖;
// 讀文件、存文件走同一個 Server 上的 docs / doc_save,文件權限(角色的 docAccess、角色端點的憑證角色)和直接呼叫它們一樣。

type diagramKind struct {
	noun   string // 線框圖、流程圖(錯誤訊息用)
	lang   string // 程式碼區塊的語言標記
	render func(spec any) (w, h int, warnings []string, svg string, err error)
	blocks func(md string) []string       // 文件裡的圖表區塊
	upsert func(md, src string) string    // 換掉第一個區塊(沒有就接在最後)
	fence  func(src string) string        // 包成程式碼區塊
	source func(spec any) (string, error) // 要存進區塊的文字
	// first 文件裡第一個這種圖的區塊原文(含 ``` 行)與語言標記;fenceLike 包成和它同語言的區塊
	first     func(md string) (block, lang string, ok bool)
	fenceLike func(lang, src string) string
	// others 文件裡「另一種圖」的區塊數與語言(更新時提醒:那些不會被換掉)
	others func(md string) (n int, lang string)
}

func renderDiagram(s *Server, c *Call, k diagramKind) (any, error) {
	agent := c.Args.Str("agent")
	withAgent := func(m map[string]any) map[string]any {
		if agent != "" {
			m["agent"] = agent
		}
		return m
	}
	// 讀一份文件的內文(依呼叫者/角色的文件權限)
	// 讀與存都用 RunNested:套用這次請求的工具開關與 Guard(角色端點關掉 docs / doc_save 的角色不能藉繪圖工具讀寫文件)
	readDoc := func(id string) (body, version string, err error) {
		out, err := s.RunNested(c, "docs", withAgent(map[string]any{"id": id, "full": true}))
		if err != nil {
			return "", "", err
		}
		m, _ := out.(map[string]any)
		body, _ = m["body"].(string)
		version, _ = m["version"].(string)
		return body, version, nil
	}
	readBody := func(id string) (string, error) {
		b, _, err := readDoc(id)
		return b, err
	}

	spec := c.Args.Raw["spec"]
	if str, ok := spec.(string); ok && strings.TrimSpace(str) == "" {
		spec = nil
	}
	if spec == nil {
		id := c.Args.Str("doc")
		if id == "" {
			return nil, Errorf("要給 spec(%s描述),或給 doc(文件 id)讀文件裡的 ```%s 區塊", k.noun, k.lang)
		}
		body, err := readBody(id)
		if err != nil {
			return nil, err
		}
		blocks := k.blocks(body)
		if len(blocks) == 0 {
			return nil, Errorf("文件 %s 裡沒有 ```%s 區塊", id, k.lang)
		}
		spec = blocks[0]
	}
	w, h, warns, svg, err := k.render(spec)
	if err != nil {
		return nil, ToolError{err.Error()}
	}
	out := map[string]any{"width": w, "height": h, "warnings": warns}
	if v, ok := c.Args.Raw["svg"].(bool); !ok || v {
		out["svg"] = svg
	}

	rawSave, has := c.Args.Raw["save"]
	if !has || rawSave == nil {
		return out, nil
	}
	sm, ok := rawSave.(map[string]any)
	if !ok {
		return nil, Errorf("save 要是物件 {section 或 tab, title} 或 {id}")
	}
	sv := Args{Raw: sm}
	src, err := k.source(spec)
	if err != nil {
		return nil, ToolError{err.Error()}
	}
	args := withAgent(map[string]any{})
	// 標籤照原樣([]any)交給 doc_save:它用 Args.Strings 讀,[]string 認不得,標籤會被當成沒給
	tags, hasTags := sv.Raw["tags"]
	if hasTags {
		args["tags"] = tags
	}
	// meta 更新既有文件時要另外改的標題、標籤:doc_save 的局部修改(edits)不能和 title、tags 一起送,
	// 所以先存圖,成功後再用一次 doc_save 只帶 id + title/tags
	var meta map[string]any
	if id := sv.Str("id"); id != "" {
		body, version, err := readDoc(id)
		if err != nil {
			return nil, Errorf("%s畫好了,但讀不到要更新的文件:%v", k.noun, err)
		}
		args["id"] = id
		// 不整份覆寫:只把第一個這種圖的區塊換掉(字串取代,帶讀到的版本;期間別人改了文件其他地方,
		// 取代會套在最新的內文上,不會蓋掉別人的修改);沒有區塊就接在最後
		// first 回的是內文裡逐字的那一段(保留原文的 \r\n),edits 才套得上
		if old, lang, ok := k.first(body); ok {
			if strings.Count(body, old) != 1 {
				return nil, Errorf("%s畫好了,但文件 %s 裡有兩段一模一樣的 ```%s 區塊,不知道要換哪一段;請先手動刪掉多的那段", k.noun, id, lang)
			}
			open, _, _ := strings.Cut(old, "\n")
			blk := k.fenceLike(strings.TrimSuffix(open, "\r"), src)
			if strings.Contains(old, "\r\n") { // 原本的區塊用 \r\n 換行:新區塊跟著用,不混用
				blk = strings.ReplaceAll(blk, "\n", "\r\n")
			}
			args["edits"] = []any{map[string]any{"old": old, "new": blk}}
			if version != "" {
				args["base_version"] = version
			}
			delete(args, "tags")
			meta = withAgent(map[string]any{"id": id})
			if t := sv.Str("title"); t != "" {
				meta["title"] = t
			}
			if hasTags {
				meta["tags"] = tags
			}
			if len(meta) == len(withAgent(map[string]any{"id": id})) {
				meta = nil // 沒有要改的標題、標籤
			}
		} else {
			// 接在最後(append_body)可以和 title、tags 一起送,一次存完
			args["append_body"] = k.fence(src)
			if t := sv.Str("title"); t != "" {
				args["title"] = t
			}
		}
		if n, lang := k.others(body); n > 0 {
			warns = append(warns, fmt.Sprintf("文件 %s 裡另有 %d 個 ```%s 區塊,這次沒有更動;不要了請自己刪掉", id, n, lang))
			out["warnings"] = warns
		}
	} else {
		title := sv.Str("title")
		if title == "" {
			return nil, Errorf("save.title 必填(新增文件時)")
		}
		if sv.Opt("section") == nil && sv.Opt("tab") == nil {
			return nil, Errorf("save 要給 section(區塊)或 tab(頁籤),或給 id 更新既有文件")
		}
		body := k.fence(src) + "\n"
		if note, _ := sv.RawString("note"); strings.TrimSpace(note) != "" {
			body = strings.TrimSpace(note) + "\n\n" + body
		}
		args["title"], args["body"] = title, body
		for _, key := range []string{"section", "tab"} {
			if v := sv.Opt(key); v != nil {
				args[key] = *v
			}
		}
	}
	d, err := s.RunNested(c, "doc_save", args)
	if err != nil {
		return nil, Errorf("%s畫好了,但存到文件櫃失敗:%v", k.noun, err)
	}
	if meta != nil {
		if d, err = s.RunNested(c, "doc_save", meta); err != nil {
			return nil, Errorf("%s已存進文件 %s,但改標題/標籤失敗:%v", k.noun, args["id"], err)
		}
	}
	out["doc"] = d
	return out, nil
}

// diagramSaveParam save 參數的規格
func diagramSaveParam(lang string) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "存到文件櫃:不給 id 新增一份(section 或 tab、title 必填,note 是區塊前面的說明文字);給 id 就更新那份文件的第一個 ```" + lang + " 區塊(沒有就接在最後),其他內文不動",
		"properties": map[string]any{
			"id":      pStr("要更新的文件 id"),
			"section": pStr("區塊 id 或名稱(新增時)"),
			"tab":     pStr("頁籤網址或名稱(新增時)"),
			"title":   pStr("標題(新增時必填;更新時給了就改標題)"),
			"note":    pStr("新增時放在圖前面的說明(Markdown)"),
			"tags":    pStrs("標籤"),
		},
		"additionalProperties": false,
	}
}
