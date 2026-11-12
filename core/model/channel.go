package model

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 訊息頻道:把需要合作的人(組織成員)與虛擬角色拉進同一個頻道對話。頻道可以自由建立,也可以綁一個 Issue。
// 成員是節點(同組織鏈):"agent:<角色 id>" 或 "user:<email 小寫>"。@ 一個角色只通知、不排任務:
// 角色下次執行(ops_agent_context)時看得到提到它的未讀訊息。規則(權限、@ 解析、未讀)在 service/channel.go。

// ChannelPrefix 頻道 id 的前綴(CH-<n>,一個組織一起編號)
const ChannelPrefix = "CH-"

// MessagePrefix 訊息 id 的前綴(M-<n>,每個頻道各自從 1 編號)
const MessagePrefix = "M-"

const (
	MaxChannelName    = 80   // 頻道名稱最多幾個字
	MaxChannelTopic   = 500  // 主題最多幾個字
	MaxChannelMessage = 8000 // 一則訊息最多幾個字
)

type Channel struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Topic     string   `json:"topic"`
	IssueID   string   `json:"issueId"` // 沒綁是 ""
	Members   []string `json:"members"`
	CreatedBy string   `json:"createdBy"`
	CreatedAt Time     `json:"createdAt"`
	UpdatedAt Time     `json:"updatedAt"`
}

// Clone 深拷貝
func (c Channel) Clone() Channel {
	c.Members = append([]string{}, c.Members...)
	return c
}

// Number CH-<n> 的 n(格式不對是 0)
func (c Channel) Number() int { return idNumber(c.ID, ChannelPrefix) }

// HasMember 節點是不是頻道成員
func (c Channel) HasMember(node string) bool {
	for _, m := range c.Members {
		if m == node {
			return true
		}
	}
	return false
}

// ChannelSummary 列表用:頻道加上「我的」未讀數與最後一則訊息的時間
type ChannelSummary struct {
	Channel
	Unread        int   `json:"unread"`
	LastMessageAt *Time `json:"lastMessageAt"`
}

type ChannelMessage struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channelId"`
	Author    string   `json:"author"` // 節點
	Text      string   `json:"text"`   // Markdown
	Mentions  []string `json:"mentions"`
	At        Time     `json:"at"`
}

// Clone 深拷貝
func (m ChannelMessage) Clone() ChannelMessage {
	m.Mentions = append([]string{}, m.Mentions...)
	return m
}

// Number M-<n> 的 n(格式不對是 0)
func (m ChannelMessage) Number() int { return idNumber(m.ID, MessagePrefix) }

// Mentioned 這則訊息有沒有提到節點
func (m ChannelMessage) Mentioned(node string) bool {
	for _, x := range m.Mentions {
		if x == node {
			return true
		}
	}
	return false
}

// ChannelRead 已讀:每個成員每個頻道一筆;LastRead 是讀到的訊息號碼(號碼 > LastRead 而且不是自己發的 = 未讀),At 是標已讀的時間
type ChannelRead struct {
	ChannelID string `json:"channelId"`
	Member    string `json:"member"`
	LastRead  int    `json:"lastRead"`
	At        Time   `json:"at"`
}

// MessageQuery 列訊息的條件(號碼):After > 0 只要號碼大於它的、Before > 0 只要號碼小於它的;
// Latest = true 時回傳範圍內最新的 Limit 筆,否則最早的 Limit 筆。結果一律由舊到新
type MessageQuery struct {
	After, Before int
	Limit         int
	Latest        bool
}

func idNumber(id, prefix string) int {
	if len(id) <= len(prefix) || !strings.EqualFold(id[:len(prefix)], prefix) {
		return 0
	}
	n, err := strconv.Atoi(id[len(prefix):])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// MessageNumber "M-12" → 12(不是訊息 id 回 0)
func MessageNumber(id string) int { return idNumber(strings.TrimSpace(id), MessagePrefix) }

// MentionCandidate @ 可以比對的寫法:Text 是 @ 後面要出現的文字(不分大小寫),Node 是對應的節點;
// Word = true 表示比對到之後下一個字元不能是英數或 ._-(角色 id、email、節點寫法;名稱不檢查,中文名稱後面常直接接字)
type MentionCandidate struct {
	Text string
	Node string
	Word bool
}

// ParseMentions 從訊息找出 @ 到的節點(依出現順序、去重):@ 前面要是開頭或非英數字元,
// 後面比對候選(不分大小寫,取最長的)。不在候選裡的 @ 忽略
func ParseMentions(text string, cands []MentionCandidate) []string {
	out := []string{}
	seen := map[string]bool{}
	lower := strings.ToLower(text)
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			if isWordRune(r) {
				continue
			}
		}
		rest := lower[i+1:]
		best, bestLen := "", 0
		for _, c := range cands {
			t := strings.ToLower(strings.TrimSpace(c.Text))
			if t == "" || len(t) <= bestLen || !strings.HasPrefix(rest, t) {
				continue
			}
			if c.Word && len(rest) > len(t) {
				after := rest[len(t):]
				r, _ := utf8.DecodeRuneInString(after)
				if isWordRune(r) || r == '-' || r == '_' {
					continue
				}
				// 句點後面還接英數(例如 email 還沒完)才算同一個字;句尾的句點不算
				if r == '.' && len(after) > 1 {
					if r2, _ := utf8.DecodeRuneInString(after[1:]); isWordRune(r2) {
						continue
					}
				}
			}
			best, bestLen = c.Node, len(t)
		}
		if best != "" {
			if !seen[best] {
				seen[best] = true
				out = append(out, best)
			}
			i += bestLen
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r))
}
