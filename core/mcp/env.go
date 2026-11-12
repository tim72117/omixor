package mcp

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"omixor-core/backend"
	"omixor-core/model"
)

// Env 核心 MCP 工具(組織、Issue)的執行環境。工具的資料操作一律透過 backend.Backend
// (Local:核心 service;Remote:omixor server;omixor-web 有自己包了本機狀態的實作),
// 參數檢查、篩選、排序、輸出格式都在這個套件。
//
// 使用的程式讓 Call.Ctx 實作 Env。每次工具呼叫會先 Lock、結束時 Unlock
// (同一次呼叫裡 Backend 的方法不會再自己上鎖),讓「讀了再改」在同一把鎖裡完成。
type Env interface {
	Lock()
	Unlock()
	// Backend 核心的操作
	Backend() backend.Backend
	// Org 目前的組織 id
	Org() string
	// Author Issue 的署名:有給 agent 就是該角色(找不到回錯誤),否則是呼叫者
	Author(agent string) (string, error)
	// AuthorName 署名 → 顯示名稱
	AuthorName(author string) string
}

func env(c *Call) Env {
	e, ok := c.Ctx.(Env)
	if !ok {
		panic("MCP 沒有設定組織與 Issue 的執行環境(Call.Ctx 沒實作 mcp.Env)")
	}
	return e
}

// issueJSON Issue 的輸出:createdBy 與留言作者換成顯示名稱
func issueJSON(e Env, i model.Issue) any {
	b, _ := json.Marshal(i)
	var d map[string]any
	_ = json.Unmarshal(b, &d)
	d["createdBy"] = e.AuthorName(i.CreatedBy)
	comments := []any{}
	for _, c := range i.Comments {
		comments = append(comments, map[string]any{"author": e.AuthorName(c.Author), "text": c.Text, "at": iso(c.At.Time)})
	}
	d["comments"] = comments
	return d
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
