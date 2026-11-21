package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 文件櫃結構(PG):一個區塊、一個頁籤各一筆資料列 ----
//
// 表 omixor_cabinet_sections(主鍵 (org, project, id);pos 是順序)、omixor_cabinet_tabs(主鍵 (org, project, path);
// section_id 記所屬區塊,pos 是在該區塊內的順序)。文件(omixor_docs)的 section、tab 仍是字串參照,不加外鍵。
//
// 「這個 (org, project) 存過結構」的標記與版本號:omixor_cabinet_layouts 的那一列(空結構也有那一列,所以能和「沒存過」區分)。
//   - version(bigint,預設 0)是樂觀鎖的版本號:每存一次 +1;存檔帶 expected 時只有目前版本等於它才存(見 SaveCabinetLayout)。
//   - 存檔(SaveCabinetLayout)一個交易裡整份取代新表;讀取一律以新表為準。
//
// 舊欄位 layout(jsonb)、split_at 已經不再寫、不再用來判斷新舊(雙寫與 split_at 判斷已停止);只有遷移與「未展開列的退回讀取」會讀 layout。
// 【不支援回滾】新版不雙寫舊欄位,回到舊版會看不到新版期間存的結構,要回滾只能從備份還原(見 MIGRATIONS.md)。
// 確定全部列都展開(UnmigratedRows() == 0)、線上全是這個版本之後,下一個版本再執行
// (升級路徑不得跳過「遷移還在」的這個版本):
//
//	alter table omixor_cabinet_layouts drop column layout, drop column split_at;
//
// 並一併移除 migrateCabinetLayouts 與退回讀取。
// 遷移(分批、冪等,見 pg_split_migrate.go)只處理 split_at 為 NULL 的列;新版存檔一律把 split_at 設成 now(),所以新表不會被舊 jsonb 覆蓋。
// 舊 layout 展開不了(JSON 壞掉、區塊 id 或頁籤網址重複)的列:split_at 保持 NULL,讀取退回舊 layout(JSON 壞到解不出來就回 ErrUnmigratedRow,
// 不回空結構);存檔時舊 JSON 壞掉的列拒絕(不蓋掉舊資料),舊 JSON 能解的(只是有重複)照常整份取代。

// replaceCabinetTables 在交易裡把 (org, project) 的區塊與頁籤整份換成 layout(刪掉不在新結構的、插入或更新存在的)
func replaceCabinetTables(c context.Context, tx pgx.Tx, org, project string, layout []model.CabinetSection) error {
	secIDs := make([]string, 0, len(layout))
	secPos := make([]int32, 0, len(layout))
	secLabel := make([]string, 0, len(layout))
	secPath := make([]string, 0, len(layout))
	var tabSec, tabPath, tabLabel []string
	var tabPos []int32
	var tabCode, tabGrp []*string
	seenSec := map[string]bool{}
	seenTab := map[string]bool{}
	for i, s := range layout {
		if seenSec[s.ID] {
			return fmt.Errorf("區塊 id 重複:%s", s.ID)
		}
		seenSec[s.ID] = true
		secIDs = append(secIDs, s.ID)
		secPos = append(secPos, int32(i))
		secLabel = append(secLabel, s.Label)
		secPath = append(secPath, s.Path)
		for j, t := range s.Tabs {
			if seenTab[t.Path] {
				return fmt.Errorf("頁籤 id 重複:%s", t.Path)
			}
			seenTab[t.Path] = true
			tabSec = append(tabSec, s.ID)
			tabPath = append(tabPath, t.Path)
			tabPos = append(tabPos, int32(j))
			tabCode = append(tabCode, t.Code)
			tabGrp = append(tabGrp, t.Group)
			tabLabel = append(tabLabel, t.Label)
		}
	}
	if tabPath == nil {
		tabPath, tabSec, tabLabel, tabPos, tabCode, tabGrp = []string{}, []string{}, []string{}, []int32{}, []*string{}, []*string{}
	}
	// 先刪不在新結構裡的,再 upsert(頁籤換區塊、換順序只是更新那一列)
	if _, err := tx.Exec(c, `delete from omixor_cabinet_tabs where org = $1 and project = $2 and not (path = any($3))`, org, project, tabPath); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `delete from omixor_cabinet_sections where org = $1 and project = $2 and not (id = any($3))`, org, project, secIDs); err != nil {
		return err
	}
	if len(secIDs) > 0 {
		if _, err := tx.Exec(c, `insert into omixor_cabinet_sections (org, project, id, pos, label, path)
			select $1, $2, id, pos, label, path from unnest($3::text[], $4::int[], $5::text[], $6::text[]) as t(id, pos, label, path)
			on conflict (org, project, id) do update set pos = excluded.pos, label = excluded.label, path = excluded.path`,
			org, project, secIDs, secPos, secLabel, secPath); err != nil {
			return err
		}
	}
	if len(tabPath) > 0 {
		if _, err := tx.Exec(c, `insert into omixor_cabinet_tabs (org, project, section_id, path, pos, code, grp, label)
			select $1, $2, section_id, path, pos, code, grp, label from unnest($3::text[], $4::text[], $5::int[], $6::text[], $7::text[], $8::text[]) as t(section_id, path, pos, code, grp, label)
			on conflict (org, project, path) do update set section_id = excluded.section_id, pos = excluded.pos, code = excluded.code, grp = excluded.grp, label = excluded.label`,
			org, project, tabSec, tabPath, tabPos, tabCode, tabGrp, tabLabel); err != nil {
			return err
		}
	}
	return nil
}

