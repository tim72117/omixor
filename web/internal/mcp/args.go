package mcp

import coremcp "omixor-core/mcp"

// 協定層、參數與錯誤型別在核心(omixor-core/mcp);這裡只是別名,讓 web 的工具照舊寫
type (
	Args      = coremcp.Args
	ToolError = coremcp.ToolError
)

func Errorf(format string, a ...any) error { return coremcp.Errorf(format, a...) }
