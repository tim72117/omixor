package mcp

// RegisterSchemaTools 登記資料庫結構檢核工具:schema_check(唯讀,只有伺服器管理員)。
// 不鎖 Env:只轉發給核心(本機內建核心或遠端 omixor server),不碰 web 的狀態。
func RegisterSchemaTools(s *Server) {
	s.Register("schema_check", func(c *Call) (any, error) {
		rep, err := env(c).Backend().SchemaCheck()
		if err != nil {
			return nil, terr(err)
		}
		return rep, nil
	})
}
