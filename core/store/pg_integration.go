package store

import (
	"encoding/json"
	"time"

	"omixor-core/model"
)

// ---- 組織設定文件(PG;表 omixor_org_integrations 的定義在 pg.go 的 pgSchema) ----
// 一列一份設定(kind = github、gitlab 等選用模組的設定,外部 MCP 登記是 mcp),設定本身存在 data(jsonb);updated_at、updated_by 另外存一份。
// 表與資料格式沒變:以前 GitHub、GitLab 的專屬方法存的資料照讀(GitHub、GitLab 模組存的 data 和以前一模一樣)。

// getIntegration 讀一份設定;回傳的第三個值是 updated_by 的 actor id(NULL = 空字串)
func (p *PG) getIntegration(org, kind string, out any) (time.Time, string, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	var at time.Time
	var by NullID
	err := p.pool.QueryRow(c, `select data, updated_at, updated_by from omixor_org_integrations where org = $1 and kind = $2`, org, kind).
		Scan(&b, &at, &by)
	if err != nil {
		return at, by.String(), notFound(err)
	}
	return secs(at), by.String(), json.Unmarshal(b, out)
}

func (p *PG) saveIntegration(org, kind string, data any, updatedAt *model.Time, updatedBy string) error {
	c, cancel := ctx()
	defer cancel()
	at := time.Now()
	if updatedAt != nil {
		at = updatedAt.Time
	}
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `insert into omixor_org_integrations (org, kind, data, updated_at, updated_by) values ($1, $2, $3, $4, $5)
		on conflict (org, kind) do update set data = $3, updated_at = $4, updated_by = $5`, org, kind, jb(data), secs(at), IDOrNil(updatedBy)); err != nil {
		return fkErr(err)
	}
	return tx.Commit(c)
}

// GetOrgDoc 組織設定文件(通用;選用模組用,見 mem_integration.go 的 OrgDoc)
func (p *PG) GetOrgDoc(org, kind string) (*OrgDoc, error) {
	if err := checkDocKind(kind); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	at, by, err := p.getIntegration(org, kind, &raw)
	if err != nil {
		return nil, err
	}
	return &OrgDoc{Data: raw, UpdatedAt: &model.Time{Time: at}, UpdatedBy: by}, nil
}

// SaveOrgDoc 存組織設定文件(整份取代;data 原樣存在 jsonb)
func (p *PG) SaveOrgDoc(org, kind string, d OrgDoc) error {
	if err := checkDocKind(kind); err != nil {
		return err
	}
	// 要是 JSON 物件(空的當 {}),設定本身不含 updatedAt、updatedBy(那兩個另外存在欄位裡)
	data, err := mergeDoc(OrgDoc{Data: d.Data})
	if err != nil {
		return err
	}
	return p.saveIntegration(org, kind, data, d.UpdatedAt, d.UpdatedBy)
}
