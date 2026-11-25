package service

import (
	"omixor-core/model"
	"omixor-core/store"
)

// SchemaCheck 資料庫結構檢核(伺服器管理員):比對全新資料庫的結構(golden)與這台資料庫目前的實際結構。
// 唯讀、只查 catalog,不寫稽核。Store 沒有實作 SchemaChecker 時 400。
func (s *Service) SchemaCheck(p *model.Principal) (*model.SchemaReport, error) {
	if err := needServerAdmin(p); err != nil {
		return nil, err
	}
	sc, ok := s.Store.(store.SchemaChecker)
	if !ok {
		return nil, fail(400, "這台伺服器沒有使用資料庫,不適用")
	}
	return sc.CheckSchema()
}
