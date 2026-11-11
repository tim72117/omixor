package core

import (
	"strconv"
	"time"
)

// 解析外部 API 的 JSON(GitHub、Google…)用的小工具,行為同 Swift 的 `v as? String ?? ""`、`(v as? NSNumber)?.intValue ?? 0`。

func jStr(v any) string {
	s, _ := v.(string)
	return s
}

// jNum 同 Swift 的 NSNumber:數字或布林
func jNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func jInt(v any) int {
	f, _ := jNum(v)
	return int(f)
}

// jIntOrString 數字,或可以轉成整數的字串(Google 的 int64 常用字串)
func jIntOrString(v any) int {
	if f, ok := jNum(v); ok {
		return int(f)
	}
	if s, ok := v.(string); ok {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}

func jMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// jMaps 物件陣列;不是陣列時回傳空的
func jMaps(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []map[string]any{}
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil // Swift 的 as? [[String: Any]] 整個失敗
		}
		out = append(out, m)
	}
	return out
}

func jBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func timePtr(t time.Time) *Time { return &Time{t} }
