package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"omixor-core/model"
)

// ---- 組織設定文件(通用;欄位宣告在 PG 的 omixor_org_integrations) ----
//
// 選用模組(core/module)存自己的組織層設定用,不必在 store 加專屬方法。例如 GitHub、GitLab 模組(core/modules/integrations)
// 用 kind = github、gitlab。
//
// PG 存在 omixor_org_integrations(data 是設定本身,updated_at、updated_by 另外存)。

// OrgDoc 一份組織設定文件
type OrgDoc struct {
	// Data 設定本身(JSON 物件,格式由模組決定;不含 updatedAt、updatedBy)
	Data json.RawMessage
	// UpdatedAt、UpdatedBy 最後修改(PG 讀回時一定有 UpdatedAt;存的時候是 nil 用現在時間)。UpdatedBy 是 actor id(空 = 沒有;不存在的 actor 存的時候回 ErrBadRef)
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

// mergeDoc 設定本身 + updatedAt + updatedBy 合成一個 JSON 物件(PG 存之前用它檢查 Data 是 JSON 物件)
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
