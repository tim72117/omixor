package mcp

import coremcp "omixor-core/mcp"

// 協定層、參數與錯誤型別在核心(omixor-core/mcp);這裡只是別名,讓 web 的工具照舊寫
type (
	Args      = coremcp.Args
	ToolError = coremcp.ToolError
)

func Errorf(format string, a ...any) error { return coremcp.Errorf(format, a...) }

// ToolErr、PrefixErr 見 omixor-core/mcp:錯誤轉成工具錯誤(資料庫等內部細節換成通用訊息,原文進 log)
func ToolErr(err error) error                  { return coremcp.ToolErr(err) }
func PrefixErr(prefix string, err error) error { return coremcp.PrefixErr(prefix, err) }
