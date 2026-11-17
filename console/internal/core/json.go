package core

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JSON 規則和 Swift 版(MCPJSON)一致:
//   - 物件 key 排序(map 本來就排序;struct 欄位請依字母順序宣告,或先轉成 map)
//   - 不跳脫 / 與 HTML 字元、不跳脫中文
//   - 時間是 ISO 8601、UTC、到秒:2026-11-11T00:15:28Z

// Time 編成 Swift 的 .iso8601 格式(沒有小數秒)
type Time struct{ time.Time }

const isoLayout = "2006-01-02T15:04:05Z07:00"

func Now() Time { return Time{time.Now().UTC().Truncate(time.Second)} }

func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.UTC().Format(isoLayout) + `"`), nil
}

func (t *Time) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*t = Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	p, err := ParseTime(s)
	if err != nil {
		return err
	}
	*t = Time{p}
	return nil
}

// ParseTime 接受 ISO 8601,有沒有小數秒都行
func ParseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(isoLayout, s)
}

// ISO 和 Swift 的 MCPJSON.iso 一樣
func ISO(t time.Time) string { return t.UTC().Format(isoLayout) }

// Marshal 依 Swift 版的規則輸出精簡 JSON
func Marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// MarshalIndent 給存檔用(兩格縮排、key 排序)
func MarshalIndent(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return buf.Bytes()
}

// Object 把任何值轉成 JSON 物件(map[string]any、[]any…),等同 Swift 的 MCPJSON.object
func Object(v any) any {
	var out any
	if err := json.Unmarshal(Marshal(v), &out); err != nil {
		return nil
	}
	return out
}

// Clone 深拷貝一個 JSON 物件
func Clone(v any) any { return Object(v) }

// UUID 大寫,和 Swift 的 UUID().uuidString 一樣
func UUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// Actor:{"kind":"person","id":"tim"}、{"kind":"ai","id":"mcp"}、{"kind":"system"}
type Actor struct {
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind"`
}

const MCPActorID = "mcp"

var (
	MCPActor    = Actor{ID: MCPActorID, Kind: "ai"}
	SystemActor = Actor{Kind: "system"}
)

func Person(id string) Actor { return Actor{ID: id, Kind: "person"} }
func AI(id string) Actor     { return Actor{ID: id, Kind: "ai"} }

// ActorFrom 從 JSON 物件還原
func ActorFrom(v any) Actor {
	m, _ := v.(map[string]any)
	a := Actor{}
	a.Kind, _ = m["kind"].(string)
	a.ID, _ = m["id"].(string)
	return a
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
