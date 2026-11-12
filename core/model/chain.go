package model

import (
	"strings"
)

// 組織鏈(資訊流動方向):CEO 給研發長「目標」,研發長給產品經理、設計師、工程師「任務」,做完的人回報給派工的人。
// 一個組織一份 OrgChain;一條邊 From → To 表示「From 可以派工作給 To(Label 例如 目標、任務,空白 = 任務);To 做完回報給 From」。
// 節點是 "agent:<角色 id>" 或 "user:<email 小寫>"。規則(驗證、依鏈派工、自動回報)在 service/chain.go。

// UserPrefix 真人成員節點的前綴(角色節點用 AgentPrefix)
const UserPrefix = "user:"

// ChainAuthor 組織鏈自動排的「檢視回報」任務的 createdBy,以及「已回報給…」備註的署名(不是鏈上節點,所以不會再觸發回報)
const ChainAuthor = "chain"

// DefaultChainLabel 邊的 label 空白時的意思
const DefaultChainLabel = "任務"

// ChainReportPrefix 回報備註的開頭(「回報給我的任務」依它判斷)
const ChainReportPrefix = "已回報給 "

type ChainEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type OrgChain struct {
	Edges     []ChainEdge `json:"edges"`
	UpdatedAt *Time       `json:"updatedAt,omitempty"`
}

// Clone 深拷貝
func (c OrgChain) Clone() OrgChain {
	c.Edges = append([]ChainEdge{}, c.Edges...)
	if c.UpdatedAt != nil {
		t := *c.UpdatedAt
		c.UpdatedAt = &t
	}
	return c
}

// LabelOrDefault 空白 = 任務
func (e ChainEdge) LabelOrDefault() string {
	if l := strings.TrimSpace(e.Label); l != "" {
		return l
	}
	return DefaultChainLabel
}

// Edge 找 from → to 的邊
func (c OrgChain) Edge(from, to string) (ChainEdge, bool) {
	for _, e := range c.Edges {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return ChainEdge{}, false
}

// Downstream 這個節點可以派工作給的邊(From == node)
func (c OrgChain) Downstream(node string) []ChainEdge {
	out := []ChainEdge{}
	for _, e := range c.Edges {
		if e.From == node {
			out = append(out, e)
		}
	}
	return out
}

// Upstream 這個節點回報給的邊(To == node)
func (c OrgChain) Upstream(node string) []ChainEdge {
	out := []ChainEdge{}
	for _, e := range c.Edges {
		if e.To == node {
			out = append(out, e)
		}
	}
	return out
}

// AgentNode agent:<id>
func AgentNode(id string) string { return AgentPrefix + id }

// UserNode user:<email 小寫>
func UserNode(email string) string { return UserPrefix + strings.ToLower(strings.TrimSpace(email)) }

// NodeOfAuthor 任務的 createdBy 對應的鏈上節點:agent:<id> 原樣、email 換成 user:<email 小寫>;其他(routine、chain、mcp…)回空字串
func NodeOfAuthor(createdBy string) string {
	a := strings.TrimSpace(createdBy)
	switch {
	case strings.HasPrefix(a, AgentPrefix) && len(a) > len(AgentPrefix):
		return a
	case strings.HasPrefix(a, UserPrefix) && len(a) > len(UserPrefix):
		return UserNode(a[len(UserPrefix):])
	case strings.Contains(a, "@"):
		return UserNode(a)
	}
	return ""
}

// ChainCycle 找循環:有的話回傳循環上的節點(頭尾相同),沒有回 nil
func ChainCycle(edges []ChainEdge) []string {
	next := map[string][]string{}
	nodes := []string{}
	seen := map[string]bool{}
	for _, e := range edges {
		next[e.From] = append(next[e.From], e.To)
		for _, n := range []string{e.From, e.To} {
			if !seen[n] {
				seen[n] = true
				nodes = append(nodes, n)
			}
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	stack := []string{}
	var found []string
	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = gray
		stack = append(stack, n)
		for _, m := range next[n] {
			switch color[m] {
			case gray:
				for i, s := range stack {
					if s == m {
						found = append(append([]string{}, stack[i:]...), m)
						break
					}
				}
				return true
			case white:
				if visit(m) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	for _, n := range nodes {
		if color[n] == white && visit(n) {
			return found
		}
	}
	return nil
}

// LabeledInstructions 依鏈派工的任務說明:開頭寫上邊的 label(「【目標】」),後面是原本的說明
func LabeledInstructions(label, instructions string) string {
	l := strings.TrimSpace(label)
	if l == "" {
		l = DefaultChainLabel
	}
	head := "【" + l + "】"
	if strings.TrimSpace(instructions) == "" {
		return head
	}
	return head + "\n\n" + instructions
}
