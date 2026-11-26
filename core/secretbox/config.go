package secretbox

import (
	"fmt"

	"omixor-core/config"
)

// FromConfig 依設定建立箱子:有 OMIXOR_SECRET_KEY 就用 AES-256-GCM(OMIXOR_SECRET_KEYS_OLD 當舊金鑰),沒設用明文(v0)。
// 金鑰格式不對時回錯誤(只點名變數,不含值);正常啟動時 config.Validate 已經擋掉
func FromConfig(cfg *config.Config) (Box, error) {
	active, old, err := keysFrom(cfg)
	if err != nil {
		return nil, err
	}
	if active == nil {
		return NewPlain(), nil
	}
	return NewAES(active, old...)
}

// keysFrom 取出 active 與舊金鑰;任何一把壞了就回錯誤(config 的訊息只點名變數,不含值)
func keysFrom(cfg *config.Config) (active []byte, old [][]byte, err error) {
	act, err := cfg.Keys(config.SecretKey)
	if err != nil {
		return nil, nil, err
	}
	if old, err = cfg.Keys(config.SecretKeysOld); err != nil {
		return nil, nil, err
	}
	if len(act) == 1 {
		active = act[0]
	}
	return active, old, nil
}

// Level 啟動檢查的嚴重程度(對應 core/oauth 的 Info/Warn/Fatal;不直接用它是為了不讓 secretbox 依賴 oauth)
type Level int

const (
	Info  Level = iota // 正常
	Warn               // 可以啟動,但機密沒加密
	Fatal              // 金鑰設定壞了,不能啟動
)

// Diagnose 啟動自我檢查:回傳一行摘要。之後要接進啟動流程時,依 Level 印出或結束
// (沒金鑰 = Warn:機密會以未加密的 v0 格式存;金鑰壞 = Fatal;正常 = Info,列出 active 的 keyid 與舊金鑰數量)
func Diagnose(cfg *config.Config) (Level, string) {
	active, old, err := keysFrom(cfg)
	switch {
	case err != nil:
		return Fatal, err.Error()
	case active == nil:
		return Warn, "no " + config.SecretKey.Name + " set: secrets will be stored without encryption (v0)"
	}
	return Info, fmt.Sprintf("secrets encrypted with AES-256-GCM (key id %s, %d old key(s))", KeyID(active), len(old))
}
