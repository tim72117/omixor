package mcp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Args 工具參數,行為同 Swift 的 MCPArgs
type Args struct{ Raw map[string]any }

// ScalarString 數字、布林轉成字串(字串原樣);其他型別回傳 ok=false
func ScalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case json.Number:
		return x.String(), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), true
		}
		return strconv.FormatFloat(x, 'f', -1, 64), true
	}
	return "", false
}

// String 字串去頭尾空白;數字、布林轉成字串;沒給回傳 ok=false
func (a Args) String(k string) (string, bool) {
	v, ok := a.Raw[k]
	if !ok || v == nil {
		return "", false
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s), true
	}
	return ScalarString(v)
}

// Str 沒給時回傳空字串
func (a Args) Str(k string) string { s, _ := a.String(k); return s }

// Opt 有給(就算是空字串)回傳指標,沒給回傳 nil(同 Swift 的 a.string(k) 是否為 nil)
func (a Args) Opt(k string) *string {
	if s, ok := a.String(k); ok {
		return &s
	}
	return nil
}

// RawString 原樣的字串(不去空白;不是字串就當沒給),同 Swift 的 a.raw[k] as? String
func (a Args) RawString(k string) (string, bool) {
	s, ok := a.Raw[k].(string)
	return s, ok
}

// Int 數字(不接受布林)或數字字串
func (a Args) Int(k string) (int, bool) {
	switch x := a.Raw[k].(type) {
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	}
	return 0, false
}

// Bool 布林,或 "true"/"1"/"yes"
func (a Args) Bool(k string) (bool, bool) {
	switch x := a.Raw[k].(type) {
	case bool:
		return x, true
	case string:
		l := strings.ToLower(x)
		return l == "true" || l == "1" || l == "yes", true
	}
	return false, false
}

// Strings 陣列(元素轉字串)或單一字串
func (a Args) Strings(k string) ([]string, bool) {
	switch x := a.Raw[k].(type) {
	case []any:
		out := []string{}
		for _, e := range x {
			if s, ok := ScalarString(e); ok {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		return []string{x}, true
	}
	return nil, false
}

func (a Args) Require(k string) (string, error) {
	s, ok := a.String(k)
	if !ok || s == "" {
		return "", ToolError{"缺少參數 " + k}
	}
	return s, nil
}

func (a Args) RequireInt(k string) (int, error) {
	n, ok := a.Int(k)
	if !ok {
		return 0, ToolError{"缺少或無效的整數參數 " + k}
	}
	return n, nil
}

// RequireEnum 必須是 values 之一
func (a Args) RequireEnum(k string, values []string) (string, error) {
	s, err := a.Require(k)
	if err != nil {
		return "", err
	}
	for _, v := range values {
		if v == s {
			return s, nil
		}
	}
	return "", ToolError{fmt.Sprintf("%s 必須是 %s 之一,收到「%s」", k, strings.Join(values, "、"), s)}
}

// OptionalEnum 沒給回傳 ("", nil)
func (a Args) OptionalEnum(k string, values []string) (string, error) {
	if _, ok := a.String(k); !ok {
		return "", nil
	}
	return a.RequireEnum(k, values)
}
