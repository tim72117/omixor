// Package env 讀環境變數(底層;程式碼不要直接用,一律經 omixor-core/config),並相容產品早期叫「Ops」時的舊名稱(OPS_*)。
//
// 一律先讀新名稱(OMIXOR_*),新名稱沒設(或是空字串)才讀舊名稱;讀到舊名稱時記一次 log,
// 提醒改用新名稱。舊名稱之後會拿掉,新的設定、文件都只寫新名稱。
package env

import (
	"log"
	"os"
	"sync"
)

var warned sync.Map // 已經提醒過的舊名稱

// Get 讀 name;沒設或空字串時依序讀 legacy(舊名稱),讀到就 log 一次「舊名稱已改名」。
func Get(name string, legacy ...string) string {
	v, _ := Lookup(name, legacy...)
	return v
}

// Lookup 同 Get,另外回傳是否有讀到非空的值。
func Lookup(name string, legacy ...string) (string, bool) {
	return LookupFunc(os.Getenv, name, legacy...)
}

// LookupFunc 同 Lookup,但從 get(取代 os.Getenv)讀;設定集中載入(omixor-core/config)與測試用
func LookupFunc(get func(string) string, name string, legacy ...string) (string, bool) {
	if v := get(name); v != "" {
		return v, true
	}
	for _, old := range legacy {
		if v := get(old); v != "" {
			if _, dup := warned.LoadOrStore(old, true); !dup {
				log.Printf("環境變數 %s 已改名為 %s,請更新設定(舊名稱暫時仍可用)", old, name)
			}
			return v, true
		}
	}
	return "", false
}