func (p *PG) GetCabinetLayout(org, project string) ([]model.CabinetSection, int64, error) {
	c, cancel := ctx()
	defer cancel()
	// 一個唯讀、可重複讀的交易:標記(含版本)、區塊、頁籤三次查詢看到同一個時間點
	tx, err := p.pool.BeginTx(c, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(c)
	var version int64
	if err := tx.QueryRow(c, `select version from omixor_cabinet_layouts where org = $1 and project = $2`,
		org, project).Scan(&version); err != nil {
		return nil, 0, notFound(err)
	}
	if p.hasUnsplit() { // 還沒展開的結構:退回讀舊 layout(jsonb)
		var b []byte
		err := tx.QueryRow(c, `select layout from omixor_cabinet_layouts where org = $1 and project = $2 and split_at is null`, org, project).Scan(&b)
		if err == nil {
			var old []model.CabinetSection
			if e := json.Unmarshal(b, &old); e != nil {
				return nil, 0, fmt.Errorf("%w:文件櫃結構 org=%q project=%q 的舊資料解不出來(%v)", ErrUnmigratedRow, org, project, e)
			}
			if old == nil {
				old = []model.CabinetSection{}
			}
			for i := range old {
				if old[i].Tabs == nil {
					old[i].Tabs = []model.CabinetTab{}
				}
			}
			return old, version, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, err
		}
	}
	l := []model.CabinetSection{}
	idx := map[string]int{}
	rows, err := tx.Query(c, `select id, label, path from omixor_cabinet_sections where org = $1 and project = $2 order by pos`, org, project)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var s model.CabinetSection
		if err := rows.Scan(&s.ID, &s.Label, &s.Path); err != nil {
			rows.Close()
			return nil, 0, err
		}
		s.Tabs = []model.CabinetTab{}
		idx[s.ID] = len(l)
		l = append(l, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows, err = tx.Query(c, `select section_id, path, code, grp, label from omixor_cabinet_tabs where org = $1 and project = $2 order by section_id, pos`, org, project)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var t model.CabinetTab
		if err := rows.Scan(&sid, &t.Path, &t.Code, &t.Group, &t.Label); err != nil {
			return nil, 0, err
		}
		if i, ok := idx[sid]; ok {
			l[i].Tabs = append(l[i].Tabs, t)
		}
	}
	return l, version, rows.Err()
}

func (p *PG) SaveCabinetLayout(org, project string, layout []model.CabinetSection, expected *int64) (int64, error) {
	c, cancel := ctx()
	defer cancel()
	if layout == nil {
		layout = []model.CabinetSection{}
	}
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if err := p.guardCabinetSave(c, tx, org, project); err != nil { // 舊 layout 壞掉、還沒展開的列:拒絕,不蓋掉舊資料
		return 0, err
	}
	// 取得(並鎖住)那一列、遞增版本:同一份結構的並行存檔會排隊,expected 不符的拿不到列 → 衝突。
	// split_at 一律設成 now():讓啟動遷移(只處理 split_at 為 NULL 的列)不會拿舊 jsonb 覆蓋新表
	var version int64
	if expected == nil {
		err = tx.QueryRow(c, `insert into omixor_cabinet_layouts (org, project, updated_at, split_at, version) values ($1, $2, now(), now(), 1)
			on conflict (org, project) do update set version = omixor_cabinet_layouts.version + 1, updated_at = now(), split_at = now()
			returning version`, org, project).Scan(&version)
		if err != nil {
			return 0, err
		}
	} else {
		// 條件式 update:並行時後到的會等前一個提交,再用新版本重新比對(READ COMMITTED 會重新評估 where)
		err = tx.QueryRow(c, `update omixor_cabinet_layouts set version = version + 1, updated_at = now(), split_at = now()
			where org = $1 and project = $2 and version = $3 returning version`, org, project, *expected).Scan(&version)
		if err == pgx.ErrNoRows && *expected == 0 {
			// 沒存過(版本 0)的第一次存檔:插入;並行的另一個先插入了就 do nothing → 衝突
			err = tx.QueryRow(c, `insert into omixor_cabinet_layouts (org, project, updated_at, split_at, version) values ($1, $2, now(), now(), 1)
				on conflict (org, project) do nothing returning version`, org, project).Scan(&version)
		}
		if err == pgx.ErrNoRows {
			var cur int64
			if e := tx.QueryRow(c, `select version from omixor_cabinet_layouts where org = $1 and project = $2`, org, project).Scan(&cur); e != nil && e != pgx.ErrNoRows {
				return 0, e
			}
			return 0, &VersionConflictError{Current: cur}
		}
		if err != nil {
			return 0, err
		}
	}
	if err := replaceCabinetTables(c, tx, org, project, layout); err != nil {
		return 0, fkErr(err) // 專案不存在:ErrBadRef
	}
	if err := tx.Commit(c); err != nil {
		return 0, err
	}
	return version, nil
}

// migrateCabinetLayouts 啟動時把還沒展開(split_at 為 NULL)的 omixor_cabinet_layouts 列展開進新表(分批、冪等,見 pg_split_migrate.go):
// 已展開的列(split_at 非 NULL,包含新版存過、新表被改過的資料)一律不碰;不合格的列(JSON 壞掉、區塊 id 或頁籤網址重複)略過,split_at 保持 NULL。
// 空陣列是合法的空結構,照樣標記。
func (p *PG) migrateCabinetLayouts() error {
	return p.splitRun(nil, splitCabinet)
}

var splitCabinet = &splitSpec{
	name: "文件櫃結構", table: "omixor_cabinet_layouts", keys: []string{"org", "project"},
	empty:  `case when jsonb_typeof(layout) = 'array' then jsonb_array_length(layout) = 0 else false end`,
	expand: expandCabinetLayout,
}

func expandCabinetLayout(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	org, project := key[0], key[1]
	var b []byte
	err := tx.QueryRow(c, `select layout from omixor_cabinet_layouts where org = $1 and project = $2 and split_at is null for update`, org, project).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var layout []model.CabinetSection
	if err := json.Unmarshal(b, &layout); err != nil {
		return skipErr(fmt.Errorf("JSON 不合格:%v", err))
	}
	if err := checkLayoutUnique(layout); err != nil {
		return skipErr(err)
	}
	if err := replaceCabinetTables(c, tx, org, project, layout); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_cabinet_layouts set split_at = now() where org = $1 and project = $2`, org, project)
	return err
}

// guardCabinetSave 存檔前:這個結構還沒展開、而且舊 layout 解不出來就拒絕(存了會蓋掉解不出來的舊資料)
func (p *PG) guardCabinetSave(c context.Context, tx pgx.Tx, org, project string) error {
	if !p.hasUnsplit() {
		return nil
	}
	var b []byte
	err := tx.QueryRow(c, `select layout from omixor_cabinet_layouts where org = $1 and project = $2 and split_at is null`, org, project).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var l []model.CabinetSection
	if e := json.Unmarshal(b, &l); e != nil {
		return fmt.Errorf("%w:文件櫃結構 org=%q project=%q(%v)", ErrUnmigratedRow, org, project, e)
	}
	return nil
}

// checkLayoutUnique 區塊 id、頁籤網址不能重複(新表的主鍵)
func checkLayoutUnique(layout []model.CabinetSection) error {
	secs := map[string]bool{}
	tabs := map[string]bool{}
	for _, s := range layout {
		if secs[s.ID] {
			return fmt.Errorf("區塊 id 重複:%q", s.ID)
		}
		secs[s.ID] = true
		for _, t := range s.Tabs {
			if tabs[t.Path] {
				return fmt.Errorf("頁籤網址重複:%q", t.Path)
			}
			tabs[t.Path] = true
		}
	}
	return nil
}
