package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"omixor-core/model"
)

// ---- 組織設定文件(通用;Mem;欄位宣告在 mem.go) ----
//
// 選用模組(core/module)存自己的組織層設定用,不必在 store 加專屬方法。例如 GitHub、GitLab 模組(core/modules/integrations)
// 用 kind = github、gitlab。
//
// 格式相容:Mem / File 存的是「設定本身 + updatedAt + updatedBy」合成的一個 JSON 物件,
// 和以前 model.OrgIntegrations 的 github、gitlab 欄位一模一樣(檔案型 store 的 integrations 照讀);
// PG 存在 omixor_org_integrations(data 是設定本身,updated_at、updated_by 另外存),表與格式都沒變。

// OrgDoc 一份組織設定文件
type OrgDoc struct {
	// Data 設定本身(JSON 物件,格式由模組決定;不含 updatedAt、updatedBy)
	Data json.RawMessage
	// UpdatedAt、UpdatedBy 最後修改(PG 讀回時一定有 UpdatedAt;存的時候是 nil 用現在時間)
	UpdatedAt *model.Time
	UpdatedBy string
}

// reservedDocKinds 有專屬方法的 kind(PG 同一張表),不能當通用文件用
var reservedDocKinds = map[string]bool{"mcp": true}

func checkDocKind(kind string) error {
	if kind == "" || reservedDocKinds[kind] {
		return fmt.Errorf("組織設定文件的 kind 不能是 %q", kind)
	}
	return nil
}

// mergeDoc 設定本身 + updatedAt + updatedBy 合成一個 JSON 物件(Mem / File 存這個)
func mergeDoc(d OrgDoc) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(d.Data) > 0 {
		if err := json.Unmarshal(d.Data, &obj); err != nil {
			return nil, errors.New("組織設定文件要是 JSON 物件:" + err.Error())
		}
	}
	delete(obj, "updatedAt")
	delete(obj, "updatedBy")
	if d.UpdatedAt != nil && !d.UpdatedAt.IsZero() {
		b, _ := json.Marshal(d.UpdatedAt)
		obj["updatedAt"] = b
	}
	if d.UpdatedBy != "" {
		b, _ := json.Marshal(d.UpdatedBy)
		obj["updatedBy"] = b
	}
	return json.Marshal(obj)
}

// splitDoc mergeDoc 的反向:拿出 updatedAt、updatedBy,其餘是設定本身
func splitDoc(raw json.RawMessage) (*OrgDoc, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	d := &OrgDoc{}
	if v, ok := obj["updatedAt"]; ok {
		var t model.Time
		if json.Unmarshal(v, &t) == nil && !t.IsZero() {
			d.UpdatedAt = &t
		}
		delete(obj, "updatedAt")
	}
	if v, ok := obj["updatedBy"]; ok {
		_ = json.Unmarshal(v, &d.UpdatedBy)
		delete(obj, "updatedBy")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	d.Data = b
	return d, nil
}

func (m *Mem) GetOrgDoc(org, kind string) (*OrgDoc, error) {
	if err := checkDocKind(kind); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.integrations[org][kind]
	if !ok {
		return nil, ErrNotFound
	}
	return splitDoc(raw)
}

func (m *Mem) SaveOrgDoc(org, kind string, d OrgDoc) error {
	if err := checkDocKind(kind); err != nil {
		return err
	}
	raw, err := mergeDoc(d)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.integrations == nil {
		m.integrations = map[string]map[string]json.RawMessage{}
	}
	if m.integrations[org] == nil {
		m.integrations[org] = map[string]json.RawMessage{}
	}
	m.integrations[org][kind] = raw
	return nil
}
