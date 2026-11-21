package service

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 文件櫃的匯入(第一次搬資料):從 cabinet.go 拆出,行為不變

// ImportCabinet 第一次搬資料用(組織管理員):結構只在組織還沒存過時寫入;文件保留 id、作者與時間,
// 組織已經有文件就整個拒絕(409)
func (s *Service) ImportCabinet(p *model.Principal, org string, in CabinetImport) (*CabinetImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if in.Layout == nil && len(in.Docs) == 0 {
		return nil, fail(400, "沒有要匯入的結構或文件")
	}
	seen := map[string]bool{}
	who := s.newImportWho(p, org)
	docs := []model.CabinetDoc{}
	for _, d := range in.Docs {
		if d.Number() == 0 || strings.TrimSpace(d.Title) == "" || d.Section == "" {
			return nil, fail(400, "每一份文件都要有 id(DOC-<n>)、title 與 section")
		}
		if seen[strings.ToUpper(d.ID)] {
			return nil, fail(400, "id 重複:%s", d.ID)
		}
		seen[strings.ToUpper(d.ID)] = true
		d.Tab = emptyTab(d.Tab)
		if d.Project != "" {
			pid, err := s.projectID(org, d.Project)
			if err != nil {
				return nil, err
			}
			d.Project = pid
		}
		d.Tags = model.CleanLabels(d.Tags)              // 匯入的標籤也要清理(去空白、去重;CleanLabels 不回 nil)
		if a, err := who.id(d.Author, ""); err != nil { // 作者的舊字串 → actor id(見 actor_import.go;沒給是 NULL)
			return nil, err
		} else {
			d.Author = a
		}
		if d.CreatedAt.IsZero() {
			d.CreatedAt = s.mt()
		}
		if d.UpdatedAt.IsZero() {
			d.UpdatedAt = d.CreatedAt
		}
		docs = append(docs, d)
	}
	if in.Layout != nil {
		for i := range in.Layout {
			if in.Layout[i].Tabs == nil {
				in.Layout[i].Tabs = []model.CabinetTab{}
			}
		}
		if e := model.ValidateCabinetLayout(in.Layout, nil); e != "" {
			return nil, fail(400, "%s", e)
		}
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	res := &CabinetImportResult{Org: org}
	if len(docs) > 0 {
		cur, err := s.Store.ListDocs(org)
		if err != nil {
			return nil, err
		}
		if len(cur) > 0 {
			return nil, fail(409, "%s 的文件櫃已經有 %d 份文件;匯入只在第一次搬資料時用", org, len(cur))
		}
	}
	// 先匯入文件再存結構:文件被拒(409)時什麼都沒寫,之後可以整個重來
	if len(docs) > 0 {
		n, err := s.Store.ImportDocs(org, docs)
		if err != nil {
			return nil, fail(409, "%v", err)
		}
		res.Docs = n
	}
	if in.Layout != nil {
		if _, saved, err := s.layoutOf(org, ""); err != nil {
			return nil, err
		} else if saved {
			res.LayoutSkipped = true
		} else {
			// 語意是「只在還沒存過時套用」(不是覆蓋式匯入):expected=0,跨實例時別人搶先存了(版本衝突)就當作已存過、略過
			zero := int64(0)
			_, err := s.Store.SaveCabinetLayout(org, "", in.Layout, &zero)
			var vc *store.VersionConflictError
			switch {
			case errors.As(err, &vc):
				res.LayoutSkipped = true
			case err != nil:
				return nil, err
			default:
				res.LayoutSaved = true
			}
		}
	}
	s.Audit(p, "cabinet.import", org, map[string]any{"docs": res.Docs, "layout": res.LayoutSaved})
	return res, nil
}
