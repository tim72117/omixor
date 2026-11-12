package core

import (
	"strconv"
	"strings"
	"unicode"
)

// 範例資料用的 JSON 輔助,對應 Swift MCPJSON.swift 的 pick / value(at:) / matches / compact。
// 其他功能也可以用。

// Pick 只留下指定的欄位(同 Swift 的 pick)
func Pick(obj any, keys ...string) map[string]any {
	out := map[string]any{}
	d, ok := obj.(map[string]any)
	if !ok {
		return out
	}
	for _, k := range keys {
		if v, ok := d[k]; ok {
			out[k] = v
		}
	}
	return out
}

// ValueAt 依 "a.b.c" 路徑取值(同 Swift 的 value(at:in:);空的段落略過)。找不到回傳 (nil, false)。
func ValueAt(path string, obj any) (any, bool) {
	cur := obj
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		d, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := d[part]
		if !ok {
			return nil, false
		}
		cur = v
	}
	if cur == nil {
		// JSON 的 null:Swift 的 JSONSerialization 是 NSNull,不是 nil,但 scalarString(NSNull) 也比不到,結果一樣
		return nil, true
	}
	return cur, true
}

// Matches 欄位等於 want;欄位是陣列時任一元素等於即可;欄位是物件時比對它的 id(同 Swift 的 matches)
func Matches(field any, present bool, want any) bool {
	if !present {
		return false
	}
	w, ok := ScalarString(want)
	if !ok {
		return false
	}
	switch f := field.(type) {
	case []any:
		for _, e := range f {
			if s, ok := ScalarString(e); ok && s == w {
				return true
			}
		}
		return false
	case map[string]any:
		if id, ok := f["id"]; ok {
			s, ok := ScalarString(id)
			return ok && s == w
		}
		return false
	}
	s, ok := ScalarString(field)
	return ok && s == w
}

// Compact 列表模式下把大量資料裁短:時間序列只留最後 3 點,工作日誌只留最後 3 行(同 Swift 的 compact)。
// 回傳新的物件,不改原本的。
func Compact(obj any) any {
	switch x := obj.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			arr, isArr := v.([]any)
			if k == "log" && isArr && len(arr) > 3 {
				out[k] = append([]any{}, arr[len(arr)-3:]...)
				out["logLines"] = len(arr)
			} else if isArr && len(arr) > 3 && isSeries(arr) {
				out[k] = append([]any{}, arr[len(arr)-3:]...)
				out[k+"Count"] = len(arr)
			} else {
				out[k] = Compact(v)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = Compact(v)
		}
		return out
	}
	return obj
}

func isSeries(arr []any) bool {
	first, ok := arr[0].(map[string]any)
	if !ok || len(first) != 2 {
		return false
	}
	_, d := first["date"]
	_, v := first["value"]
	return d && v
}

// List 把 []map 轉成 []any
func List(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
	}
	return out
}

// Str 取字串欄位
func Str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// Num 取數字欄位
func Num(m map[string]any, k string) float64 {
	switch x := m[k].(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// DigitsInt 同 Swift 的 Int(id.filter(\.isNumber)):只留數字字元再轉整數
func DigitsInt(id string) (int, bool) {
	var b strings.Builder
	for _, r := range id {
		if unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	n, err := strconv.Atoi(b.String())
	return n, err == nil
}

// NextNumber 集合裡 id 數字部分的最大值 + 1(沒有時用 base + 1),同 Swift 的流水號規則
func (a *App) NextNumber(collection string, base int) int {
	max, found := 0, false
	for _, it := range a.C[collection] {
		s, ok := ScalarString(it["id"])
		if !ok {
			continue
		}
		if n, ok := DigitsInt(s); ok && (!found || n > max) {
			max, found = n, true
		}
	}
	if !found {
		max = base
	}
	return max + 1
}
